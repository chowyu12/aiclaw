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
