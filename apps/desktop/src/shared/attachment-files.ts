/** File attachment parsing limits apply before IPC and again in the parser. */
export const MAX_DOCUMENT_BYTES = 25 * 1024 * 1024;
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
export interface ParsedAttachment { text: string; truncated: boolean }
export function limitAttachmentText(text: string): ParsedAttachment {
  const bytes = new TextEncoder().encode(text);
  if (bytes.length <= MAX_ATTACHMENT_TEXT_BYTES) return { text, truncated: false };
  // Streaming decode drops an incomplete final UTF-8 character.
  return { text: new TextDecoder().decode(bytes.subarray(0, MAX_ATTACHMENT_TEXT_BYTES), { stream: true }), truncated: true };
}
