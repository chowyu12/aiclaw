/**
 * 内核冒烟：agent-client → claw-agent → 应用库（模型服务、插件、通道）。
 *
 * 不碰模型、不碰网络、不需要凭据，用一个临时目录当应用数据目录，所以能进 CI。
 * 它验的是桌面冒烟验不到的那一段——桌面冒烟不拉内核。第 3 步就漏过一次：
 * 两个 sqlite 驱动同名注册，内核一启动就 panic，而桌面冒烟 8/8 全绿。
 *
 * 用法：
 *   npm run build && npm run smoke
 */
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { ClawAgentClient } from "../packages/agent-client/dist/index.js";

const steps: { name: string; ok: boolean; detail: string }[] = [];
function record(name: string, ok: boolean, detail = ""): void {
  steps.push({ name, ok, detail });
  console.log(`${ok ? "  ok" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
}

async function main(): Promise<number> {
  const repo = resolve(import.meta.dirname, "..");
  const bin = join(repo, "tools/claw-agent", process.platform === "win32" ? "claw-agent.exe" : "claw-agent");
  if (!existsSync(bin)) {
    record("二进制可执行", false, `${bin} 不存在，先 npm run build:go`);
    return 1;
  }
  const root = mkdtempSync(join(tmpdir(), "aiclaw-kernel-smoke-"));
  const client = new ClawAgentClient({
    command: bin,
    args: ["serve", `--data-home=${join(root, "agent")}`, `--app-db=${join(root, "aiclaw.db")}`],
    env: { ...process.env },
  });
  const stderr: string[] = [];
  client.on("stderr", (chunk) => stderr.push(String(chunk)));

  try {
    const init = await client.start();
    record("initialize", init.tools.length > 0, `内置工具 ${init.tools.length} 个`);

    // 内置插件在第一次启动时同步进库，全部停用。
    const plugins = await client.pluginList();
    const ids = plugins.map((p) => p.pluginId).sort();
    record(
      "内置插件同步进库且默认停用",
      ids.join(",") === "aiclaw.computer-use,aiclaw.email,aiclaw.wechat,aiclaw.wecom" && plugins.every((p) => !p.enabled),
      ids.join(", "),
    );
    record("插件文件落在 <root>/plugins", existsSync(join(root, "plugins", "wechat", "plugin.json")));

    // 模型服务：建一个，Key 只回 apiKeySet。
    const provider = await client.providerCreate({
      name: "冒烟", type: "openai-compatible", baseUrl: "http://127.0.0.1:1/v1", apiKey: "sk-smoke", models: ["m1"],
    });
    const listed = await client.providerList();
    record(
      "模型服务写入并回读，Key 不回传",
      listed.length === 1 && listed[0]!.apiKeySet && !JSON.stringify(listed).includes("sk-smoke"),
      JSON.stringify(listed[0]),
    );

    // 渠道插件的配置按连接存：没有配齐的连接时不能启用；连接各自一套凭据，秘密只回 isSet。
    const wecom = plugins.find((p) => p.pluginId === "aiclaw.wecom")!;
    let refused = "";
    try {
      await client.pluginToggle(wecom.uuid, true);
    } catch (error) {
      refused = String(error);
    }
    record("没有可用连接的渠道插件拒绝启用", refused.includes("连接"), refused.slice(0, 80));
    const botA = await client.connectionCreate(wecom.uuid, "机器人 A");
    const botB = await client.connectionCreate(wecom.uuid, "机器人 B");
    record("新建的连接缺凭据", botA.missingConfig.includes("bot_id") && botA.missingConfig.includes("bot_secret"), JSON.stringify(botA));
    await client.pluginSetConfig(wecom.uuid, "bot_id", "bot-a", botA.uuid);
    await client.pluginSetConfig(wecom.uuid, "bot_secret", "s3cret-a", botA.uuid);
    await client.pluginSetConfig(wecom.uuid, "bot_id", "bot-b", botB.uuid);
    const fieldsA = await client.pluginConfig(wecom.uuid, botA.uuid);
    const fieldsB = await client.pluginConfig(wecom.uuid, botB.uuid);
    const secret = fieldsA.find((f) => f.key === "bot_secret");
    record(
      "秘密配置只报 isSet",
      secret?.isSet === true && secret.value === undefined && !JSON.stringify(fieldsA).includes("s3cret"),
      JSON.stringify(secret),
    );
    record(
      "两个连接的凭据互不影响",
      fieldsA.find((f) => f.key === "bot_id")?.value === "bot-a" && fieldsB.find((f) => f.key === "bot_id")?.value === "bot-b" &&
        fieldsB.find((f) => f.key === "bot_secret")?.isSet === false,
      `${fieldsA.find((f) => f.key === "bot_id")?.value} / ${fieldsB.find((f) => f.key === "bot_id")?.value}`,
    );
    await client.connectionRename(botB.uuid, "客服机器人");
    const listed2 = (await client.pluginList()).find((p) => p.uuid === wecom.uuid)!;
    record(
      "插件列出各连接与它们缺的配置",
      listed2.connections.length === 2 && listed2.connections.some((c) => c.name === "客服机器人" && c.missingConfig.includes("bot_secret")) &&
        listed2.missingConfig.length === 0,
      JSON.stringify(listed2.connections),
    );
    await client.connectionDelete(botB.uuid);
    const listed3 = (await client.pluginList()).find((p) => p.uuid === wecom.uuid)!;
    record("删掉一个连接，另一个还在", listed3.connections.length === 1 && listed3.connections[0]!.uuid === botA.uuid);

    // computer use 的开关就是插件：启用后贡献里 computerUse 为真。
    const cu = plugins.find((p) => p.pluginId === "aiclaw.computer-use")!;
    const before = await client.pluginContributions();
    await client.pluginToggle(cu.uuid, true);
    const after = await client.pluginContributions();
    record("启用 computer-use 插件即打开 computer use", !before.computerUse && after.computerUse);

    // 邮件插件：没填邮箱不能启用；填了之后「测试」给出按域名识别的服务器，启用后贡献里 email 为真。
    const email = plugins.find((p) => p.pluginId === "aiclaw.email")!;
    let emailRefused = "";
    try {
      await client.pluginToggle(email.uuid, true);
    } catch (error) {
      emailRefused = String(error);
    }
    record("没填邮箱的邮件插件拒绝启用", emailRefused.includes("address") || emailRefused.includes("password"), emailRefused.slice(0, 80));
    await client.pluginSetConfig(email.uuid, "address", "smoke@qq.com");
    await client.pluginSetConfig(email.uuid, "password", "not-a-real-code");
    // 端口指到本机一个没人听的口，测试必然失败——要验的是它失败得快、说得清。
    await client.pluginSetConfig(email.uuid, "imap_host", "127.0.0.1");
    await client.pluginSetConfig(email.uuid, "imap_port", "1");
    const tested = await client.emailTest(email.uuid);
    record(
      "测试邮箱：连不上时说清楚，发信服务器按域名识别",
      !tested.ok && (tested.error ?? "").includes("收信服务器") && tested.smtpHost === "smtp.qq.com" && tested.smtpPort === 465,
      `${tested.error?.slice(0, 50)} · ${tested.smtpHost}:${tested.smtpPort}`,
    );
    await client.pluginToggle(email.uuid, true);
    const withEmail = await client.pluginContributions();
    record("启用邮件插件后贡献里有邮件", withEmail.email === true && after.email !== true);
    const mailSession = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
      enableEmail: true,
    });
    record(
      "邮件插件开着的会话挂上邮件工具",
      ["email_list", "email_read", "email_attachment", "email_send", "email_reply"].every((name) => mailSession.tools.includes(name)),
      mailSession.tools.filter((name) => name.startsWith("email_")).join(", "),
    );
    // 已经开着的会话：启用邮件插件后宿主会带着 enableEmail 重挂当前会话，工具要当场出现。
    const earlier = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
    });
    const remounted = await client.sessionResume(earlier.sessionId, {
      mcpServers: {}, skillDirs: [], memoryFile: "", enableComputerUse: false,
      disableSandbox: false, codeMode: false, approvalPolicy: "never", enableEmail: true,
    });
    record(
      "启用邮件插件后，当前会话重挂即可用",
      !earlier.tools.includes("email_send") && remounted.tools.includes("email_send"),
      `之前 ${earlier.tools.filter((n) => n.startsWith("email_")).length} 个 → 之后 ${remounted.tools.filter((n) => n.startsWith("email_")).length} 个`,
    );

    // 会话按 providerId 选模型服务；端点打不通，但错误应在开会话之后（挂载阶段不打模型）。
    const session = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
    });
    record("按模型服务 id 开会话", session.providerId === provider.id, `session ${session.sessionId}`);
    let missing = "";
    try {
      await client.sessionStart({ model: { providerId: 999, baseUrl: "", model: "m1" } });
    } catch (error) {
      missing = String(error);
    }
    record("不存在的模型服务开不了会话", missing.includes("999"), missing.slice(0, 80));

    record("通道与授权列表可读", (await client.channelStatus()).length === 0 && (await client.channelBindings()).length === 0);

    // 语音输入：没配听写角色时说清楚去哪儿配；配了的话，录音经内核送到听写接口，文字原样回来。
    let noRole = "";
    try {
      await client.audioTranscribe({ audio: "UklGRg==", name: "voice.wav", role: { providerId: 0, model: "" } });
    } catch (error) {
      noRole = String(error);
    }
    record("语音输入：没配听写模型时说清楚", noRole.includes("听写"), noRole.slice(0, 60));
    let heard = { contentType: "", bytes: 0, model: "" };
    const asr = createServer((request, response) => {
      const chunks: Buffer[] = [];
      request.on("data", (chunk: Buffer) => chunks.push(chunk));
      request.on("end", () => {
        const body = Buffer.concat(chunks).toString("latin1");
        heard = {
          contentType: String(request.headers["content-type"] ?? ""),
          bytes: body.includes("RIFF") ? body.length : 0,
          model: /name="model"\r\n\r\n([^\r]+)/.exec(body)?.[1] ?? "",
        };
        response.writeHead(200, { "Content-Type": "application/json" });
        response.end(JSON.stringify({ text: " 明天上午十点开会 " }));
      });
    });
    await new Promise<void>((done) => asr.listen(0, "127.0.0.1", done));
    const asrPort = (asr.address() as { port: number }).port;
    const asrProvider = await client.providerCreate({
      name: "听写", type: "openai-compatible", baseUrl: `http://127.0.0.1:${asrPort}/v1`, apiKey: "sk-asr", models: ["whisper-1"],
    });
    const wav = Buffer.concat([Buffer.from("RIFF"), Buffer.alloc(40), Buffer.alloc(3200)]).toString("base64");
    const spoken = await client.audioTranscribe({ audio: wav, name: "voice.wav", role: { providerId: asrProvider.id, model: "whisper-1" } });
    asr.close();
    record(
      "语音输入：录音送到听写接口，文字回到宿主",
      spoken === "明天上午十点开会" && heard.contentType.startsWith("multipart/form-data") && heard.bytes > 0 && heard.model === "whisper-1",
      `${spoken} · ${heard.model}`,
    );

    // 多模态：没配角色时那几个工具根本不该出现——给模型一个用不了的工具，
    // 它会调、会失败、会重试，而失败原因它无从修复。
    const bare = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
    });
    record(
      "没配角色时多模态工具不注册",
      !["generate_image", "transcribe_audio", "speak"].some((name) => bare.tools.includes(name)),
      bare.tools.join(", "),
    );

    const withRoles = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
      roles: {
        image: { providerId: provider.id, model: "wan" },
        stt: { providerId: provider.id, model: "asr" },
      },
    });
    record(
      "配了角色就只注册那几个",
      withRoles.tools.includes("generate_image") &&
        withRoles.tools.includes("transcribe_audio") &&
        !withRoles.tools.includes("speak"),
      withRoles.tools.filter((t) => ["generate_image", "transcribe_audio", "speak"].includes(t)).join(", ") || "一个都没有",
    );

    // 搜索引擎：建一个、Key 不回传；再把内核自带的搜索 MCP server 挂进会话，
    // 工具应当以 web_search__web_search 出现（挂载只列工具，不打网络）。
    const engine = await client.searchCreate({ provider: "tavily", apiKey: "tvly-smoke", enabled: true });
    const engines = await client.searchList();
    record(
      "搜索引擎写入并回读，Key 不回传",
      engines.length === 1 && engines[0]!.apiKeySet && engines[0]!.enabled && !JSON.stringify(engines).includes("tvly-smoke"),
      `${engine.name} (${engine.provider})`,
    );
    const withSearch = await client.sessionStart({
      model: { providerId: provider.id, baseUrl: "", model: "m1" },
      approvalPolicy: "never",
      mcpServers: {
        web_search: { command: bin, args: ["mcp-search", `--app-db=${join(root, "aiclaw.db")}`], trusted: true },
      },
    });
    record(
      "内置搜索 MCP server 能挂上",
      withSearch.tools.includes("web_search__web_search"),
      `mcpStatus=${JSON.stringify(withSearch.mcpStatus)}`,
    );

    // 向用户提问（ask_user）与用量：一个本机的假模型服务先调 ask_user，
    // 拿到工具结果后把它原样说出来——走的是真的内核、真的客户端、真的协议帧。
    const fake = createServer((request, response) => {
      let body = "";
      request.on("data", (chunk) => (body += chunk));
      request.on("end", () => {
        const messages = (JSON.parse(body) as { messages: { role: string; content?: unknown }[] }).messages;
        const toolResult = messages.find((message) => message.role === "tool");
        response.writeHead(200, { "Content-Type": "text/event-stream" });
        const usage = `data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}\n\n`;
        if (!toolResult) {
          const call = {
            choices: [{ delta: { tool_calls: [{ index: 0, id: "c1", type: "function",
              function: { name: "ask_user", arguments: JSON.stringify({ question: "删哪些？", options: [{ label: "日志" }, { label: "缓存" }] }) } }] } }],
          };
          response.end(`data: ${JSON.stringify(call)}\n\n${usage}data: [DONE]\n\n`);
          return;
        }
        const text = { choices: [{ delta: { content: `收到：${String(toolResult.content)}` } }] };
        response.end(`data: ${JSON.stringify(text)}\n\n${usage}data: [DONE]\n\n`);
      });
    });
    await new Promise<void>((done) => fake.listen(0, "127.0.0.1", () => done()));
    const fakePort = (fake.address() as { port: number }).port;
    try {
      const asker = await client.providerCreate({
        name: "假模型", type: "openai-compatible", baseUrl: `http://127.0.0.1:${fakePort}/v1`, apiKey: "sk-fake", models: ["fake"],
      });
      const session = await client.sessionStart({ model: { providerId: asker.id, baseUrl: "", model: "fake" }, approvalPolicy: "on-write" });
      let asked = "";
      client.on("userInput", (question) => {
        asked = question.request.question;
        question.respond({ selected: ["缓存"], text: "别动 Downloads" });
      });
      let answer = "";
      const finished = new Promise<void>((done) => {
        client.on("notification", (note) => {
          const params = note.params as { sessionId?: string; item?: { kind?: string; text?: string } };
          if (params.sessionId !== session.sessionId) return;
          if (note.method === "item/completed" && params.item?.kind === "agentMessage") answer = params.item.text ?? "";
          if (note.method === "turn/completed") done();
        });
      });
      await client.turnStart(session.sessionId, "清理磁盘");
      await Promise.race([finished, new Promise((_, fail) => setTimeout(() => fail(new Error("这一轮 15 秒没跑完")), 15_000))]);
      record("ask_user：问题到了宿主", asked === "删哪些？", asked || "没收到提问");
      record(
        "ask_user：回答回到模型，这一轮接着跑完",
        answer.includes("用户选了：缓存") && answer.includes("别动 Downloads"),
        answer.slice(0, 80),
      );
      // 用量记录是后台写的（每秒一批）：等它落盘。
      await new Promise((done) => setTimeout(done, 1500));
      const usage = await client.usageSummary(1);
      record(
        "用量：这一轮的模型调用、token 与 ask_user 都记下了",
        usage.totals.modelCalls >= 2 && usage.totals.total >= 36 && usage.tools.some((tool) => tool.tool === "ask_user") && usage.days.length === 1,
        `模型 ${usage.totals.modelCalls} 次，token ${usage.totals.total}，工具 ${usage.tools.map((t) => t.tool).join("、")}`,
      );
    } finally {
      fake.close();
    }
  } catch (error) {
    record("unexpected", false, String(error));
  } finally {
    await client.stop().catch(() => undefined);
    rmSync(root, { recursive: true, force: true });
  }

  const failed = steps.filter((s) => !s.ok);
  if (failed.length > 0 && stderr.length > 0) {
    console.log("--- 内核 stderr ---");
    console.log(stderr.join("").split("\n").filter((l) => !l.includes("database connected")).join("\n"));
  }
  console.log(`${steps.length - failed.length}/${steps.length} 通过`);
  return failed.length === 0 ? 0 : 1;
}

process.exit(await main());
