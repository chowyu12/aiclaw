import { tr } from "../shared/i18n.js";

/**
 * 用量页上的数字怎么写。纯函数单独成文件，为的是能在 node 里测（scripts/test-usage-format.ts）。
 */

/** 大数写成 K / M：12 345 → 12.3K，1 234 567 → 1.23M。一万以下原样带千分位。 */
export function formatCount(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value < 10_000) return Math.round(value).toLocaleString("en-US");
  if (value < 1_000_000) return `${trim(value / 1000, 1)}K`;
  if (value < 1_000_000_000) return `${trim(value / 1_000_000, 2)}M`;
  return `${trim(value / 1_000_000_000, 2)}B`;
}

function trim(value: number, digits: number): string {
  return value.toFixed(digits).replace(/\.?0+$/, "");
}

/** 毫秒写成人看的耗时：850ms、1.2s、2m 5s。 */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${trim(ms / 1000, 1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return seconds ? `${minutes}m ${seconds}s` : `${minutes}m`;
}

/**
 * 工具来源写成中文：builtin → 内置，mcp:aihot → MCP · aihot，
 * exec:mcp:aihot → 代码模式 · MCP · aihot（代码模式脚本里调的）。
 */
export function sourceLabel(source: string): string {
  const parts: string[] = [];
  let rest = source;
  if (rest.startsWith("exec:")) {
    parts.push(tr("代码模式"));
    rest = rest.slice(5);
  }
  if (rest.startsWith("mcp:")) parts.push(`MCP · ${rest.slice(4)}`);
  else if (rest === "builtin" || rest === "") parts.push(tr("内置"));
  else parts.push(rest);
  return parts.join(" · ");
}
