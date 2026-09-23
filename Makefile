# AIClaw 桌面应用：统一入口。
#
# 日常只要记两个：
#   make dev     起整个应用，手动测功能
#   make check   交付前全量预检（格式、vet、测试、类型、冒烟）
#
# 其余 target 是这两个的组成部分，单独跑用来定位问题。
# 真正的构建规则在各自的 package.json 与 go build 里，这里只做编排。

SHELL := /bin/bash
.DEFAULT_GOAL := help

ROOT      := $(shell pwd)
DESKTOP   := $(ROOT)/apps/desktop
AGENT_DIR := $(ROOT)/tools/claw-agent
AGENT_BIN := $(AGENT_DIR)/claw-agent
STAMP     := $(ROOT)/node_modules/.make-install-stamp

# 渲染层热更用的端口。strictPort 保证要么就是它，要么直接失败——
# 端口被占时静默换一个，Electron 那边还连着旧地址，会很难查。
VITE_PORT ?= 5173

# 内核是根模块的一部分：它 import 了 internal/ 下的插件系统与存储层，
# 所以这几处任何一个 .go 变了都要重编。
GO_SOURCES := $(shell find $(AGENT_DIR) $(ROOT)/internal $(ROOT)/pkg -name '*.go' -not -name '*_test.go' 2>/dev/null)

.PHONY: help deps build build-go build-client build-desktop dev dev-ui \
        test test-go test-renderer smoke smoke-desktop scenarios icon \
        package package-mac package-mac-intel package-win package-linux install-mac \
        typecheck fmt fmt-check vet nocgo check doctor clean distclean

help: ## 列出可用 target
	@echo "AIClaw —— 可用命令："
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "第一次跑：make dev"

# ---------- 依赖 ----------

deps: $(STAMP) ## 安装 npm 依赖（已装则跳过）

# 用戳文件而不是 node_modules 目录本身：目录的 mtime 会被任何一次
# 安装子包改掉，拿它当依据会反复重装。
$(STAMP): package.json package-lock.json
	npm install
	@mkdir -p $(dir $(STAMP)) && touch $(STAMP)

# ---------- 构建 ----------

build: build-client build-go build-desktop ## 全量构建：TS 客户端 + Go 内核 + 桌面应用

build-client: deps ## 编译 agent-client（TS）
	npm run build -w @aiclaw/agent-client

build-go: $(AGENT_BIN) ## 编译 claw-agent

# CGO_ENABLED=0 是硬要求，不是优化：开了 cgo 就没法交叉编译（一台 Linux 上
# 出四个平台的包），还要求每台构建机装好 C 工具链。两个 SQLite 库都是纯 Go
# 实现，正是为此选的。make nocgo 守着这条。
$(AGENT_BIN): $(GO_SOURCES) go.mod
	CGO_ENABLED=0 go build -o $(AGENT_BIN) ./tools/claw-agent

build-desktop: deps build-client ## 编译 Electron 主进程与渲染层
	cd $(DESKTOP) && npm run build

# ---------- 起应用 ----------

dev: build ## 起整个应用，手动测功能
	@echo
	@echo "配置存在 Electron 的 userData 目录；模型服务、插件、搜索引擎在 ~/.aiclaw/aiclaw.db。"
	@echo "首次启动请在「模型服务」页添加一个端点、填 Key、写上模型名。"
	@echo "内核二进制：$(AGENT_BIN)"
	@echo
	cd $(DESKTOP) && npx electron .

dev-ui: deps build-go build-client ## 同 dev，但渲染层带热更（改 Vue 立即生效）
	@cd $(DESKTOP) && npm run build:main && npm run build:preload
	@set -e; \
	cd $(DESKTOP); \
	npm run dev -- --port $(VITE_PORT) --strictPort & \
	VITE_PID=$$!; \
	trap 'kill $$VITE_PID 2>/dev/null || true' EXIT INT TERM; \
	for i in $$(seq 1 100); do \
		curl -sf http://localhost:$(VITE_PORT) >/dev/null && break; \
		sleep 0.2; \
	done; \
	VITE_DEV_SERVER_URL=http://localhost:$(VITE_PORT) npx electron .

# ---------- 验证 ----------

check: fmt-check vet nocgo test typecheck smoke smoke-desktop ## 交付前全量预检
	@echo
	@echo "全部通过。"

test: test-go test-renderer ## 跑测试（带 race）

test-go: ## Go 单元与集成测试（内核 + 插件系统 + 存储层）
	go test -race -count=1 ./...

# 这组用例里一大半是安全用例，别只当成格式测试：
#   - Markdown 渲染器的输入是模型输出，而模型输出会被它读到的文件和命令结果影响；
#   - computer use 的自身进程名表是自点审批的防护。
test-renderer: ## 纯函数测试（Markdown 渲染与转义、配置归一化、版本比较……）
	npm run test:renderer

smoke: deps build-go build-client ## 内核冒烟：真拉起 claw-agent，走模型服务、插件、搜索引擎；不需要凭据
	npm run smoke

# 发布前跑，不进 check：它真打模型（十几次调用，画图朗读各一次），花钱也慢。
# 用桌面应用「配置」页里的默认模型与多模态角色；凭据从 ~/.aiclaw/aiclaw.db 拷一份出来用，
# 不碰原库。产物（会话、生成的图与音频）在 build/scenarios/<时间>/。
scenarios: deps build-go build-client ## 发布前场景测试：真打模型，对话 / 文件 / 命令 / 审批 / 看图 / 画图 / 朗读 / 听写 / 搜索 / 并发
	npm run scenarios

# 这条要起 Electron，需要图形会话（Linux CI 上套 xvfb-run）。
# 没有图形会话的环境用 SKIP_DESKTOP_SMOKE=1 跳过——但那样就漏掉了
# preload、IPC 桥接、界面起不起得来这一整段，本机务必跑。
smoke-desktop: deps build-desktop ## 桌面宿主冒烟：preload 注入、IPC 桥接、页面渲染、应用图标
ifdef SKIP_DESKTOP_SMOKE
	@echo "跳过桌面冒烟（SKIP_DESKTOP_SMOKE 已设）——preload 与界面这段没有验。"
else
	npm run smoke:desktop
endif

icon: deps ## 从品牌标识重新生成应用图标（产物要提交）
	npx electron scripts/make-icon.cjs

typecheck: deps build-client ## TS 类型检查（主进程 + preload + 渲染层）
	cd $(DESKTOP) && npm run typecheck

fmt: ## 格式化 Go 代码
	gofmt -w $(AGENT_DIR) $(ROOT)/internal $(ROOT)/pkg

fmt-check: ## 检查 Go 格式与安装脚本语法（CI 用，不改文件）
	@unformatted=$$(gofmt -l $(AGENT_DIR) $(ROOT)/internal $(ROOT)/pkg); \
	if [ -n "$$unformatted" ]; then \
		echo "以下文件未格式化，跑 make fmt："; echo "$$unformatted"; exit 1; \
	fi
	@bash -n scripts/install-mac.sh

vet: ## go vet
	go vet ./...

# 交叉编译到 Windows 是最直接的 cgo 探针：只要有哪个依赖需要 cgo，这里就断。
# 本机 CGO_ENABLED=0 能过不代表没问题——cgo 的依赖在本机有 C 工具链时照样能编。
nocgo: ## 确认内核不依赖 cgo（交叉编译到 Windows 验证）
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./tools/claw-agent
	@echo "内核不需要 cgo。"

doctor: ## 检查本机环境与构建产物
	@echo "go        : $$(go version 2>/dev/null || echo '未安装')"
	@echo "node      : $$(node --version 2>/dev/null || echo '未安装')"
	@echo "npm       : $$(npm --version 2>/dev/null || echo '未安装')"
	@echo "依赖      : $$([ -d $(ROOT)/node_modules ] && echo 已安装 || echo '未安装，跑 make deps')"
	@echo "claw-agent: $$([ -x $(AGENT_BIN) ] && echo $(AGENT_BIN) || echo '未编译，跑 make build-go')"

# ---------- 打包 ----------
#
# 四个目标：macOS Apple Silicon / macOS Intel / Windows x64 / Linux x64。
# Go 内核交叉编译到 build/bin/<平台>-<架构>/，Electron 那边由
# scripts/package-app.mjs 用 @electron/packager 组装成目录，CI 再打成 zip
# 挂到 GitHub Release。一台 Linux 就能出全部四个包——这就是 CGO_ENABLED=0 的回报。

define cross_build
	mkdir -p build/bin/$(1)-$(2)
	CGO_ENABLED=0 GOOS=$(3) GOARCH=$(4) go build -trimpath -ldflags "-s -w" \
		-o build/bin/$(1)-$(2)/claw-agent$(5) ./tools/claw-agent
	node scripts/package-app.mjs --platform=$(1) --arch=$(2)
endef

package-mac: deps build-client build-desktop ## 打 macOS arm64 包（未签名）
	$(call cross_build,darwin,arm64,darwin,arm64,)

package-mac-intel: deps build-client build-desktop ## 打 macOS x64 包（未签名）
	$(call cross_build,darwin,x64,darwin,amd64,)

package-win: deps build-client build-desktop ## 打 Windows x64 包（exe 图标与版本信息由 resedit 写，不需要 wine）
	$(call cross_build,win32,x64,windows,amd64,.exe)

package-linux: deps build-client build-desktop ## 打 Linux x64 包
	$(call cross_build,linux,x64,linux,amd64,)

package: package-mac package-mac-intel package-win package-linux ## 打全部四个平台的包

install-mac: package-mac ## 本机构建并装进「应用程序」（不需要网络与凭据）
	./scripts/install-mac.sh release/AIClaw-darwin-arm64

# ---------- 清理 ----------

clean: ## 删构建产物，保留 node_modules
	rm -f $(AGENT_BIN) $(AGENT_BIN).exe
	rm -rf $(DESKTOP)/dist $(ROOT)/packages/agent-client/dist
	rm -rf $(ROOT)/build $(ROOT)/release $(ROOT)/dist-release

distclean: clean ## 连 node_modules 一起删
	rm -rf $(ROOT)/node_modules $(ROOT)/packages/*/node_modules $(ROOT)/apps/*/node_modules
