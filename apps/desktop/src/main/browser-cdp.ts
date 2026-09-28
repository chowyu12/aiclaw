/**
 * 「用我的浏览器」那条路上的纯函数：按键怎么翻成 CDP 事件、截图多大、是哪个浏览器。
 * 标签页列表怎么写给模型在 browser-snapshot.ts（与快照共用不可信边界）。单独成文件是为了能在 node 里测（scripts/test-browser-cdp.ts）。
 */

/** 一次按键在 CDP Input.dispatchKeyEvent 里的样子。 */
export interface CdpKey {
  key: string;
  code: string;
  windowsVirtualKeyCode: number;
  /** 会产生字符的键才有：回车是 "\r"，空格是 " "。 */
  text?: string;
}

const NAMED: Record<string, CdpKey> = {
  enter: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" },
  return: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" },
  tab: { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9 },
  esc: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 },
  escape: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 },
  space: { key: " ", code: "Space", windowsVirtualKeyCode: 32, text: " " },
  backspace: { key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 },
  delete: { key: "Delete", code: "Delete", windowsVirtualKeyCode: 46 },
  up: { key: "ArrowUp", code: "ArrowUp", windowsVirtualKeyCode: 38 },
  arrowup: { key: "ArrowUp", code: "ArrowUp", windowsVirtualKeyCode: 38 },
  down: { key: "ArrowDown", code: "ArrowDown", windowsVirtualKeyCode: 40 },
  arrowdown: { key: "ArrowDown", code: "ArrowDown", windowsVirtualKeyCode: 40 },
  left: { key: "ArrowLeft", code: "ArrowLeft", windowsVirtualKeyCode: 37 },
  arrowleft: { key: "ArrowLeft", code: "ArrowLeft", windowsVirtualKeyCode: 37 },
  right: { key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39 },
  arrowright: { key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39 },
  pageup: { key: "PageUp", code: "PageUp", windowsVirtualKeyCode: 33 },
  pagedown: { key: "PageDown", code: "PageDown", windowsVirtualKeyCode: 34 },
  home: { key: "Home", code: "Home", windowsVirtualKeyCode: 36 },
  end: { key: "End", code: "End", windowsVirtualKeyCode: 35 },
};

/** 按键名（与内置窗口认的同一套）翻成 CDP 按键。认不出的多字符名返回 null。 */
export function cdpKey(name: string): CdpKey | null {
  const trimmed = name.trim();
  const named = NAMED[trimmed.toLowerCase()];
  if (named) return named;
  if ([...trimmed].length === 1) {
    const upper = trimmed.toUpperCase();
    const letter = /^[A-Z]$/.test(upper);
    const digit = /^[0-9]$/.test(trimmed);
    return {
      key: trimmed,
      code: letter ? `Key${upper}` : digit ? `Digit${trimmed}` : "",
      windowsVirtualKeyCode: letter || digit ? upper.charCodeAt(0) : 0,
      text: trimmed,
    };
  }
  return null;
}

/** 从 PNG 文件头读宽高（IHDR 紧跟在 8 字节签名后面）。读不出来返回 0×0。 */
export function pngSize(png: Buffer): { width: number; height: number } {
  const signature = "89504e470d0a1a0a";
  if (png.length < 24 || png.subarray(0, 8).toString("hex") !== signature) return { width: 0, height: 0 };
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

/** 从浏览器的 UA 里认出是哪个浏览器，给设置页与工具回复看。 */
export function browserName(userAgent: string): string {
  if (/Edg\//.test(userAgent)) return "Microsoft Edge";
  if (/OPR\//.test(userAgent)) return "Opera";
  if (/Brave/.test(userAgent)) return "Brave";
  if (/Chrome\//.test(userAgent)) return "Chrome";
  return "浏览器";
}
