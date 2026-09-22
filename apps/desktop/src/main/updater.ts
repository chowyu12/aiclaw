import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { copyFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { app, shell } from "electron";

import { isNewer, normalizeVersion } from "./version.js";

/**
 * 检查更新。
 *
 * **这不是自动更新。** 真正的静默自动更新（Squirrel.Mac / electron-updater）
 * 要求安装包有 Developer ID 签名，而公司还没有 Apple 开发者证书——没有签名
 * 的更新包 Squirrel 会直接拒绝。所以这里做的是「发现新版本并帮你装」：
 * 提示 → 用户点一下 → 跑与手工安装完全相同的那个脚本。
 *
 * 检查走的是软件包库里那个固定地址的指针文件，**不需要任何凭据**
 * （upstream-app 开了「允许任何人从软件包库拉取」）。用它而不是 releases 接口，
 * 是因为发现类接口匿名仍然 401/404。
 *
 * 装的时候不自己实现「替换正在运行的自己」这套：那要处理退出时机、
 * 替换失败回滚、重新拉起，每一条都能把用户的应用变成打不开。
 * 复用 install-mac.sh——它已经在做同样的事，而且是同事手工安装走的同一条路，
 * 出问题时两边一起暴露，不会有一条没人走过的分支。
 */

const PACKAGES =
  "https://git.example.internal/api/v4/projects/1752/packages/generic/upstream-app";
const LATEST_POINTER = `${PACKAGES}/latest/latest.txt`;
const INSTALL_SCRIPT =
  "https://git.example.internal/infra/upstream/-/raw/master/install-mac.sh";
const RELEASES_PAGE = "https://git.example.internal/sfs/upstream-app/-/releases";

export interface UpdateStatus {
  current: string;
  latest: string;
  hasUpdate: boolean;
  /** 查不到时的原因，给界面显示用。空串表示这次查成功了。 */
  error: string;
  /**
   * 这个平台点一下能不能把新版本弄下来。
   *
   * macOS 是真·一键（跑安装脚本、自动重开）；Windows 只到「下载好并指给你」
   * ——自替换在那边风险太高且我们无法验证。两者都比让用户自己去发布页
   * 找文件强，所以都算 true，文案由界面按平台分。
   */
  canInstall: boolean;
  /** 界面上那个按钮该写什么。 */
  installLabel: string;
}

export class Updater {
  /**
   * 已经下好、等着装的那一份。
   *
   * 记着 tag 是为了在版本又更新时作废它：拿一个旧包去「重启更新」，
   * 用户会发现重启完版本号没变，而这种错没人会怀疑到缓存上。
   */
  private prepared = { tag: "", path: "" };

  async check(): Promise<UpdateStatus> {
    const current = app.getVersion();
    const canInstall = process.platform === "darwin" || process.platform === "win32";
    const installLabel = process.platform === "darwin" ? "升级并重启" : "下载新版本";
    try {
      const response = await fetch(LATEST_POINTER, {
        signal: AbortSignal.timeout(10_000),
        // 指针文件会被每次发版覆盖，别让中间层给我们一份旧的。
        headers: { "Cache-Control": "no-cache" },
      });
      if (!response.ok) {
        return {
          current,
          latest: "",
          hasUpdate: false,
          canInstall,
          installLabel,
          error: `HTTP ${response.status}`,
        };
      }
      // 只读前面一小段：正常内容就是一行版本号。真拿到一整页 HTML 错误页时，
      // 截断能避免把它整个塞进内存和日志。
      const latest = normalizeVersion((await response.text()).slice(0, 64));
      return {
        current,
        latest,
        hasUpdate: isNewer(latest, current),
        canInstall,
        installLabel,
        error: "",
      };
    } catch (error) {
      // 查不到不是错误状态，只是这次没查到。网络不通时不该在界面上报红。
      return {
        current,
        latest: "",
        hasUpdate: false,
        canInstall,
        installLabel,
        error: error instanceof Error ? error.message : String(error),
      };
    }
  }

  /**
   * 后台把新版本下好，等用户点「重启更新」。
   *
   * 为什么改成自动下：旧流程是「提示 → 用户点 → 等几十秒下载 → 应用退出」，
   * 那几十秒卡在用户按下按钮之后，他只能盯着一个不动的界面等。改成发现
   * 新版本就先下好，点下去只剩替换与重开——两三秒的事。
   *
   * 只在 macOS 上做：Windows 那边没有「重启即更新」这条路（替换正在运行的
   * 自己要另一套机制），下好了也还是要用户自己解压，不如等他点了再下。
   */
  async prepare(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ ready: boolean; version: string; detail: string }> {
    if (process.platform !== "darwin") {
      return { ready: false, version: "", detail: "这个平台不支持自动下载" };
    }
    const status = await this.check();
    if (!status.hasUpdate || !status.latest) {
      return { ready: false, version: "", detail: status.error };
    }
    const tag = `v${status.latest}`;
    if (this.prepared.tag === tag && existsSync(this.prepared.path)) {
      return { ready: true, version: status.latest, detail: this.prepared.path };
    }
    try {
      const path = await this.download(tag, `upstream-${tag}-mac-arm64.zip`, onProgress);
      this.prepared = { tag, path };
      return { ready: true, version: status.latest, detail: path };
    } catch (error) {
      // 下不下来不报到界面上：用户没点过任何东西，弹一条他看不懂的错误只是打扰。
      // 点「升级」时还会再试一次，那时候失败才该说。
      return {
        ready: false,
        version: status.latest,
        detail: error instanceof Error ? error.message : String(error),
      };
    }
  }

  /**
   * 下载安装包，边下边报进度。
   *
   * 自己下而不是把整件事甩给安装脚本，就是为了这个进度：脚本在终端里有
   * curl 的进度条，而从应用里跑它时 stdio 是丢掉的——用户点完「升级」之后
   * 看到的是一个不动的按钮，几十秒后应用突然退出。那不是慢，是没有反馈，
   * 但感觉上比慢更糟。
   */
  private async download(
    tag: string,
    file: string,
    onProgress?: (received: number, total: number) => void,
  ): Promise<string> {
    const response = await fetch(`${PACKAGES}/${tag}/${file}`, {
      signal: AbortSignal.timeout(600_000),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const total = Number(response.headers.get("content-length") ?? 0);

    const target = join(tmpdir(), file);
    const chunks: Uint8Array[] = [];
    let received = 0;
    // 分片读而不是 arrayBuffer()：后者要等下完才有第一个字节，
    // 也就没有进度可报。
    const reader = response.body?.getReader();
    if (!reader) throw new Error("这个平台的 fetch 不支持流式读取");
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      received += value.length;
      onProgress?.(received, total);
    }
    await writeFile(target, Buffer.concat(chunks));
    return target;
  }

  /**
   * 下载并安装最新版。
   *
   * macOS：脱离父进程跑安装脚本。脚本第一件事就是退掉正在运行的内部平台——
   * 也就是**杀掉我们自己**，所以这个子进程必须 detached + unref，
   * 不然它会跟着我们一起死，用户看到的是应用退了但没升级。
   *
   * Windows：只下载并在资源管理器里指给用户，**不自动替换**。
   * 「替换正在运行的自己」在 Windows 上比 macOS 还难（文件被占用、
   * 杀进程的时机、失败后没有回滚），而我们一台 Windows 机器都没有，
   * 写一个没验证过的自替换脚本，失败方式正好是「应用打不开了」。
   * 下载这一段是能验的，也已经省掉了最烦的一步——找到该下哪个文件。
   */
  async install(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ started: boolean; detail: string }> {
    if (process.platform === "win32") return this.downloadForWindows(onProgress);
    if (process.platform !== "darwin") {
      await shell.openExternal(RELEASES_PAGE);
      return { started: false, detail: "已打开发布页，请下载对应平台的包。" };
    }

    // 先把包下下来（有进度），再让脚本装本地这一份。
    //
    // 脚本支持 `install-mac.sh <zip>`，走这条等于跳过它自己的「查版本 + 下载」
    // 那一段——耗时没变，但**慢的那段发生在应用还活着的时候**，用户看得见
    // 进度；剩下的替换与重开只有两三秒。
    let localZip = "";
    try {
      const status = await this.check();
      const tag = status.latest ? `v${status.latest}` : "";
      if (!tag) throw new Error(status.error || "查不到最新版本");
      // 后台已经下好同一个版本就直接用，别再下一遍。
      if (this.prepared.tag === tag && existsSync(this.prepared.path)) {
        localZip = this.prepared.path;
      } else {
        localZip = await this.download(tag, `upstream-${tag}-mac-arm64.zip`, onProgress);
      }
    } catch (error) {
      // 下载失败就退回老路：让脚本自己去下。它在终端里久经使用，
      // 比我们这段新代码更可靠。
      const reason = error instanceof Error ? error.message : String(error);
      this.runInstaller("");
      return { started: true, detail: `自己下载失败（${reason}），改由安装脚本下载。` };
    }

    this.runInstaller(localZip);
    return { started: true, detail: "已下载完成，正在替换并重新打开。" };
  }

  /**
   * 跑安装脚本。zip 为空表示让脚本自己去下。
   *
   * **必须脱离父进程**：脚本第一件事就是退掉正在运行的内部平台——也就是杀掉
   * 我们自己。不 detached + unref 的话它会跟着我们一起死，用户看到的是
   * 应用退了但没升级。
   */
  private runInstaller(zip: string): void {
    const script = "/tmp/upstream-install.sh";
    const argument = zip ? ` '${zip}'` : "";
    const child = spawn(
      "/bin/bash",
      [
        "-c",
        `sleep 1; curl -fsSL '${INSTALL_SCRIPT}' -o '${script}' && /bin/bash '${script}'${argument}`,
      ],
      { detached: true, stdio: "ignore" },
    );
    child.unref();
  }

  /**
   * Windows：把安装包下到「下载」目录并在资源管理器里选中它。
   *
   * 下载走的同样是那个匿名可读的包仓库，所以不需要任何凭据；文件名里带着
   * 版本号，用户解压覆盖就行。失败时退回开发布页——那条路一直是通的。
   */
  private async downloadForWindows(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ started: boolean; detail: string }> {
    try {
      const status = await this.check();
      const tag = status.latest ? `v${status.latest}` : "";
      if (!tag) throw new Error(status.error || "查不到最新版本");

      const name = `upstream-${tag}-win-x64.zip`;
      const downloaded = await this.download(tag, name, onProgress);
      const target = join(app.getPath("downloads"), name);
      await copyFile(downloaded, target);
      shell.showItemInFolder(target);
      return {
        started: false,
        detail: `已下载到 ${target}。退出内部平台后解压覆盖原目录即可。`,
      };
    } catch (error) {
      await shell.openExternal(RELEASES_PAGE);
      return {
        started: false,
        detail: `自动下载失败（${error instanceof Error ? error.message : String(error)}），已打开发布页。`,
      };
    }
  }
}
