import { appendFileSync, mkdirSync, readdirSync, rmSync, statSync } from "node:fs";
import { join } from "node:path";

import { redact } from "./diagnostics.js";
import { expiredLogs, fileNameFor } from "./logfile-rules.js";

/**
 * 落盘的运行日志。策略（文件名、保留几天）在 logfile-rules.ts。
 *
 * 内存里那份（DiagnosticsLog）只留最后 400 行，是给「现在出了什么事」用的；
 * 真正难查的是**昨天那次**——用户第二天才说「昨天它卡了一下」，那时候内存里
 * 的早冲没了，应用可能还重启过。
 *
 * **每一行都过 redact**：日志会被复制、会被发到群里，与诊断报告同类。
 */

/** 单个文件的上限。一次疯狂刷屏不该把磁盘写满。 */
const MAX_FILE_BYTES = 32 * 1024 * 1024;

export class LogFile {
  private readonly dir: string;
  /** 当前写的文件名，用来发现跨天。 */
  private current = "";

  constructor(homeDir: string) {
    this.dir = join(homeDir, "logs");
  }

  get directory(): string {
    return this.dir;
  }

  /**
   * 写一行。
   *
   * 用同步写：日志量小（每秒几行），而异步写要排队、要处理并发追加，
   * 出错时还可能丢掉正是要查的那几行。**写失败一律吞掉**——
   * 日志写不进去不该让应用出问题，那是本末倒置。
   */
  append(source: string, text: string): void {
    for (const raw of text.split("\n")) {
      const line = raw.trimEnd();
      if (!line) continue;
      try {
        this.writeLine(`${new Date().toISOString()} [${source}] ${redact(line)}`);
      } catch {
        return;
      }
    }
  }

  private writeLine(line: string): void {
    const name = fileNameFor(new Date());
    if (name !== this.current) {
      // 换文件的时机也是清理的时机：不另起定时器，跨天就顺手收一次。
      mkdirSync(this.dir, { recursive: true });
      this.current = name;
      this.cleanup();
    }
    const path = join(this.dir, name);
    try {
      if (statSync(path).size > MAX_FILE_BYTES) return;
    } catch {
      // 文件还不存在，正常。
    }
    appendFileSync(path, `${line}\n`, "utf8");
  }

  /** 删掉过期的日志。失败不报——清不掉最多是多占点磁盘。 */
  cleanup(now = new Date()): void {
    try {
      for (const name of expiredLogs(readdirSync(this.dir), now)) {
        rmSync(join(this.dir, name), { force: true });
      }
    } catch {
      // 目录还不存在。
    }
  }
}
