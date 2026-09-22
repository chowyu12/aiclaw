/**
 * 落盘日志的**保留策略**。
 *
 * 单独成文件是为了能在 node 里测（logfile.ts 要 import diagnostics 与 fs，
 * 那些在这里跑不起来）——而删文件的逻辑写错了没人会发现：要么日志一直堆着，
 * 要么把今天的也删了。与 open-file-rules.ts 是同一套分法。
 *
 * 下面这段说明讲的是整件事。
 *
 * 内存里那份（DiagnosticsLog）只留最后 400 行，是给「现在出了什么事」用的；
 * 而真正难查的问题是**昨天那次**——用户第二天才说「昨天它卡了一下」，那时候
 * 内存里的早冲没了，应用可能还重启过。所以再落一份到文件。
 *
 * 放 `~/.aiclaw/logs`：那是用户能自己打开看的地方，和技能、记忆放在一起。
 * 埋进 `~/Library/Application Support/` 等于藏起来。
 *
 * **每一行都过 redact。** 日志会被复制、会被发到群里，与诊断报告是同一类
 * 东西；凭据漏一次就是把等同登录态的 Key 交出去。
 */

/** 保留几天。再久的价值很低，而日志会一直长。 */
export const RETENTION_DAYS = 7;

/** 日志文件名。按天滚动——按大小滚的话，「昨天那次」要自己去翻哪个文件。 */
export function fileNameFor(at: Date): string {
  const year = at.getFullYear();
  const month = `${at.getMonth() + 1}`.padStart(2, "0");
  const day = `${at.getDate()}`.padStart(2, "0");
  return `aiclaw-${year}-${month}-${day}.log`;
}

/**
 * 挑出该删的日志文件。
 *
 * 纯函数分出来是为了能测：删文件的逻辑写错了没人会发现——要么日志一直堆着
 * （磁盘慢慢满），要么把今天的也删了（真出事时什么都没有）。
 */
export function expiredLogs(names: string[], now: Date, retentionDays = RETENTION_DAYS): string[] {
  const keep = new Set<string>();
  for (let day = 0; day < retentionDays; day++) {
    const at = new Date(now);
    at.setDate(at.getDate() - day);
    keep.add(fileNameFor(at));
  }
  return names.filter((name) => /^aiclaw-\d{4}-\d{2}-\d{2}\.log$/.test(name) && !keep.has(name));
}

