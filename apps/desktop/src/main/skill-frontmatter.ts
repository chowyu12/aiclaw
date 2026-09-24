/**
 * 读 SKILL.md 开头的 frontmatter。
 *
 * 与 claw-agent 的 internal/skills（splitFrontmatter）同一套规则——两边都只认
 * name 与 description。改一边要改另一边，否则技能页显示的说明会和模型看到的对不上。
 * 两边各有一组同样的用例（Go：skills_test.go；这边：scripts/test-skill-frontmatter.ts）。
 *
 * 单独成文件是为了能在 node 里测：skills.ts 要 import electron。
 *
 * **块标量要认**（`description: >` 或 `|` 后面跟缩进行）：说明写长了就得折行，
 * 而折行在 YAML 里只有这一种写法。不认的话取到的值是字符串 ">"——技能页上
 * 显示成一个尖括号。Codex 装的技能就这么写。
 */

export interface SkillMeta {
  name: string;
  description: string;
}

export function parseFrontmatter(source: string): SkillMeta {
  const normalized = source.replace(/\r\n/g, "\n");
  const result: SkillMeta = { name: "", description: "" };
  if (!normalized.startsWith("---\n")) return result;
  const end = normalized.indexOf("\n---", 4);
  if (end < 0) return result;

  const lines = normalized.slice(4, end).split("\n");
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]!;
    // 缩进行是上一个键的续行，已经在下面一并吃掉了。
    if (/^[ \t]/.test(line)) continue;
    const separator = line.indexOf(":");
    if (separator < 0) continue;
    const key = line.slice(0, separator).trim().toLowerCase();
    if (key !== "name" && key !== "description") continue;

    let value = line.slice(separator + 1).trim();
    const style = blockStyle(value);
    if (style !== null) {
      const { text, consumed } = readBlockScalar(lines.slice(index + 1), style === "folded");
      value = text;
      index += consumed;
    } else {
      value = value.replace(/^["']|["']$/g, "");
    }
    result[key] = value;
  }
  return result;
}

/**
 * 认出块标量的引子：`>`、`|`，可带 chomping 指示符（-、+）与缩进数字。
 * 引子后面有别的字就不是块标量（`description: >不是块标量`）。
 */
function blockStyle(value: string): "folded" | "literal" | null {
  if (!value || (value[0] !== ">" && value[0] !== "|")) return null;
  if (!/^[-+0-9]*$/.test(value.slice(1))) return null;
  return value[0] === ">" ? "folded" : "literal";
}

/** 读块标量的正文：缩进的连续行。折叠式（>）换行折成空格，字面式（|）保留换行。 */
function readBlockScalar(lines: string[], folded: boolean): { text: string; consumed: number } {
  let indent = -1;
  const collected: string[] = [];
  let consumed = 0;
  for (const line of lines) {
    const trimmed = line.replace(/^[ \t]+/, "");
    if (trimmed === "") {
      collected.push("");
      consumed++;
      continue;
    }
    const width = line.length - trimmed.length;
    if (width === 0) break; // 回到顶格，块结束
    if (indent < 0) indent = width;
    if (width < indent) break;
    collected.push(line.slice(indent));
    consumed++;
  }
  // 去掉块尾的空行，不然折叠出来会多一串空格。
  while (collected.length > 0 && collected[collected.length - 1]!.trim() === "") collected.pop();
  if (folded) return { text: foldLines(collected).trim(), consumed };
  return { text: collected.join("\n").replace(/\n+$/, ""), consumed };
}

/** YAML 折叠式：相邻非空行用空格连起来，空行成为换行。 */
function foldLines(lines: string[]): string {
  let out = "";
  for (const line of lines) {
    if (line.trim() === "") {
      out += "\n";
      continue;
    }
    if (out.length > 0 && !out.endsWith("\n")) out += " ";
    out += line.replace(/[ \t]+$/, "");
  }
  return out;
}
