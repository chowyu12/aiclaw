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
extension="$PWD/apps/browser-extension"
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
"$electron" scripts/e2e-browser-extension.cjs 2>&1 | grep -vE '^\[[0-9]+:[0-9]+/' || true
