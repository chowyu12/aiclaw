/**
 * 端到端冒烟：agent-client → claw-agent → llm → mcpclient → claw-mcp → 内部平台。
 *
 * 模型与内部平台都是本进程内的假服务，所以**不需要真实凭据、不花额度、不打真实内部平台**，
 * 可以进 CI。它验证的是几段接缝一起工作时最容易断的地方：
 *   - 按会话下发的 mcp_servers 能让 claw-agent 拉起 claw-mcp 并拿到工具；
 *   - 只读的内部平台工具**不弹审批**（查一条数据每次都要点确认，会把用户训练成
 *     闭眼点「允许」——那比少问一次危险）；
 *   - 工具结果回填给模型，模型据此给出最终回答；
 *   - 事件按 turn/started → item/* → turn/completed 的顺序到达宿主。
 *
 * 用法：
 *   npm run build && npm run smoke
 */
import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { ClawAgentClient } from "../packages/agent-client/dist/index.js";

const steps: { name: string; ok: boolean; detail: string }[] = [];
function record(name: string, ok: boolean, detail = ""): void {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "  ok" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
}

const TOOL_NAME = "sales_data__sales_42";

function sseText(text: string): string {
  const chunk = JSON.stringify({ choices: [{ delta: { content: text } }] });
  const usage = JSON.stringify({ choices: [], usage: { prompt_tokens: 5, completion_tokens: 3, total_tokens: 8 } });
  return `data: ${chunk}\n\ndata: ${usage}\n\ndata: [DONE]\n\n`;
}

function sseToolCall(name: string, args: string): string {
  const chunk = JSON.stringify({
    choices: [{
      delta: { tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name, arguments: args } }] },
      finish_reason: "tool_calls",
    }],
  });
  return `data: ${chunk}\n\ndata: [DONE]\n\n`;
}

async function listen(handler: Parameters<typeof createServer>[1]): Promise<{ url: string; close: () => void }> {
  const server = createServer(handler);
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", () => r()));
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : 0;
  return { url: `http://127.0.0.1:${port}`, close: () => server.close() };
}

async function main(): Promise<number> {
  // 假模型：第一轮发起 MCP 工具调用，第二轮给最终回答。
  let modelCalls = 0;
  // 非空时，假模型下一次采样就发一个 exec 调用，内容是这段脚本。
  let scriptToRun = "";
  const toolNamesSeen: string[][] = [];
  const model = await listen((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      try {
        const parsed = JSON.parse(body) as { tools?: { function: { name: string } }[] };
        toolNamesSeen.push((parsed.tools ?? []).map((t) => t.function.name));
      } catch {
        toolNamesSeen.push([]);
      }
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      // 代码模式那一段会把 scriptToRun 设上，这时假模型改成发一次 exec。
      if (scriptToRun) {
        const code = scriptToRun;
        scriptToRun = "";
        res.end(sseToolCall("exec", JSON.stringify({ code })));
        return;
      }
      res.end(modelCalls++ === 0 ? sseToolCall(TOOL_NAME, `{"date":"2026-09-20"}`) : sseText("查到了：当日销售额 12345。"));
    });
  });

  // 假内部平台：三个路径——按库展开、取运行契约、执行。
  //
  // 展开那一步是必须的：配置里存的是**库**，claw-mcp 在装配时先问
  // 「这个库下有哪些数据 API」，再逐个取契约。
  let clawCalls = 0;
  const claw = await listen((req, res) => {
    clawCalls++;
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const path = req.url ?? "";
      const data = path.includes("/api-sources/operations/list")
        ? {
            // 一个「接口很多」的源：装配时应当改挂目录，而不是 161 个工具。
            list: Array.from({ length: 161 }, (_, i) => ({
              id: i + 1, access_checked: true, accessible: true,
              summary: i === 0 ? "查询企业工商信息" : `接口 ${i + 1}`,
              method: "POST", path: `/v1/op/${i + 1}`,
            })),
          }
        : path.includes("/data-apis/list")
        ? { list: [{ id: 42, enabled: true, table_permission_checked: true, table_accessible: true }] }
        : path.includes("runtime-contract")
          ? {
              id: "42", name: "销售日报", description: "按日期查销售汇总", available: true, risk: "low",
              input_schema: { type: "object", properties: { date: { type: "string" } }, required: ["date"] },
            }
          : { limit: 100, result: { columns: ["amount"], rows: [{ amount: 12345 }], count: 1 } };
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ code: 0, message: "ok", data }));
    });
  });

  const root = mkdtempSync(join(tmpdir(), "aiclaw-smoke-"));
  const workdir = join(root, "work");
  const dataHome = join(root, "agent-home");
  mkdirSync(workdir, { recursive: true });
  mkdirSync(dataHome, { recursive: true });
  const instancePath = join(root, "instance.json");
  // assets 是**库**主键（云鉴数据源），不是数据 API 主键；
  // claw-mcp 会把它展开成库里的一个个 API。这里库 7 下面有 API 42。
  writeFileSync(
    instancePath,
    JSON.stringify({ type: "data-api", label: "销售口径数据", assets: ["7"], tool_prefix: "sales" }),
  );

  // 第三个实例：联网搜索。它绑的是**一个操作**（不是一个源），
  // 装出来应当正好是一个叫 search 的工具。
  const webInstancePath = join(root, "instance-web.json");
  writeFileSync(
    webInstancePath,
    JSON.stringify({ type: "web-search", label: "联网搜索", assets: ["77"], tool_prefix: "" }),
  );

  // 第二个实例：一个有 161 个接口的 API 源，用来验目录模式。
  const bigInstancePath = join(root, "instance-big.json");
  writeFileSync(
    bigInstancePath,
    JSON.stringify({ type: "api-operation", label: "API 服务", assets: ["9"] }),
  );

  const repo = resolve(import.meta.dirname, "..");
  const agentBin = join(repo, "tools/claw-agent/claw-agent");
  const mcpBin = join(repo, "tools/claw-mcp/claw-mcp");
  for (const bin of [agentBin, mcpBin]) {
    if (spawnSync(bin, ["version"]).status !== 0) {
      record("二进制可执行", false, `${bin} 不可用，先 npm run build:go`);
      return 1;
    }
  }

  const client = new ClawAgentClient({
    command: agentBin,
    args: ["serve", `--data-home=${dataHome}`],
    env: { ...process.env, AICLAW_LLM_KEY: "sk-smoke", CLAW_URL: claw.url, CLAW_TOKEN: "bff-smoke" },
  });

  const events: { method: string; params: Record<string, unknown> }[] = [];
  let approvals = 0;
  client.on("notification", (n) => events.push(n as { method: string; params: Record<string, unknown> }));
  client.on("approval", (request) => {
    approvals++;
    client.respondApproval(request.id, true);
  });
  if (process.env.SMOKE_VERBOSE) client.on("stderr", (c) => process.stderr.write(`[claw-agent] ${c}`));

  try {
    const init = await client.start();
    record("initialize", init.dataHome === dataHome, `内置工具 ${init.tools.length} 个`);

    const session = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      workdir,
      approvalPolicy: "on-write",
      mcpServers: { sales_data: { command: mcpBin, args: ["serve", "--type=data-api", `--config=${instancePath}`] } },
    });
    // 状态串里带着上下文占用的估算，所以按前缀匹配而不是全等——
    // 那个数字随工具描述变，钉死它只会让这条冒烟变成每次改文案都要跟着改的负担。
    record(
      "claw-agent 拉起 claw-mcp",
      (session.mcpStatus?.sales_data ?? "").startsWith("已挂载 1 个工具"),
      session.mcpStatus?.sales_data ?? "（无状态）",
    );
    record("MCP 工具进入会话工具集", session.tools.includes(TOOL_NAME), session.tools.join(", "));

    await client.turnStart(session.sessionId, "查一下 2026-09-20 的销售额");

    // 等 turn/completed，最多 20 秒。
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline && !events.some((e) => e.method === "turn/completed")) {
      await new Promise((r) => setTimeout(r, 100));
    }

    const completed = events.find((e) => e.method === "turn/completed");
    record("轮次完成", Boolean(completed) && !completed?.params.error, completed ? JSON.stringify(completed.params.usage) : "超时未完成");

    record("模型看到了 MCP 工具定义", toolNamesSeen[0]?.includes(TOOL_NAME) ?? false, `第一次请求带 ${toolNamesSeen[0]?.length ?? 0} 个工具`);
    // 数据 API 是只读的，claw-mcp 会给它标 readOnlyHint，客户端据此不问。
    // 这条如果变成 1，说明只读标注没传到、或者客户端没认——那会让每次查询
    // 都弹一个框。
    record("只读的内部平台工具不弹审批", approvals === 0, `${approvals} 次`);

    const toolDone = events.find((e) => e.method === "item/completed" && (e.params.item as { kind?: string })?.kind === "toolCall");
    const toolResult = String((toolDone?.params.item as { toolResult?: string })?.toolResult ?? "");
    record("内部平台结果经 claw-mcp 回到 agent", toolResult.includes("12345") && !(toolDone?.params.item as { toolFailed?: boolean })?.toolFailed, toolResult.slice(0, 80));

    const finalMsg = [...events].reverse().find((e) => e.method === "item/completed" && (e.params.item as { kind?: string })?.kind === "agentMessage");
    const finalText = String((finalMsg?.params.item as { text?: string })?.text ?? "");
    record("模型基于工具结果作答", finalText.includes("12345"), finalText);

    const order = events.map((e) => e.method).filter((m) => m.startsWith("turn/"));
    record("事件顺序", order.join(",") === "turn/started,turn/completed", order.join(" → "));

    // ---------- 目录模式 ----------
    //
    // 一个 161 个接口的源直挂就是 161 个工具、约 110KB 的 schema，而工具清单
    // 每次请求都整份重发、压缩碰不到它——小窗口的模型开局就发不出请求。
    // 这里验的是「超了会自动改成三件套」这条真的在端到端链路上成立。
    const big = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      workdir,
      approvalPolicy: "on-write",
      mcpServers: {
        api_svc: {
          command: mcpBin,
          args: ["serve", "--type=api-operation", `--config=${bigInstancePath}`],
          trusted: true,
        },
      },
    });
    // 会话工具集是排过序的，所以比集合而不是比顺序。
    const catalogTools = big.tools.filter((name) => name.startsWith("api_svc__")).sort();
    record(
      "161 个接口改挂目录三件套",
      catalogTools.join(",") === "api_svc__call,api_svc__describe,api_svc__search",
      catalogTools.join(", ") || "（一个都没挂）",
    );
    // 状态串里那个估算是这件事唯一能被用户看见的地方。目录模式下它必须是
    // 个位数 K 以内——直挂 161 个接口那边是 30K 量级。
    const status = big.mcpStatus?.api_svc ?? "";
    const matched = /约占 ([\d.]+)(K?) token/.exec(status);
    const tokens = matched ? Number(matched[1]) * (matched[2] === "K" ? 1000 : 1) : NaN;
    record(
      "目录模式的上下文占用是常数级",
      status.includes("已挂载 3 个工具") && Number.isFinite(tokens) && tokens < 2000,
      status || "（无状态）",
    );

    // ---------- 开会话不该把内部平台重问一遍 ----------
    //
    // 每开一个会话都重新拉起 MCP server 的话，claw-mcp 会把「这个库里有哪些
    // API、每个的运行契约是什么」再问一遍内部平台——本机测不出来（假内部平台在
    // localhost 上是零延迟），但真机上那是每个 server 十几次串行 HTTPS，
    // 切一次会话就卡一两秒。这里量的是**打了多少次内部平台**，不是耗时。
    const before = clawCalls;
    for (let i = 0; i < 3; i++) {
      await client.sessionStart({
        model: { baseUrl: model.url, model: "fake" },
        workdir,
        approvalPolicy: "on-write",
        mcpServers: {
          pooled: { command: mcpBin, args: ["serve", "--type=data-api", `--config=${instancePath}`] },
        },
      });
    }
    // 一次挂载要打 2 次内部平台（展开库 + 取运行契约）。复用之后三次总共还是 2 次；
    // 不复用就是 6 次。这个数字比耗时可靠：本机的假内部平台是零延迟，量不出时间差。
    const calls = clawCalls - before;
    record(
      "重复开会话复用已经起好的 MCP server",
      calls <= 2,
      `三次共打了 ${calls} 次内部平台（不复用是 6 次）`,
    );

    // ---------- 代码模式 ----------
    //
    // 把工具收进一个 exec，模型改写 JavaScript。这里验的是整条链真的通：
    // 会话里只剩 exec、脚本里 await 一个真的 claw-mcp 工具、拿到真实结果。
    // 本机测不出它省多少 token（那要真模型），但「能不能用」必须在这里钉住。
    const coded = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      workdir,
      approvalPolicy: "on-write",
      codeMode: true,
      mcpServers: {
        sales_data: { command: mcpBin, args: ["serve", "--type=data-api", `--config=${instancePath}`] },
      },
    });
    record(
      "代码模式下只剩一个 exec 工具",
      coded.tools.length === 1 && coded.tools[0] === "exec",
      coded.tools.join(", "),
    );

    // 脚本只把需要的那个数字挑出来返回——整份响应留在脚本里，不进上下文。
    // 这正是代码模式省上下文的方式，所以断言也照这个形状写。
    scriptToRun = `
      const result = await tools.${TOOL_NAME.replace(/[^A-Za-z0-9_]/g, "_")}({ date: "2026-09-20" });
      return "脚本拿到：" + result.result.rows[0].amount;
    `;
    events.length = 0;
    await client.turnStart(coded.sessionId, "用脚本查一下");
    const codeDeadline = Date.now() + 20_000;
    while (Date.now() < codeDeadline && !events.some((e) => e.method === "turn/completed")) {
      await new Promise((r) => setTimeout(r, 100));
    }
    const scriptResult = events.find(
      (e) =>
        e.method === "item/completed" &&
        (e.params.item as { toolName?: string })?.toolName === "exec",
    );
    const scriptText = String((scriptResult?.params.item as { toolResult?: string })?.toolResult ?? "");
    record(
      "脚本里 await 真实工具并拿到结果",
      scriptText.includes("脚本拿到：") && scriptText.includes("12345"),
      scriptText.slice(0, 90) || "（没有 exec 结果）",
    );

    // ---------- 会话工作区 ----------
    //
    // 工作区是按会话的，而且**可以不设**。早先「没配工作目录」是起不来的，
    // 用户刚打开应用还没想好在哪儿干活，就被一个配置项挡在门外。
    const noWorkspace = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      approvalPolicy: "on-write",
    });
    record(
      "没有工作区也能开会话",
      Boolean(noWorkspace.sessionId) && !noWorkspace.workspace,
      noWorkspace.workspace || "（未设置）",
    );

    // 中途指一个：用户聊着聊着才想起「这事要在那个仓库里做」。
    await client.sessionConfigure(noWorkspace.sessionId, { baseUrl: "", model: "" }, workdir);
    const afterSet = await client.sessionResume(noWorkspace.sessionId);
    record("中途设置工作区并被记住", afterSet.workspace === workdir, afterSet.workspace ?? "（空）");

    // ---------- 联网搜索 ----------
    //
    // 它本来就在某个 API 源的一百多个接口里，那个源一挂就落进目录模式，
    // 模型很难想到「先去目录里搜一个搜索接口」。单拎成一个能力之后，
    // 挂出来必须是一个名字固定、一眼能认出来的工具。
    const web = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      workdir,
      approvalPolicy: "on-write",
      mcpServers: {
        web: {
          command: mcpBin,
          args: ["serve", "--type=web-search", `--config=${webInstancePath}`],
          trusted: true,
        },
      },
    });
    record(
      "联网搜索挂成一个 web__search 工具",
      web.tools.includes("web__search"),
      web.tools.filter((name) => name.startsWith("web__")).join(", ") || "（一个都没挂）",
    );

    // ---------- 试连 ----------
    //
    // 配置页要在不开会话的情况下回答「这个 server 通不通、有哪些工具」。
    const probed = await client.mcpProbe({
      command: mcpBin,
      args: ["serve", "--type=data-api", `--config=${instancePath}`],
    });
    record(
      "mcp/probe 列出工具",
      probed.ok && (probed.tools ?? []).some((t) => t.name === "sales_42"),
      probed.ok ? (probed.tools ?? []).map((t) => t.name).join(", ") : String(probed.error),
    );
    const bad = await client.mcpProbe({ url: "http://127.0.0.1:1/mcp" });
    record(
      "试连失败是结果不是异常",
      bad.ok === false && Boolean(bad.error),
      bad.error ?? "（没有原因）",
    );

    // ---------- 恢复会话按当前配置重挂 ----------
    //
    // 存档里的 MCP server 是建会话那一刻的，而应用一启动就接着上次的会话。
    // 不按当前配置重挂的话，用户新加的 server 永远挂不上——「配了没用」。
    // 实际踩过，所以这条钉在端到端这一层。
    const plain = await client.sessionStart({
      model: { baseUrl: model.url, model: "fake" },
      workdir,
      approvalPolicy: "on-write",
    });
    record("新会话先不挂任何 MCP", !plain.tools.some((n) => n.startsWith("later__")), plain.tools.length + " 个工具");

    const resumed = await client.sessionResume(plain.sessionId, {
      mcpServers: {
        later: { command: mcpBin, args: ["serve", "--type=data-api", `--config=${instancePath}`] },
      },
      skillDirs: [],
      memoryFile: "",
      enableComputerUse: false,
      approvalPolicy: "on-write",
    });
    record(
      "恢复会话时挂上后来加的 MCP server",
      resumed.tools.includes("later__sales_42"),
      resumed.mcpStatus?.later ?? "（无状态）",
    );

    return steps.every((s) => s.ok) ? 0 : 1;
  } catch (error) {
    record("unexpected", false, String(error));
    return 1;
  } finally {
    await client.stop();
    model.close();
    claw.close();
  }
}

const code = await main();
console.log(`\n${steps.filter((s) => s.ok).length}/${steps.length} 通过${code === 0 ? "" : " —— 有失败项"}`);
process.exit(code);
