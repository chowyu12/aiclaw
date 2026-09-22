/**
 * 诊断信息：一段能直接复制给别人的文本。
 *
 * 为什么要有：出了研发范围之后，同事报「它不工作」，而我们手上什么都没有——
 * 内核的 stderr 只在启动失败时拼进一句错误里，之后就被冲掉了；版本、平台、
 * 挂载状态、工作目录这些每次都要一问一答。
 *
 * **凭据绝不能进这里。** 它们不写配置文件、不进日志、不经协议帧，但 stderr 是
 * 子进程随便写的，上游返回的错误里也可能回显 Authorization。所以每一行都过
 * 一遍 redact——这一步是纯函数，被 scripts/test-diagnostics.ts 钉着。
 */

/** 一行日志。 */
export interface LogLine {
  /** Unix 毫秒。 */
  at: number;
  /** 来自哪儿：kernel（内核 stderr）、app（宿主自己）。 */
  source: string;
  text: string;
}

/** 报告里那些「是什么环境」的事实。值都必须是非敏感的。 */
export interface ReportFacts {
  version: string;
  electron: string;
  node: string;
  platform: string;
  arch: string;
  /** 各种目录，排查「东西写到哪儿去了」。 */
  paths: Record<string, string>;
  /** 运行时状态、模型、端点这类。**只放地址不放 Key。** */
  runtime: Record<string, string>;
  /** MCP 挂载结果。失败原因是排查时第一个要看的。 */
  mounts: Record<string, string>;
}

const REDACTIONS: [RegExp, string][] = [
  // OpenAI 风格的 Key。内部平台的 BFF Key 不是这个形状，但下面几条兜得住。
  [/\bsk-[A-Za-z0-9_-]{6,}/g, "sk-***"],
  [/\b(bearer\s+)\S+/gi, "$1***"],
  // KEY=value / "token": "value" / token: value
  [
    /\b([A-Za-z_]*(?:token|key|secret|password|passwd|authorization)[A-Za-z_]*)(\s*["']?\s*[=:]\s*["']?)([^\s"',}]+)/gi,
    "$1$2***",
  ],
  // URL 上的凭据参数。真实例子：?aihot_actor=<uuid> 就是一把钥匙。
  [
    /([?&][A-Za-z0-9_-]*(?:token|key|actor|secret|sig|signature|auth)[A-Za-z0-9_-]*=)[^&\s"']+/gi,
    "$1***",
  ],
];

/**
 * 抹掉看起来像凭据的东西。
 *
 * 宁可多抹：抹错了最多让一行日志少几个字，漏了一次就是把等同登录态的
 * BFF Key 贴进了工单。
 */
export function redact(text: string): string {
  let out = text;
  for (const [pattern, replacement] of REDACTIONS) out = out.replace(pattern, replacement);
  return out;
}

/** 拼出给人看（和给人复制）的报告。 */
export function buildReport(facts: ReportFacts, lines: LogLine[]): string {
  const rows: string[] = [];
  rows.push("# 内部平台诊断信息");
  rows.push("");
  rows.push(`版本：${facts.version}　Electron ${facts.electron}　Node ${facts.node}`);
  rows.push(`平台：${facts.platform} ${facts.arch}`);
  rows.push("");

  const section = (title: string, pairs: Record<string, string>): void => {
    rows.push(`## ${title}`);
    const entries = Object.entries(pairs);
    if (entries.length === 0) rows.push("（无）");
    for (const [key, value] of entries) rows.push(`- ${key}：${redact(value)}`);
    rows.push("");
  };

  section("运行时", facts.runtime);
  section("挂载", facts.mounts);
  section("目录", facts.paths);

  rows.push(`## 最近日志（${lines.length} 行）`);
  if (lines.length === 0) rows.push("（无）");
  for (const line of lines) {
    rows.push(`${new Date(line.at).toISOString()} [${line.source}] ${redact(line.text)}`);
  }
  return rows.join("\n");
}

/**
 * 一个定长的日志环。
 *
 * 定长是关键：内核可能在一个循环里疯狂打日志，无上限地留着就是把内存
 * 慢慢吃光，而排查需要的只有最后那几百行。
 */
export class DiagnosticsLog {
  private readonly limit: number;
  private lines: LogLine[] = [];

  constructor(limit = 400) {
    this.limit = limit;
  }

  /** 收一段输出。按行切开，空行丢掉——子进程的 stderr 常常以换行结尾。 */
  push(source: string, chunk: string): void {
    for (const raw of chunk.split("\n")) {
      const text = raw.trimEnd();
      if (!text) continue;
      this.lines.push({ at: Date.now(), source, text });
    }
    if (this.lines.length > this.limit) {
      this.lines = this.lines.slice(-this.limit);
    }
  }

  all(): LogLine[] {
    return this.lines;
  }
}
