export interface DiffLine { kind: "same" | "add" | "remove"; text: string }
/** Bounded line diff: very large files use replacement blocks to keep rendering responsive. */
export function fileDiff(before: string, after: string, maxLines = Infinity): DiffLine[] {
  if (before === after) return [];
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  if (a.length === 0) return b.slice(0,maxLines).map(text=>({kind:"add",text}));
  if (b.length === 0) return a.slice(0,maxLines).map(text=>({kind:"remove",text}));
  if (a.length * b.length > 250_000) return [
    ...a.slice(0,maxLines).map(text => ({kind: "remove" as const, text})),
    ...b.slice(0,Math.max(0,maxLines-a.length)).map(text => ({kind: "add" as const, text})),
  ];
  const widths = b.length + 1;
  const dp = new Uint32Array((a.length + 1) * widths);
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--)
    dp[i * widths + j] = a[i] === b[j] ? 1 + dp[(i + 1) * widths + j + 1]! : Math.max(dp[(i + 1) * widths + j]!, dp[i * widths + j + 1]!);
  const out: DiffLine[] = [];
  let i = 0, j = 0;
  while ((i < a.length || j < b.length) && out.length < maxLines) {
    if (i < a.length && j < b.length && a[i] === b[j]) { out.push({kind: "same", text: a[i++]!}); j++; }
    else if (i < a.length && (j === b.length || dp[(i + 1) * widths + j]! >= dp[i * widths + j + 1]!)) out.push({kind: "remove", text: a[i++]!});
    else out.push({kind: "add", text: b[j++]!});
  }
  return out;
}
