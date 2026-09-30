/**
 * 输入框里的 @ 引用会话（参照 Codex 的 task mentions，codex-rs/tui/src/task_mentions.rs）。
 *
 * 打 @ 弹出会话列表，选中后插入「@标题 」，并记下它绑的是哪个会话。发送时只把还留在
 * 正文里的那几个交给内核（用户可能又删掉了），内核在给模型的那份消息里附上引用说明。
 */

/** 与内核、Codex 一致：一条消息最多 16 个引用，标题最长 160 个字符。 */
export const MAX_REFERENCES = 16;
export const MAX_TITLE_CHARS = 160;
/** @ 之后最多打多少字还算在「找会话」：再长就是普通文字里恰好有个 @。 */
const MAX_QUERY_CHARS = 40;

export interface MentionBinding {
  id: string;
  title: string;
}

/**
 * 光标前面是不是一个正在打的 @：@ 在开头或者前面是空白，@ 与光标之间没有空白。
 * 是的话给出 @ 的位置与已经打了的查询词。
 */
export function mentionQuery(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0) return null;
  if (at > 0 && !/\s/.test(before[at - 1]!)) return null;
  const query = before.slice(at + 1);
  if (/\s/.test(query) || [...query].length > MAX_QUERY_CHARS) return null;
  return { start: at, query };
}

/** 引用标签上与插进正文里的标题：去掉换行、截到上限。 */
export function mentionTitle(title: string): string {
  const flat = (title || "未命名会话").replace(/\s+/g, " ").trim();
  return [...flat].slice(0, MAX_TITLE_CHARS).join("");
}

/** 把「@查询词」换成「@标题 」，返回新正文与光标位置。 */
export function applyMention(text: string, start: number, caret: number, title: string): { text: string; caret: number } {
  const inserted = `@${mentionTitle(title)} `;
  const next = text.slice(0, start) + inserted + text.slice(caret);
  return { text: next, caret: start + inserted.length };
}

/** 发送时还有效的引用：正文里还留着「@标题」的，按 id 去重，兜住条数。 */
export function activeReferences(text: string, bindings: readonly MentionBinding[]): MentionBinding[] {
  const seen = new Set<string>();
  const result: MentionBinding[] = [];
  for (const binding of bindings) {
    if (seen.has(binding.id) || !text.includes(`@${binding.title}`)) continue;
    seen.add(binding.id);
    result.push({ id: binding.id, title: binding.title });
    if (result.length === MAX_REFERENCES) break;
  }
  return result;
}

/** 候选会话：标题里含查询词的在前（不分大小写），然后是正文搜索命中的；去掉当前会话。 */
export function mentionCandidates<T extends { id: string; title: string }>(
  query: string,
  sessions: readonly T[],
  searchHits: readonly T[],
  currentId: string,
  limit = 8,
): T[] {
  const needle = query.trim().toLowerCase();
  const seen = new Set<string>([currentId]);
  const result: T[] = [];
  const push = (session: T) => {
    if (seen.has(session.id) || result.length >= limit) return;
    seen.add(session.id);
    result.push(session);
  };
  for (const session of sessions) {
    if (!needle || (session.title || "").toLowerCase().includes(needle)) push(session);
  }
  if (needle) for (const session of searchHits) push(session);
  return result;
}
