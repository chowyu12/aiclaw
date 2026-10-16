import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { copyFile, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { app, shell } from "electron";

import { tr } from "../shared/i18n.js";
import { isNewer, normalizeVersion } from "./version.js";

/**
 * 检查更新。
 *
 * macOS 的 Squirrel.Mac 自动更新要求 Developer ID 签名；当前包未签名，
 * 因此 macOS 做的是「发现新版本并帮你装」：
 * 提示 → 用户点一下 → 下载 zip → 一段脱离父进程的脚本替换 .app 并重开。
 * Windows 下载并校验 Setup.exe，启动交互安装器后退出，由用户确认安装；Linux 下载 ZIP。
 *
 * 检查走 GitHub Releases 的公开接口，**不需要任何凭据**。包名与
 * .github/workflows/release.yml 与 package-win-installer.mjs 的产物命名一致。
 */

const REPO = "chowyu12/aiclaw";
const LATEST_RELEASE = `https://api.github.com/repos/${REPO}/releases/latest`;
const RELEASES_PAGE = `https://github.com/${REPO}/releases`;

export interface UpdateStatus {
  current: string;
  latest: string;
  hasUpdate: boolean;
  /** 查不到时的原因，给界面显示用。空串表示这次查成功了。 */
  error: string;
  /**
   * 这个平台点一下能不能把新版本弄下来。
   *
   * macOS 替换 .app 并重开；Windows 启动安装器（旧 Release 退回 ZIP）；Linux 下载 ZIP。
   */
  canInstall: boolean;
  /** 界面上那个按钮该写什么。 */
  installLabel: string;
}

/** 这台机器该下哪个包。与 release.yml 的 zip 命名一致。 */
function assetName(tag: string): string {
  const arch = process.arch === "arm64" ? "arm64" : "x64";
  switch (process.platform) {
    case "darwin":
      return `AIClaw-${tag}-mac-${arch}.zip`;
    case "win32":
      return `AIClaw-${tag}-win-x64.zip`;
    default:
      return `AIClaw-${tag}-linux-x64.zip`;
  }
}

export class Updater {
  /**
   * 已经下好、等着装的那一份。
   *
   * 记着 tag 是为了在版本又更新时作废它：拿一个旧包去「重启更新」，
   * 用户会发现重启完版本号没变，而这种错没人会怀疑到缓存上。
   */
  private prepared = { tag: "", path: "" };
  /** 最近一次查到的各资源下载地址，按文件名。 */
  private assets = new Map<string, string>();

  async check(): Promise<UpdateStatus> {
    const current = app.getVersion();
    const canInstall = true;
    let installLabel = process.platform === "darwin" ? tr("升级并重启") : tr("下载新版本");
    try {
      const response = await fetch(LATEST_RELEASE, {
        signal: AbortSignal.timeout(10_000),
        headers: { Accept: "application/vnd.github+json", "Cache-Control": "no-cache" },
      });
      if (!response.ok) {
        return { current, latest: "", hasUpdate: false, canInstall, installLabel, error: `HTTP ${response.status}` };
      }
      const release = (await response.json()) as {
        tag_name?: string;
        assets?: { name?: string; browser_download_url?: string }[];
      };
      const latest = normalizeVersion(release.tag_name ?? "");
      this.assets = new Map(
        (release.assets ?? [])
          .filter((asset) => asset.name && asset.browser_download_url)
          .map((asset) => [asset.name!, asset.browser_download_url!]),
      );
      if (process.platform === "win32" && this.assets.has(this.windowsInstallerName(latest))) {
        installLabel = tr("下载安装更新");
      }
      return { current, latest, hasUpdate: isNewer(latest, current), canInstall, installLabel, error: "" };
    } catch (error) {
      // 查不到不是错误状态，只是这次没查到。网络不通时不该在界面上报红。
      return {
        current, latest: "", hasUpdate: false, canInstall, installLabel,
        error: error instanceof Error ? error.message : String(error),
      };
    }
  }

  /**
   * 后台把新版本下好，等用户点「重启更新」。
   *
   * 发现新版本就先下好，点下去只剩替换与重开——两三秒的事；否则那几十秒
   * 卡在用户按下按钮之后，他只能盯着一个不动的界面等。
   *
   * 只在 macOS 上做：别的平台没有「重启即更新」这条路，下好了也还是要用户
   * 自己解压，不如等他点了再下。
   */
  async prepare(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ ready: boolean; version: string; detail: string }> {
    if (process.platform !== "darwin") {
      return { ready: false, version: "", detail: tr("这个平台不支持自动下载") };
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
      const path = await this.download(assetName(tag), onProgress);
      this.prepared = { tag, path };
      return { ready: true, version: status.latest, detail: path };
    } catch (error) {
      // 下不下来不报到界面上：用户没点过任何东西，弹一条他看不懂的错误只是打扰。
      // 点「升级」时还会再试一次，那时候失败才该说。
      return { ready: false, version: status.latest, detail: error instanceof Error ? error.message : String(error) };
    }
  }

  /**
   * 下载安装包，边下边报进度。
   *
   * 分片读而不是 arrayBuffer()：后者要等下完才有第一个字节，也就没有进度可报——
   * 用户点完「升级」看到的会是一个不动的按钮，几十秒后应用突然退出。
   */
  private async download(
    file: string,
    onProgress?: (received: number, total: number) => void,
    directory = tmpdir(),
  ): Promise<string> {
    const url = this.assets.get(file);
    if (!url) throw new Error(tr("最新版本里没有 {file}", { file }));
    const response = await fetch(url, { signal: AbortSignal.timeout(600_000) });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const total = Number(response.headers.get("content-length") ?? 0);

    const target = join(directory, file);
    const chunks: Uint8Array[] = [];
    let received = 0;
    const reader = response.body?.getReader();
    if (!reader) throw new Error(tr("这个平台的 fetch 不支持流式读取"));
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
   * macOS：脱离父进程跑一段替换脚本。脚本第一件事是等我们退出——它要替换的
   * 就是**正在运行的我们自己**，所以这个子进程必须 detached + unref，
   * 不然它会跟着我们一起死，用户看到的是应用退了但没升级。
   *
   * Windows 交给交互安装器，不传静默安装参数；旧版本只有 ZIP 时保持下载行为。
   * Linux 仍只下载并在文件管理器里显示。
   */
  async install(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ started: boolean; detail: string }> {
    if (process.platform === "win32") return this.installWindows(onProgress);
    if (process.platform !== "darwin") return this.downloadOnly(onProgress);
    if (!app.isPackaged) {
      return { started: false, detail: tr("开发模式下不替换自己；打好的包才能一键升级。") };
    }
    try {
      const status = await this.check();
      const tag = status.latest ? `v${status.latest}` : "";
      if (!tag) throw new Error(status.error || tr("查不到最新版本"));
      // 后台已经下好同一个版本就直接用，别再下一遍。
      const zip =
        this.prepared.tag === tag && existsSync(this.prepared.path)
          ? this.prepared.path
          : await this.download(assetName(tag), onProgress);
      this.replaceAndRelaunch(zip);
      app.quit();
      return { started: true, detail: tr("已下载完成，正在替换并重新打开。") };
    } catch (error) {
      await shell.openExternal(RELEASES_PAGE);
      return {
        started: false,
        detail: tr("自动升级失败（{error}），已打开发布页。", { error: error instanceof Error ? error.message : String(error) }),
      };
    }
  }

  private windowsInstallerName(version: string): string {
    return `AIClaw-v${version}-win-x64-setup.exe`;
  }

  private async installWindows(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ started: boolean; detail: string }> {
    if (!app.isPackaged) {
      return { started: false, detail: tr("开发模式下不替换自己；打好的包才能一键升级。") };
    }
    let directory = "";
    try {
      const status = await this.check();
      if (status.error) throw new Error(status.error);
      if (!status.hasUpdate) return { started: false, detail: tr("当前已是最新版本") };
      if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(status.latest)) throw new Error(tr("查不到最新版本"));
      const name = this.windowsInstallerName(status.latest);
      if (!this.assets.has(name)) return this.downloadOnly(onProgress);
      directory = await mkdtemp(join(tmpdir(), "aiclaw-update-"));
      const installer = await this.download(name, onProgress, directory);
      const sums = await this.download("SHA256SUMS.txt", undefined, directory);
      const entry = (await readFile(sums, "utf8")).split(/\r?\n/).map(line => /^([a-f0-9]{64})\s+\*?(?:\.\/)?(.+)$/i.exec(line)).find(match => match?.[2] === name);
      const actual = createHash("sha256").update(await readFile(installer)).digest("hex");
      if (!entry || entry[1]!.toLowerCase() !== actual) throw new Error(tr("安装包校验失败，请重新下载。"));
      const error = await shell.openPath(installer);
      if (error) throw new Error(error);
      app.quit();
      return { started: true, detail: tr("已启动安装器，请确认安装；完成后可重新打开 AIClaw。") };
    } catch (error) {
      if (directory) await rm(directory, { recursive: true, force: true }).catch(() => undefined);
      await shell.openExternal(RELEASES_PAGE);
      return { started: false, detail: tr("自动升级失败（{error}），已打开发布页。", { error: error instanceof Error ? error.message : String(error) }) };
    }
  }

  /**
   * macOS：等自己退出，解包，替换当前的 .app，去掉 quarantine，再打开。
   *
   * 替换的是**我们正在运行的那个 bundle**（从可执行文件路径往上找 .app），
   * 不假定装在 /Applications——用户可能放在别处。先解到临时目录、确认里面
   * 真有 .app 再动原位置：解一半失败的话，原来的应用还完好。
   */
  private replaceAndRelaunch(zip: string): void {
    const bundle = resolve(dirname(app.getPath("exe")), "..", "..");
    const script = [
      "set -e",
      `while kill -0 ${process.pid} 2>/dev/null; do sleep 0.2; done`,
      `TMP=$(mktemp -d)`,
      `ditto -x -k '${zip}' "$TMP"`,
      `NEW=$(find "$TMP" -maxdepth 2 -name '*.app' -print -quit)`,
      `[ -n "$NEW" ] || { echo "zip 里没有 .app"; exit 1; }`,
      `rm -rf '${bundle}'`,
      `mv "$NEW" '${bundle}'`,
      `xattr -dr com.apple.quarantine '${bundle}' 2>/dev/null || true`,
      `rm -rf "$TMP"`,
      `open '${bundle}'`,
    ].join("\n");
    const child = spawn("/bin/bash", ["-c", script], { detached: true, stdio: "ignore" });
    child.unref();
  }

  /** Windows / Linux：把包下到「下载」目录并在文件管理器里选中它。 */
  private async downloadOnly(
    onProgress?: (received: number, total: number) => void,
  ): Promise<{ started: boolean; detail: string }> {
    try {
      const status = await this.check();
      const tag = status.latest ? `v${status.latest}` : "";
      if (!tag) throw new Error(status.error || tr("查不到最新版本"));
      const name = assetName(tag);
      const downloaded = await this.download(name, onProgress);
      const target = join(app.getPath("downloads"), name);
      await copyFile(downloaded, target);
      shell.showItemInFolder(target);
      return { started: false, detail: tr("已下载到 {path}。退出 AIClaw 后解压覆盖原目录即可。", { path: target }) };
    } catch (error) {
      await shell.openExternal(RELEASES_PAGE);
      return {
        started: false,
        detail: tr("自动下载失败（{error}），已打开发布页。", { error: error instanceof Error ? error.message : String(error) }),
      };
    }
  }
}
