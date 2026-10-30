import { tr } from "../shared/i18n.js";
import type { StepEntry } from "./turns.js";

/** Summaries describe observed tool activity, never infer business progress from reasoning. */
export function activitySummary(steps: readonly StepEntry[]): string {
  const tools = steps.filter(step => step.step === "tool");
  const running = steps.some(step => step.state === "running");
  if (!tools.length) {
    const notice = steps.find(step => step.step === "notice");
    if (steps.some(step => step.state === "failed")) return notice?.title || tr("处理失败");
    return notice?.title || (running ? tr("正在思考…") : tr("处理完成"));
  }
  const counts = new Map<string, number>();
  for (const step of tools.filter(step => step.state === "done")) {
    const name = step.title.split(" · ")[0]!;
    const type = /^(read_file|read_office|view_image|list_dir|grep)$/.test(name) ? "read"
      : /^(run_command|exec|write_stdin)$/.test(name) ? "command"
      : /search/i.test(name) ? "search"
      : /^(write_|edit_|apply_patch)/.test(name) ? "write" : "tool";
    counts.set(type, (counts.get(type) ?? 0) + 1);
  }
  const labels = [...counts].map(([type, n]) => {
    switch (type) {
      case "read": return tr("读取了 {n} 项内容", { n });
      case "command": return tr("执行了 {n} 组操作", { n });
      case "search": return tr("进行了 {n} 次搜索", { n });
      case "write": return tr("修改了 {n} 个文件", { n });
      default: return tr("调用了 {n} 个工具", { n });
    }
  });
  if (steps.some(step => step.state === "failed")) labels.push(tr("有操作失败"));
  for (const notice of steps.filter(step => step.step === "notice")) labels.push(notice.title);
  if (running) {
    const active = tools.find(step => step.state === "running");
    const action = active?.title.split(" · ").slice(1).join(" · ");
    labels.push(action ? tr("正在执行：{action}", { action }) : tr("正在处理…"));
  }
  return labels.join(" · ");
}
