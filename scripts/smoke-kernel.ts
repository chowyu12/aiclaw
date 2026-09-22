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
      ids.join(",") === "aiclaw.computer-use,aiclaw.wechat,aiclaw.wecom" && plugins.every((p) => !p.enabled),
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

    // 缺必填配置时不能启用；填了秘密之后只回 isSet。
    const wecom = plugins.find((p) => p.pluginId === "aiclaw.wecom")!;
    let refused = "";
    try {
      await client.pluginToggle(wecom.uuid, true);
    } catch (error) {
      refused = String(error);
    }
    record("缺配置的插件拒绝启用", refused.includes("bot_id"), refused.slice(0, 80));
    await client.pluginSetConfig(wecom.uuid, "bot_secret", "s3cret");
    const fields = await client.pluginConfig(wecom.uuid);
    const secret = fields.find((f) => f.key === "bot_secret");
    record(
      "秘密配置只报 isSet",
      secret?.isSet === true && secret.value === undefined && !JSON.stringify(fields).includes("s3cret"),
      JSON.stringify(secret),
    );

    // computer use 的开关就是插件：启用后贡献里 computerUse 为真。
    const cu = plugins.find((p) => p.pluginId === "aiclaw.computer-use")!;
    const before = await client.pluginContributions();
    await client.pluginToggle(cu.uuid, true);
    const after = await client.pluginContributions();
    record("启用 computer-use 插件即打开 computer use", !before.computerUse && after.computerUse);

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
