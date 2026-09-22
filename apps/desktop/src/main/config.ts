import { app, safeStorage } from "electron";
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

import { parseCapabilityChoices, DEFAULT_CONFIG, normalizeConfig } from "./config-defaults.js";

/**
 * 配置分两层落盘：
 *
 * - config.json     非敏感配置，明文，用户可以直接看、直接改；
 * - credentials.bin 凭据，safeStorage 加密（macOS Keychain / Windows DPAPI）。
 *
 * 分开放是刻意的：出问题时用户能把 config.json 发给支持同事，
 * 而不用担心里面夹着一把等同登录态的 BFF Key。
 * 诊断包同理——只带 config.json，绝不带 credentials.bin。
 */

export interface AppConfig {
  /** airouter 的 OpenAI 兼容地址，形如 https://.../v1 */
  modelBaseUrl: string;
  model: string;
  reasoningEffort: string;
  /**
   * 模型的上下文窗口（token）。内核据此在撑满之前主动压缩历史。
   * 0 表示不知道：那时只能等上游报「超出上下文」再被动压缩，白花一次请求。
   * airouter 不提供按模型查窗口的接口，所以只能在这里由用户填。
   */
  contextWindow: number;
  /** 内部平台地址。留空会被折回默认值（见 config-defaults.ts）。 */
  clawUrl: string;
  /**
   * 审批策略：on-write 默认，always 严格，never 无人值守（给定时任务）。
   */
  profile: "on-write" | "always" | "never";
  /**
   * 没经过确认的命令跑不跑在 macOS 沙箱里。默认开。
   *
   * 关掉的唯一理由是「某个命令被沙箱挡了而我确定它没问题」——留这个开关
   * 是为了不让人卡死在这儿，不是给日常用的。Windows 上没有这一层。
   */
  sandboxCommands: boolean;
  /** 会话记录保留天数；0 表示不自动清理。 */
  retentionDays: number;
  /**
   * 代码模式：把工具收进一个 exec 工具，模型写 JavaScript 调用它们。
   *
   * 默认关。工具多的时候省的是**开局就占掉的上下文地板**，以及来回次数；
   * 代价是模型得会写对代码——所以交给用户按自己用的模型来定。
   */
  codeMode: boolean;
  /**
   * 是否启用 computer use（截屏 + 鼠标键盘）。
   *
   * 默认关，而且应当保持默认关：开了之后模型能看见并操作**整个屏幕**，
   * 不受工作目录约束。这是本应用里权限最大的一组能力。
   */
  enableComputerUse: boolean;
}

/**
 * 一个内部平台能力插件实例。
 *
 * 与 claw-mcp 的实例配置是同一份东西：这里存用户在插件管理页的选择，
 * 启动会话时落成文件交给 claw-mcp 读。不直接把它塞进 Codex 配置，
 * 是因为 claw-mcp 需要的字段（assets、allow_write）Codex 并不认识。
 */
/**
 * 用户自己配的 MCP server。
 *
 * 与内部平台能力插件（PluginInstance）分开存：那些是由实例配置生成的，
 * 这些是用户逐个填的第三方 server，两者的生命周期和编辑方式都不一样。
 */
export interface McpServer {
  id: string;
  /** 界面上显示的名字，也用作工具名前缀。 */
  label: string;
  transport: "stdio" | "http";
  /** stdio：可执行文件与参数。 */
  command?: string;
  args?: string[];
  /** stdio：追加到子进程环境的变量。 */
  env?: Record<string, string>;
  /** http：server 地址。 */
  url?: string;
  /** http：附加到每条请求的头，鉴权走这里。 */
  headers?: Record<string, string>;
  enabled: boolean;
}

/** 用户自建的会话分组，像文件夹。 */
export interface SessionGroup {
  id: string;
  name: string;
}

export interface SessionGroups {
  groups: SessionGroup[];
  /** 会话 id → 分组 id。不在表里的会话就是「未分组」。 */
  assignments: Record<string, string>;
}

/**
 * 一个内部平台能力对象：一个数据 API、一个 API 操作、一个知识库、一个语料库。
 *
 * 粒度是**单个对象**而不是「一个实例装一组资产」：用户要的是逐个开关，
 * 模型那边每个对象本来也是一个独立工具。旧的 PluginInstance 模型要求用户
 * 手打主键列表，既难用又容易配错。
 *
 * 挂载时按 type 归并成一个 claw-mcp 进程，不是一个对象一个进程——
 * 二十个对象就是二十次进程启动加二十次握手，每次开会话都要等一遍。
 * 对用户和模型来说粒度都没变：开关还是逐个的，工具还是一个对象一个。
 */
export interface ClawCapability {
  type: "knowledge" | "data-api" | "api-operation" | "corpus" | "web-search";
  /** 内部平台侧的主键。与 type 合起来唯一。 */
  id: string;
  /** 给人看的名字，来自内部平台；也会进工具描述让模型知道这是什么。 */
  name: string;
  description?: string;
  enabled: boolean;
}

/** 旧模型。保留只为把已有配置迁过来，不再新增。 */
export interface PluginInstance {
  /** 实例 id，同时用作 MCP server 名，必须是合法标识符。 */
  id: string;
  type: "knowledge" | "data-api" | "api-operation" | "corpus" | "web-search";
  label: string;
  assets: string[];
  toolPrefix: string;
  enabled: boolean;
  allowWrite?: boolean;
  categories?: string[];
  topK?: number;
}

const LABELS: Record<string, string> = {
  knowledge: "知识库",
  "data-api": "数据 API",
  "api-operation": "API 服务",
  corpus: "语料库",
  "web-search": "联网搜索",
};

/**
 * 把旧的 PluginInstance 拆成单个能力对象。
 *
 * 旧模型里 knowledge 不带资产（整体检索），拆出来给它一个固定 id，
 * 让它在新模型里也有一个可开关的条目。
 */
function migrateInstances(instances: PluginInstance[]): ClawCapability[] {
  const out: ClawCapability[] = [];
  for (const instance of instances) {
    if (instance.type === "knowledge") {
      out.push({
        type: "knowledge",
        id: "default",
        name: instance.label || "知识库检索",
        enabled: instance.enabled,
      });
      continue;
    }
    for (const asset of instance.assets) {
      out.push({
        type: instance.type,
        id: asset,
        name: `${instance.label || LABELS[instance.type]} ${asset}`,
        enabled: instance.enabled,
      });
    }
  }
  return out;
}

export interface Credentials {
  /** 员工自己的 LLM Key，用于打 airouter。 */
  llmKey: string;
  /**
   * 内部平台 BFF Key。
   *
   * 注意：它等同于登录态且永不过期（upstream-auth 的 bff- 分支不检查
   * 过期或吊销），泄露后只能删 Key 或禁用用户止血。所以这里只进加密存储，
   * 不写 config.json、不进日志、不进诊断包。
   */
  clawToken: string;
}

const EMPTY_CREDENTIALS: Credentials = { llmKey: "", clawToken: "" };

export class ConfigStore {
  private readonly dir: string;
  private readonly configPath: string;
  private readonly credentialsPath: string;

  constructor(dir = app.getPath("userData")) {
    this.dir = dir;
    this.configPath = join(dir, "config.json");
    this.credentialsPath = join(dir, "credentials.bin");
    mkdirSync(this.dir, { recursive: true });
  }

  /** claw-agent 的数据目录（会话文件）。指向应用自己的数据目录，纳入保留策略。 */
  get agentHome(): string {
    const home = join(this.dir, "agent-home");
    mkdirSync(home, { recursive: true });
    return home;
  }

  readConfig(): AppConfig {
    if (!existsSync(this.configPath)) return { ...DEFAULT_CONFIG };
    try {
      const raw = JSON.parse(readFileSync(this.configPath, "utf8")) as Partial<AppConfig>;
      // 用默认值兜底而不是整份丢弃：用户手改坏一个字段不该让整个应用回到未配置状态。
      return normalizeConfig({ ...DEFAULT_CONFIG, ...raw });
    } catch {
      return { ...DEFAULT_CONFIG };
    }
  }

  writeConfig(patch: Partial<AppConfig>): AppConfig {
    const next = normalizeConfig({ ...this.readConfig(), ...patch });
    writeFileSync(this.configPath, `${JSON.stringify(next, null, 2)}\n`, "utf8");
    return next;
  }

  // ---------- 内部平台能力对象 ----------

  private get capabilitiesPath(): string {
    return join(this.dir, "capabilities.json");
  }

  /**
   * 读已选的内部平台能力。
   *
   * 首次读取时把旧的 PluginInstance 拆成单个对象搬过来——用户配过的东西
   * 不能因为模型换了就消失。搬完写盘，之后不再走这条路。
   */
  readCapabilities(): ClawCapability[] {
    if (existsSync(this.capabilitiesPath)) {
      try {
        const raw = JSON.parse(readFileSync(this.capabilitiesPath, "utf8")) as unknown;
        return Array.isArray(raw) ? (raw as ClawCapability[]) : [];
      } catch {
        return [];
      }
    }
    const migrated = migrateInstances(this.readInstances());
    if (migrated.length > 0) this.writeCapabilities(migrated);
    return migrated;
  }

  // ---------- 用户对能力的显式选择 ----------
  //
  // 与上面那份「当前有哪些能力」分开存，因为它们的生命周期不同：
  // 能力列表每次同步都整份重写（内部平台那边加了库就自动带上），
  // 而「用户自己开过/关过哪个」必须**跨同步存活**——否则关掉的下一次同步
  // 又自己开回来，那比不做自动同步还糟。
  //
  // 分开还有一个好处：某个库临时从内部平台消失又回来（权限调整、接口抖动），
  // 它的选择不会跟着丢。
  //
  // 记的是**双向**的选择而不只是「关掉的」：有些能力默认是关的
  //（联网搜索里那些不太像的候选），用户手动开了同样要记住。

  private get choicesPath(): string {
    return join(this.dir, "capability-optout.json");
  }

  /**
   * 读用户的显式选择，键是 `类型:id`，值是开还是关。
   *
   * 兼容旧格式：v0.1.4 那版写的是一个「关掉的键」数组，读到数组就当成
   * 全是 false。不兼容的话，升级上来的用户关掉的能力会一次性全开回来。
   */
  readCapabilityChoices(): Record<string, boolean> {
    try {
      return parseCapabilityChoices(JSON.parse(readFileSync(this.choicesPath, "utf8")));
    } catch {
      return {};
    }
  }

  writeCapabilityChoices(choices: Record<string, boolean>): Record<string, boolean> {
    const sorted = Object.fromEntries(
      Object.entries(choices).sort(([a], [b]) => a.localeCompare(b)),
    );
    writeFileSync(this.choicesPath, `${JSON.stringify(sorted, null, 2)}\n`, "utf8");
    return sorted;
  }

  writeCapabilities(capabilities: ClawCapability[]): ClawCapability[] {
    writeFileSync(
      this.capabilitiesPath,
      `${JSON.stringify(capabilities, null, 2)}\n`,
      "utf8",
    );
    return capabilities;
  }

  // ---------- 自定义 MCP server ----------

  private get mcpPath(): string {
    return join(this.dir, "mcp-servers.json");
  }

  readMcpServers(): McpServer[] {
    if (!existsSync(this.mcpPath)) return [];
    try {
      const raw = JSON.parse(readFileSync(this.mcpPath, "utf8")) as unknown;
      return Array.isArray(raw) ? (raw as McpServer[]) : [];
    } catch {
      return [];
    }
  }

  writeMcpServers(servers: McpServer[]): McpServer[] {
    writeFileSync(this.mcpPath, `${JSON.stringify(servers, null, 2)}\n`, "utf8");
    return servers;
  }

  // ---------- 本地技能与长期记忆 ----------

  /**
   * 用户自己看得见、改得动的那部分数据放这里，不放 userData。
   *
   * 技能和长期记忆都是**用户要用编辑器打开的东西**：技能是他写的 SKILL.md，
   * 记忆是他想让模型一直记住的几句话。埋在
   * `~/Library/Application Support/aiclaw/` 里等于藏起来——那个路径要先
   * 知道它存在才找得到，Finder 默认还不显示资源库。`~/.aiclaw/` 与
   * `~/.claude/`、`~/.codex/` 是同一个位置感，`cd ~/.aiclaw` 就到了。
   *
   * 会话库、配置、凭据仍在 userData：那些是应用的内部状态，不该邀请用户去改。
   */
  /**
   * 应用数据目录（会话库、配置、凭据都在这儿）。
   *
   * 暴露出来是给「敏感路径」用的：这个目录要作为硬拒绝名单下发给内核。
   */
  get dataDir(): string {
    return this.dir;
  }

  get homeDir(): string {
    const dir = join(homedir(), ".aiclaw");
    mkdirSync(dir, { recursive: true });
    return dir;
  }

  /** 技能目录：一个子目录一个技能，里面放 SKILL.md。 */
  get skillsDir(): string {
    return join(this.homeDir, "skills");
  }

  /** 长期记忆文件。 */
  get memoryFile(): string {
    return join(this.homeDir, "memory.md");
  }

  /**
   * 把旧位置（userData 下）的技能与记忆搬到 ~/.aiclaw。
   *
   * 只在目标不存在时搬，**搬完不删原件**：万一搬错了还能翻回去，而且它们
   * 占不了多少地方。搬不动不报错——技能没跟过来是「少几个技能」，
   * 让应用因此起不来才是真故障。
   */
  migrateHomeData(): void {
    const moves: [string, string][] = [
      [join(this.dir, "skills"), this.skillsDir],
      [join(this.dir, "memory.md"), this.memoryFile],
    ];
    for (const [from, to] of moves) {
      if (!existsSync(from) || existsSync(to)) continue;
      try {
        cpSync(from, to, { recursive: true });
      } catch {
        // 忽略：见上面的说明。
      }
    }
  }

  // ---------- 会话分组 ----------

  private get groupsPath(): string {
    return join(this.dir, "session-groups.json");
  }

  /**
   * 分组存在宿主这边，不进 claw-agent 的会话存档。
   *
   * 分组是界面上的归档方式，内核不该知道有「文件夹」这回事；放这边还省得
   * 每改一次归属就去重写一个会话文件。代价是删会话时要顺手清掉归属，
   * 见 pruneGroupAssignments。
   */
  readGroups(): SessionGroups {
    if (!existsSync(this.groupsPath)) return { groups: [], assignments: {} };
    try {
      const raw = JSON.parse(readFileSync(this.groupsPath, "utf8")) as Partial<SessionGroups>;
      return {
        groups: Array.isArray(raw.groups) ? raw.groups : [],
        assignments:
          raw.assignments && typeof raw.assignments === "object" ? raw.assignments : {},
      };
    } catch {
      return { groups: [], assignments: {} };
    }
  }

  writeGroups(next: SessionGroups): SessionGroups {
    writeFileSync(this.groupsPath, `${JSON.stringify(next, null, 2)}\n`, "utf8");
    return next;
  }

  /**
   * 丢掉指向已不存在会话的归属记录。
   *
   * 不清的话，删掉的会话会以「幽灵成员」的形式一直留在分组里：界面上看不见，
   * 但分组计数是错的。每次列会话时顺手清一遍，比在删除路径上补更不容易漏。
   */
  pruneGroupAssignments(existingSessionIds: string[]): SessionGroups {
    const current = this.readGroups();
    const alive = new Set(existingSessionIds);
    const groupIds = new Set(current.groups.map((group) => group.id));
    const assignments: Record<string, string> = {};
    for (const [sessionId, groupId] of Object.entries(current.assignments)) {
      // 分组被删掉时它的成员也要放回「未分组」，否则会话会整个消失在界面上。
      if (alive.has(sessionId) && groupIds.has(groupId)) assignments[sessionId] = groupId;
    }
    if (Object.keys(assignments).length === Object.keys(current.assignments).length) {
      return current;
    }
    return this.writeGroups({ groups: current.groups, assignments });
  }

  // ---------- 内部平台能力插件实例 ----------

  private get instancesPath(): string {
    return join(this.dir, "instances.json");
  }

  /** 只剩 readCapabilities 的一次性迁移在用。新配置走 capabilities.json。 */
  readInstances(): PluginInstance[] {
    if (!existsSync(this.instancesPath)) return [];
    try {
      const raw = JSON.parse(readFileSync(this.instancesPath, "utf8")) as unknown;
      return Array.isArray(raw) ? (raw as PluginInstance[]) : [];
    } catch {
      return [];
    }
  }

  /**
   * 把启用的实例落成 claw-mcp 能读的配置文件，返回 MCP server 名 → 文件路径。
   *
   * 每次开会话都重写一遍：用户在插件管理页改了选择之后，下一个会话就该生效，
   * 而不是等到重启应用。
   */
  /**
   * 把启用的能力落成 claw-mcp 能读的配置文件，返回 MCP server 名 → 文件路径。
   *
   * 按 type 归并：四种能力最多四个进程。一个对象一个进程的话，二十个对象
   * 就是二十次进程启动加二十次 MCP 握手，每次开会话都要等一遍——而用户和
   * 模型看到的粒度并不会因此变细，开关本来就是逐个的，工具也本来就是
   * 一个对象一个。
   *
   * 每次开会话都重写一遍：用户在能力页改完，下一个会话就生效。
   */
  materializeInstances(): { id: string; type: string; configPath: string }[] {
    const dir = join(this.dir, "instances");
    mkdirSync(dir, { recursive: true });

    const byType = new Map<string, ClawCapability[]>();
    for (const capability of this.readCapabilities()) {
      if (!capability.enabled) continue;
      const list = byType.get(capability.type);
      if (list) list.push(capability);
      else byType.set(capability.type, [capability]);
    }

    const materialized: { id: string; type: string; configPath: string }[] = [];
    for (const [type, capabilities] of byType) {
      const configPath = join(dir, `${type}.json`);
      writeFileSync(
        configPath,
        `${JSON.stringify(
          {
            type,
            label: LABELS[type] ?? type,
            assets: capabilities.map((capability) => capability.id),
            // **不写工具名前缀。** 内核挂载时已经用 server 名（就是这个 type）
            // 给每个工具加了一层前缀，这里再加一次就成了
            // `knowledge__knowledge_knowledge_search`、`corpus__corpus_corpus_1`。
            // 前缀留给 claw-mcp 单独跑的场景用。
            tool_prefix: "",
            // 留着这个字段只为兼容旧配置文件：claw-mcp 已经不读它了。
            //
            // 早先写类接口要 allow_write 才挂，结果是用户选了一个 161 个接口的
            // 服务、界面上只显示挂了几个，差额没有任何地方说明。现在一律挂，
            // 可见性与可调用性由内部平台按人判；写类操作标成非只读，由审批那一层管。
            allow_write: false,
            categories: [],
            top_k: 0,
          },
          null,
          2,
        )}\n`,
        "utf8",
      );
      materialized.push({ id: type, type, configPath });
    }

    // 上一轮启用、这一轮关掉的类型，配置文件留着没害处但会让人以为还在挂，
    // 清掉。
    for (const type of Object.keys(LABELS)) {
      if (!byType.has(type)) rmSync(join(dir, `${type}.json`), { force: true });
    }
    return materialized;
  }

  readCredentials(): Credentials {
    if (!existsSync(this.credentialsPath)) return { ...EMPTY_CREDENTIALS };
    if (!safeStorage.isEncryptionAvailable()) return { ...EMPTY_CREDENTIALS };
    try {
      const decrypted = safeStorage.decryptString(readFileSync(this.credentialsPath));
      return { ...EMPTY_CREDENTIALS, ...(JSON.parse(decrypted) as Partial<Credentials>) };
    } catch {
      // 解不开通常意味着换了机器或系统钥匙串变了。当作未配置，让用户重填，
      // 而不是抛异常把应用卡在启动阶段。
      return { ...EMPTY_CREDENTIALS };
    }
  }

  writeCredentials(patch: Partial<Credentials>): void {
    if (!safeStorage.isEncryptionAvailable()) {
      throw new Error(
        "当前系统不支持安全存储，拒绝以明文保存凭据。请检查系统钥匙串是否可用。",
      );
    }
    const next = { ...this.readCredentials(), ...patch };
    writeFileSync(this.credentialsPath, safeStorage.encryptString(JSON.stringify(next)));
  }

  /** 凭据是否已配齐。UI 用它决定是否跳配置页。 */
  credentialStatus(): { llmKey: boolean; clawToken: boolean } {
    const creds = this.readCredentials();
    return {
      llmKey: creds.llmKey.length > 0,
      clawToken: creds.clawToken.length > 0,
    };
  }

  /**
   * 清空全部本地数据。对应方案 §15 的验收项 12。
   *
   * 会话记录、凭据、配置一起清——分开清会留下「配置还在但凭据没了」
   * 这种让用户困惑的中间态。
   */
  purgeAll(): void {
    rmSync(this.credentialsPath, { force: true });
    rmSync(this.configPath, { force: true });
    rmSync(this.instancesPath, { force: true });
    rmSync(this.groupsPath, { force: true });
    rmSync(this.mcpPath, { force: true });
    rmSync(this.capabilitiesPath, { force: true });
    rmSync(this.choicesPath, { force: true });
    rmSync(this.skillsDir, { recursive: true, force: true });
    rmSync(this.memoryFile, { force: true });
    rmSync(join(this.dir, "instances"), { recursive: true, force: true });
    rmSync(join(this.dir, "agent-home"), { recursive: true, force: true });
  }
}
