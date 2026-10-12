/** Explicit text formats; unsupported files keep the existing system-open behavior. */
const languages: Record<string, string> = {
  txt: "plaintext", text: "plaintext", log: "plaintext", csv: "plaintext", tsv: "plaintext",
  sql: "sql", md: "markdown", markdown: "markdown", mdx: "markdown",
  js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", tsx: "typescript", go: "go", py: "python", java: "java",
  c: "c", h: "c", cpp: "cpp", cc: "cpp", cxx: "cpp", hpp: "cpp", cs: "csharp",
  rs: "rust", rb: "ruby", php: "php", swift: "swift", kt: "kotlin", kts: "kotlin",
  sh: "bash", bash: "bash", zsh: "bash", ps1: "powershell", lua: "lua", r: "r",
  html: "xml", htm: "xml", xml: "xml", svg: "xml", vue: "xml", svelte: "xml",
  css: "css", scss: "scss", less: "less", json: "json", yaml: "yaml", yml: "yaml",
  toml: "ini", ini: "ini", conf: "plaintext", cfg: "ini", env: "bash",
  graphql: "graphql", gql: "graphql", proto: "protobuf", dockerfile: "dockerfile",
};
const names: Record<string, string> = { dockerfile: "dockerfile", makefile: "makefile", readme: "plaintext", license: "plaintext", '.gitignore': "plaintext", '.editorconfig': "ini" };
export function fileLanguage(path: string): string | null {
  const name = path.split(/[\\/]/).pop()?.toLowerCase() ?? "";
  return names[name] ?? (name.includes(".") ? languages[name.slice(name.lastIndexOf(".") + 1)] ?? null : null);
}
export interface TextFilePreview { path: string; content: string; language: string; }
export const MAX_TEXT_PREVIEW_BYTES = 512 * 1024;

export function previewReference(file: string, reference: string): string {
  if (/^(?:[\\/]|~[\\/]|[A-Za-z]:[\\/])/.test(reference)) return reference;
  return file.slice(0, Math.max(file.lastIndexOf("/"), file.lastIndexOf("\\")) + 1) + reference;
}
