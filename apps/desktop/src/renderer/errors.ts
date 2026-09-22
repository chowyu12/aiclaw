/**
 * 把异常整理成一句给人看的话。
 *
 * Electron 的 invoke 会把主进程的错误包成
 * `Error: Error invoking remote method 'runtime:start': Error: 真正的原因`，
 * 直接显示等于把通道名和两层 Error 前缀糊到用户脸上。这里只留最里面那句。
 */
export function describeError(error: unknown): string {
  let text = error instanceof Error ? error.message : String(error);
  const invoking = text.lastIndexOf("': ");
  if (text.startsWith("Error invoking remote method") && invoking >= 0) {
    text = text.slice(invoking + 3);
  }
  // 可能还剩一层或多层 "Error: " 前缀。
  while (text.startsWith("Error: ")) text = text.slice(7);
  return text.trim() || "未知错误";
}
