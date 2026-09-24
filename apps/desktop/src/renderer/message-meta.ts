/**
 * 消息下面那一行：时间与复制。
 *
 * 纯函数单独成文件，为的是能在 node 里测（scripts/test-message-meta.ts）。
 */

/**
 * 消息时间的显示。
 *
 * 今天的只写时分；今年的带月日；更早的带年份。看会话的人关心的是「这是多久以前的」，
 * 今天的消息写上日期只是噪音，而一条去年的消息只写时分会被当成今天的。
 * 0 或非法值（旧存档里的消息没有时间）返回空串，界面上就不显示。
 */
export function formatMessageTime(at: number | undefined, now: Date = new Date()): string {
  if (!at || !Number.isFinite(at) || at <= 0) return "";
  const time = new Date(at);
  const pad = (value: number): string => String(value).padStart(2, "0");
  const clock = `${pad(time.getHours())}:${pad(time.getMinutes())}`;
  const sameDay =
    time.getFullYear() === now.getFullYear() &&
    time.getMonth() === now.getMonth() &&
    time.getDate() === now.getDate();
  if (sameDay) return clock;
  if (time.getFullYear() === now.getFullYear()) return `${time.getMonth() + 1}月${time.getDate()}日 ${clock}`;
  return `${time.getFullYear()}年${time.getMonth() + 1}月${time.getDate()}日 ${clock}`;
}

/** 悬停时的完整时间，精确到秒。 */
export function fullMessageTime(at: number | undefined): string {
  if (!at || !Number.isFinite(at) || at <= 0) return "";
  const time = new Date(at);
  const pad = (value: number): string => String(value).padStart(2, "0");
  return (
    `${time.getFullYear()}-${pad(time.getMonth() + 1)}-${pad(time.getDate())} ` +
    `${pad(time.getHours())}:${pad(time.getMinutes())}:${pad(time.getSeconds())}`
  );
}

/**
 * 一轮回答要复制的文本。
 *
 * 一轮里模型会被采样好几次（每次工具回填之后再问一次），回答散成几截；复制的是
 * 这一轮完整的回答，原样的 Markdown——粘到别处还能再渲染，粘进纯文本也读得懂。
 */
export function answerText(messages: readonly { text: string }[]): string {
  return messages
    .map((message) => message.text.trim())
    .filter((text) => text.length > 0)
    .join("\n\n");
}

/** 一轮回答的时间：最后一截的时间，那是答完的时候。 */
export function answerTime(messages: readonly { at?: number }[]): number | undefined {
  for (let index = messages.length - 1; index >= 0; index--) {
    const at = messages[index]!.at;
    if (at && at > 0) return at;
  }
  return undefined;
}
