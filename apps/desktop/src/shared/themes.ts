/** App-wide color choices; missing or invalid saved values retain the white default. */
export const THEMES = [
  { id: "white", label: "白色（默认）", accent: "#4b63a9", surface: "#ffffff", sidebar: "#f7f8fa" },
  { id: "green", label: "品牌青绿", accent: "#16846a", surface: "#ffffff", sidebar: "#f3f7f5" },
  { id: "blue", label: "冷静雾蓝", accent: "#5269c5", surface: "#ffffff", sidebar: "#f2f5fc" },
  { id: "sand", label: "暖砂阅读", accent: "#a87536", surface: "#fffefb", sidebar: "#f7f4ed" },
  { id: "violet", label: "柔和墨紫", accent: "#7b62af", surface: "#ffffff", sidebar: "#f5f2fa" },
  { id: "mist", label: "雾蓝浅紫", accent: "#6655a6", surface: "#fcfdff", sidebar: "#f0f4fa" },
  { id: "mint", label: "薄荷青绿", accent: "#168266", surface: "#fafcfc", sidebar: "#eaf5f0" },
  { id: "studio", label: "轻盈工作室", accent: "#23785f", surface: "#fcfdfe", sidebar: "#f1f5fa" },
  { id: "reading", label: "暖白阅读", accent: "#a45640", surface: "#fffdf8", sidebar: "#f5f0e8" },
  { id: "developer", label: "清爽开发台", accent: "#6d5da8", surface: "#ffffff", sidebar: "#f1f3f9" },
  { id: "dark", label: "深色", accent: "#9baded", surface: "#1c1f26", sidebar: "#1a1d23" },
  { id: "modern", label: "现代极简", accent: "#c1b1ff", surface: "#18191c", sidebar: "#131416" },
] as const;

export type Theme = typeof THEMES[number]["id"];

export function usesWorkspaceStyle(theme: unknown): boolean {
  return theme === "studio" || theme === "reading" || theme === "developer";
}

export function usesMessageIdentity(theme: unknown): boolean {
  return theme === "mint" || usesWorkspaceStyle(theme);
}

export function normalizeTheme(value: unknown): Theme {
  return THEMES.find(theme => theme.id === value)?.id ?? "white";
}
