export interface PromptPreset { slot: number; title: string; text: string; }
export const PRESET_SLOTS = 10;
export function normalizePresets(value: unknown): PromptPreset[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<number>();
  return value.filter((p): p is PromptPreset => Boolean(p) && Number.isInteger(p.slot) && p.slot >= 1 && p.slot <= PRESET_SLOTS && typeof p.title === "string" && typeof p.text === "string" && Boolean(p.title.trim()) && Boolean(p.text.trim()) && !seen.has(p.slot) && Boolean(seen.add(p.slot)))
    .map(p => ({slot:p.slot,title:p.title.trim().slice(0,80),text:p.text.slice(0,20000)})).sort((a,b) => a.slot-b.slot);
}
export function presetCandidates(query: string, presets: readonly PromptPreset[]): PromptPreset[] {
  const needle = query.toLowerCase();
  return presets.filter(p => `preset${p.slot} ${p.title}`.toLowerCase().includes(needle));
}
export function applyPreset(draft: string, start: number, caret: number, preset: PromptPreset): {text: string;caret: number} {
  const tail = draft.slice(caret);
  const inserted = preset.text + (tail && !/^\s/.test(tail) ? " " : "");
  return {text:draft.slice(0,start)+inserted+tail,caret:start+inserted.length};
}
