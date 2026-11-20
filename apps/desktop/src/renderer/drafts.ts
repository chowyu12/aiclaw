import { reactive, watch } from "vue";
import type { Attachment } from "./attachments";
import type { MentionBinding } from "./mentions";

interface DraftContent { text: string; attachments: Attachment[]; bindings: MentionBinding[] }
export interface ChatDraft extends DraftContent {
  pendingAttachments: number;
  attachmentError: string;
}

interface DraftSnapshot extends DraftContent { start: number; end: number }
interface DraftHistory { undo: DraftSnapshot[]; redo: DraftSnapshot[] }

const drafts = new Map<string, { draft: ChatDraft; stop: () => void }>();
const histories = new WeakMap<ChatDraft, DraftHistory>();
const storageKey = (id: string) => `aiclaw:chat-draft:${id}`;

export function draftFor(sessionId: string): ChatDraft {
  const existing = drafts.get(sessionId);
  if (existing) return existing.draft;

  const content: DraftContent = { text: "", attachments: [], bindings: [] };
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey(sessionId)) ?? "null");
    if (saved && typeof saved.text === "string" && Array.isArray(saved.bindings) && Array.isArray(saved.attachments)) {
      content.text = saved.text;
      content.bindings = saved.bindings;
      content.attachments = saved.attachments.filter((item: Attachment) => item?.kind === "document" && item.file?.id && item.file.claimToken);
    }
  } catch { /* Storage can be unavailable. The live draft remains usable. */ }

  const draft = reactive<ChatDraft>({ ...content, pendingAttachments: 0, attachmentError: "" });
  const stop = watch(() => ({ text: draft.text, bindings: draft.bindings, attachments: draft.attachments.filter(item => item.kind === "document") }), value => {
    try {
      if (value.text || value.attachments.length) localStorage.setItem(storageKey(sessionId), JSON.stringify(value));
      else localStorage.removeItem(storageKey(sessionId));
    } catch { /* Keep the live draft if the local store is full or unavailable. */ }
  }, { deep: true, flush: "sync" });
  drafts.set(sessionId, { draft, stop });
  return draft;
}

export function forgetDraft(sessionId: string): void {
  drafts.get(sessionId)?.stop();
  drafts.delete(sessionId);
  try { localStorage.removeItem(storageKey(sessionId)); } catch { /* Storage unavailable. */ }
}

function snapshot(draft: ChatDraft, start: number, end: number): DraftSnapshot {
  return { text: draft.text, attachments: [...draft.attachments], bindings: [...draft.bindings], start, end };
}

export function checkpointDraft(draft: ChatDraft, start = draft.text.length, end = start): void {
  let history = histories.get(draft);
  if (!history) histories.set(draft, history = { undo: [], redo: [] });
  history.undo.push(snapshot(draft, start, end));
  if (history.undo.length > 100) history.undo.shift();
  history.redo.length = 0;
}

export function stepDraft(draft: ChatDraft, redo: boolean, start: number, end: number): { start: number; end: number } | undefined {
  const history = histories.get(draft);
  if (!history) return;
  const target = (redo ? history.redo : history.undo).pop();
  if (!target) return;
  (redo ? history.undo : history.redo).push(snapshot(draft, start, end));
  const { text, attachments, bindings } = target;
  Object.assign(draft, { text, attachments, bindings });
  return { start: target.start, end: target.end };
}

export function resetDraftHistory(draft: ChatDraft): void {
  histories.delete(draft);
}
