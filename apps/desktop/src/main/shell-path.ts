import { execFile } from "node:child_process";
import { existsSync, readdirSync } from "node:fs";
import { delimiter, join } from "node:path";

/**
 * 给内核（以及它替模型跑的命令）一个像终端里那样的 PATH。
 *
 * 从程序坞、访达打开的 macOS 应用拿到的 PATH 只有 `/usr/bin:/bin:/usr/sbin:/sbin`：
 * 用户在终端里 `npm install -g` 装的 CLI（落在 nvm 或 Homebrew 的 bin 下）在这里
 * 一律「command not found」。表现是技能明明认出来了，一跑就说命令不存在——
 * 比如企业微信的 `wecom-cli`。
 *
 * 做法：问一次用户的登录 shell（`$SHELL -ilc`，与终端里一样会读 .zshrc，nvm 就在那里
 * 初始化），拿到它的 PATH；再补上几处常见的 bin 目录兜底（shell 配置报错、超时时
 * 也不至于一个都找不到）。Windows 的 GUI 应用本来就继承完整 PATH，不用管。
 */

const MARKER = "__AICLAW_PATH__";

/** 读登录 shell 的 PATH。读不到返回空串，不抛。 */
export function loginShellPath(shell = process.env.SHELL || "/bin/zsh", timeoutMs = 5000): Promise<string> {
  if (process.platform === "win32") return Promise.resolve("");
  return new Promise((resolve) => {
    execFile(
      shell,
      ["-ilc", `printf '${MARKER}%s${MARKER}' "$PATH"`],
      // oh-my-zsh 之类在启动时会检查更新、打印横幅：关掉能关的，输出只认标记之间的那段。
      { timeout: timeoutMs, env: { ...process.env, DISABLE_AUTO_UPDATE: "true", ZSH_DISABLE_COMPFIX: "true" } },
      (_error, stdout) => resolve(extractPath(String(stdout ?? ""))),
    );
  });
}

export function extractPath(output: string): string {
  const start = output.indexOf(MARKER);
  if (start < 0) return "";
  const end = output.indexOf(MARKER, start + MARKER.length);
  if (end < 0) return "";
  return output.slice(start + MARKER.length, end).trim();
}

/** 常见的用户级 bin 目录（存在的才要）。nvm 每个版本一份，新的在前。 */
export function commonBinDirs(home: string): string[] {
  const dirs = ["/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin"];
  const nvm = join(home, ".nvm", "versions", "node");
  try {
    const versions = readdirSync(nvm)
      .filter((name) => name.startsWith("v"))
      .sort((a, b) => compareVersions(b, a));
    for (const version of versions) dirs.push(join(nvm, version, "bin"));
  } catch {
    // 没装 nvm
  }
  for (const relative of [".volta/bin", ".bun/bin", ".local/bin", ".cargo/bin", "go/bin", "bin", ".npm-global/bin"]) {
    dirs.push(join(home, relative));
  }
  return dirs.filter((dir) => existsSync(dir));
}

function compareVersions(a: string, b: string): number {
  const parse = (v: string) => v.replace(/^v/, "").split(".").map((n) => Number.parseInt(n, 10) || 0);
  const [x, y] = [parse(a), parse(b)];
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const diff = (x[i] ?? 0) - (y[i] ?? 0);
    if (diff !== 0) return diff;
  }
  return 0;
}

/** 按顺序合并几段 PATH，去掉空项与重复项。先出现的优先。 */
export function mergePath(...parts: (string | string[] | undefined)[]): string {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const part of parts) {
    const entries = Array.isArray(part) ? part : (part ?? "").split(delimiter);
    for (const entry of entries) {
      const trimmed = entry.trim();
      if (!trimmed || seen.has(trimmed)) continue;
      seen.add(trimmed);
      result.push(trimmed);
    }
  }
  return result.join(delimiter);
}

/** 登录 shell 的 PATH 在前（与终端一致），然后是应用自己的，最后是兜底目录。 */
export async function toolPath(home: string): Promise<string> {
  if (process.platform === "win32") return process.env.PATH ?? "";
  const login = await loginShellPath();
  return mergePath(login, process.env.PATH, commonBinDirs(home));
}
