#!/usr/bin/env bash
# 「用我的浏览器」端到端。见 scripts/e2e-browser-extension.cjs。
#
# 浏览器在这里起（临时配置，不碰你自己的），测试在 Electron 里跑。分两步是因为从
# Electron 里派生的 Chromium 系浏览器在 macOS 上起不来。
set -euo pipefail
cd "$(dirname "$0")/.."

browser="${1:-}"
if [ -z "$browser" ]; then
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" \
    "/Applications/Chromium.app/Contents/MacOS/Chromium"; do
    if [ -x "$candidate" ]; then browser="$candidate"; break; fi
  done
fi
if [ -z "$browser" ]; then echo "没有找到 Chrome / Edge / Chromium，跳过"; exit 0; fi

profile="$(mktemp -d -t aiclaw-e2e-browser)"
# 扩展复制一份、换一个端口：本机正在用的 AIClaw 开了「我的浏览器」时占着 17891，
# 测试不能去连它。manifest 里的 key 不变，所以扩展 ID 不变，Origin 校验照样生效。
port=27999
extension="$profile/extension"
cp -R "$PWD/apps/browser-extension" "$extension"
sed -i '' "s/^const PORT = 17891;/const PORT = $port;/" "$extension/background.js"
"$browser" --user-data-dir="$profile" --load-extension="$extension" \
  --disable-extensions-except="$extension" --remote-debugging-port=9339 \
  --no-first-run --no-default-browser-check about:blank >"$profile/browser.log" 2>&1 &
pid=$!
cleanup() { kill "$pid" 2>/dev/null || true; sleep 1; rm -rf "$profile"; }
trap cleanup EXIT

for _ in $(seq 1 40); do
  curl -s --max-time 1 http://127.0.0.1:9339/json/version >/dev/null && break
  sleep 0.5
done

electron="$(node -e 'console.log(require("electron"))')"
AICLAW_BRIDGE_PORT=$port "$electron" scripts/e2e-browser-extension.cjs 2>&1 | grep -vE '^\[[0-9]+:[0-9]+/' || true
