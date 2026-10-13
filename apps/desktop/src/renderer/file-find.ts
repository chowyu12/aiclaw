export interface TextMatch { start: number; length: number; }
/** Literal, case-insensitive search; cap ranges so large files remain responsive. */
export function textMatches(text: string, query: string, limit = 1000): { matches: TextMatch[]; truncated: boolean } {
  if (!query) return {matches:[],truncated:false};
  const expression = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g,"\\$&"),"giu"), matches: TextMatch[] = [];
  for (const found of text.matchAll(expression)) {
    if (matches.length === limit) return {matches,truncated:true};
    matches.push({start:found.index!,length:found[0].length});
  }
  return {matches,truncated:false};
}
