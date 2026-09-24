import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";
import type { ConfigStore } from "./config.js";
import { dedupeByName, discoverSkills, type FoundSkill } from "./skill-roots.js";
import { parseFrontmatter } from "./skill-frontmatter.js";

/**
 * 本地技能的管理：列出、启停、删除。
 *
 * 技能的运行时形态由 claw-agent 的 internal/skills 决定（目录 + SKILL.md +
 * frontmatter），这里只负责把目录整理成那个形状。
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
  /** 来源标签：AIClaw / Claude Code / Codex / 项目 / npm 全局…… */
  source: string;
  /** false 表示技能在别人的目录里：能用、能关，但不能删也不能改。 */
  writable: boolean;
}

const DISABLED_MARKER = ".disabled";

export class SkillManager {
  private readonly store: ConfigStore;

  constructor(store: ConfigStore) {
    this.store = store;
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

  /**
   * 插件贡献的技能目录。由 SessionManager 在开会话前从内核拿到后设进来。
   *
   * 它们不在文件系统扫描的范围里（装在应用数据目录的 plugins/ 下），
   * 而且来源要显示成插件名而不是路径，所以单独一份。
   */
  private pluginSkills: { dir: string; pluginName: string }[] = [];

  setPluginSkills(skills: { dir: string; pluginName: string }[]): void {
    this.pluginSkills = skills;
  }

  private discover(): FoundSkill[] {
    const found = discoverSkills({
      ownDir: this.dir,
      home: homedir(),
      workdir: this.workspace,
    });
    // 插件的排在自己目录之后、别处的之前：装了插件就是想用它带的技能。
    const extra: FoundSkill[] = this.pluginSkills.map((skill) => ({
      dir: skill.dir,
      dirName: basename(skill.dir),
      rootLabel: `插件 · ${skill.pluginName}`,
      writable: false,
    }));
    return [...found.filter((f) => f.writable), ...extra, ...found.filter((f) => !f.writable)];
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

