#!/bin/bash
# 把 scripts/install-mac.sh 同步到 public 仓库 infra/upstream。
#
# 为什么要有个 public 副本：一键安装命令得能在**没有任何凭据**的情况下取到脚本，
# 而 upstream-app 是 internal 的。安装包本身仍在 upstream-app 的软件包库里，
# 靠那边打开的「允许任何人从软件包库拉取」匿名下载——只放开包，源码不动。
#
# 源头永远是 upstream-app/scripts/install-mac.sh，那边改完跑这个。
set -euo pipefail

REPO="git@git.example.internal:infra/upstream.git"
root=$(cd "$(dirname "$0")/.." && pwd)
src="${root}/scripts/install-mac.sh"
[ -f "$src" ] || { echo "找不到 $src" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git clone -q --depth 1 "$REPO" "$work"
if cmp -s "$src" "$work/install-mac.sh"; then
  echo "public 仓库里的脚本已经是最新的，什么都不用做。"
  exit 0
fi

cp "$src" "$work/install-mac.sh"
chmod +x "$work/install-mac.sh"
cd "$work"
git add install-mac.sh
git commit -q -m "chore: 同步安装脚本（来自 sfs/upstream-app $(git -C "$root" rev-parse --short HEAD)）"
git push -q origin HEAD
echo "已同步到 ${REPO}"
echo "  curl -fsSL https://git.example.internal/infra/upstream/-/raw/master/install-mac.sh | bash"
