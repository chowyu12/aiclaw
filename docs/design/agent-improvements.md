# AiClaw Agent 架构改进设计文档

> 基于 [NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent) 和 [Cursor IDE](https://cursor.com) 的设计分析，对 AiClaw Agent 执行引擎的系统性改进。

---

## 一、改进全景

```
┌──────────────────────────────────────────────────────────────────┐
│                     AiClaw Agent 执行引擎                        │
│                                                                  │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────────┐ │
│  │ 持久记忆系统 │  │ 跨会话搜索   │  │    任务规划与分解        │ │
│  │ MEMORY.md   │  │ FTS5 全文索引 │  │    todo 工具            │ │
│  │ USER.md     │  │ session_search│  │    结构化进度追踪        │ │
│  │ (Hermes)    │  │ (Hermes)     │  │    (Cursor)             │ │
│  └──────┬──────┘  └──────┬───────┘  └───────────┬─────────────┘ │
│         │                │                      │               │
│  ┌──────▼──────────────────────────────────────▼──────────────┐ │
│  │                   System Prompt 组装                        │ │
│  │  base prompt + 持久记忆快照 + 会话笔记 + Todo列表           │ │
│  └──────────────────────┬────────────────────────────────────┘ │
│                         │                                      │
│  ┌──────────────────────▼────────────────────────────────────┐ │
│  │                 执行循环 (run loop)                         │ │
│  │  每轮: 刷新 todo → LLM → tool calls → 结果 → 下一轮       │ │
│  └──────────────────────┬────────────────────────────────────┘ │
│                         │                                      │
│  ┌──────────────────────▼────────────────────────────────────┐ │
│  │              多模态子代理系统 (sub_agent)                    │ │
│  │  ┌─────────┐  ┌──────────┐  ┌────────┐  ┌──────────────┐ │ │
│  │  │  auto   │  │ explore  │  │ shell  │  │ model=fast   │ │ │
│  │  │ 全功能  │  │ 只读探索  │  │ 命令行 │  │ 轻量模型选择  │ │ │
│  │  │(Hermes) │  │ (Cursor) │  │(Cursor)│  │  (Cursor)    │ │ │
│  │  └─────────┘  └──────────┘  └────────┘  └──────────────┘ │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────────┐│
│  │                  定时任务调度器 (cron)                        ││
│  │  in-process scheduler · prompt/command · 持久化 · 执行日志   ││
│  │  (Hermes)                                                    ││
│  └──────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────┘
```

---

## 二、核心改进详解

### 2.1 持久记忆系统 — 借鉴 Hermes Agent

**设计来源**: Hermes Agent 的 `MEMORY.md` + `USER.md` 模式，通过「冻结快照」在会话启动时注入 system prompt。

**核心思路**:
- Agent 需要跨会话的「个性化」能力：记住用户偏好、环境特征、项目惯例
- 记忆不应放在数据库 blob 中（不可审计），而是纯文本 Markdown（可人工编辑/审查）
- 记忆注入 system prompt 时是「冻结快照」，会话中途的修改不影响当前会话（下次会话生效）

**架构**:

```
~/.aiclaw/memories/
├── MEMORY.md   ← Agent 个人笔记（环境事实、惯例、经验）  上限 2200 字符
└── USER.md     ← 用户画像（偏好、沟通风格、习惯）       上限 1375 字符
```

**数据流**:

```
会话启动
  │
  ├── LoadSnapshot() ─── 读取 MEMORY.md + USER.md
  │                        ↓
  │                 格式化为带使用率的文本块
  │                        ↓
  ├── 注入 system prompt（位于 base prompt 之后）
  │
  └── 会话中 Agent 调用 memory 工具
        ↓
      add / replace / remove / read
        ↓
      安全扫描（拦截注入攻击、不可见字符）
        ↓
      原子写入文件（下次会话生效）
```

**安全机制**:
- 正则匹配注入攻击模式（`ignore previous instructions` 等）
- 检测不可见 Unicode 字符（零宽字符、方向控制符）
- 字符上限防止记忆膨胀

**关键代码**: `internal/tools/memorytool/memory.go`

---

### 2.2 跨会话搜索 — 借鉴 Hermes Agent

**设计来源**: Hermes Agent 的 `session_search_tool.py`，使用 SQLite FTS5 全文索引实现跨会话的消息搜索。

**核心思路**:
- 用户经常需要回顾「我们之前讨论过 X」「上次那个问题怎么解决的」
- 简单的 `LIKE '%keyword%'` 在大量消息下性能差
- FTS5 提供分词、排名、高效匹配

**架构**:

```
┌──────────────────────────────────┐
│        session_search 工具       │
│                                  │
│  query=""  ──→ 最近会话列表模式  │  ← 零成本
│  query="k" ──→ 全文搜索模式     │  ← FTS5 或 LIKE 降级
└──────────┬───────────────────────┘
           │
    ┌──────▼──────┐     ┌──────────────┐
    │  FTS5 索引  │ ──→ │ messages_fts │  ← SQLite 虚拟表
    └─────────────┘     └──────────────┘
           │
    自动触发器: INSERT/DELETE 同步
    启动时回填已有数据
```

**两种搜索模式**:

| 模式 | 触发条件 | 实现 | 成本 |
|------|---------|------|------|
| 最近会话 | `query` 为空 | `ListConversations` + 首条消息预览 | 零 LLM 成本 |
| 关键词搜索 | `query` 非空 | FTS5 MATCH → LIKE 降级 | 零 LLM 成本 |

**降级策略**:
- 优先尝试 FTS5（仅 SQLite 可用）
- FTS5 失败 → LIKE 模糊匹配
- MySQL/PostgreSQL → 直接走 LIKE

**关键代码**: `internal/tools/sessionsearch/session_search.go`, `internal/store/gormstore/fts.go`

---

### 2.3 任务规划与分解 — 借鉴 Cursor

**设计来源**: Cursor IDE 的 `TodoWrite` 工具，Agent 在执行前创建结构化任务列表，逐项推进。

**核心思路**:
- 复杂任务如果不拆解，Agent 容易遗漏步骤或迷失方向
- 任务列表需要「每轮可见」，不是一次性的
- 任务状态更新应该是轻量的（不需要重建整个列表）

**执行流程**:

```
用户消息 ──→ LLM 判断复杂度
                │
         ┌──────┴──────┐
         │             │
      简单任务      复杂任务（3+ 步骤）
         │             │
      直接执行     todo(create) 创建任务列表
                       │
                ┌──────▼──────┐
                │  执行循环    │◄──── 每轮刷新 system prompt 中的 todo
                │             │
                │  选 pending  │
                │  → in_progress
                │             │
                │  需要探索?   │
                │  ├── 是: sub_agent(mode=explore, model=fast)
                │  └── 否: 直接用工具执行
                │             │
                │  todo(update)│
                │  → completed│
                │             │
                │  还有 pending?
                │  ├── 是: 继续循环
                │  └── 否: 最终回复
                └─────────────┘
```

**per-round 刷新机制**:

```go
// run loop 中，每轮 LLM 调用前
if i > 0 && !ec.ephemeral {
    refreshTodoInSystemMessage(st.Messages, ec.conv.UUID)
}
```

这确保 Agent 在每轮思考时都看到最新的任务状态，即使中途通过 todo 工具修改了列表。

**数据结构**:
```go
type TodoItem struct {
    ID      string // 唯一标识
    Content string // 任务描述
    Status  string // pending / in_progress / completed / cancelled
}
```

**关键代码**: `internal/tools/todotool/todo.go`

---

### 2.4 子代理类型分化 — 借鉴 Cursor

**设计来源**: Cursor 的 `subagent_type` 参数，将子代理分为 `explore`（只读探索）、`shell`（命令执行）、`generalPurpose`（全功能）。

**核心思路**:
- 不同任务需要不同的工具集，给探索任务全部工具是浪费且有风险
- `explore` 模式是最常用的子代理场景（先看再改）
- 限制工具集可以减少 LLM 的决策空间，提高效率和安全性

**三种模式**:

| mode | 可用工具 | 约束 | 场景 |
|------|---------|------|------|
| `auto` | 全部（减去 blocklist） | 无特殊约束 | 通用任务 |
| `explore` | read, grep, find, ls, web_fetch, session_search, current_time | 只读，不可修改文件 | 代码探索、架构理解 |
| `shell` | exec, process, read, ls, current_time | 仅命令执行 | 构建、测试、部署 |

**实现方式 — 白名单反转为 blocklist**:

```go
// 不是硬编码白名单过滤，而是把「不在白名单中的工具」加入 blocklist
// 这样与现有 blocklist 机制完全兼容，无需修改 filterBlockedTools
var modeAllowedTools = map[string]map[string]bool{
    "explore": {"read": true, "grep": true, "find": true, ...},
    "shell":   {"exec": true, "process": true, "read": true, ...},
}
```

**关键代码**: `internal/agent/subagent.go` 中的 `mergeBlockedToolsWithMode`

---

### 2.5 子代理模型选择 — 借鉴 Cursor

**设计来源**: Cursor 的 `model: "fast"` 参数，允许为简单子任务选择更快/更便宜的模型。

**核心思路**:
- 探索类任务不需要最强模型，gpt-4o-mini 或 claude-3.5-haiku 足够
- 节省 token 成本，加速执行
- 选择权交给调用方（父 Agent），而非硬编码

**数据流**:

```
sub_agent({tasks: [{goal: "...", mode: "explore", model: "fast"}]})
    │
    ▼
executeOneTask()
    │
    ├── prepareSubAgent() ── 加载 Agent 配置
    │
    ├── model == "fast" && ag.FastModelName != "" ?
    │   ├── 是: ag.ModelName = ag.FastModelName
    │   └── 否: 保持原模型
    │
    └── run() ── 完整执行循环
```

**Agent 配置新增字段**:
```go
type Agent struct {
    // ...
    ModelName     string  // 主模型，如 gpt-4o, claude-3.5-sonnet
    FastModelName string  // 轻量模型，如 gpt-4o-mini, claude-3.5-haiku
}
```

---

### 2.6 单任务 sub_agent 缺陷修复

**问题**: 原实现中 `len(tasks)==1` 走 `inlineSubAgentCall`，这是一个**单次 LLM 补全**，子代理没有工具可用。

**影响**: 单任务子代理只能做「纯思考」，不能读文件、搜索代码、执行命令。这严重限制了 `sub_agent` 的实用性。

**修复**: 删除 `inlineSubAgentCall` 路径，所有任务统一走 `executeOneTask` → `prepareSubAgent` → `run` 的完整流程。

```
修复前:
  tasks=1 → inlineSubAgentCall (单次补全，无工具)
  tasks>1 → executeTaskBatch (并行，完整工具)

修复后:
  tasks=1 → executeTaskBatch (同步，完整工具)  ← 统一路径
  tasks>1 → executeTaskBatch (并行，完整工具)
```

---

### 2.7 定时任务调度器 — 借鉴 Hermes Agent

**设计来源**: Hermes Agent 的 `cron_tool.py`，内置 cron scheduler。

**核心思路**:
- 原实现写入系统 crontab，受限于 Unix 环境且无法触发 Agent 执行
- 新实现为 in-process 调度器，可以直接调用 Agent 执行引擎
- 任务和执行日志持久化，重启后自动恢复

**架构**:

```
┌───────────────────────────────────────────────┐
│              Scheduler (in-process)            │
│                                                │
│  robfig/cron ──→ 秒级 cron 表达式              │
│                                                │
│  ┌─────────────┐    ┌──────────────────┐       │
│  │ JobType:     │    │ 持久化:           │       │
│  │  prompt ──────────→ scheduler/jobs.json│      │
│  │  command     │    │                  │       │
│  └──────┬──────┘    └──────────────────┘       │
│         │                                      │
│  ┌──────▼──────────────────────────────┐       │
│  │ 执行回调:                            │       │
│  │  prompt → Executor.Execute(req)     │       │
│  │  command → exec.CommandContext()    │       │
│  └──────┬──────────────────────────────┘       │
│         │                                      │
│  ┌──────▼──────────────────────────────┐       │
│  │ 执行日志: scheduler/logs/{id}.jsonl │       │
│  └─────────────────────────────────────┘       │
└───────────────────────────────────────────────┘
```

**关键特性**:
- 6 字段 cron 表达式（含秒）+ 描述符（`@daily`, `@every 30m`）
- 两种任务类型：`prompt`（发送给 Agent）和 `command`（执行 Shell）
- `max_runs` 限制，达到后自动禁用
- 启用/禁用切换
- JSONL 格式执行日志

---

### 2.8 系统提示增强

在执行策略中注入任务分解指导：

```
## 执行策略

1. **工具优先**: 可通过工具获得更准确结果时，必须调用工具
2. **任务规划**: 复杂请求（3+ 步骤）先用 todo 创建任务列表，再逐项执行
3. **探索优先**: 不确定时先用 sub_agent(mode=explore) 并行探索
4. **并行利用**: 独立子任务用 sub_agent 的 tasks 数组并行执行
5. **组合调用**: 复杂问题可串联或并行调用多个工具
6. **结果驱动**: 基于工具返回的真实数据生成回答
```

---

## 三、System Prompt 组装流程

```
buildSystemPrompt()
│
├── 1. Agent.SystemPrompt 或默认提示
├── 2. ## 技能（如有）
├── 3. ## 执行策略（含任务分解指导）
│
buildMessages()
│
├── 4. 持久记忆快照（MEMORY.md + USER.md 冻结注入）
├── 5. ## 会话笔记（session-memory）
├── 6. ## 当前任务（todo 列表，每轮刷新）
│
├── 7. 历史消息
└── 8. 当前用户消息（含文件附件）
```

---

## 四、文件变更清单

### 新增文件

| 文件 | 来源 | 说明 |
|------|------|------|
| `internal/tools/memorytool/memory.go` | Hermes | 持久记忆工具：MEMORY.md + USER.md + 安全扫描 |
| `internal/tools/sessionsearch/session_search.go` | Hermes | 跨会话搜索工具：最近会话 + 关键词搜索 |
| `internal/store/gormstore/fts.go` | Hermes | FTS5 虚拟表 + 触发器 + 搜索实现 |
| `internal/scheduler/scheduler.go` | Hermes | in-process 定时任务调度器核心 |
| `internal/scheduler/handler.go` | Hermes | cron 工具 handler + context 注入 |
| `internal/tools/todotool/todo.go` | Cursor | 结构化任务规划工具 |

### 修改文件

| 文件 | 变更内容 |
|------|---------|
| `internal/tools/builtin_defs.go` | 新增 memory / session_search / cron / todo / sub_agent mode+model schema |
| `internal/tools/tools.go` | 注册 memory / todo handler，替换 cron handler |
| `internal/agent/subagent.go` | mode/model 字段、删除 inlineSubAgentCall、白名单 blocklist |
| `internal/agent/prompt.go` | 注入持久记忆 + todo 列表、任务分解执行策略 |
| `internal/agent/run.go` | 加载持久记忆 + todo、每轮刷新 todo |
| `internal/agent/session_memory.go` | loadPersistentMemory + loadTodoBlock |
| `internal/agent/executor.go` | 注入 session_search handler + todo store + scheduler context |
| `internal/model/agent.go` | Agent 新增 FastModelName 字段 |
| `internal/bootstrap/run.go` | 初始化 FTS5 + scheduler 启动/停止生命周期 |

---

## 五、设计原则

1. **渐进增强**: 所有新功能都是可选的，不配置则不影响现有行为
2. **降级友好**: FTS5 仅 SQLite 生效，其他 DB 自动降级为 LIKE；FastModelName 为空则保持原模型
3. **安全第一**: 持久记忆注入前做安全扫描；sub_agent 深度限制 + 工具 blocklist
4. **最小侵入**: 新功能通过 context 注入和工具注册实现，不改变核心执行循环结构
5. **可观测性**: scheduler 执行日志、sub_agent 结构化结果、todo 进度追踪
