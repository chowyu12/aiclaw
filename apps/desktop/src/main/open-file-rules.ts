import { join, sep } from "node:path";

/**
 * 「能不能直接打开」的判断规则。
 *
 * **单独成文件是为了能在 node 里测**：同目录的 open-file.ts 要 import
 * electron，而那个在 node 里 import 不了（与 config-defaults.ts 同理）。
 * 这里每一条都是安全判断，恰恰是最该逐条钉住的部分。
 *
 * 它连的是两头都不可信的东西：**路径来自模型输出**（而模型读得到文件、命令
 * 结果、内部平台数据，那些都可能被注入），**动作是交给系统用默认程序打开**——
 * 在 macOS 上「用默认程序打开」对 .command/.app/.pkg 这类就是**执行**。
 * 所以这里的规矩是：
 *
 *   1. 可执行的那几类**不直接打开**，改成在访达里选中它，让用户自己决定；
 *   2. 带执行位的普通文件同样按可执行处理（扩展名骗不过 chmod +x）；
 *   3. 凭据目录一律拒绝，连「在访达里显示」都不给——那些路径没有任何
 *      正当的「点开看看」理由；
 *   4. 路径按**会话工作区**解析，不存在就如实说找不到，不去猜。
 */

/** 打开一个文件之后会发生什么。 */
export type OpenVerdict =
  /** 用默认程序打开。 */
  | { action: "open" }
  /** 只在访达/资源管理器里选中——打开等于执行。 */
  | { action: "reveal"; reason: string }
  /** 连显示都不给。 */
  | { action: "refuse"; reason: string };

/**
 * 「用默认程序打开」等于执行的那些扩展名。
 *
 * macOS 那几个（.command/.app/.scpt/.workflow）双击就跑；Windows 那几个
 * （.exe/.bat/.cmd/.ps1/.vbs/.scr/.reg/.msi）同理。.dmg/.pkg 不直接执行，
 * 但一路点下去就是装东西，同样不该由模型输出一键触发。
 */
const EXECUTABLE_EXTENSIONS = new Set([
  "command", "app", "scpt", "applescript", "workflow", "term", "tool",
  "sh", "bash", "zsh", "fish", "py", "rb", "pl", "php", "jar",
  "exe", "bat", "cmd", "com", "ps1", "psm1", "vbs", "vbe", "js", "jse",
  "wsf", "wsh", "scr", "reg", "msi", "msp", "lnk", "inf", "hta",
  "dmg", "pkg", "mpkg", "iso",
]);

/** 凭据类目录。与内核那份是同一个判断，只是这边管「打开」。 */
const PROTECTED_RELATIVE = [
  ".ssh", ".aws", ".gnupg", ".netrc", ".npmrc", ".pypirc",
  ".docker/config.json", ".kube/config", ".config/gcloud",
  "Library/Keychains",
];

function within(root: string, target: string): boolean {
  if (target === root) return true;
  return target.startsWith(root.endsWith(sep) ? root : root + sep);
}

/** 扩展名（小写，不含点）。没有扩展名返回空串。 */
export function extensionOf(path: string): string {
  const name = path.split(/[\\/]/).pop() ?? "";
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : "";
}

/**
 * 只看路径与属性，决定能不能直接打开。纯函数那半，便于逐条钉。
 *
 * executable 由调用方从文件权限位上取——扩展名骗得过人，骗不过 chmod +x。
 */
export function classifyOpen(
  path: string,
  options: { home: string; protectedPaths: string[]; executable: boolean },
): OpenVerdict {
  const candidates = [
    ...PROTECTED_RELATIVE.map((relative) => join(options.home, relative)),
    ...options.protectedPaths,
  ];
  for (const candidate of candidates) {
    if (candidate && within(candidate, path)) {
      return { action: "refuse", reason: "这是存放凭据的位置，不能从对话里打开" };
    }
  }
  const extension = extensionOf(path);
  if (EXECUTABLE_EXTENSIONS.has(extension)) {
    return { action: "reveal", reason: `.${extension} 文件打开就等于执行，先在访达里给你标出来` };
  }
  if (options.executable) {
    return { action: "reveal", reason: "这个文件带可执行权限，打开就等于执行，先在访达里给你标出来" };
  }
  return { action: "open" };
}

