#!/usr/bin/env bash
set -euo pipefail

tag=${1:?Expected a version tag}
output="$PWD/dist-release"
mkdir -p "$output"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

package() {
  local target=$1 source=$2 executable=${3:-aiclaw}
  install -m 0755 "build/bin/$source" "$stage/$executable"
  (cd "$stage" && zip -9q "$output/AIClaw-CLI-$tag-$target.zip" "$executable")
  rm "$stage/$executable"
}

package darwin-arm64 darwin-arm64/claw-agent
package darwin-x64 darwin-x64/claw-agent
package windows-x64 win32-x64/claw-agent.exe aiclaw.exe
package linux-x64 linux-x64/claw-agent
if [[ $(uname -s) == Linux && $(uname -m) == x86_64 ]]; then
  version=$(build/bin/linux-x64/claw-agent --version)
  if [[ ${GITHUB_REF_TYPE:-} == tag && "v$version" != "$tag" ]]; then
    echo "Linux CLI version v$version does not match $tag" >&2
    exit 1
  fi
fi
