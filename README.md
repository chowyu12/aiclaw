# AiClaw

基于 Go + Vue 3 构建的 AI Agent 管理与执行平台，支持多模型供应商接入、工具调用、技能编排和多轮对话。

## 核心优势

### 灵活的 Agent 自定义

每个 Agent 是一个独立的智能体，可自由组合模型、工具、技能和 MCP 服务。通过 Web UI 配置系统提示词、模型参数、关联资源，无需编码即可构建面向不同场景的专属 Agent。支持 Agent Token 直接对外提供 API 服务，方便后端系统集成。

### 二阶段技能加载（Two-Phase Skill Loading）

借鉴 Cursor 的技能加载策略，采用**摘要注入 + 按需读取**的两阶段模式：

- **阶段一**：仅将技能名称、描述和 `SKILL.md` 文件路径注入 System Prompt，不加载完整指令内容
- **阶段二**：LLM 判断需要使用某项技能时，主动调用 `read` 工具读取 `SKILL.md` 获取详细指令

相比一次性注入所有技能内容，此方案在 Agent 挂载大量技能时可显著降低 System Prompt 长度，减少每次请求的基础 Token 消耗，同时保持技能的完整可用性。

### Tool Search — 工具按需发现

当 Agent 挂载大量工具时，传统方式会将所有工具的 Function 定义（名称 + 描述 + 参数 Schema）一次性发送给 LLM，随着工具数量增长，Token 开销急剧膨胀且工具选择准确率下降。

开启 **Tool Search** 后，Agent 会加载系统中全部已启用工具（含 MCP / 技能声明工具），但根据**工具条数自动选择模式**：

- **工具条数 ≤ 24（默认阈值）**：**自动全量下发**所有工具定义，不进入懒加载，避免「只有十几个工具却反复 `tool_search`」浪费轮次（与少量内置工具场景对齐）。
- **工具条数 > 24**：进入**懒加载**：
  1. **初始请求**只携带轻量的 `tool_search` 与技能预加载工具
  2. LLM 调用 `tool_search("关键词")`，本地关键词匹配返回 Top 5
  3. 匹配工具**完整定义注入下一轮**，已发现工具会保留

此外，执行器内置**轻量循环检测**（参考 OpenClaw `loop-detection` 思路）：连续相同参数调用、`tool_search` 短时内过多、两工具 ping-pong 交替等模式会触发拦截，向模型返回 `[loop_guard]` 提示，减少无效重复工具调用。

可在 Agent 配置页面一键开启 Tool Search；阈值 `ToolSearchAutoFullThreshold` 见 `internal/agent/tool_search.go`。

> 参考：[OpenAI / Anthropic Tool Search 机制](https://platform.openai.com/docs/guides/function-calling)

---

## 功能特性

### Agent 管理

- Agent 增删改查，支持设置名称、UUID、系统提示词、模型参数（温度、最大 token 等）
- 每个 Agent 可关联多个工具（Tools）、技能（Skills）和 MCP 服务
- 支持工具优先执行策略，Agent 自动判断是否需要调用工具（Function Calling）

### 模型供应商

- 支持多种 LLM Provider：OpenAI、Qwen（通义千问）、Kimi、Moonshot、OpenRouter、**OpenAI 兼容接口**（如 New API / 第三方代理）、**Anthropic Claude**、**Google Gemini**
- 可配置 Base URL、API Key、可用模型列表
- 创建 Agent 时自动拉取供应商模型列表，支持搜索过滤

### 工具系统

14 个内置工具，覆盖文件操作、命令执行、网页交互和任务调度：

| 工具               | 说明                                                            |
| ------------------ | --------------------------------------------------------------- |
| `read`             | 读取文件内容，支持按行范围读取                                  |
| `write`            | 创建或覆盖文件，自动创建父目录                                  |
| `edit`             | 精确编辑文件（查找并替换）                                      |
| `grep`             | 按正则表达式搜索文件内容                                        |
| `find`             | 按 glob 模式查找文件                                            |
| `ls`               | 列出目录内容                                                    |
| `exec`             | 运行 Shell 命令，支持 PTY（适配需要 TTY 的命令行工具）          |
| `process`          | 管理后台命令会话（启动、列出、读取输出、终止）                  |
| `web_fetch`        | 抓取 URL 并提取可读内容，自动回退浏览器渲染                     |
| `browser`          | 浏览器自动化：33 种操作（导航、截图、快照、交互、监控、仿真等） |
| `canvas`           | 渲染 HTML/CSS/JS 画布，执行 JS 表达式，截取快照                 |
| `cron`             | 管理定时任务与唤醒事件（提醒）                                  |
| `code_interpreter` | 代码解释器：Python/JavaScript/Shell 沙箱执行                    |
| `current_time`     | 获取当前系统时间                                                |

- 支持自定义 HTTP 工具和命令工具（通过 Web UI 或 API 创建）
- MCP 协议客户端，支持接入 MCP 远程工具服务
- 工具执行过程全链路追踪

### 技能系统

- 采用 OpenClaw 标准格式，每个技能是一个独立目录（`SKILL.md` + `manifest.json` + 可执行代码）
- 技能来自 Workspace 下 `skills/` 目录扫描（将技能目录放入该文件夹即可；设置页可刷新列表）
- 技能可在 `manifest.json` 中声明工具定义（parameters），Agent 执行时自动注册为可调用工具
- 支持可执行技能（`index.js` / `index.py`），通过子进程运行工具逻辑
- 纯指令技能将 `SKILL.md` 内容注入 System Prompt，引导 LLM 按指令推理
- 预置 5 个内置技能，每个都组合多个工具形成完整工作流：
  - **深度研究** — `web_fetch` + `browser` + `write`，多源信息采集与研究报告生成
  - **定时任务** — `cron` + `exec` + `write`，自然语言描述自动生成脚本并配置定时执行
  - **系统运维** — `exec` + `process` + `read` + `grep`，系统健康检查、日志排错、进程管理
  - **数据处理** — `code_interpreter` + `read` + `write`，CSV/JSON/Excel 数据清洗、转换、统计
  - **网页采集** — `browser` + `web_fetch` + `code_interpreter` + `write`，结构化提取网页数据
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
- 支持流式（SSE）和阻塞式两种响应模式
- 流式响应实时展示执行步骤

### 执行日志

- 完整记录每次 Agent 调用的执行链路
- 详细记录每个步骤：LLM 调用、工具调用、技能匹配
- 包含输入输出、耗时、Token 用量、错误信息等

### 用户系统

- 超管（admin）和访客（guest）两种角色
- 使用配置文件中的 `auth.web_token` 登录，浏览器以 Bearer 方式携带同一令牌访问 API
- 首次访问引导创建超级管理员
- 访客只读，无法进行新增、编辑、删除操作
- 超管可管理用户（创建、禁用、删除、修改角色）

### 管理后台

- 现代化 Web UI（Vue 3 + Element Plus）
- 供应商、Agent、工具、技能等的 CRUD 管理
- 对话 Playground（默认首页）
- 执行日志查看器
- 前端编译后嵌入 Go 二进制，单文件部署

## 技术栈

| 层级    | 技术                                               |
| ------- | -------------------------------------------------- |
| 后端    | Go 1.25、net/http、logrus                          |
| AI 编排 | Function Calling（openai-go SDK）                  |
| ORM     | GORM（MySQL / PostgreSQL / SQLite）                |
| 认证    | Web 访问令牌（Bearer）、Agent Token（ag- 前缀）    |
| 前端    | Vue 3、TypeScript、Element Plus、Pinia、Vue Router |
| 构建    | Go embed、Vite                                     |

## 快速开始

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

auth:
  # 留空则首次启动会自动生成并写回配置文件
  web_token: ""

# 单例 Agent 在本文件持久化（控制台保存会写回；直接编辑 yaml 会自动热加载）。MCP 在数据库。
# agent:
#   name: Assistant
#   model_name: gpt-4o
#   temperature: 0.7
#   provider_id: 0   # 0 表示自动使用数据库中第一个供应商
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

浏览器访问 `http://localhost:8080`，首次打开会引导创建超级管理员账号。

### 5. 配置模型供应商

登录后进入「模型供应商」页面，添加至少一个 LLM Provider（如 OpenAI），填入 API Key 和 Base URL。启动时会自动创建默认单例 Agent（内存 + `config.yaml` 的 `agent` 段）；若 `agent.provider_id` 为 0，会绑定到第一个供应商。

### 6. 创建 Agent 开始对话

进入「Agent 管理」创建 Agent，选择模型、配置工具和技能，然后在「对话测试」中体验。

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

每个 Agent 创建时会自动生成一个 `ag-` 前缀的 API Token，后端服务可以直接用这个 Token 调用 chat 接口，无需使用 Web 控制台令牌。Token 可在 Agent 编辑页面查看、复制和重置。

**阻塞式调用**

```bash
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "今天天气怎么样？", "user_id": "backend-service"}'
```

使用 Agent Token 时无需传 `agent_id`，系统会自动匹配。

**流式调用（SSE）**

```bash
curl -N -X POST http://localhost:8080/api/v1/chat/stream \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "帮我写一个排序算法", "user_id": "backend-service"}'
```

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

# 也支持直接传文件 URL
curl -X POST http://localhost:8080/api/v1/chat/completions \
  -H "Authorization: Bearer ag-xxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "message": "分析这张图片",
    "user_id": "backend-service",
    "files": [
      {"type": "image", "transfer_method": "remote_url", "url": "https://example.com/image.png"}
    ]
  }'
```

> Agent Token 仅可访问 `/api/v1/chat/` 下的接口，不能访问管理类接口。

## 部署

项目支持单文件部署，构建后的二进制文件已包含前端静态资源：

```bash
make all
./bin/aiclaw
```
