/** Same byte limit as the kernel's long-term memory loader. */
export const MAX_MEMORY_BYTES = 32 * 1024;
export function memoryBytes(content: string): number { return new TextEncoder().encode(content).length; }

/** Preserve raw text and dates; a bullet plus continuation lines is one entry. */
export function memoryEntries(content: string): string[] {
  const entries: string[] = [];
  for (const line of content.match(/[^\n]*\n|[^\n]+$/g) ?? []) {
    if (!entries.length || /^\s*[-*] /.test(line)) entries.push(line);
    else entries[entries.length - 1] += line;
  }
  return entries;
}

/** Exact textual deduplication only; preserve the user's existing text and dates. */
export function memoryImportEntries(existing: string, incoming: string): string[] {
  const key = (entry: string) => entry.trim().replace(/^[-*]\s+/, "").replace(/\s+/g," ");
  const seen = new Set(memoryEntries(existing).map(key));
  return memoryEntries(incoming).filter(entry => {
    const id = key(entry);
    if (!id || seen.has(id)) return false;
    seen.add(id); return true;
  });
}

export function mergeMemoryImport(existing: string, selected: readonly string[]): string {
  const additions = memoryImportEntries(existing, selected.join("\n"));
  return existing + (existing && !existing.endsWith("\n") && additions.length ? "\n" : "") + additions.map(entry=>entry.trimEnd()+"\n").join("");
}
