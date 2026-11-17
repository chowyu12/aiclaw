/** File attachment parsing limits apply before IPC and again in the parser. */
export const MAX_DOCUMENT_BYTES = 25 * 1024 * 1024;
export const MAX_DOCUMENT_ATTACHMENTS = 32;
export const MAX_EXTRACTED_BYTES = 64 * 1024 * 1024;
export const MAX_ARCHIVE_ENTRIES = 512;
export const MAX_ATTACHMENT_TEXT_BYTES = 128 * 1024;
export const DOCUMENT_EXTENSIONS = new Set(["pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx"]);
export const ARCHIVE_EXTENSIONS = new Set(["zip", "7z", "rar", "tar", "gz", "tgz"]);
export const TEXT_EXTENSIONS = new Set([
  "txt", "md", "markdown", "json", "jsonl", "yaml", "yml", "toml", "ini", "conf", "env",
  "csv", "tsv", "log", "sql", "sh", "bash", "zsh", "py", "go", "rs", "java", "kt", "c", "h",
  "cc", "cpp", "hpp", "cs", "rb", "php", "swift", "ts", "tsx", "js", "jsx", "mjs", "cjs",
  "vue", "css", "scss", "less", "html", "htm", "xml", "svg", "gradle", "properties", "diff",
  "patch", "gitignore", "dockerfile", "makefile",
]);
export function attachmentExtension(name: string): string {
  return (name.split(/[\\/]/).pop()?.split(".").pop() ?? "").toLowerCase();
}
export interface AttachmentRef {
  id: string;
  name: string;
  size: number;
  sha256: string;
  /** Only the renderer uses this token to bind an upload to its first session. */
  claimToken: string;
}
export interface AttachmentReadQuery {
  mode?: "index" | "read";
  /** One-based document part/page/sheet/slide number. */
  section?: number;
  /** Exact archive member name from the index. */
  member?: string;
  /** Index position in index mode; UTF-8 byte offset in read mode. */
  offset?: number;
  limit?: number;
}
export interface AttachmentSection { number: number; kind: string; label: string }
export interface AttachmentMember { name: string; size: number; supported: boolean }
export interface ParsedAttachment {
  text: string;
  truncated: boolean;
  sections?: AttachmentSection[];
  members?: AttachmentMember[];
  total?: number;
  nextOffset?: number;
}
export function limitAttachmentText(text: string): ParsedAttachment {
  const bytes = new TextEncoder().encode(text);
  if (bytes.length <= MAX_ATTACHMENT_TEXT_BYTES) return { text, truncated: false };
  // Streaming decode drops an incomplete final UTF-8 character.
  return { text: new TextDecoder().decode(bytes.subarray(0, MAX_ATTACHMENT_TEXT_BYTES), { stream: true }), truncated: true };
}
