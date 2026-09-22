#!/bin/bash
# 内部平台 · macOS 一键安装
#
# 一条命令装最新版（不需要任何凭据）：
#
#   curl -fsSL https://git.example.internal/infra/upstream/-/raw/master/install-mac.sh | bash
#
# 也可以下下来再跑：
#
#   ./install-mac.sh                    下载最新 Release 并安装
#   ./install-mac.sh -l                 用 ~/Downloads 里已经下好的包
#   ./install-mac.sh ~/某处/upstream.zip  指定安装包（给目录也行）
#
# 脚本本身托管在 public 仓库 infra/upstream，取它不需要任何凭据。
# 安装包匿名下载靠的是 upstream-app 开了「允许任何人从软件包库拉取」——它只放开包仓库，源码
# 仍然按 internal 管住。发现类接口（releases / tags / packages）仍要登录，
# 所以「最新版是哪个」读的是包仓库里一个固定地址的指针文件。
# 匿名那条路不通时（开关被关掉了），脚本会退回用本机的 glab 或 GITLAB_TOKEN。
#
# 在这个仓库里的话，`make install-mac` 会本机构建再装。
#
# 脚本做的事：找包 → 退掉正在跑的 → 解压 → 装进应用程序 → 去掉隔离标记 → 打开。
#
# **去隔离标记这一步不能省。** 包没有 Apple 开发者签名（公司还没有证书），
# 带着「从网上下载的」这个标记时 Gatekeeper 会判定签名结构不合法，弹出来的框
# 只有【完成】【移到废纸篓】两个按钮，连「仍要打开」都没有——看起来像中毒了。
# 去掉标记之后 Gatekeeper 根本不参与，双击就能开。
#
# 标识符一律用 ASCII：macOS 自带的是 bash 3.2，**变量名不接受非 ASCII**
# （函数名可以，但没必要冒这个险）。中文只出现在给人看的文案里。
set -euo pipefail

APP_NAME="内部平台"
PROJECT_ID=1752
GITLAB_HOST="https://git.example.internal"
RELEASES_URL="${GITLAB_HOST}/sfs/upstream-app/-/releases"
PKG_URL="${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/packages/generic/upstream-app"

# 标识符一律用 ASCII：macOS 自带的是 bash 3.2，**变量名不接受非 ASCII**
# （函数名可以，但没必要冒这个险）。中文只出现在给人看的文案里。
#
# 全部写到 stderr。函数里既打日志又用 $(...) 取返回值，日志走 stdout 的话
# 会被一起抓进返回值里——第一版就是这样，下载完之后报「找不到文件：查最新版本…」。
say_err() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
say_ok() { printf '\033[32m%s\033[0m\n' "$*" >&2; }
say() { printf '\033[90m%s\033[0m\n' "$*" >&2; }

die() { say_err "✗ $*"; exit 1; }

# 内联而不是 sed 读 $0：`curl … | bash` 时 $0 是 bash，读不到脚本自己。
usage() {
  cat <<'USAGE'
内部平台 · macOS 一键安装

  curl -fsSL https://git.example.internal/infra/upstream/-/raw/master/install-mac.sh | bash

或者把脚本下下来再跑：

  ./install-mac.sh                    下载最新 Release 并安装
  ./install-mac.sh -l                 用 ~/Downloads 里已经下好的包
  ./install-mac.sh ~/某处/upstream.zip  指定安装包（给目录也行）
  ./install-mac.sh -h                 看这段

装完在「应用程序」里，直接双击就能开。
USAGE
}

# ---------- 环境检查 ----------

[ "$(uname -s)" = "Darwin" ] || die "这个脚本只能在 macOS 上跑。"

arch=$(uname -m)
if [ "$arch" != "arm64" ]; then
  # 在 Rosetta 下跑的终端会报 x86_64，但机器其实是 Apple Silicon。
  if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
    红 "当前终端跑在 Rosetta 下，识别成了 Intel。"
    灰 "请用原生终端重跑，或执行：arch -arm64 $0 $*"
    exit 1
  fi
  die "目前只有 Apple Silicon（M 系列）的包，这台是 ${arch}。需要 Intel 版请在群里说一声。"
fi

# ---------- 找安装包 ----------

# 从 releases 接口的 JSON 里取**第一个** tag_name（接口按时间倒序，第一个最新）。
#
# 不用 `sed 's/.*"tag_name":"\([^"]*\)".*/\1/'`：整个 JSON 在一行里，而 `.*`
# 是贪婪的，它会匹配到**最后一个** tag_name——也就是最旧的那个 release。
# 只有一个 release 的时候这个错看不出来，第二个一出现就开始装旧版本。
first_tag() {
  grep -o '"tag_name":"[^"]*"' | head -1 | cut -d'"' -f4
}

# 查最新版本号。三条路，按「不需要凭据」优先。
discover_tag() {
  # 1) 匿名读指针文件。包仓库放开了匿名拉取，但发现类接口没有，所以用它。
  local tag
  tag=$(curl -fsSL --max-time 20 "${PKG_URL}/latest/latest.txt" 2>/dev/null | tr -d '[:space:]')
  if [ -n "$tag" ]; then printf '%s' "$tag"; return 0; fi

  # 2) 本机的 glab。认证是它自己的事，脚本不碰任何凭据。
  if command -v glab >/dev/null 2>&1 && glab auth status >/dev/null 2>&1; then
    tag=$(glab api "projects/${PROJECT_ID}/releases" 2>/dev/null | first_tag)
    if [ -n "$tag" ]; then printf '%s' "$tag"; return 0; fi
  fi

  # 3) token。
  if [ -n "${GITLAB_TOKEN:-}" ]; then
    tag=$(curl -fsSL --header "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
      "${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/releases" 2>/dev/null | first_tag)
    if [ -n "$tag" ]; then printf '%s' "$tag"; return 0; fi
  fi
  return 1
}

# 下载某个版本的 mac 包，回显落地路径。
download_zip() {
  local tag="$1"
  local file="upstream-${tag}-mac-arm64.zip"
  local out="${TMPDIR:-/tmp}/${file}"
  local url="${PKG_URL}/${tag}/${file}"
  say "下载 ${file}（约 140MB，会花一会儿）…"

  if curl -fL --progress-bar --max-time 900 -o "$out" "$url" 2>/dev/null; then
    :
  elif command -v glab >/dev/null 2>&1 && glab auth status >/dev/null 2>&1; then
    say "匿名下载不通，改用本机的 glab…"
    glab api "projects/${PROJECT_ID}/packages/generic/upstream-app/${tag}/${file}" > "$out" 2>/dev/null \
      || die "下载失败。"
  elif [ -n "${GITLAB_TOKEN:-}" ]; then
    curl -fL --progress-bar --header "PRIVATE-TOKEN: ${GITLAB_TOKEN}" -o "$out" "$url" \
      || die "下载失败。"
  else
    die "下载失败，而且没有 glab 也没有 GITLAB_TOKEN 可以退回。"
  fi

  # 没下完的文件解压时才报错，那时已经走了半天。这里立刻校验。
  unzip -tq "$out" >/dev/null 2>&1 || die "下下来的不是完整的 zip，重试一次。"
  printf '%s' "$out"
}

fetch_release() {
  say "查最新版本…"
  local tag
  tag=$(discover_tag) || die "查不到最新版本。网络不通，或者包仓库的匿名拉取被关了（可以用 -l 装已经下好的包）。"
  say_ok "最新版本：${tag}"
  download_zip "$tag"
}

find_local() {
  # 按修改时间取最新的一个。装过好几版的人 ~/Downloads 里会有一堆。
  local newest=""
  local f
  for f in "$HOME/Downloads"/upstream-*-mac-arm64.zip; do
    [ -e "$f" ] || continue
    [ -z "$newest" ] || [ "$f" -nt "$newest" ] && newest="$f"
  done
  printf '%s' "$newest"
}

zip_path=""
case "${1:-}" in
  -d|--download) zip_path=$(fetch_release) ;;
  -l|--local) zip_path=$(find_local) ;;
  -h|--help) usage; exit 0 ;;
  "") zip_path=$(fetch_release) ;;
  *) zip_path="$1" ;;
esac

if [ -z "$zip_path" ]; then
  say_err "没找到安装包。"
  cat >&2 <<TIPS

在浏览器里打开下面这个页面，下载 upstream-<版本>-mac-arm64.zip（会落到 ~/Downloads），
然后重新跑一次这个脚本：

  ${RELEASES_URL}

或者带上 token 让脚本自己下：GITLAB_TOKEN=xxx $0 -d
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
  say_ok "来源目录：$zip_path"
  app_src=$(find "$zip_path" -maxdepth 3 -name "*.app" -type d | head -1)
  [ -n "$app_src" ] || die "$zip_path 里没找到 .app。"
  keep_source=1
else
  [ -f "$zip_path" ] || die "找不到文件：$zip_path"
  say_ok "安装包：$zip_path"
  say "解压…"
  unzip -oq "$zip_path" -d "$work" || die "解压失败，这个文件可能没下完。"
  # 包里是 内部平台-darwin-arm64/内部平台.app，但别写死路径——以后目录名变了不该悄悄失败。
  app_src=$(find "$work" -maxdepth 3 -name "*.app" -type d | head -1)
  [ -n "$app_src" ] || die "压缩包里没找到 .app，这不像是内部平台的安装包。"
fi

[ -x "$app_src/Contents/MacOS/${APP_NAME}" ] || die "包内容不完整（缺可执行文件）。"

version=$(defaults read "$app_src/Contents/Info.plist" CFBundleVersion 2>/dev/null || echo "未知")
say_ok "版本：${version}"

# ---------- 装 ----------

dest_dir="/Applications"
if [ ! -w "$dest_dir" ]; then
  dest_dir="$HOME/Applications"
  mkdir -p "$dest_dir"
  say "/Applications 不可写，改装到 ${dest_dir}"
fi
dest="${dest_dir}/${APP_NAME}.app"

# 退掉正在运行的实例。
#
# **按进程名匹配（pgrep -x），不能按命令行（pgrep -f）。**
# -f 匹配整条命令行，会误伤任何命令行里恰好含这个路径的进程——实测中它把
# 一条「参数里带着 app 路径」的普通 shell 命令也杀了。跳过 $$ / $PPID 挡不住
# 这个，因为被误伤的是第三方进程，不是自己。-x 比的是进程名，只会命中真正的
# 内部平台主进程（helper 进程跟着主进程退，不用单独收）。
quit_running() {
  pgrep -x "$APP_NAME" >/dev/null 2>&1 || return 0

  say "先退掉正在运行的内部平台…"
  osascript -e "tell application \"${APP_NAME}\" to quit" >/dev/null 2>&1 || true
  sleep 2

  local pid
  for pid in $(pgrep -x "$APP_NAME" 2>/dev/null); do
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
}

quit_running

say "安装到 ${dest}…"
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
  say_err "隔离标记没能去掉，双击可能会被 Gatekeeper 拦。手动执行："
  echo "  /usr/bin/xattr -dr com.apple.quarantine \"$目标\""
fi

say_ok "✓ 装好了：${dest}"
echo
open "$dest"
say "已经打开。第一次用要在配置页填 LLM Key 与内部平台 BFF Key，"
say "模型端点和内部平台地址有默认值，不用填。"
