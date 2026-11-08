import { reactive } from "vue";
import type { Attachment } from "./attachments";
import type { MentionBinding } from "./mentions";

export interface ChatDraft { text: string; attachments: Attachment[]; bindings: MentionBinding[] }

// Keep drafts outside the view so navigating away does not discard them.
const drafts = reactive<Record<string, ChatDraft>>({});

export function draftFor(sessionId: string): ChatDraft {
  return drafts[sessionId] ??= { text: "", attachments: [], bindings: [] };
}
