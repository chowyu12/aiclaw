import { tr } from "../shared/i18n.js";

export function filename(path: string): string {
  return path.split(/[\\/]/).pop() || path;
}

export function fileType(path: string): string {
  const name = filename(path);
  return name.includes(".") ? name.split(".").pop()?.toUpperCase() || "FILE" : "FILE";
}

/** Shared markup for Markdown references and generated attachments. Paths stay in attributes only. */
export function fileCard(path: string): string {
  const escape = (text: string) => text.replace(/[&<>"']/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]!);
  const name = filename(path), kind = fileType(path);
  const label = ({ DOC: "Word", DOCX: "Word", XLS: "Excel", XLSX: "Excel", PPT: "PowerPoint", PPTX: "PowerPoint", MD: "Markdown", MARKDOWN: "Markdown", TXT: tr("文本") } as Record<string, string>)[kind] ?? kind;
  const mark = ({ DOC: "W", DOCX: "W", XLS: "X", XLSX: "X", PPT: "P", PPTX: "P" } as Record<string, string>)[kind] ?? kind;
  return `<span class="file-card" data-file-path="${escape(path)}"><a href="#local-file" class="file-ref" role="button" aria-label="${escape(tr("打开文件 {name}", { name }))}"><span class="file-type" data-type="${escape(kind)}" aria-hidden="true"><span>${escape(mark)}</span></span><span class="file-info"><span class="file-name">${escape(name)}</span><span class="file-format">${escape(label)}</span></span></a><a href="#file-options" class="file-open-trigger" role="button" aria-haspopup="menu" aria-label="${escape(tr("打开方式：{name}", { name }))}">${escape(tr("打开方式"))}<span aria-hidden="true">⌄</span></a></span>`;
}
