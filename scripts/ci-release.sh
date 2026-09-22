#!/bin/bash
# 建 GitLab Release，并把包仓库里的两个 zip 挂成下载链接。
#
# 分两步：先建 Release（只带名字、tag、说明），再逐个挂链接。
#
# 一次性带 assets 的写法在 v0.0.1-rc5 上拿到 400——那要把
# `assets[links][0][name]` 这种嵌套结构编进表单，GitLab 认不认取决于版本。
# 挂链接的端点收的是扁平参数（name / url / link_type），没有这个问题。
#
# 另外**不用 --fail**：它会在非 2xx 时直接退出且不打印响应体，而失败原因全在
# 响应体里。rc5 那次只看到一句 "curl: (22) 400"，等于什么都没说。
# 这里自己取状态码，出错时把响应体原样打出来。
set -euo pipefail

API="${CI_API_V4_URL}/projects/${CI_PROJECT_ID}"
BASE="${API}/packages/generic/upstream-app/${CI_COMMIT_TAG}"

# 说明正文放在仓库里，这里只替换版本号占位符。
sed "s/__TAG__/${CI_COMMIT_TAG}/g" docs/release-notes.md > /tmp/notes.md

post() {
  local what=$1 url=$2
  shift 2
  local out code body
  out=$(curl --silent --show-error --write-out $'\n%{http_code}' --request POST \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" "$@" "$url")
  code=${out##*$'\n'}
  body=${out%$'\n'*}
  if [ "$code" -ge 300 ]; then
    echo "$what 失败：POST $url -> HTTP $code" >&2
    echo "$body" >&2
    return 1
  fi
  echo "$what: HTTP $code"
}

# 安装脚本也传上去，而且传两份：一份在本版本号下，一份在固定的 latest/ 下。
#
# latest/ 那份是为了**匿名一键安装**。项目开了「允许任何人从软件包库拉取」之后，
# 包文件不需要凭据，但 releases / tags / packages 这些**发现类**接口仍然要登录
# ——也就是说匿名能下文件，却不知道最新版是哪个。所以在固定地址放一个只写着
# tag 的指针文件，安装脚本先读它再去取对应版本的包。
#
# 同名重传是覆盖（试过），不会堆出一堆同名文件。
LATEST="${API}/packages/generic/upstream-app/latest"
printf '%s\n' "${CI_COMMIT_TAG}" > /tmp/latest.txt

for pair in "scripts/install-mac.sh:${BASE}/install-mac.sh" \
            "scripts/install-mac.sh:${LATEST}/install-mac.sh" \
            "/tmp/latest.txt:${LATEST}/latest.txt"; do
  src="${pair%%:*}"; dst="${pair#*:}"
  echo "uploading ${src} -> ${dst##*/generic/}"
  curl --fail --silent --show-error \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --upload-file "$src" "$dst"
  echo
done

post "建 Release" "${API}/releases" \
  --data-urlencode "name=内部平台 ${CI_COMMIT_TAG}" \
  --data-urlencode "tag_name=${CI_COMMIT_TAG}" \
  --data-urlencode "description@/tmp/notes.md"

add_link() {
  post "挂链接 $1" "${API}/releases/${CI_COMMIT_TAG}/assets/links" \
    --data-urlencode "name=$1" \
    --data-urlencode "url=$2" \
    --data-urlencode "link_type=package"
}

add_link "macOS（Apple Silicon）" "${BASE}/upstream-${CI_COMMIT_TAG}-mac-arm64.zip"
add_link "Windows（x64）" "${BASE}/upstream-${CI_COMMIT_TAG}-win-x64.zip"
add_link "macOS 一键安装脚本 install-mac.sh" "${BASE}/install-mac.sh"

echo "done -> ${CI_PROJECT_URL}/-/releases/${CI_COMMIT_TAG}"
