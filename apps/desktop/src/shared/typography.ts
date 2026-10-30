/** Font stacks use local fonts only, with fallbacks for each desktop platform. */
export const FONTS = [
  { id: "system", label: "系统默认字体", family: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "PingFang SC", "Microsoft YaHei", "Segoe UI", system-ui, sans-serif' },
  { id: "pingfang", label: "苹方", family: '"PingFang SC", "Microsoft YaHei", sans-serif' },
  { id: "yahei", label: "微软雅黑", family: '"Microsoft YaHei", "PingFang SC", sans-serif' },
  { id: "songti", label: "宋体", family: '"Songti SC", "SimSun", "Noto Serif CJK SC", serif' },
  { id: "mono", label: "等宽字体", family: '"SF Mono", Menlo, Consolas, "PingFang SC", monospace' },
] as const;
export type Font = typeof FONTS[number]["id"];
export const FONT_SIZES = [12, 13, 14, 15, 16, 18, 20, 22, 24] as const;
export type FontSize = typeof FONT_SIZES[number];

export function normalizeFont(value: unknown): Font {
  return FONTS.find(font => font.id === value)?.id ?? "system";
}
export function normalizeFontSize(value: unknown): FontSize {
  return FONT_SIZES.find(size => size === value) ?? 14;
}
