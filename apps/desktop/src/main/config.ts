import { app, safeStorage } from "electron";
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

import { DEFAULT_CONFIG, normalizeConfig } from "./config-defaults.js";

/**
 * 配置分两层落盘：
 *
 * - config.json     非敏感配置，明文，用户可以直接看、直接改；
 * - credentials.bin 凭据，safeStorage 加密（macOS Keychain / Windows DPAPI）。
 *
 * 分开放是刻意的：出问题时用户能把 config.json 发给别人看，
 * 而不用担心里面夹着模型 Key。诊断包同理——只带 config.json，绝不带 credentials.bin。
 */

export interface AppConfig {
  /** OpenAI 兼容地址，形如 https://.../v1 */
  modelBaseUrl: string;
  model: string;
  reasoningEffort: string;
  /**
   * 模型的上下文窗口（token）。内核据此在撑满之前主动压缩历史。
   * 0 表示不知道：那时只能等上游报「超出上下文」再被动压缩，白花一次请求。
   * 端点不一定提供按模型查窗口的接口，所以也允许在这里由用户填。
   */
  contextWindow: number;
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

/** 用户自己配的 MCP server。 */
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

export interface Credentials {
  /** 模型 Key。只进加密存储，不写 config.json、不进日志、不进诊断包。 */
  llmKey: string;
}

const EMPTY_CREDENTIALS: Credentials = { llmKey: "" };

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
  credentialStatus(): { llmKey: boolean } {
    const creds = this.readCredentials();
    return { llmKey: creds.llmKey.length > 0 };
  }

  /**
   * 清空全部本地数据。
   *
   * 会话记录、凭据、配置一起清——分开清会留下「配置还在但凭据没了」
   * 这种让用户困惑的中间态。
   */
  purgeAll(): void {
    rmSync(this.credentialsPath, { force: true });
    rmSync(this.configPath, { force: true });
    rmSync(this.groupsPath, { force: true });
    rmSync(this.mcpPath, { force: true });
    rmSync(this.skillsDir, { recursive: true, force: true });
    rmSync(this.memoryFile, { force: true });
    rmSync(join(this.dir, "agent-home"), { recursive: true, force: true });
  }
}
