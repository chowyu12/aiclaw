#!/usr/bin/env bash
#
# AIClaw 一键安装脚本
# 用法: curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/main/install.sh | bash
#
# 支持平台: Linux (amd64/arm64)、macOS (amd64/arm64)
# 功能: 下载最新 release → 安装到 /usr/local/bin → 注册系统服务(开机自启) → 启动 → 输出 Web 访问地址
#
set -euo pipefail

REPO="chowyu12/aiclaw"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="aiclaw"
SERVICE_NAME="aiclaw"
CONFIG_DIR="$HOME/.aiclaw"
DEFAULT_PORT=8080

# ───────────────────── 工具函数 ─────────────────────

info()  { printf "\033[1;34m[INFO]\033[0m  %s\n" "$*"; }
ok()    { printf "\033[1;32m[OK]\033[0m    %s\n" "$*"; }
warn()  { printf "\033[1;33m[WARN]\033[0m  %s\n" "$*"; }
fatal() { printf "\033[1;31m[ERROR]\033[0m %s\n" "$*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fatal "需要 $1，请先安装"
}

# ───────────────────── 平台检测 ─────────────────────

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "$os" in
    linux)  OS="linux" ;;
    darwin) OS="darwin" ;;
    *)      fatal "不支持的操作系统: $os" ;;
  esac

  case "$arch" in
    x86_64|amd64)   ARCH="amd64" ;;
    aarch64|arm64)   ARCH="arm64" ;;
    *)               fatal "不支持的架构: $arch" ;;
  esac

  ASSET_NAME="${BINARY_NAME}-${OS}-${ARCH}"
  info "检测到平台: ${OS}/${ARCH}"
}

# ───────────────────── 获取最新版本 ─────────────────────

fetch_latest_version() {
  need_cmd curl
  local api_url="https://api.github.com/repos/${REPO}/releases/latest"
  info "正在获取最新版本..."

  LATEST_TAG=$(curl -fsSL "$api_url" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
  if [ -z "$LATEST_TAG" ]; then
    fatal "无法获取最新版本，请检查网络或 GitHub API 限流"
  fi
  ok "最新版本: ${LATEST_TAG}"
}

# ───────────────────── 下载并安装 ─────────────────────

download_and_install() {
  local url="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${ASSET_NAME}"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local tmp_file="${tmp_dir}/${BINARY_NAME}"

  info "正在下载 ${url} ..."
  curl -fSL --progress-bar -o "$tmp_file" "$url" || fatal "下载失败，请检查网络连接"

  chmod +x "$tmp_file"

  info "安装到 ${INSTALL_DIR}/${BINARY_NAME} ..."
  if [ -w "$INSTALL_DIR" ]; then
    mv "$tmp_file" "${INSTALL_DIR}/${BINARY_NAME}"
  else
    sudo mv "$tmp_file" "${INSTALL_DIR}/${BINARY_NAME}"
  fi
  rm -rf "$tmp_dir"

  ok "二进制文件已安装: ${INSTALL_DIR}/${BINARY_NAME}"
}

# ───────────────────── 启动服务 ─────────────────────

start_service() {
  SERVICE_MODE="manual"

  case "$OS" in
    linux)
      if command -v systemctl >/dev/null 2>&1; then
        install_systemd
        SERVICE_MODE="systemd"
        return
      fi
      ;;
    darwin)
      install_launchd
      SERVICE_MODE="launchd"
      return
      ;;
  esac

  info "未检测到系统服务管理器，使用内置后台模式..."
  "${INSTALL_DIR}/${BINARY_NAME}" stop 2>/dev/null || true
  "${INSTALL_DIR}/${BINARY_NAME}" start
  ok "已通过 aiclaw start 后台启动（无开机自启，重启后需手动执行 aiclaw start）"
}

install_systemd() {
  local service_file="/etc/systemd/system/${SERVICE_NAME}.service"
  local run_user="${SUDO_USER:-$USER}"
  local run_home
  run_home="$(eval echo "~${run_user}")"

  info "注册 systemd 服务（开机自启）..."

  sudo tee "$service_file" >/dev/null <<UNIT
[Unit]
Description=AIClaw AI Agent Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${run_user}
Environment=HOME=${run_home}
ExecStart=${INSTALL_DIR}/${BINARY_NAME}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

  sudo systemctl daemon-reload
  sudo systemctl enable "$SERVICE_NAME" >/dev/null 2>&1
  sudo systemctl restart "$SERVICE_NAME"
  ok "systemd 服务已启动（开机自启已开启）"
}

install_launchd() {
  local plist_dir="$HOME/Library/LaunchAgents"
  local plist_file="${plist_dir}/com.aiclaw.agent.plist"

  mkdir -p "$plist_dir"

  info "注册 launchd 服务（开机自启）..."

  cat > "$plist_file" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.aiclaw.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_DIR}/${BINARY_NAME}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${CONFIG_DIR}/aiclaw.log</string>
    <key>StandardErrorPath</key>
    <string>${CONFIG_DIR}/aiclaw.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>${HOME}</string>
    </dict>
</dict>
</plist>
PLIST

  launchctl unload "$plist_file" 2>/dev/null || true
  launchctl load -w "$plist_file"
  ok "launchd 服务已启动（开机自启已开启）"
}

# ───────────────────── 等待服务就绪 ─────────────────────

wait_for_ready() {
  local port="${DEFAULT_PORT}"

  if [ -f "${CONFIG_DIR}/config.yaml" ]; then
    local cfg_port
    cfg_port=$(grep -E '^\s*port:' "${CONFIG_DIR}/config.yaml" 2>/dev/null | head -1 | awk '{print $2}' | tr -d '[:space:]')
    if [ -n "$cfg_port" ] && [ "$cfg_port" -gt 0 ] 2>/dev/null; then
      port="$cfg_port"
    fi
  fi

  info "等待服务启动 (端口 ${port}) ..."
  local i=0
  while [ $i -lt 30 ]; do
    if curl -sf "http://127.0.0.1:${port}/" >/dev/null 2>&1; then
      ok "服务已就绪"
      PORT="$port"
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done

  PORT="$port"
  warn "等待超时，服务可能仍在初始化（首次启动需要配置数据库）"
}

# ───────────────────── 打印访问信息 ─────────────────────

print_access_info() {
  local ip
  if [ "$OS" = "linux" ]; then
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
  else
    ip=$(ipconfig getifaddr en0 2>/dev/null || echo "")
  fi
  [ -z "$ip" ] && ip="127.0.0.1"

  local token=""
  if [ -f "${CONFIG_DIR}/config.yaml" ]; then
    token=$(grep -E '^\s*web_token:' "${CONFIG_DIR}/config.yaml" 2>/dev/null | head -1 | awk '{print $2}' | tr -d '[:space:]"'"'" || true)
  fi

  echo ""
  echo "╔══════════════════════════════════════════════════════════════╗"
  echo "║                   AIClaw 安装完成！                         ║"
  echo "╠══════════════════════════════════════════════════════════════╣"
  echo "║  版本:  ${LATEST_TAG}"
  echo "║  平台:  ${OS}/${ARCH}"
  echo "║  二进制: ${INSTALL_DIR}/${BINARY_NAME}"
  echo "║  配置:  ${CONFIG_DIR}/config.yaml"
  echo "║"
  echo "║  本地访问:  http://127.0.0.1:${PORT}/"
  if [ "$ip" != "127.0.0.1" ]; then
  echo "║  局域网访问: http://${ip}:${PORT}/"
  fi
  if [ -n "$token" ]; then
  echo "║"
  echo "║  登录令牌: ${token}"
  fi
  echo "║"
  case "$SERVICE_MODE" in
    systemd)
  echo "║  管理命令:"
  echo "║    查看状态:  sudo systemctl status ${SERVICE_NAME}"
  echo "║    查看日志:  sudo journalctl -u ${SERVICE_NAME} -f"
  echo "║    重启服务:  sudo systemctl restart ${SERVICE_NAME}"
  echo "║    停止服务:  sudo systemctl stop ${SERVICE_NAME}"
      ;;
    launchd)
  echo "║  管理命令:"
  echo "║    查看日志:  tail -f ${CONFIG_DIR}/aiclaw.log"
  echo "║    重启服务:  launchctl kickstart -k gui/\$(id -u)/com.aiclaw.agent"
  echo "║    停止服务:  launchctl unload ~/Library/LaunchAgents/com.aiclaw.agent.plist"
      ;;
    *)
  echo "║  管理命令:"
  echo "║    查看状态:  aiclaw status"
  echo "║    查看日志:  tail -f ${CONFIG_DIR}/aiclaw.log"
  echo "║    重启服务:  aiclaw stop && aiclaw start"
  echo "║    停止服务:  aiclaw stop"
      ;;
  esac
  echo "║"
  echo "║  更新版本:  aiclaw update"
  echo "╚══════════════════════════════════════════════════════════════╝"
  echo ""
}

# ───────────────────── 主流程 ─────────────────────

main() {
  echo ""
  echo "  ╔═══════════════════════════════════════╗"
  echo "  ║   AIClaw Installer — AI Agent Server  ║"
  echo "  ╚═══════════════════════════════════════╝"
  echo ""

  detect_platform
  fetch_latest_version
  download_and_install
  start_service
  wait_for_ready
  print_access_info
}

main "$@"
