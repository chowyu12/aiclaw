import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { desktopCapturer, screen, systemPreferences } from "electron";

import {
  appleString,
  isSelf,
  macKeyScript,
  windowsKeys,
  escapeSendKeys,
  psString,
} from "./computer-keys.js";

const run = promisify(execFile);

/**
 * computer use 的宿主侧实现：截屏与鼠标键盘。
 *
 * 为什么在宿主而不是内核：
 *   - 截屏走 Electron 的 desktopCapturer，用的是**应用自己**的屏幕录制授权。
 *     shell 出去调 screencapture 是另一个进程，授权对不上，会以
 *     「could not create image from display」失败，而那句话对用户毫无意义。
 *   - 输入要按平台合成事件，而且要先知道「最前面的应用是不是我自己」——
 *     只有宿主知道自己是谁。
 *
 * **自点审批的防护在这里。** 如果模型能点自己审批弹窗上的「允许」，审批这道闸
 * 就废了——而没有沙箱之后，审批是仅剩的闸。所以任何输入动作之前先查最前面的
 * 应用：是本应用就拒绝。computer use 是用来驱动**别的**应用的。
 * 这一条挡不住所有情况（用户可能手动把别的窗口放到前面又切回来），但它挡住了
 * 最直接的那条路，而且代价只有一次系统调用。
 */

export type ComputerAction =
  | "screenshot"
  | "click"
  | "doubleClick"
  | "rightClick"
  | "move"
  | "type"
  | "key"
  | "scroll";

export interface ComputerRequest {
  action: ComputerAction;
  x?: number;
  y?: number;
  dx?: number;
  dy?: number;
  text?: string;
  keys?: string;
}

export interface ComputerResult {
  text: string;
  imageBase64?: string;
  width?: number;
  height?: number;
}

export class ComputerController {
  async perform(request: ComputerRequest): Promise<ComputerResult> {
    if (request.action === "screenshot") return this.screenshot();

    // 输入动作：先确认不是在操作我们自己。
    const frontmost = await this.frontmostApp();
    if (isSelf(frontmost)) {
      throw new Error(
        `当前最前面的应用是 AIClaw 自己（${frontmost}），已拒绝这次屏幕操作。` +
          `computer use 用来驱动别的应用；点自己的窗口意味着可能在点审批弹窗。` +
          `请先切到你要操作的那个应用。`,
      );
    }
    return this.input(request);
  }

  /**
   * 截屏。
   *
   * thumbnailSize 给到屏幕的物理像素，否则 desktopCapturer 默认给一张 150px
   * 的缩略图——模型看见的是一团马赛克，然后按它推出来的坐标去点。
   */
  private async screenshot(): Promise<ComputerResult> {
    if (process.platform === "darwin") {
      const status = systemPreferences.getMediaAccessStatus("screen");
      if (status !== "granted") {
        throw new Error(
          "没有屏幕录制权限，截不了屏。到「系统设置 → 隐私与安全性 → 屏幕录制」" +
            "里勾上 AIClaw，然后重启应用。",
        );
      }
    }

    const display = screen.getPrimaryDisplay();
    const { width, height } = display.size;
    const scale = display.scaleFactor || 1;

    const sources = await desktopCapturer.getSources({
      types: ["screen"],
      thumbnailSize: { width: Math.round(width * scale), height: Math.round(height * scale) },
    });
    const source = sources[0];
    if (!source || source.thumbnail.isEmpty()) {
      throw new Error("没有取到屏幕画面。可能是屏幕录制权限刚授予、还没重启应用。");
    }

    return {
      // 明确告诉模型坐标系：它看到的图是物理像素，而点击用的是逻辑像素，
      // 高分屏上这两个差一倍，不说清楚它会把坐标算错一倍。
      text:
        `已截屏。屏幕逻辑尺寸 ${width}×${height}，图片是 ${scale} 倍分辨率。` +
        `点击坐标请按逻辑尺寸给，原点在左上角。`,
      imageBase64: source.thumbnail.toPNG().toString("base64"),
      width,
      height,
    };
  }

  private async input(request: ComputerRequest): Promise<ComputerResult> {
    switch (process.platform) {
      case "darwin":
        return this.macInput(request);
      case "win32":
        return this.windowsInput(request);
      default:
        throw new Error(`${process.platform} 上还没有实现屏幕输入；目前支持 macOS 与 Windows。`);
    }
  }

  // ---------- macOS ----------

  /**
   * macOS 用 AppleScript 的 System Events 合成事件。
   *
   * 不用 cgo 调 CGEvent 是因为整个仓库都不许引 cgo（要交叉编译）；
   * 这里虽然是 Node 不是 Go，但装个原生模块同样要为每个平台出二进制，
   * 而 osascript 是系统自带的。代价是需要「辅助功能」授权，且比 CGEvent 慢。
   */
  private async macInput(request: ComputerRequest): Promise<ComputerResult> {
    if (!systemPreferences.isTrustedAccessibilityClient(false)) {
      throw new Error(
        "没有辅助功能权限，动不了鼠标键盘。到「系统设置 → 隐私与安全性 → 辅助功能」" +
          "里勾上 AIClaw。",
      );
    }
    const script = this.macScript(request);
    try {
      await run("osascript", ["-e", script], { timeout: 15_000 });
    } catch (error) {
      throw new Error(`屏幕操作失败：${describe(error)}`);
    }
    return { text: `已执行 ${request.action}。` };
  }

  private macScript(request: ComputerRequest): string {
    const { x = 0, y = 0 } = request;
    switch (request.action) {
      case "click":
        return `tell application "System Events" to click at {${x}, ${y}}`;
      case "doubleClick":
        return `tell application "System Events"
          click at {${x}, ${y}}
          click at {${x}, ${y}}
        end tell`;
      case "rightClick":
        // System Events 没有直接的右键；用 control+点击，等价。
        return `tell application "System Events"
          key down control
          click at {${x}, ${y}}
          key up control
        end tell`;
      case "move":
        // System Events 不能单独移动鼠标。这是这条实现路径的真实缺口，
        // 说清楚比假装做了强。
        throw new Error("macOS 上暂不支持单独移动鼠标（AppleScript 没有这个能力）。");
      case "type":
        return `tell application "System Events" to keystroke ${appleString(request.text ?? "")}`;
      case "key":
        return macKeyScript(request.keys ?? "");
      case "scroll":
        return `tell application "System Events" to scroll {${request.dx ?? 0}, ${request.dy ?? 0}}`;
      default:
        throw new Error(`不认识的动作：${request.action}`);
    }
  }

  // ---------- Windows ----------

  private async windowsInput(request: ComputerRequest): Promise<ComputerResult> {
    const script = windowsScript(request);
    try {
      await run("powershell", ["-NoProfile", "-NonInteractive", "-Command", script], {
        timeout: 15_000,
      });
    } catch (error) {
      throw new Error(`屏幕操作失败：${describe(error)}`);
    }
    return { text: `已执行 ${request.action}。` };
  }

  /** 查最前面的应用名。查不到返回空串——查不到时不阻断，只是少一道防护。 */
  private async frontmostApp(): Promise<string> {
    try {
      if (process.platform === "darwin") {
        const { stdout } = await run(
          "osascript",
          ["-e", 'tell application "System Events" to name of first application process whose frontmost is true'],
          { timeout: 5_000 },
        );
        return stdout.trim();
      }
      if (process.platform === "win32") {
        const { stdout } = await run(
          "powershell",
          [
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "Add-Type -AssemblyName System.Windows.Forms; " +
              "(Get-Process -Id (Get-Process | Where-Object { $_.MainWindowHandle -eq " +
              "[System.Windows.Forms.Form]::ActiveForm.Handle }).Id).ProcessName",
          ],
          { timeout: 5_000 },
        );
        return stdout.trim();
      }
    } catch {
      // 查不到就算了。这道防护是额外的，不该因为它失败就让整个功能不可用。
    }
    return "";
  }
}

function windowsScript(request: ComputerRequest): string {
  const { x = 0, y = 0 } = request;
  const prelude =
    "Add-Type -AssemblyName System.Windows.Forms; " +
    "Add-Type -MemberDefinition '[DllImport(\"user32.dll\")] public static extern void " +
    "mouse_event(int f,int dx,int dy,int d,int e);' -Name U -Namespace W; ";
  const moveTo = `[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(${x}, ${y}); `;
  // 常量：左键按下 0x02 / 抬起 0x04，右键 0x08 / 0x10，滚轮 0x0800。
  switch (request.action) {
    case "move":
      return prelude + moveTo;
    case "click":
      return prelude + moveTo + "[W.U]::mouse_event(0x02,0,0,0,0); [W.U]::mouse_event(0x04,0,0,0,0);";
    case "doubleClick":
      return (
        prelude +
        moveTo +
        "[W.U]::mouse_event(0x02,0,0,0,0); [W.U]::mouse_event(0x04,0,0,0,0); " +
        "[W.U]::mouse_event(0x02,0,0,0,0); [W.U]::mouse_event(0x04,0,0,0,0);"
      );
    case "rightClick":
      return prelude + moveTo + "[W.U]::mouse_event(0x08,0,0,0,0); [W.U]::mouse_event(0x10,0,0,0,0);";
    case "scroll":
      return prelude + moveTo + `[W.U]::mouse_event(0x0800,0,0,${-(request.dy ?? 0) * 120},0);`;
    case "type":
      return (
        prelude +
        `[System.Windows.Forms.SendKeys]::SendWait(${psString(escapeSendKeys(request.text ?? ""))});`
      );
    case "key":
      return prelude + `[System.Windows.Forms.SendKeys]::SendWait(${psString(windowsKeys(request.keys ?? ""))});`;
    default:
      throw new Error(`不认识的动作：${request.action}`);
  }
}

function describe(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}
