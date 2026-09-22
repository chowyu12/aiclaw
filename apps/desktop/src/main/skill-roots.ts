import { existsSync, readdirSync, realpathSync, statSync } from "node:fs";
import { join } from "node:path";

/**
 * 找出这台机器上所有的技能目录。
 *
 * 技能是「目录 + SKILL.md」这个形状，Claude Code、Codex、内部平台 SkillHub 用的是
 * 同一套。既然如此，用户在别处装好的技能没有理由在这里看不见——让他为了同一份
 * SKILL.md 再装一遍，只会装出两份很快就不一致的副本。
 *
 * 三件必须处理对的事：
 *
 *  1. **跟符号链接。** 用 npx 跑的 example-cli CLI 把技能装进
 *     `~/.example-cli/skill-cache/<名字>/<版本>/`，再软链到 `~/.claude/skills`
 *     和 `~/.codex/skills` 下。而 `Dirent.isDirectory()` 对软链返回 **false**
 *     ——按它筛的话，这台机器上十个技能里有八个是看不见的。所以一律用
 *     `statSync`（跟链）而不是 Dirent 判类型。
 *  2. **按真实路径去重。** 同一个缓存目录会同时被 Claude 和 Codex 链过去，
 *     不去重的话每个技能都出现两三遍。
 *  3. **目录结构不止一种。** 扁平的（`<root>/<技能>/SKILL.md`）、带版本的
 *     （CLI 缓存 `<名字>/<版本>/SKILL.md`）、npm 包的（`<包>/SKILL.md`，
 *     外加 `@scope/<包>/SKILL.md` 这一层）。
 *
 * 不扫 `~/.npm/_npx/<hash>/`：那是 npx 的临时缓存，里面的东西随时会被清掉，
 * 把它当成「装好的技能」会让列表时有时无。
 */

export type RootLayout = "flat" | "versioned" | "npm";

export interface SkillRoot {
  path: string;
  /** 界面上显示「这个技能是从哪儿来的」。 */
  label: string;
  /** 只有我们自己那个目录可写——别处的技能不能删、不能改名。 */
  writable: boolean;
  layout: RootLayout;
}

export interface FoundSkill {
  /** 技能目录的绝对路径（未解析软链，保留用户看得懂的那个）。 */
  dir: string;
  /** 目录名，用作展示。 */
  dirName: string;
  rootLabel: string;
  writable: boolean;
}

export interface DiscoverOptions {
  /** 我们自己的技能目录，排在最前面：同名时它说了算。 */
  ownDir: string;
  home: string;
  /** 当前工作目录，用来找项目级的 `.claude/skills`。 */
  workdir?: string;
  env?: NodeJS.ProcessEnv;
  platform?: NodeJS.Platform;
}

/** 列出要扫的根，按优先级从高到低。同名技能先出现的赢。 */
export function skillRoots(options: DiscoverOptions): SkillRoot[] {
  const { ownDir, home, workdir } = options;
  const env = options.env ?? process.env;
  const platform = options.platform ?? process.platform;

  const roots: SkillRoot[] = [
    { path: ownDir, label: "内部平台", writable: true, layout: "flat" },
  ];

  // 项目级排在用户级前面：放进仓库的技能是为这个项目量身写的，
  // 与全局同名时应当以它为准。
  if (workdir && workdir.trim()) {
    roots.push({
      path: join(workdir, ".claude", "skills"),
      label: "项目",
      writable: false,
      layout: "flat",
    });
  }

  roots.push(
    { path: join(home, ".claude", "skills"), label: "Claude Code", writable: false, layout: "flat" },
    { path: join(home, ".codex", "skills"), label: "Codex", writable: false, layout: "flat" },
    {
      path: join(home, ".example-cli", "skill-cache"),
      label: "内部平台 CLI",
      writable: false,
      layout: "versioned",
    },
  );

  for (const path of npmGlobalRoots(home, env, platform)) {
    roots.push({ path, label: "npm 全局", writable: false, layout: "npm" });
  }

  return roots;
}

/**
 * npm 全局包目录。
 *
 * 不去 `npm root -g`：那要起一个子进程，几百毫秒，而这段代码在**每次开会话时**
 * 都要跑。已知的几个位置直接查就够了，查错的代价只是少发现一个技能。
 */
function npmGlobalRoots(
  home: string,
  env: NodeJS.ProcessEnv,
  platform: NodeJS.Platform,
): string[] {
  const candidates: string[] = [];
  const prefix = env.npm_config_prefix ?? env.NPM_CONFIG_PREFIX;
  if (prefix) candidates.push(join(prefix, "lib", "node_modules"));

  if (platform === "win32") {
    if (env.APPDATA) candidates.push(join(env.APPDATA, "npm", "node_modules"));
  } else {
    candidates.push("/usr/local/lib/node_modules", "/opt/homebrew/lib/node_modules");
  }

  // nvm 每个 node 版本一份。装了好几个版本时只有当前那个有意义，但我们不知道
  // 是哪个，所以全扫——重复的会在去重那步合掉。
  const nvm = join(home, ".nvm", "versions", "node");
  for (const version of safeReaddir(nvm)) {
    candidates.push(join(nvm, version, "lib", "node_modules"));
  }

  return candidates.filter((path) => isDir(path));
}

/** 扫一个根，返回里面每个「含 SKILL.md 的目录」。 */
export function scanRoot(root: SkillRoot): FoundSkill[] {
  const found: FoundSkill[] = [];
  const add = (dir: string, dirName: string): void => {
    if (!isDir(dir) || !existsSync(join(dir, "SKILL.md"))) return;
    found.push({ dir, dirName, rootLabel: root.label, writable: root.writable });
  };

  for (const name of safeReaddir(root.path)) {
    if (name.startsWith(".")) continue;
    const entry = join(root.path, name);

    switch (root.layout) {
      case "flat":
        add(entry, name);
        break;
      case "versioned": {
        // `<名字>/<版本>/SKILL.md`。装了多个版本时只取最新的那个——
        // 同一个技能的两个版本同时挂给模型，它无从选择。
        const latest = latestVersion(safeReaddir(entry).filter((v) => isDir(join(entry, v))));
        if (latest) add(join(entry, latest), name);
        break;
      }
      case "npm":
        if (name.startsWith("@")) {
          // 作用域包多一层：`@scope/<包>/SKILL.md`。
          for (const scoped of safeReaddir(entry)) {
            if (scoped.startsWith(".")) continue;
            add(join(entry, scoped), `${name}/${scoped}`);
          }
        } else {
          add(entry, name);
        }
        break;
    }
  }
  return found;
}

/**
 * 扫出全部技能，按真实路径去重。
 *
 * 同一个 CLI 缓存目录会被 Claude 和 Codex 各软链一次，再加上缓存本身就是三份；
 * 按 `realpath` 去重之后只剩一份，而且留下的是**优先级最高**的那条路径——
 * 用户在界面上看到的来源标签因此是稳定的。
 */
export function discoverSkills(options: DiscoverOptions): FoundSkill[] {
  const seen = new Set<string>();
  const found: FoundSkill[] = [];
  for (const root of skillRoots(options)) {
    for (const skill of scanRoot(root)) {
      const real = safeRealpath(skill.dir);
      if (seen.has(real)) continue;
      seen.add(real);
      found.push(skill);
    }
  }
  return found;
}

/**
 * 同名只留第一个。
 *
 * 按真实路径去重之后还会剩同名的：Claude 下是一份真目录、Codex 下是指向 CLI
 * 缓存的软链，路径不同但装的是同一个技能。两个同名技能一起挂给模型，
 * `load_skill` 取到哪一个就成了运气——所以按 `skillRoots` 的顺序取第一个。
 *
 * 名字由调用方给出（要读 SKILL.md 的 frontmatter），这里只负责挑。
 */
export function dedupeByName<T>(items: T[], nameOf: (item: T) => string): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of items) {
    const name = nameOf(item);
    if (seen.has(name)) continue;
    seen.add(name);
    out.push(item);
  }
  return out;
}

/** 语义化版本比较，比不出来就按字符串排。取最大的那个。 */
function latestVersion(versions: string[]): string | undefined {
  if (versions.length === 0) return undefined;
  const parse = (value: string): number[] =>
    value.split(/[.\-+]/).map((part) => {
      const n = Number(part);
      return Number.isFinite(n) ? n : -1;
    });
  return [...versions].sort((a, b) => {
    const left = parse(a);
    const right = parse(b);
    for (let i = 0; i < Math.max(left.length, right.length); i++) {
      const diff = (right[i] ?? 0) - (left[i] ?? 0);
      if (diff !== 0) return diff;
    }
    return b.localeCompare(a);
  })[0];
}

/** statSync 跟符号链接，Dirent.isDirectory() 不跟——见文件头第 1 条。 */
function isDir(path: string): boolean {
  try {
    return statSync(path).isDirectory();
  } catch {
    return false;
  }
}

function safeReaddir(path: string): string[] {
  try {
    return readdirSync(path);
  } catch {
    return [];
  }
}

function safeRealpath(path: string): string {
  try {
    return realpathSync(path);
  } catch {
    return path;
  }
}
