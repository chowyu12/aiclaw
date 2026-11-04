#!/bin/bash
# AIClaw · macOS 一键安装
#
# 一条命令装最新版（不需要任何凭据，包在 GitHub Releases 上公开）：
#
#   curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/scripts/install-mac.sh | bash
#
# 也可以下下来再跑：
#
#   ./install-mac.sh                     下载最新 Release 并安装
#   ./install-mac.sh -l                  用 ~/Downloads 里已经下好的包
#   ./install-mac.sh ~/某处/AIClaw.zip    指定安装包（给 .app 所在目录也行）
#
# 在这个仓库里的话，`make install-mac` 会本机构建再装。
#
# 脚本做的事：找包 → 退掉正在跑的 → 解压 → 装进应用程序 → 去掉隔离标记 → 打开。
#
# **去隔离标记这一步不能省。** 包没有 Apple 开发者签名，带着「从网上下载的」这个
# 标记时 Gatekeeper 会判定签名结构不合法，弹出来的框只有【完成】【移到废纸篓】
# 两个按钮，连「仍要打开」都没有——看起来像中毒了。去掉标记之后 Gatekeeper 根本
# 不参与，双击就能开。
#
# 标识符一律用 ASCII：macOS 自带的是 bash 3.2，**变量名不接受非 ASCII**。
# 中文只出现在给人看的文案里。
set -euo pipefail

APP_NAME="AIClaw"
REPO="chowyu12/aiclaw"
RELEASES_URL="https://github.com/${REPO}/releases"
API_LATEST="https://api.github.com/repos/${REPO}/releases/latest"

# 全部写到 stderr。函数里既打日志又用 $(...) 取返回值，日志走 stdout 的话
# 会被一起抓进返回值里。
say_err() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
say_ok() { printf '\033[32m%s\033[0m\n' "$*" >&2; }
say() { printf '\033[90m%s\033[0m\n' "$*" >&2; }
die() { say_err "✗ $*"; exit 1; }

# 内联而不是 sed 读 $0：`curl … | bash` 时 $0 是 bash，读不到脚本自己。
usage() {
  cat <<'USAGE'
AIClaw · macOS installer

  curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/scripts/install-mac.sh | bash

Or download the script and run it locally:

  ./install-mac.sh                     Download and install the latest release
  ./install-mac.sh -l                  Use a package already in ~/Downloads
  ./install-mac.sh ~/path/AIClaw.zip    Use a ZIP file or a directory containing the .app
  ./install-mac.sh -h                  Show this help

Environment variables:
  AICLAW_INSTALL_DIR   Installation directory (default: /Applications; falls back to ~/Applications if not writable)
  AICLAW_NO_OPEN=1     Do not launch the app after installation

After installation, open AIClaw from the installation directory.
USAGE
}

# ---------- 环境检查 ----------

[ "$(uname -s)" = "Darwin" ] || die "This installer only supports macOS."

# 包按 CPU 架构分两个。Rosetta 下的终端会把 Apple Silicon 报成 x86_64，
# 那样装到的是 Intel 版——能跑，但慢；这里认出来并纠正。
arch=$(uname -m)
if [ "$arch" = "x86_64" ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
  arch="arm64"
fi
case "$arch" in
  arm64) pkg_arch="arm64" ;;
  x86_64) pkg_arch="x64" ;;
  *) die "No package is available for architecture: ${arch}." ;;
esac

# ---------- 找安装包 ----------

# 查最新版本号。
#
# 先走网页的跳转：`/releases/latest` 会 302 到 `/releases/tag/<tag>`，只看响应头
# 就拿到 tag，**不经 GitHub API**——匿名 API 每小时只有 60 次，公司出口共用一个
# IP 时几个人一起装就会撞上「rate limit exceeded」。API 只作后备。
latest_tag() {
  local location tag
  location=$(curl -fsSIL --max-time 20 -o /dev/null -w '%{url_effective}' "${RELEASES_URL}/latest" 2>/dev/null || true)
  tag=${location##*/releases/tag/}
  if [ -n "$tag" ] && [ "$tag" != "$location" ]; then printf '%s' "$tag"; return 0; fi
  # 不用贪婪的 sed：整个 JSON 在一行里，`.*` 会匹配到最后一个引号。
  curl -fsSL --max-time 20 -H 'Accept: application/vnd.github+json' "$API_LATEST" 2>/dev/null \
    | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/'
}

# 下载某个版本的 mac 包，回显落地路径。文件名与 .github/workflows/release.yml 一致。
download_zip() {
  local tag="$1"
  local file="${APP_NAME}-${tag}-mac-${pkg_arch}.zip"
  local out="${TMPDIR:-/tmp}/${file}"
  local url="${RELEASES_URL}/download/${tag}/${file}"
  say "Downloading ${file} (over 100 MB; this may take a while)..."
  # 到 GitHub 的下载在国内网络上常常中途断掉：断了就续传（-C -），最多重试 8 次。
  # 没有 --retry-all-errors 的话 curl 只重试少数几种错误，连接被重置不在其中。
  curl -fL --progress-bar --max-time 1800 -C - --retry 8 --retry-delay 3 --retry-all-errors \
    -o "$out" "$url" \
    || die "Download failed: ${url}
If your connection to GitHub is unstable, download this file to ~/Downloads in your browser, then run: $0 -l
The mac-${pkg_arch} package may not be available for this release yet. Check ${RELEASES_URL}."
  # 没下完的文件解压时才报错，那时已经走了半天。这里立刻校验。
  unzip -tq "$out" >/dev/null 2>&1 || die "The downloaded ZIP file is incomplete or invalid. Please try again."
  printf '%s' "$out"
}

fetch_release() {
  say "Checking for the latest release..."
  local tag
  tag=$(latest_tag)
  [ -n "$tag" ] || die "Could not find the latest release. Check your network connection or GitHub API rate limit. Use -l to install a downloaded package."
  say_ok "Latest release: ${tag}"
  download_zip "$tag"
}

find_local() {
  # 按修改时间取最新的一个。装过好几版的人 ~/Downloads 里会有一堆。
  local newest=""
  local f
  for f in "$HOME/Downloads/${APP_NAME}"-*-mac-"${pkg_arch}".zip; do
    [ -e "$f" ] || continue
    if [ -z "$newest" ] || [ "$f" -nt "$newest" ]; then newest="$f"; fi
  done
  printf '%s' "$newest"
}

zip_path=""
case "${1:-}" in
  -d|--download|"") zip_path=$(fetch_release) ;;
  -l|--local) zip_path=$(find_local) ;;
  -h|--help) usage; exit 0 ;;
  *) zip_path="$1" ;;
esac

if [ -z "$zip_path" ]; then
  say_err "No installation package found."
  cat >&2 <<TIPS

Open the page below in your browser and download ${APP_NAME}-<version>-mac-${pkg_arch}.zip
to ~/Downloads, then run this script again with -l:

  ${RELEASES_URL}
TIPS
  exit 1
fi

# ---------- 取出 .app ----------
#
# 参数可以是 zip，也可以是一个目录（本机 `make package-mac` 的产物就是目录）。
# 目录那条路要 cp 不能 mv：那是用户自己的构建产物，装一次不该把它搬走。
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
keep_source=0

if [ -d "$zip_path" ]; then
  say_ok "Source directory: $zip_path"
  app_src=$(find "$zip_path" -maxdepth 3 -name "*.app" -type d | head -1)
  [ -n "$app_src" ] || die "No .app bundle found in $zip_path."
  keep_source=1
else
  [ -f "$zip_path" ] || die "File not found: $zip_path"
  say_ok "Installation package: $zip_path"
  say "Extracting..."
  # ditto 保留 .app 里的符号链接与资源分叉；unzip 也行，但 ditto 是系统自带里最稳的。
  ditto -x -k "$zip_path" "$work" || die "Extraction failed. The download may be incomplete."
  app_src=$(find "$work" -maxdepth 3 -name "*.app" -type d | head -1)
  [ -n "$app_src" ] || die "No .app bundle found in the archive. This does not appear to be an ${APP_NAME} package."
fi

[ -x "$app_src/Contents/MacOS/${APP_NAME}" ] || die "The package is incomplete: the app executable is missing."

# 用 PlistBuddy 而不是 defaults：defaults 只认绝对路径，给相对路径会说「域不存在」。
version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app_src/Contents/Info.plist" 2>/dev/null || echo "unknown")
say_ok "Version: ${version}"

# ---------- 装 ----------

dest_dir="${AICLAW_INSTALL_DIR:-/Applications}"
if [ ! -w "$dest_dir" ]; then
  if [ -n "${AICLAW_INSTALL_DIR:-}" ]; then
    mkdir -p "$dest_dir" || die "Could not create ${dest_dir}."
  else
    dest_dir="$HOME/Applications"
    mkdir -p "$dest_dir"
    say "/Applications is not writable. Installing to ${dest_dir} instead."
  fi
fi
dest="${dest_dir}/${APP_NAME}.app"

# 退掉正在运行的实例。
#
# **按进程名匹配（pgrep -x），不能按命令行（pgrep -f）。** -f 匹配整条命令行，
# 会误伤任何命令行里恰好含这个名字的进程——包括正在跑这个脚本的终端。
quit_running() {
  pgrep -x "$APP_NAME" >/dev/null 2>&1 || return 0
  say "Quitting the running ${APP_NAME} app..."
  osascript -e "tell application \"${APP_NAME}\" to quit" >/dev/null 2>&1 || true
  sleep 2
  local pid
  for pid in $(pgrep -x "$APP_NAME" 2>/dev/null); do
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
}

quit_running

say "Installing to ${dest}..."
rm -rf "$dest"
if [ "$keep_source" = "1" ]; then
  cp -R "$app_src" "$dest"
else
  mv "$app_src" "$dest"
fi

# 用 /usr/bin/xattr 而不是 xattr：装过 Homebrew 的机器上 xattr 可能指向一个
# 跟着旧 Python 一起坏掉的版本，写全路径省得排查。
/usr/bin/xattr -dr com.apple.quarantine "$dest" 2>/dev/null || true
if /usr/bin/xattr -p com.apple.quarantine "$dest" >/dev/null 2>&1; then
  say_err "Could not remove the quarantine attribute. Gatekeeper may block the app. Run this command manually:"
  echo "  /usr/bin/xattr -dr com.apple.quarantine \"$dest\"" >&2
fi

say_ok "✓ Installed: ${dest}"
if [ -z "${AICLAW_NO_OPEN:-}" ]; then
  open "$dest"
  say "AIClaw has been launched. To get started, add an endpoint, API key, and model name on the Model Services page."
fi
