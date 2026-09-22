#!/bin/bash
# 把 dist-release/ 下的 zip 传进 GitLab 的通用包仓库。
#
# 为什么不走 job artifact：一是有大小上限——130MB 的包上传直接 413
# Request Entity Too Large（v0.0.1-rc4 就是这么挂的，两个包其实都打好了）；
# 二是 artifact 会过期，而 Release 上的下载链接不该有一天变成 404。
#
# 单独成脚本而不是写在 .gitlab-ci.yml 里：两个打包 job 都要用，而 YAML 锚点
# 展开出来是嵌套数组，GitLab 压不压平是个「要等真打 tag 才知道」的赌注。
set -euo pipefail

BASE="${CI_API_V4_URL}/projects/${CI_PROJECT_ID}/packages/generic/upstream-app/${CI_COMMIT_TAG}"

shopt -s nullglob
files=(dist-release/*.zip)
if [ ${#files[@]} -eq 0 ]; then
  echo "dist-release/ 下没有 zip，打包那一步没产出东西" >&2
  exit 1
fi

for file in "${files[@]}"; do
  name=$(basename "$file")
  echo "uploading $name ($(wc -c < "$file") bytes)"
  curl --fail --silent --show-error \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --upload-file "$file" "${BASE}/${name}"
  echo
done
echo "done -> ${BASE}/"
