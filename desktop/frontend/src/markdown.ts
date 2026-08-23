import DOMPurify, { type Config } from "dompurify";
import { marked } from "marked";

const sanitizeOptions: Config = {
  USE_PROFILES: { html: true },
  FORBID_TAGS: [
    "base",
    "button",
    "embed",
    "form",
    "iframe",
    "input",
    "link",
    "meta",
    "object",
    "option",
    "select",
    "style",
    "textarea",
  ],
  FORBID_ATTR: ["style"],
};

/** Convert model output to safe HTML while retaining Markdown and benign HTML. */
export function renderMarkdown(source: string): string {
  if (!source) return "";
  const parsed = marked.parse(source, {
    async: false,
    breaks: true,
    gfm: true,
  }) as string;
  return DOMPurify.sanitize(parsed, sanitizeOptions);
}
