# AiClaw

基于 Go + Vue 3 构建的 AI Agent 管理与执行平台，支持**多 Agent 协作**、**子 Agent 嵌套执行**、多模型供应商接入、工具并行调用、技能编排、多渠道接入和多轮对话。

## 一键安装

适用于 Linux (amd64/arm64) 和 macOS (amd64/arm64)，自动下载最新 Release、注册系统服务并启动：

```bash
curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/install.sh | bash
```

安装完成后会输出 Web 访问地址（含登录令牌），打开浏览器即可使用。首次启动自动使用 SQLite 并生成配置文件 `~/.aiclaw/config.yaml`。

```bash
aiclaw start      # 启动
aiclaw stop       # 停止
aiclaw status     # 查看状态
aiclaw update     # 更新到最新版本
aiclaw version    # 查看当前版本
```

> 如需从源码构建或自定义配置，请参考下方「从源码构建」章节。

## 核心架构

### 子 Agent 系统

支持嵌套式 Agent 执行：父 Agent 可通过 `sub_agent` 工具启动子 Agent 处理独立子任务，每个子 Agent 拥有独立会话、推理链和工具集。

- 最大嵌套深度 3 层，通过 context 传播深度计数防止无限递归
- 子 Agent 可使用与父 Agent 相同或不同的 Agent 配置（模型、提示词、工具集）
- **三种执行模式**: `auto`（全功能）、`explore`（只读探索）、`shell`（命令执行）
- **模型选择**: 支持 `model: "fast"` 为简单子任务使用轻量模型（如 gpt-4o-mini），降低成本
- 批量任务自动并行执行，返回结构化 JSON 结果
- 适用于并行调研、专家分工、复杂任务拆分等场景

### 任务规划与分解

借鉴 Cursor IDE 的任务拆解模式，Agent 可通过 `todo` 工具创建结构化任务列表，逐项推进：

- 复杂请求自动拆解为多个子任务（`pending` → `in_progress` → `completed`）
- 任务列表每轮 LLM 调用前动态刷新注入 system prompt，Agent 始终清楚当前进度
- 配合 `sub_agent(mode=explore)` 先并行探索再动手修改，提高准确性

### 记忆架构

按"时间跨度 × 触发方式 × 注入位置"分层组织，每层职责单一、互不重叠：

| 层 | 名称 | 触发 / 写入 | 注入 prompt | 用途 |
| -- | -- | -- | -- | -- |
| L1 | **持久记忆** (`memory` 工具) | LLM 主动写入 `<root>/memories/{MEMORY,USER}.md` | 启动注入冻结快照；超 70% 容量自动切 INDEX 模式（仅注入 `[id]+#tag+摘要`，按需 `recall`） | 跨会话事实、用户画像、项目惯例 |
| L2 | **Todo 块** (`todo` 工具) | LLM 调 `todo` | 每轮 LLM 调用前刷新注入 system prompt | 当前任务进度规划 |
| L3 | **会话归档** (Session Archive) | 每累计 N 条消息后零 LLM 重生 `<root>/agents/<u>/archives/<conv>.md` | 否（通过 `/continue` 命令显式切换会话） | 跨会话切换 + 长会话快照 |
| L4 | **历史结构性截断** (`LoadHistory`) | 每次 Execute 自动应用 | 是（作为 history） | 三级压缩：近 N 轮全保留 / 中段轻压缩 / 早期重压缩 |
| L5 | **运行时上下文压缩** (`ContextCompressor`) | 每 round 检查；超阈值时 LLM 摘要中段 | 是（替换 messages） | 长对话防超窗 |
| L6 | **Skill 自动结晶** | 一次执行调 ≥3 个不同工具且成功 → 落 `<root>/skills-pending/*.md` | 否（`skill` 工具列出 / 转正） | 经验沉淀为可复用技能 |

**配套能力**

- **跨会话搜索** (`session_search` 工具)：SQLite FTS5 全文索引，用户说"上次/我们之前"时 Agent 自动调用
- **L1 安全扫描**：拦截 prompt injection、不可见 Unicode 字符、伪造元数据头
- **斜杠命令**：在任意接入渠道发送 `/new`（开新会话）/ `/continue`（列归档）/ `/continue N`（切换到第 N 个归档）/ `/help`
- **持久化层**：对话历史写入 MySQL / PostgreSQL / SQLite

### 工具并行执行

对同一轮 LLM 返回的多个 tool_calls，自动识别并发安全的工具（`read`、`grep`、`find`、`ls`、`web_fetch`、`current_time`）并行执行，串行工具按顺序执行。共享状态通过 `sync.Mutex` 保护。

### Token 预算控制

Agent 级可配置 Token 预算（`token_budget`），单次执行的 Token 消耗超出预算时优雅停止，防止失控的工具循环造成过高成本。0 表示不限制。

### 生命周期 Hook

支持在 Agent 执行的关键节点注入自定义逻辑：

| 事件            | 触发时机       | 可用 Action      |
| --------------- | -------------- | ---------------- |
| `pre_tool_use`  | 工具调用前     | Continue / Skip  |
| `post_tool_use` | 工具调用后     | Continue         |
| `pre_llm_call`  | LLM 调用前     | Continue / Abort |
| `post_llm_call` | LLM 调用后     | Continue         |
| `agent_done`    | Agent 循环结束 | —                |

### 二阶段技能加载

借鉴 Cursor 的技能加载策略，采用**摘要注入 + 按需读取**的两阶段模式：

- **阶段一**：仅将技能名称、描述和 `SKILL.md` 文件路径注入 System Prompt，不加载完整指令内容
- **阶段二**：LLM 判断需要使用某项技能时，主动调用 `read` 工具读取 `SKILL.md` 获取详细指令

相比一次性注入所有技能内容，此方案在 Agent 挂载大量技能时可显著降低 System Prompt 长度。

### Tool Search — 工具按需发现

当 Agent 挂载大量工具时，开启 Tool Search 后根据工具条数自动选择模式，避免 Token 开销膨胀。执行器内置轻量循环检测（连续相同参数调用、ping-pong 交替等）触发拦截，减少无效重复工具调用。

---

## 功能特性

### Agent 管理

- 多 Agent 配置，每个 Agent 可独立设置模型、系统提示词、温度、最大 Token、超时等参数
- Agent 可关联多个工具（Tools）、技能（Skills）和 MCP 服务
- Token 预算控制（单次执行上限）
- 支持 Agent Token 直接对外提供 API 服务
- 支持设为默认 Agent

### 模型供应商

- 支持多种 LLM Provider：OpenAI、Qwen（通义千问）、Kimi、Moonshot、OpenRouter、**OpenAI 兼容接口**（如 New API / 第三方代理）、**Anthropic Claude**、**Google Gemini**
- 可配置 Base URL、API Key、可用模型列表
- 创建 Agent 时自动拉取供应商模型列表，支持搜索过滤

### 渠道接入

支持将 Agent 接入多种消息渠道，实现多平台自动回复。所有渠道均使用流式执行（`ExecuteStream`），支持在 Agent 处理期间发送"正在输入"提示：

| 渠道     | 类型标识   | 接入方式         | Typing 支持            |
| -------- | ---------- | ---------------- | ---------------------- |
| 企业微信 | `wecom`    | WebSocket 长连接 | —                      |
| 微信     | `wechat`   | iLink 长轮询     | SendTyping（周期调用） |
| 飞书     | `feishu`   | Webhook          | —                      |
| 钉钉     | `dingtalk` | Webhook          | —                      |
| WhatsApp | `whatsapp` | Webhook          | —                      |
| Telegram | `telegram` | Webhook          | sendChatAction         |

渠道管理页面支持一键启用/禁用、查看会话记录和消息明细。

### 工具系统

20 个内置工具，覆盖文件操作、命令执行、网页交互、桌面自动化、任务调度、记忆管理和 Agent 协作：

| 工具               | 说明                                                            |
| ------------------ | --------------------------------------------------------------- |
| `read`             | 读取文件内容，支持按行范围读取，图片自动传入视觉模型            |
| `write`            | 创建或覆盖文件，自动创建父目录                                  |
| `edit`             | 精确编辑文件（查找并替换）                                      |
| `grep`             | 按正则表达式搜索文件内容                                        |
| `find`             | 按 glob 模式查找文件                                            |
| `ls`               | 列出目录内容                                                    |
| `exec`             | 运行 Shell 命令，支持 PTY、working_dir 与超时                   |
| `process`          | 管理后台命令会话（启动、列出、读取输出、终止）                  |
| `web_fetch`        | 抓取 URL 并提取可读内容，自动回退浏览器渲染                     |
| `browser`          | 浏览器自动化：33 种操作（导航、截图、快照、交互、监控、仿真等） |
| `canvas`           | 渲染 HTML/CSS/JS 画布，执行 JS 表达式，截取快照                 |
| `cron`             | 进程内定时调度：cron 表达式、执行次数限制、持久化、执行日志     |
| `code_interpreter` | 代码解释器：Python/JavaScript/Shell 沙箱执行                    |
| `current_time`     | 获取当前系统时间                                                |
| `sub_agent`        | 启动子 Agent（支持 explore/shell/auto 模式，可选快速模型）      |
| `memory`           | 持久记忆 (L1)：读写 MEMORY.md / USER.md，支持 add/replace/remove/read/recall，超 70% 容量自动 INDEX 模式 |
| `session_search`   | 跨会话搜索：FTS5 全文索引查找历史对话                           |
| `todo`             | 结构化任务规划：创建/更新/查看任务列表，动态注入上下文          |
| `skill`            | Skill 结晶管理：列出/查看/转正/丢弃 `skills-pending/` 中自动归档的执行路径候选 |

- 支持自定义 HTTP 工具和命令工具（通过 Web UI 或 API 创建）
- MCP 协议客户端，支持接入 MCP 远程工具服务
- 工具执行过程全链路追踪
- 并发安全的只读工具自动并行执行

### 技能系统

- 采用标准格式，每个技能是一个独立目录（`SKILL.md` + `manifest.json` + 可执行代码）
- 技能来自 `~/.aiclaw/skills/` 目录扫描（将技能目录放入该文件夹即可）
- 技能可在 `manifest.json` 中声明工具定义，Agent 执行时自动注册为可调用工具
- 支持可执行技能（`index.js` / `index.py`），通过子进程运行工具逻辑
- 预置 5 个内置技能，均支持 sub_agent 协作模式：
  - **深度研究** — `sub_agent` + `web_fetch` + `browser` + `write`，通过子 Agent 并行多源采集与研究报告生成
  - **定时任务** — `cron` + `exec` + `write`，自然语言描述自动生成脚本并配置定时执行
  - **系统运维** — `sub_agent` + `exec` + `process` + `read` + `grep`，并行诊断系统问题
  - **数据处理** — `sub_agent` + `code_interpreter` + `read` + `write`，大批量数据分治处理
  - **网页采集** — `sub_agent` + `browser` + `web_fetch` + `code_interpreter` + `write`，并行多站点结构化数据提取

**技能目录结构：**

```
~/.aiclaw/skills/
  brave-web-search/
    SKILL.md          # 技能指令（注入 System Prompt）
    manifest.json     # 元数据、工具定义、配置、权限
    index.js          # 可选：可执行工具逻辑
    README.md         # 可选：文档
```

**manifest.json 示例：**

```json
{
  "name": "brave-web-search",
  "version": "1.0.0",
  "description": "Search the web using Brave Search API",
  "author": "niceperson",
  "main": "index.js",
  "tools": [
    {
      "name": "web_search",
      "description": "Search the web",
      "parameters": {
        "type": "object",
        "properties": { "query": { "type": "string" } },
        "required": ["query"]
      }
    }
  ]
}
```

### 对话与记忆

- 支持多轮对话，自动维护上下文
- 对话历史持久化存储（MySQL / PostgreSQL / SQLite）
- 完整记忆体系详见上方 [「记忆架构」](#记忆架构) 章节，覆盖持久记忆、Todo、会话归档、历史压缩、运行时压缩、Skill 结晶六层
- 在任意接入渠道支持 `/new` `/continue` `/help` 斜杠命令切换或续接会话
- 支持流式（SSE）和阻塞式两种响应模式
- 流式响应实时展示执行步骤，done chunk 包含完整结果

### 执行日志

- 完整记录每次 Agent 调用的执行链路
- 详细记录每个步骤：LLM 调用、工具调用、技能匹配
- 包含输入输出、耗时、Token 用量、错误信息等

### 管理后台

- 现代化 Web UI（Vue 3 + Element Plus）
- 供应商、Agent、工具、技能、渠道等的管理
- 对话 Playground（默认首页）
- 执行日志查看器
- 前端编译后嵌入 Go 二进制，单文件部署

## 技术栈

| 层级    | 技术                                                           |
| ------- | -------------------------------------------------------------- |
| 后端    | Go 1.25、net/http、logrus、lumberjack（日志轮转）              |
| AI 编排 | Function Calling（openai-go SDK）、子 Agent 嵌套、工具并行执行 |
| ORM     | GORM（MySQL / PostgreSQL / SQLite）                            |
| 认证    | Web 访问令牌（Bearer）、Agent Token（ag- 前缀）                |
| 前端    | Vue 3、TypeScript、Element Plus、Pinia、Vue Router             |
| 构建    | Go embed、Vite                                                 |

## 从源码构建

### 前置要求

- Go 1.25+
- Node.js 18+
- 数据库（任选其一）：MySQL 8.0+ / PostgreSQL 14+ / SQLite 3

### 1. 克隆项目

```bash
git clone https://github.com/chowyu12/aiclaw.git
cd aiclaw
```

### 2. 配置数据库

编辑 `etc/config.yaml`，选择一种数据库：

**MySQL**（推荐生产环境）：

```yaml
database:
  driver: mysql
  dsn: "YOUR_USER:YOUR_PASSWORD@tcp(127.0.0.1:3306)/aiclaw?charset=utf8mb4&parseTime=True&loc=Local"
```

**PostgreSQL**：

```yaml
database:
  driver: postgres
  dsn: "host=127.0.0.1 user=YOUR_USER password=YOUR_PASSWORD dbname=aiclaw port=5432 sslmode=disable"
```

**SQLite**（零配置，适合开发/单机部署）：

```yaml
database:
  driver: sqlite
  dsn: "aiclaw.db"
```

> 启动时 GORM 会自动创建/迁移表结构，无需手动执行 SQL。

### 3. 修改其他配置

```yaml
server:
  host: "0.0.0.0"
  port: 8080

log:
  level: info
  max_size: 10 # 日志文件最大 MB，超出自动轮转

auth:
  # 留空则首次启动会自动生成并写回配置文件
  web_token: ""
```

### 4. 安装依赖并启动

```bash
# 安装 Go 依赖
go mod tidy

# 安装前端依赖
cd web && npm install && cd ..

# 构建前端 + 启动服务
make dev
```

### 5. 配置模型供应商

登录后进入「模型供应商」页面，添加至少一个 LLM Provider（如 OpenAI），填入 API Key 和 Base URL。

### 6. 创建 Agent 开始对话

进入「Agent 管理」创建 Agent，选择模型、配置工具和技能，设置 Token 预算（可选），然后在「对话测试」中体验。

## 常用命令

```bash
make build            # 编译后端二进制（含嵌入前端）
make dev              # 开发模式启动（自动构建前端）
make test             # 运行所有测试
make build-frontend   # 单独构建前端
make dev-frontend     # 前端开发模式（热更新，需单独启动后端）
make clean            # 清理构建产物
make deps             # 整理 Go 依赖
```

### Agent Token（后端调用）

系统为每个 Agent 自动生成 `ag-` 前缀的 API Token，后端服务可用该 Token 调用 chat 接口（与 Web 控制台的 `web_token` 不同）。Token 可在 Agent 管理页面查看、复制和重置。

**阻塞式调用**

```bash
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "今天天气怎么样？", "user_id": "backend-service"}'
```

**流式调用（SSE）**

```bash
curl -N -X POST http://localhost:8080/api/v1/chat/stream \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "帮我写一个排序算法", "user_id": "backend-service"}'
```

流式响应的 done chunk 包含完整的 `content`、`tokens_used` 和 `steps`，客户端可直接使用而非拼接 delta。

**带会话上下文的多轮对话**

```bash
# 第一轮，返回的 conversation_id 用于后续对话
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "什么是微服务？", "user_id": "backend-service"}'

# 第二轮，传入上一轮返回的 conversation_id
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "它和单体架构有什么区别？", "conversation_id": "上一轮返回的ID", "user_id": "backend-service"}'
```

**带文件的对话**

```bash
# 先上传文件，获取 upload_file_id
curl -X POST http://localhost:8080/api/v1/files/upload \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -F "file=@document.pdf"

# 在对话中引用文件
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "帮我总结这个文档",
    "user_id": "backend-service",
    "files": [
      {"type": "document", "transfer_method": "local_file", "upload_file_id": "文件UUID"}
    ]
  }'
```

> Agent Token 仅可访问 `/api/v1/chat/` 下的接口，不能访问管理类接口。

## 部署

### 编译

项目支持单文件部署，构建后的二进制文件已包含前端静态资源：

```bash
# 安装前端依赖 + 构建前端 + 编译 Go 二进制
make all

# 产物在 bin/ 目录
ls -lh bin/aiclaw
```

也可分步构建：

```bash
# 仅构建前端
make build-frontend

# 仅编译后端（需先构建前端）
make build

# 交叉编译示例
GOOS=linux GOARCH=amd64 go build -o bin/aiclaw-linux-amd64 cmd/server/main.go
GOOS=windows GOARCH=amd64 go build -o bin/aiclaw-windows-amd64.exe cmd/server/main.go
GOOS=darwin GOARCH=arm64 go build -o bin/aiclaw-darwin-arm64 cmd/server/main.go
```

部署时只需将编译产物和配置文件拷贝到目标机器：

```bash
scp bin/aiclaw user@server:/opt/aiclaw/
scp etc/config.yaml user@server:/opt/aiclaw/etc/
```

### 后台运行与开机自启

#### Linux（systemd）

创建服务文件 `/etc/systemd/system/aiclaw.service`：

```ini
[Unit]
Description=AiClaw AI Agent Platform
After=network.target

[Service]
Type=simple
User=aiclaw
WorkingDirectory=/opt/aiclaw
ExecStart=/opt/aiclaw/aiclaw
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable aiclaw
sudo systemctl start aiclaw
sudo journalctl -u aiclaw -f
```

#### macOS（launchd）

创建 `~/Library/LaunchAgents/com.aiclaw.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.aiclaw</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/aiclaw</string>
  </array>
  <key>WorkingDirectory</key>
  <string>/usr/local/bin</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.aiclaw.plist
```

#### Docker

```bash
docker run -d \
  --name aiclaw \
  --restart unless-stopped \
  -p 8080:8080 \
  -v ~/.aiclaw:/root/.aiclaw \
  aiclaw:latest
```

## 设计文档

详细的架构设计与实现说明请参阅 [`docs/design/`](docs/design/) 目录：

- [Agent 改进方案](docs/design/agent-improvements.md) — 持久记忆、跨会话搜索、任务规划、子 Agent 类型分化、定时调度等核心增强的设计思路与实现细节
