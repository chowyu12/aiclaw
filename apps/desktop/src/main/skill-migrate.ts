import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

/**
 * 把旧技能目录（`~/.aiclaw/skills`）里的技能搬进新目录（`~/.agents/skills`）。
 *
 * - 一个一个搬（rename，跨盘时退回复制再删）。目标里已有同名的不动、留在原处——
 *   多半是用户两边各装了一份，替他决定用哪份不合适。
 * - 旧版在技能目录里放 `.disabled` 标记表示关着；新目录是几个工具共用的，不再往里写，
 *   搬过去时把标记删掉、记进状态文件。
 * - 旧版记外部技能开关的 `.disabled-external.json` 一并并进状态文件。
 *
 * 搬不动的不报错：少一个技能不该让应用起不来。
 */
export function migrateSkillDir(from: string, to: string, statePath: string): { moved: string[]; disabled: string[] } {
  const result = { moved: [] as string[], disabled: [] as string[] };
  if (!existsSync(from)) return result;
  let names: string[] = [];
  try {
    names = readdirSync(from);
  } catch {
    return result;
  }
  for (const name of names) {
    const source = join(from, name);
    if (name.startsWith(".") || !existsSync(join(source, "SKILL.md"))) continue;
    const target = join(to, name);
    if (existsSync(target)) continue;
    try {
      mkdirSync(to, { recursive: true });
      try {
        renameSync(source, target);
      } catch {
        cpSync(source, target, { recursive: true });
        rmSync(source, { recursive: true, force: true });
      }
      const marker = join(target, ".disabled");
      if (existsSync(marker)) {
        rmSync(marker, { force: true });
        result.disabled.push(target);
      }
      result.moved.push(name);
    } catch {
      // 见上面的说明。
    }
  }

  const off = readList(statePath);
  const legacyState = join(from, ".disabled-external.json");
  if (existsSync(legacyState)) {
    off.push(...readList(legacyState));
    rmSync(legacyState, { force: true });
  }
  off.push(...result.disabled);
  if (off.length > 0) writeFileSync(statePath, `${JSON.stringify([...new Set(off)], null, 2)}\n`, "utf8");
  return result;
}

function readList(path: string): string[] {
  try {
    const raw = JSON.parse(readFileSync(path, "utf8")) as unknown;
    return Array.isArray(raw) ? raw.map(String) : [];
  } catch {
    return [];
  }
}
