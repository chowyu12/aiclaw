import hljs from "highlight.js/lib/common";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import powershell from "highlight.js/lib/languages/powershell";
import protobuf from "highlight.js/lib/languages/protobuf";
hljs.registerLanguage("dockerfile", dockerfile);
hljs.registerLanguage("powershell", powershell);
hljs.registerLanguage("protobuf", protobuf);
export function highlightCode(content: string, language: string): string {
  if (content.length <= 50_000 && hljs.getLanguage(language)) {
    return hljs.highlight(content, { language, ignoreIllegals: true }).value;
  }
  return content.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;");
}
