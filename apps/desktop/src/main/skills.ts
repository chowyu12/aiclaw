import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ClawClient } from "./claw.js";
import type { ConfigStore } from "./config.js";
import { dedupeByName, discoverSkills, type FoundSkill } from "./skill-roots.js";
import { commonTopLevelDir, unzipInto } from "./unzip.js";

/**
 * 本地技能的管理：列出、启停、删除、从内部平台 SkillHub 安装。
 *
 * 技能的运行时形态由 claw-agent 的 internal/skills 决定（目录 + SKILL.md +
 * frontmatter），这里只负责把目录整理成那个形状，以及把内部平台的包搬下来。
 *
 * 为什么「内部平台技能」不是另一套机制：查过内部平台的接口，SkillHub 没有运行时契约、
 * 也没有执行端点——它分发的就是装着 SKILL.md 的 ZIP。装到本地目录之后，
 * 与用户自己写的技能没有任何区别。
 */

export interface SkillView {
  /**
   * 启停与删除时的标识：自己目录里的技能用目录名，别处的用绝对路径。
   *
   * 不能一律用目录名——Claude 与 Codex 下可以有同名目录，撞上之后
   * 关掉一个会把另一个也关掉。
   */
  id: string;
  /** 目录名，展示用。 */
  dirName: string;
  /** 技能目录的绝对路径。 */
  dir: string;
  name: string;
  description: string;
  enabled: boolean;
  /** 来源标签：内部平台 / Claude Code / Codex / 项目 / npm 全局…… */
  source: string;
  /** false 表示技能在别人的目录里：能用、能关，但不能删也不能改。 */
  writable: boolean;
  /** 来自内部平台 SkillHub 时记下包 id，用来判断能不能升级。 */
  sourceId?: string;
  sourceVersion?: string;
}

/** 内部平台 SkillHub 上的一个包。 */
export interface SkillHubItem {
  id: string;
  name: string;
  description: string;
  category: string;
  version: string;
  installed: boolean;
}

const DISABLED_MARKER = ".disabled";
/** 记安装来源。放在技能目录里，删目录就一起没了，不留孤儿记录。 */
const SOURCE_FILE = ".source.json";

export class SkillManager {
  private readonly store: ConfigStore;
  private readonly claw: ClawClient;

  constructor(store: ConfigStore, claw: ClawClient) {
    this.store = store;
    this.claw = claw;
  }

  get dir(): string {
    return this.store.skillsDir;
  }

  /**
   * 当前会话的工作区，用来找项目级的 `.claude/skills`。
   *
   * 由 SessionManager 在开会话/恢复会话/改工作区时设进来——技能发现是
   * 全局的（技能页没有会话上下文），但「项目级技能」这一条只有会话知道
   * 自己在哪个项目里。没有会话时就是空的，那一条来源自然不参与。
   */
  private workspace = "";

  setWorkspace(workspace: string): void {
    this.workspace = workspace.trim();
  }

  private discover(): FoundSkill[] {
    return discoverSkills({
      ownDir: this.dir,
      home: homedir(),
      workdir: this.workspace,
    });
  }

  /**
   * 列出这台机器上所有能用的技能。
   *
   * 不只是我们自己那个目录——Claude Code、Codex、npx 装的 CLI 用的是同一种
   * 技能格式，用户在那边装好的没有理由在这里看不见。发现逻辑在 skill-roots.ts。
   */
  list(): SkillView[] {
    const off = this.disabledSet();
    const skills: SkillView[] = [];

    const withMeta = this.discover().map((found) => ({
      found,
      meta: parseFrontmatter(safeRead(join(found.dir, "SKILL.md"))),
    }));
    // 同名只留优先级最高的那个：Claude 下的真目录与 Codex 下指向同一个 CLI
    // 缓存的软链路径不同，去重不掉，但它们是同一个技能。
    for (const { found, meta } of dedupeByName(withMeta, (x) => x.meta.name || x.found.dirName)) {
      const name = meta.name || found.dirName;
      const id = found.writable ? found.dirName : found.dir;
      const source = readSource(found.dir);
      skills.push({
        id,
        dirName: found.dirName,
        dir: found.dir,
        name,
        description: meta.description || "",
        // 两套关闭方式，各管各的：自己目录里的用目录内的标记文件（删目录就
        // 连状态一起删了），别处的记在我们自己的配置里——往 ~/.claude/skills
        // 里写标记会把人家 Claude Code 里的技能也关掉。
        enabled: found.writable
          ? !existsSync(join(found.dir, DISABLED_MARKER))
          : !off.has(found.dir),
        source: found.rootLabel,
        writable: found.writable,
        sourceId: source?.id,
        sourceVersion: source?.version,
      });
    }
    skills.sort((a, b) => a.name.localeCompare(b.name, "zh"));
    return skills;
  }

  /** 挂给内核的技能目录：只给启用的。 */
  enabledDirs(): string[] {
    return this.list().filter((skill) => skill.enabled).map((skill) => skill.dir);
  }

  /** 启停一个技能。外部技能记在自己的配置里，不去动人家的目录。 */
  setEnabled(id: string, enabled: boolean): SkillView[] {
    const skill = this.list().find((item) => item.id === id);
    if (!skill) return this.list();

    if (skill.writable) {
      const marker = join(this.resolve(skill.dirName), DISABLED_MARKER);
      if (enabled) rmSync(marker, { force: true });
      else writeFileSync(marker, "", "utf8");
      return this.list();
    }

    const off = this.disabledSet();
    if (enabled) off.delete(skill.dir);
    else off.add(skill.dir);
    this.writeDisabled([...off]);
    return this.list();
  }

  remove(id: string): SkillView[] {
    const skill = this.list().find((item) => item.id === id);
    if (!skill) return this.list();
    if (!skill.writable) {
      // 别人的目录不归我们删。说清楚它在哪儿，用户自己去处理。
      throw new Error(`「${skill.name}」来自${skill.source}（${skill.dir}），请到那边删除。`);
    }
    rmSync(this.resolve(skill.dirName), { recursive: true, force: true });
    return this.list();
  }

  // ---------- 外部技能的关闭状态 ----------

  private get disabledPath(): string {
    return join(this.dir, ".disabled-external.json");
  }

  private disabledSet(): Set<string> {
    try {
      const raw = JSON.parse(readFileSync(this.disabledPath, "utf8")) as unknown;
      return new Set(Array.isArray(raw) ? (raw as string[]) : []);
    } catch {
      return new Set();
    }
  }

  private writeDisabled(list: string[]): void {
    mkdirSync(this.dir, { recursive: true });
    writeFileSync(this.disabledPath, `${JSON.stringify(list, null, 2)}\n`, "utf8");
  }

  /** 列出内部平台 SkillHub 上可装的包。 */
  async listHub(keyword: string): Promise<SkillHubItem[]> {
    const installed = new Set(this.list().map((skill) => skill.sourceId).filter(Boolean));
    const body = await this.claw.post<Record<string, unknown>>("/api/v1/skill-hub/list", {
      keyword: keyword || undefined,
      page: 1,
      page_size: 100,
    });
    const list = (body?.list ?? body?.packages ?? []) as Record<string, unknown>[];
    return list.map((item) => ({
      id: String(item.id ?? ""),
      name: String(item.display_name ?? item.name ?? ""),
      description: String(item.description ?? ""),
      category: String(item.category ?? ""),
      version: String(
        (item.current_version as Record<string, unknown> | undefined)?.semver ?? item.version ?? "",
      ),
      installed: installed.has(String(item.id ?? "")),
    }));
  }

  /**
   * 从内部平台装一个技能包。
   *
   * ZIP 解包走 unzip.ts，它会拦住指向目录之外的条目——包是别人上传的，
   * 这条防护不是可选的。
   */
  async installFromHub(id: string, name: string): Promise<SkillView[]> {
    const archive = await this.claw.download(
      `/api/v1/skill-hub/${encodeURIComponent(id)}/download`,
    );

    // 先解到一个临时目录，确认里面真有 SKILL.md 再搬过去。直接解到目标位置的话，
    // 一个不含 SKILL.md 的包会留下一个永远加载不了的半成品目录。
    const staging = join(this.dir, `.staging-${Date.now().toString(36)}`);
    rmSync(staging, { recursive: true, force: true });
    mkdirSync(staging, { recursive: true });

    try {
      const { files } = unzipInto(archive, staging);
      // 包通常整个套在一层顶层目录里，脱掉它，别让技能目录多一层。
      const top = commonTopLevelDir(files);
      const contentRoot = top ? join(staging, top) : staging;
      if (!existsSync(join(contentRoot, "SKILL.md"))) {
        throw new Error("这个包里没有 SKILL.md，不是一个技能包");
      }

      const dirName = sanitizeDirName(top || name || id);
      const target = this.resolve(dirName);
      rmSync(target, { recursive: true, force: true });
      mkdirSync(this.dir, { recursive: true });
      // 同盘改名是原子的：不会留下一个解到一半的技能目录。
      const { renameSync } = await import("node:fs");
      renameSync(contentRoot, target);

      writeFileSync(
        join(target, SOURCE_FILE),
        JSON.stringify({ id, name, installedAt: new Date().toISOString() }, null, 2),
        "utf8",
      );
      return this.list();
    } finally {
      rmSync(staging, { recursive: true, force: true });
    }
  }

  /** 解析目录名并确认它没跑出技能目录。界面传来的值也不能无条件相信。 */
  private resolve(dirName: string): string {
    const clean = sanitizeDirName(dirName);
    if (!clean) throw new Error("技能名为空");
    return join(this.dir, clean);
  }
}

/** 目录名只保留安全字符，顺带挡掉 `..` 与分隔符。 */
function sanitizeDirName(raw: string): string {
  return raw
    .trim()
    .replace(/[/\\]/g, "-")
    .replace(/^\.+/, "")
    .slice(0, 80);
}

function safeRead(path: string): string {
  try {
    return readFileSync(path, "utf8");
  } catch {
    return "";
  }
}

function readSource(dir: string): { id: string; version?: string } | undefined {
  try {
    const parsed = JSON.parse(readFileSync(join(dir, SOURCE_FILE), "utf8")) as {
      id?: string;
      version?: string;
    };
    return parsed.id ? { id: parsed.id, version: parsed.version } : undefined;
  } catch {
    return undefined;
  }
}

/**
 * 读 SKILL.md 开头的 frontmatter。
 *
 * 与 claw-agent 的 internal/skills 同一套规则——两边都只认 name 与 description。
 * 改一边要改另一边，否则列表里显示的名字会和模型看到的对不上。
 */
function parseFrontmatter(source: string): { name: string; description: string } {
  const normalized = source.replace(/\r\n/g, "\n");
  const result = { name: "", description: "" };
  if (!normalized.startsWith("---\n")) return result;
  const end = normalized.indexOf("\n---", 4);
  if (end < 0) return result;

  for (const line of normalized.slice(4, end).split("\n")) {
    const separator = line.indexOf(":");
    if (separator < 0) continue;
    const key = line.slice(0, separator).trim().toLowerCase();
    const value = line.slice(separator + 1).trim().replace(/^["']|["']$/g, "");
    if (key === "name") result.name = value;
    else if (key === "description") result.description = value;
  }
  return result;
}
