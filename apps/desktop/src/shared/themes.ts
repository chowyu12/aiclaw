/** App-wide color choices; missing or invalid saved values retain the white default. */
export const THEMES = [
  { id: "white", label: "白色（默认）", accent: "#4b63a9", surface: "#ffffff", sidebar: "#f7f8fa" },
  { id: "green", label: "品牌青绿", accent: "#16846a", surface: "#ffffff", sidebar: "#f3f7f5" },
  { id: "blue", label: "冷静雾蓝", accent: "#5269c5", surface: "#ffffff", sidebar: "#f2f5fc" },
  { id: "sand", label: "暖砂阅读", accent: "#a87536", surface: "#fffefb", sidebar: "#f7f4ed" },
  { id: "violet", label: "柔和墨紫", accent: "#7b62af", surface: "#ffffff", sidebar: "#f5f2fa" },
  { id: "dark", label: "深色", accent: "#9baded", surface: "#1c1f26", sidebar: "#1a1d23" },
] as const;

export type Theme = typeof THEMES[number]["id"];

export function normalizeTheme(value: unknown): Theme {
  return THEMES.find(theme => theme.id === value)?.id ?? "white";
}
