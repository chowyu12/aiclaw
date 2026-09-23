/**
 * 发布前的场景测试：真拉起内核、真打模型，把用户会做的事各走一遍。
 *
 * 对话、读写文件、跑命令、审批拦截、看图、画图、朗读、听写、联网搜索、代码模式、
 * 两个会话同时跑。每一条都是「像用户那样说一句话，然后看结果对不对」——不看
 * 内部结构，只看产出：文件在不在、图是不是 PNG、回答里有没有那个词。
 *
 * **它花钱、也慢**（十几次模型调用，画图朗读各一次），所以不进 `make check`，
 * 只在发版前手动跑：`make scenarios`。单元测试与冒烟保证的是「代码没坏」，
 * 这一组保证的是「配上真模型之后这些事真的能做」——上一版就漏过：单元测试全绿，
 * 而画图工具打到的是一个 404 的地址。
 *
 * 用哪个模型：默认读桌面应用的配置（默认模型 + 四个多模态角色），所以跑之前把
 * 「配置」页填好就行；也可以用环境变量指定：
 *
 *   AICLAW_SCENARIO_CONFIG=<config.json 路径>     换一份配置
 *   AICLAW_SCENARIO_ONLY=看图,画图                只跑名字里含这些词的场景
 *
 * 凭据从 ~/.aiclaw/aiclaw.db 来，但**不碰它**：拷一份到临时目录、把插件全部停掉
 * （不然会再连一个企业微信机器人），内核对着那份跑。会话、生成的图与音频都落在
 * build/scenarios/<时间>/ 下，跑完留着，可以打开看。
 */
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { deflateSync } from "node:zlib";
import { ClawAgentClient } from "../packages/agent-client/dist/index.js";

// ---------- 配置 ----------

interface RoleModel {
  providerId: number;
  model: string;
}
interface DesktopConfig {
  providerId: number;
  model: string;
  roles?: Partial<Record<"vision" | "stt" | "tts" | "image", RoleModel>>;
}

function configPath(): string {
  if (process.env.AICLAW_SCENARIO_CONFIG) return process.env.AICLAW_SCENARIO_CONFIG;
  switch (process.platform) {
    case "darwin":
      return join(homedir(), "Library", "Application Support", "aiclaw", "config.json");
    case "win32":
      return join(process.env.APPDATA ?? join(homedir(), "AppData", "Roaming"), "aiclaw", "config.json");
    default:
      return join(process.env.XDG_CONFIG_HOME ?? join(homedir(), ".config"), "aiclaw", "config.json");
  }
}

function configured(role: RoleModel | undefined): role is RoleModel {
  return !!role && role.providerId > 0 && !!role.model;
}

/**
 * 角色选反了是最常见的配置错误（听写与朗读两格挨着），而上游的报错完全看不出来
 * ——把一个 ASR 模型当 TTS 用，百炼说的是「prompt 或 messages 必须有一个」。
 * 按模型名猜一下，猜中了就直说。
 */
function swapHint(role: "tts" | "stt", model: string): string {
  const name = model.toLowerCase();
  if (role === "tts" && /asr|whisper|transcribe/.test(name)) return `（${model} 看起来是听写模型，朗读和听写是不是选反了？）`;
  if (role === "stt" && /tts|speech/.test(name)) return `（${model} 看起来是朗读模型，朗读和听写是不是选反了？）`;
  return "";
}

// ---------- 结果 ----------

type Outcome = { name: string; status: "pass" | "fail" | "skip"; detail: string; ms: number };
const outcomes: Outcome[] = [];

function report(outcome: Outcome): void {
  outcomes.push(outcome);
  const tag = { pass: "  ok ", fail: "FAIL ", skip: "skip " }[outcome.status];
  const time = outcome.status === "skip" ? "" : ` (${(outcome.ms / 1000).toFixed(1)}s)`;
  console.log(`${tag} ${outcome.name}${time}${outcome.detail ? ` — ${outcome.detail}` : ""}`);
}

// ---------- 一轮对话 ----------

interface ToolCall {
  name: string;
  failed: boolean;
  result: string;
  artifacts: string[];
}
interface TurnResult {
  text: string;
  tools: ToolCall[];
  error: string;
  approvals: number;
}

/** 正在收事件的轮次，按 sessionId 分：两个会话同时跑时事件是交错到的。 */
const collectors = new Map<
  string,
  { result: TurnResult; done: () => void; approve: (request: { title: string }) => boolean }
>();

function attach(client: ClawAgentClient): void {
  client.on("notification", ({ method, params }) => {
    const p = params as Record<string, unknown>;
    const collector = collectors.get(String(p.sessionId ?? ""));
    if (!collector) return;
    const item = (p.item ?? {}) as Record<string, unknown>;
    switch (method) {
      case "item/completed":
        if (item.kind === "agentMessage" && typeof item.text === "string") {
          collector.result.text += item.text;
        } else if (item.kind === "toolCall") {
          collector.result.tools.push({
            name: String(item.toolName ?? ""),
            failed: !!item.toolFailed,
            result: typeof item.toolResult === "string" ? item.toolResult : "",
            artifacts: Array.isArray(item.artifacts) ? (item.artifacts as string[]) : [],
          });
        }
        return;
      case "turn/completed":
        if (typeof p.error === "string") collector.result.error = p.error;
        collector.done();
        return;
      case "error":
        collector.result.error = String(p.message ?? "error");
        collector.done();
        return;
      default:
        return;
    }
  });
  client.on("approval", (request) => {
    const collector = collectors.get(request.sessionId);
    const approved = collector ? collector.approve(request) : false;
    if (collector) collector.result.approvals++;
    client.respondApproval(request.id, approved);
  });
}

async function turn(
  client: ClawAgentClient,
  sessionId: string,
  text: string,
  options: {
    images?: string[];
    audioPaths?: string[];
    timeoutMs?: number;
    approve?: (request: { title: string }) => boolean;
  } = {},
): Promise<TurnResult> {
  const result: TurnResult = { text: "", tools: [], error: "", approvals: 0 };
  const finished = new Promise<void>((done) => {
    collectors.set(sessionId, { result, done, approve: options.approve ?? (() => true) });
  });
  const timeout = options.timeoutMs ?? 180_000;
  const timer = new Promise<void>((_, reject) =>
    setTimeout(() => reject(new Error(`${timeout / 1000}s 内没有跑完`)), timeout),
  );
  try {
    await client.turnStart(sessionId, text, options.images, options.audioPaths);
    await Promise.race([finished, timer]);
  } finally {
    collectors.delete(sessionId);
  }
  return result;
}

// ---------- 素材 ----------

/** 一张纯色 PNG。自己编码，免得为一张测试图拉一个依赖。 */
function solidPng(width: number, height: number, rgb: [number, number, number]): Buffer {
  const raw = Buffer.alloc((width * 3 + 1) * height);
  for (let y = 0; y < height; y++) {
    const row = y * (width * 3 + 1);
    raw[row] = 0; // filter: none
    for (let x = 0; x < width; x++) raw.set(rgb, row + 1 + x * 3);
  }
  const chunk = (type: string, data: Buffer): Buffer => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body));
    return Buffer.concat([length, body, crc]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; // bit depth
  header[9] = 2; // truecolor
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function crc32(data: Buffer): number {
  let crc = ~0;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return ~crc >>> 0;
}

function isPng(path: string): boolean {
  if (!existsSync(path)) return false;
  const head = readFileSync(path).subarray(0, 4);
  return head[0] === 0x89 && head.toString("ascii", 1, 4) === "PNG";
}

/** 把应用库拷到临时目录并停掉全部插件，内核对着这一份跑。 */
async function stageAppDB(root: string): Promise<string> {
  const source = join(homedir(), ".aiclaw", "aiclaw.db");
  if (!existsSync(source)) throw new Error(`没有应用库：${source}。先在应用里配好模型服务。`);
  const target = join(root, "aiclaw.db");
  cpSync(source, target);
  const key = join(homedir(), ".aiclaw", "secret.key");
  if (existsSync(key)) cpSync(key, join(root, "secret.key"));
  // 停插件：拷贝里的企业微信若仍是启用的，内核一起来就会再连一个机器人。
  const { DatabaseSync } = await import("node:sqlite");
  const db = new DatabaseSync(target);
  try {
    db.exec("UPDATE plugins SET enabled = 0");
  } finally {
    db.close();
  }
  return target;
}

// ---------- 场景 ----------

interface Context {
  client: ClawAgentClient;
  bin: string;
  appDB: string;
  root: string;
  config: DesktopConfig;
  /** 朗读场景留下的音频，听写场景拿来用。 */
  spokenAudio: string;
}

type Scenario = { name: string; run: (ctx: Context) => Promise<string | { skip: string }> };

function workspace(ctx: Context, name: string): string {
  const dir = join(ctx.root, "ws", name);
  mkdirSync(dir, { recursive: true });
  return dir;
}

async function openSession(
  ctx: Context,
  name: string,
  extra: Record<string, unknown> = {},
): Promise<{ id: string; dir: string }> {
  const dir = workspace(ctx, name);
  const started = await ctx.client.sessionStart({
    model: { providerId: ctx.config.providerId, baseUrl: "", model: ctx.config.model },
    workdir: dir,
    approvalPolicy: "bypass",
    instructions: "这是自动化场景测试。按字面完成任务，不要反问，不要解释。",
    ...extra,
  } as never);
  return { id: started.sessionId, dir };
}

const scenarios: Scenario[] = [
  {
    name: "对话：按要求回复",
    async run(ctx) {
      const session = await openSession(ctx, "chat");
      const result = await turn(ctx.client, session.id, "只回复两个字：收到");
      if (result.error) throw new Error(result.error);
      if (!result.text.includes("收到")) throw new Error(`回答里没有「收到」：${result.text.slice(0, 80)}`);
      return result.text.trim().slice(0, 20);
    },
  },
  {
    name: "文件：在工作区新建文件",
    async run(ctx) {
      const session = await openSession(ctx, "write");
      const result = await turn(
        ctx.client,
        session.id,
        "在工作区新建 hello.txt，内容只有一行：hello scenario。建好后回复「好了」。",
      );
      if (result.error) throw new Error(result.error);
      const path = join(session.dir, "hello.txt");
      if (!existsSync(path)) throw new Error("hello.txt 没有出现在工作区");
      const content = readFileSync(path, "utf8");
      if (!content.includes("hello scenario")) throw new Error(`内容不对：${content.slice(0, 60)}`);
      return `工具 ${result.tools.map((t) => t.name).join(",") || "（代码模式）"}`;
    },
  },
  {
    name: "文件：读工作区里的文件",
    async run(ctx) {
      const session = await openSession(ctx, "read");
      writeFileSync(join(session.dir, "notes.txt"), "今天的暗号是 蓝鲸-7731\n");
      const result = await turn(ctx.client, session.id, "读工作区里的 notes.txt，把里面的暗号原样回复，别加别的字。");
      if (result.error) throw new Error(result.error);
      if (!result.text.includes("蓝鲸-7731")) throw new Error(`没读出暗号：${result.text.slice(0, 80)}`);
      return "读到了暗号";
    },
  },
  {
    name: "命令：执行 shell 命令并回显",
    async run(ctx) {
      const session = await openSession(ctx, "exec");
      const result = await turn(ctx.client, session.id, "运行命令 echo scenario-ok-4821，把命令输出原样回复。");
      if (result.error) throw new Error(result.error);
      if (!result.text.includes("scenario-ok-4821")) throw new Error(`回答里没有命令输出：${result.text.slice(0, 80)}`);
      return "输出回来了";
    },
  },
  {
    name: "审批：严格档位下拒绝写入就真没写",
    async run(ctx) {
      const session = await openSession(ctx, "approval", { approvalPolicy: "always" });
      const result = await turn(
        ctx.client,
        session.id,
        "在工作区新建 denied.txt，内容随意。如果被拒绝就回复「被拒绝了」。",
        { approve: () => false },
      );
      if (result.approvals === 0) throw new Error("严格档位下应当先请求审批，但没有收到审批请求");
      if (existsSync(join(session.dir, "denied.txt"))) throw new Error("拒绝了却还是写了文件");
      return `拦下 ${result.approvals} 次审批`;
    },
  },
  {
    name: "代码模式：工具收进 exec 仍能干活",
    async run(ctx) {
      const session = await openSession(ctx, "codemode", { codeMode: true });
      const result = await turn(ctx.client, session.id, "运行命令 echo code-mode-5566，把输出原样回复。");
      if (result.error) throw new Error(result.error);
      if (!result.text.includes("code-mode-5566")) throw new Error(`回答里没有命令输出：${result.text.slice(0, 80)}`);
      const viaExec = result.tools.some((t) => t.name === "exec");
      if (!viaExec) throw new Error(`没有走 exec：${result.tools.map((t) => t.name).join(",")}`);
      return "exec 跑通";
    },
  },
  {
    name: "看图：识别贴进来的图片",
    async run(ctx) {
      const vision = ctx.config.roles?.vision;
      if (!configured(vision)) return { skip: "没配看图模型（配置 → 多模态 → 看图）" };
      const session = await openSession(ctx, "vision", { roles: { vision } });
      const image = solidPng(64, 64, [220, 20, 20]).toString("base64");
      const result = await turn(ctx.client, session.id, "这张图整体是什么颜色？只回答一个颜色词。", { images: [image] });
      if (result.error) throw new Error(result.error);
      const answer = result.text.toLowerCase();
      if (!answer.includes("红") && !answer.includes("red")) throw new Error(`没认出红色：${result.text.slice(0, 60)}`);
      return `看图模型 ${vision.model}`;
    },
  },
  {
    name: "画图：generate_image 出一张 PNG",
    async run(ctx) {
      const image = ctx.config.roles?.image;
      if (!configured(image)) return { skip: "没配画图模型（配置 → 多模态 → 画图）" };
      const session = await openSession(ctx, "image", { roles: { image } });
      const result = await turn(
        ctx.client,
        session.id,
        "用 generate_image 画一只坐着的橘猫，尺寸 1024x1024。画完只回复文件名。",
        { timeoutMs: 300_000 },
      );
      if (result.error) throw new Error(result.error);
      const call = result.tools.find((t) => t.name === "generate_image");
      if (!call) throw new Error(`没有调用 generate_image：${result.tools.map((t) => t.name).join(",")}`);
      if (call.failed) throw new Error(`generate_image 失败：${call.result.slice(0, 160)}`);
      const artifact = call.artifacts[0];
      if (!artifact) throw new Error("工具没有报告产出文件");
      const path = join(session.dir, artifact);
      if (!isPng(path)) throw new Error(`${artifact} 不是 PNG`);
      return `${artifact}（${image.model}）`;
    },
  },
  {
    name: "朗读：speak 出一段音频",
    async run(ctx) {
      const tts = ctx.config.roles?.tts;
      if (!configured(tts)) return { skip: "没配朗读模型（配置 → 多模态 → 朗读）" };
      const session = await openSession(ctx, "speak", { roles: { tts } });
      const result = await turn(ctx.client, session.id, "用 speak 工具把这句话读出来：「场景测试一二三」。读完只回复文件名。");
      if (result.error) throw new Error(result.error);
      const call = result.tools.find((t) => t.name === "speak");
      if (!call) throw new Error(`没有调用 speak：${result.tools.map((t) => t.name).join(",")}`);
      if (call.failed) throw new Error(`speak 失败：${call.result.slice(0, 160)}${swapHint("tts", tts.model)}`);
      const artifact = call.artifacts[0];
      if (!artifact) throw new Error("工具没有报告产出文件");
      const path = join(session.dir, artifact);
      if (!existsSync(path) || readFileSync(path).length < 1000) throw new Error(`${artifact} 不存在或太小`);
      ctx.spokenAudio = path;
      return `${artifact}（${tts.model}）`;
    },
  },
  {
    name: "听写：贴进来的音频自动转写",
    async run(ctx) {
      const stt = ctx.config.roles?.stt;
      if (!configured(stt)) return { skip: "没配听写模型（配置 → 多模态 → 听写）" };
      if (!ctx.spokenAudio) return { skip: "朗读场景没有留下音频，没东西可听写" };
      const session = await openSession(ctx, "transcribe", { roles: { stt } });
      const result = await turn(ctx.client, session.id, "这段音频说了什么？把转写内容原样回复。", {
        audioPaths: [ctx.spokenAudio],
      });
      if (result.error) throw new Error(result.error + swapHint("stt", stt.model));
      const compact = result.text.replace(/\s|[，,。.、]/g, "");
      if (!compact.includes("场景测试")) {
        throw new Error(`转写里没有「场景测试」：${result.text.slice(0, 80)}${swapHint("stt", stt.model)}`);
      }
      return `听写模型 ${stt.model}`;
    },
  },
  {
    name: "搜索：联网搜索工具被调用",
    async run(ctx) {
      const engines = await ctx.client.searchList();
      if (!engines.some((e) => e.enabled && e.apiKeySet)) return { skip: "没有启用且配了 Key 的搜索引擎" };
      const session = await openSession(ctx, "search", {
        mcpServers: { web_search: { command: ctx.bin, args: ["mcp-search", `--app-db=${ctx.appDB}`], trusted: true } },
      });
      const result = await turn(ctx.client, session.id, "用搜索工具查「阿里云百炼」，把第一条结果的标题回复我。");
      if (result.error) throw new Error(result.error);
      const call = result.tools.find((t) => t.name.startsWith("web_search"));
      if (!call) throw new Error(`没有调用搜索工具：${result.tools.map((t) => t.name).join(",")}`);
      if (call.failed) throw new Error(`搜索失败：${call.result.slice(0, 160)}`);
      return result.text.trim().slice(0, 40);
    },
  },
  {
    name: "标注：按两份公开能力表自动标记",
    async run(ctx) {
      // 建一个临时服务，清单里放几个两份表各自才有的模型，看标出来对不对。
      // 这一条打的是 models.dev 与 LiteLLM，不打模型服务。
      const provider = await ctx.client.providerCreate({
        name: "场景-标注",
        type: "openai-compatible",
        baseUrl: "http://127.0.0.1:1/v1",
        apiKey: "sk-scenario",
        models: ["whisper-1", "gpt-4o-mini-tts", "dall-e-3", "qwen-image-3.0", "qwen3-vl-plus", "完全不存在的模型"],
      });
      const result = await ctx.client.providerAutoMark(provider.id);
      if (result.note) throw new Error(`有一份表没拉到：${result.note}`);
      const marks = new Map(result.provider.models.map((entry) => {
        const [name, rest = ""] = entry.split("#");
        return [name, rest.split("@")[0]!];
      }));
      const expect: Record<string, string> = {
        "whisper-1": "stt", "gpt-4o-mini-tts": "tts", "dall-e-3": "image", "qwen3-vl-plus": "vision",
      };
      for (const [name, want] of Object.entries(expect)) {
        if (marks.get(name) !== want) throw new Error(`${name} 标成了「${marks.get(name) ?? ""}」，应为 ${want}`);
      }
      // 画图模型接受图片输入是为了改图，不能因此被标成看图模型——上一版就是这么错的。
      const qwenImage = marks.get("qwen-image-3.0") ?? "";
      if (!qwenImage.includes("image") || qwenImage.includes("vision")) {
        throw new Error(`qwen-image-3.0 标成了「${qwenImage}」，应为 image 且不含 vision`);
      }
      if (result.unmatched !== 1) throw new Error(`应有 1 个查不到，实际 ${result.unmatched}`);
      return `查到 ${result.matched} 个，两份表都在`;
    },
  },
  {
    name: "并发：两个会话同时跑互不串",
    async run(ctx) {
      const [a, b] = await Promise.all([openSession(ctx, "par-a"), openSession(ctx, "par-b")]);
      const [ra, rb] = await Promise.all([
        turn(ctx.client, a.id, "只回复一个字：甲"),
        turn(ctx.client, b.id, "只回复一个字：乙"),
      ]);
      if (ra.error || rb.error) throw new Error(ra.error || rb.error);
      if (!ra.text.includes("甲") || ra.text.includes("乙")) throw new Error(`甲会话的回答不对：${ra.text.slice(0, 40)}`);
      if (!rb.text.includes("乙") || rb.text.includes("甲")) throw new Error(`乙会话的回答不对：${rb.text.slice(0, 40)}`);
      return "两边各得其所";
    },
  },
];

// ---------- 主流程 ----------

async function main(): Promise<number> {
  const repo = resolve(import.meta.dirname, "..");
  const bin = join(repo, "tools/claw-agent", process.platform === "win32" ? "claw-agent.exe" : "claw-agent");
  if (!existsSync(bin)) {
    console.error(`${bin} 不存在，先 make build-go`);
    return 1;
  }
  const path = configPath();
  if (!existsSync(path)) {
    console.error(`没有桌面配置 ${path}。先在应用的「配置」页选好默认模型，或用 AICLAW_SCENARIO_CONFIG 指一份。`);
    return 1;
  }
  const config = JSON.parse(readFileSync(path, "utf8")) as DesktopConfig;
  if (!config.providerId || !config.model) {
    console.error("配置里没有默认模型。");
    return 1;
  }

  const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
  const root = join(repo, "build", "scenarios", stamp);
  mkdirSync(root, { recursive: true });
  const appDB = await stageAppDB(root);

  const client = new ClawAgentClient({
    command: bin,
    args: ["serve", `--data-home=${join(root, "agent")}`, `--app-db=${appDB}`],
    env: { ...process.env },
  });
  const stderr: string[] = [];
  client.on("stderr", (chunk) => stderr.push(String(chunk)));
  attach(client);

  const only = (process.env.AICLAW_SCENARIO_ONLY ?? "").split(",").map((s) => s.trim()).filter(Boolean);
  console.log(`模型：${config.model}（服务 ${config.providerId}）  产物：${root}`);
  const roles = Object.entries(config.roles ?? {})
    .filter(([, role]) => configured(role))
    .map(([name, role]) => `${name}=${role!.model}`);
  console.log(`角色：${roles.join("  ") || "都没配"}`);
  console.log();

  const ctx: Context = { client, bin, appDB, root, config, spokenAudio: "" };
  try {
    await client.start();
    for (const scenario of scenarios) {
      if (only.length > 0 && !only.some((word) => scenario.name.includes(word))) continue;
      const started = Date.now();
      try {
        const outcome = await scenario.run(ctx);
        if (typeof outcome === "object") {
          report({ name: scenario.name, status: "skip", detail: outcome.skip, ms: 0 });
        } else {
          report({ name: scenario.name, status: "pass", detail: outcome, ms: Date.now() - started });
        }
      } catch (error) {
        report({ name: scenario.name, status: "fail", detail: String(error instanceof Error ? error.message : error), ms: Date.now() - started });
      }
    }
  } finally {
    await client.stop().catch(() => undefined);
  }

  const failed = outcomes.filter((o) => o.status === "fail");
  const skipped = outcomes.filter((o) => o.status === "skip");
  console.log();
  if (failed.length > 0) {
    const noise = stderr.join("").split("\n").filter((line) => line && !line.includes("database connected"));
    if (noise.length > 0) {
      console.log("--- 内核 stderr（最后 40 行）---");
      console.log(noise.slice(-40).join("\n"));
      console.log();
    }
  }
  console.log(
    `${outcomes.length - failed.length - skipped.length} 通过，${failed.length} 失败，${skipped.length} 跳过。产物在 ${root}`,
  );
  return failed.length === 0 ? 0 : 1;
}

process.exit(await main());
