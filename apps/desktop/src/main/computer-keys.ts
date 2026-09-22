/**
 * computer use 里不依赖 Electron 的纯函数：按键翻译、转义、自点判断。
 *
 * 单独成文件是为了能测——computer.ts 要 import electron，在普通 Node 里
 * import 不了（与 renderer/errors.ts 同样的原因）。
 *
 * 按键翻译错了不会报错，只会**按下别的键**：把 cmd+s 按成一个字母 s，
 * 在正在编辑的窗口里就是往文档里插字符。所以这两组表由 scripts/test-computer.ts
 * 钉着。
 */

/** 本应用在系统里的进程名。查「最前面是不是我」时比对它。 */
// 三个名字对应三种跑法：开发期 `electron .` 的进程叫 Electron，app.setName
// 设的是 aiclaw，打出来的 .app 叫 AIClaw。这张表是自点审批的防护，
// 少一个名字就是多一条模型点得到自己弹窗的路，而多一个名字不会误伤。
export const SELF_PROCESS_NAMES = ["Electron", "aiclaw", "AIClaw"];

/**
 * 最前面的应用是不是我们自己。
 *
 * 是的话要拒绝输入：模型点自己的窗口意味着它可能在点审批弹窗，
 * 而审批是没有沙箱之后仅剩的闸。查不到（空串）时不阻断——
 * 这道防护是额外的，不该因为查询失败就让整个功能不可用。
 */
export function isSelf(frontmost: string): boolean {
  if (!frontmost.trim()) return false;
  return SELF_PROCESS_NAMES.some((name) => frontmost.includes(name));
}

/** 把文本包成 AppleScript 字符串字面量。 */
export function appleString(text: string): string {
  return `"${text.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/** AppleScript 里的修饰键名。 */
const MAC_MODIFIERS: Record<string, string> = {
  cmd: "command down",
  command: "command down",
  ctrl: "control down",
  control: "control down",
  alt: "option down",
  option: "option down",
  shift: "shift down",
};

/** 常见的功能键在 AppleScript 里要用 key code。 */
const MAC_KEY_CODES: Record<string, number> = {
  return: 36,
  enter: 36,
  tab: 48,
  space: 49,
  delete: 51,
  backspace: 51,
  escape: 53,
  esc: 53,
  left: 123,
  right: 124,
  down: 125,
  up: 126,
};

export function macKeyScript(combo: string): string {
  const parts = combo.split("+").map((part) => part.trim().toLowerCase()).filter(Boolean);
  if (parts.length === 0) throw new Error("按键组合为空");

  const target = parts[parts.length - 1] ?? "";
  const modifiers = parts.slice(0, -1).map((part) => {
    const name = MAC_MODIFIERS[part];
    if (!name) throw new Error(`不认识的修饰键：${part}`);
    return name;
  });
  const using = modifiers.length > 0 ? ` using {${modifiers.join(", ")}}` : "";

  const code = MAC_KEY_CODES[target];
  if (code !== undefined) {
    return `tell application "System Events" to key code ${code}${using}`;
  }
  if (target.length !== 1) {
    throw new Error(`不认识的按键：${target}`);
  }
  return `tell application "System Events" to keystroke ${appleString(target)}${using}`;
}

/** SendKeys 把这些字符当控制符，要用大括号转义。 */
export function escapeSendKeys(text: string): string {
  return text.replace(/[+^%~(){}[\]]/g, (char) => `{${char}}`);
}

const WINDOWS_KEYS: Record<string, string> = {
  return: "{ENTER}",
  enter: "{ENTER}",
  tab: "{TAB}",
  escape: "{ESC}",
  esc: "{ESC}",
  backspace: "{BACKSPACE}",
  delete: "{DELETE}",
  up: "{UP}",
  down: "{DOWN}",
  left: "{LEFT}",
  right: "{RIGHT}",
  space: " ",
};

const WINDOWS_MODIFIERS: Record<string, string> = {
  ctrl: "^",
  control: "^",
  cmd: "^", // Windows 上没有 cmd，按 ctrl 处理——模型经常按 macOS 的习惯给
  alt: "%",
  shift: "+",
};

export function windowsKeys(combo: string): string {
  const parts = combo.split("+").map((part) => part.trim().toLowerCase()).filter(Boolean);
  if (parts.length === 0) throw new Error("按键组合为空");
  const target = parts[parts.length - 1] ?? "";
  const modifiers = parts.slice(0, -1).map((part) => {
    const symbol = WINDOWS_MODIFIERS[part];
    if (!symbol) throw new Error(`不认识的修饰键：${part}`);
    return symbol;
  });
  const key = WINDOWS_KEYS[target] ?? (target.length === 1 ? target : undefined);
  if (key === undefined) throw new Error(`不认识的按键：${target}`);
  return modifiers.join("") + key;
}

export function psString(text: string): string {
  return `'${text.replace(/'/g, "''")}'`;
}

