# AIClaw

AIClaw 是一个 local-first 的原生桌面 AI 应用。它使用 Wails 提供 macOS、Windows 和 Linux 桌面窗口，所有项目、会话、模型设置和插件配置都保存在本机 SQLite 中；应用不启动 HTTP 服务，也不是命令行聊天程序。

## 当前能力

- 左侧统一管理项目与会话；会话既可归属某个项目，也可保持未归属，并可随时在两种状态间移动。
- 支持深色与亮色主题一键切换，并持久保存上次使用的主题。
- 每个对话直接选择 Provider 和模型，不再需要创建或维护 Agent。
- Provider 可从远程接口同步模型列表、搜索候选模型并添加，也可手动添加或删除模型名称；删除模型配置不会删除历史会话。
- 对话使用 Provider 的流式接口实时显示增量内容，并支持对最后一次模型输出进行重试。
- 支持像 Codex 一样在输入框添加或拖入本地文件和图片：发送前可预览、移除，发送后会随会话历史恢复，重试时也会保留原附件。
- JPEG、PNG、WebP、GIF 以原生多模态图像块发送给模型；PDF、DOCX、XLSX、PPTX、文本、代码与常见配置文件在本机提取内容后加入模型上下文。
- 内置完全保存在 SQLite 中的本地记忆：支持跨会话检索、明确记忆、候选审核、批准和遗忘，并可分别关闭记忆使用与生成。
- 联网搜索在新会话中默认开启；搜索服务在“设置 → 联网搜索”中管理。
- Provider、Computer Use、MCP 和插件统一放在设置中。
- 内置 `browser` Computer Use 工具，可由支持工具调用的模型直接控制浏览器。
- 支持本地插件目录的发现、复制安装、启用和停用。
- 插件可提供 `SKILL.md` 指令、JavaScript/Python 工具和 MCP Server。
- 会话、消息、项目与配置保存在 `~/.aiclaw/aiclaw.db`。
- GitHub Actions 在版本标签发布时构建 macOS 通用应用、Windows 应用/安装器和 Linux 应用包，并生成 SHA-256 校验文件。

## 从源码运行桌面应用

需要 Go、Node.js、Wails v2.11.0，以及对应平台的桌面构建依赖。

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
make dev
```

`make dev` 会启动原生 Wails 开发窗口。它不是浏览器页面；Vite 地址只服务于 Wails 的前端热更新。

构建正式应用：

```bash
make test
make build
```

macOS 产物通常位于 `desktop/build/bin/AIClaw.app`。macOS 构建需要完整、较新的 Xcode SDK；仅有过旧的 Command Line Tools 可能无法链接 Wails 所需的系统框架。

## 首次使用

1. 打开“设置 → 模型 Provider”，添加 API 地址和密钥；随后同步并搜索选择模型，或手动添加模型名称。点击已添加模型右侧的 `×` 可将其从可用模型中删除。
2. 如需外部联网搜索，在“设置 → 联网搜索”中添加并启用搜索服务。
3. 返回对话，在输入框上方点击模型按钮选择 Provider 与模型。
4. 新建对话时默认不归属项目；可从左侧项目进入后新建归属该项目的对话，已有会话也可调整归属。左侧“未归属”只显示独立会话。
5. 点击输入框工具栏的“附件”选择文件，或把文件直接拖到输入框。可以只发送附件，也可以同时写明希望 AIClaw 执行的任务。

AIClaw 不内置或托管模型凭据。API 密钥保存在本地数据库中。

### 模型删除与历史会话

模型列表是 Provider 的“当前可用模型”配置。删除某个模型时，AIClaw 只会把它从该列表移除：已经保存的项目、会话、消息和当时使用的模型名称都会保留。若删除的是当前选中的模型，应用会自动选择同一 Provider 的下一个可用模型；如果没有其他模型，需要先添加模型才能继续发送消息。

### 本地记忆与流式回复

“设置 → 本地记忆”可分别控制新对话是否使用记忆、是否允许对话生成记忆，并可审核或遗忘已有条目。明确输入“请记住……”会直接保存为本地记忆；相关记忆会在后续会话中按需注入。记忆与审核记录只写入 `~/.aiclaw/aiclaw.db`。

Provider 回复通过流式连接逐段显示。最后一条模型回复下方的“重试”会从对应用户消息重新请求，并以同样的流式方式替换原回复。

### 文件与图片

附件会先复制到 AIClaw 的本地私有目录，再与对应会话的 Rollout 记录关联；远端 Provider 不会收到原始本地路径。单次最多添加 10 个附件，单个文件最大 20MB。

- 图片：JPEG、PNG、WebP、GIF。图片理解能力取决于当前选择的模型是否支持视觉输入。
- 文档：PDF、DOCX、XLSX、PPTX。
- 文本：Markdown、JSON/JSONL、CSV/TSV、XML/YAML，以及常见代码、脚本和配置文件。
- 安全降级：文档内容在本机解析并限制注入大小；不支持的二进制文件会在发送前被拒绝，不会作为乱码传给模型。

发送前移除附件会同时删除暂存副本；已发送附件属于会话历史，不能从单条消息中静默删除。未发送且超过 24 小时的暂存附件会在应用启动时自动清理。

## 插件格式

从“设置 → 插件”选择本地目录安装。AIClaw 识别以下内容：

```text
example-plugin/
  .codex-plugin/plugin.json   # 或根目录 plugin.json
  skills/
    research/
      SKILL.md
      manifest.json           # 可声明 JS/Python 工具入口
      main.py                 # 或 JavaScript 入口
  mcp.json                    # 也支持 .mcp.json
```

插件清单至少应提供名称：

```json
{
  "name": "Research Kit",
  "description": "Local research helpers",
  "version": "1.0.0"
}
```

MCP 文件使用常见的 `mcpServers` 结构，可配置本地 `command`/`args`/`env`，或远程 `url`/`headers`。停用插件时，其关联的 Skill 和 MCP Server 会同时停用。

## 本地数据

```text
~/.aiclaw/
  aiclaw.db
  attachments/
  plugins/
  logs/
```

删除项目会归档该项目下的会话，不会把对话内容直接从数据库中物理删除。

项目归属是可选的会话分组信息，不影响 Provider、模型、消息或本地记忆。会话从项目移动到“未归属”时只会清空其项目 UUID，不会归档或删除会话。

删除 Provider 仍会在它被会话引用时受到保护；删除 Provider 下的单个模型不受此限制，因为它不会改变历史会话记录。

## 发布应用包

推送 `v*` 标签会触发 `.github/workflows/release.yml`：

- `AIClaw-macos-universal.zip`
- `AIClaw-windows-amd64.zip`
- `AIClaw-linux-amd64.tar.gz`
- `SHA256SUMS.txt`

工作流先运行 Go、桌面桥接和前端测试，再并行构建三个平台，最后把产物上传到对应的 GitHub Release。手动运行 workflow 只验证并生成 Actions artifacts；只有版本标签运行会创建 Release。

CI 会把形如 `v1.2.3` 的标签写入各平台应用元数据，并对 macOS 包执行 ad-hoc 签名与完整性校验。面向公开分发时，仍建议在仓库中配置 Apple Developer ID、公证和 Windows 代码签名证书。

## 技术结构

```text
desktop/                 Wails 原生窗口、Go 绑定与 Vue 前端
internal/core/           本地会话、模型采样与工具调度
internal/store/gormstore SQLite 持久化
internal/tools/          文件、命令、浏览器与联网工具
internal/skills/         Skill 解析和 JS/Python 执行
internal/mcp/            MCP 客户端与工具桥接
```

桌面对话运行链路为：`Wails UI → Go desktop bridge → local core session → Provider/MCP/Skill tools → SQLite rollout`。附件链路为：`原始文件 → 本地私有副本与 SQLite 元数据 → Rollout 附件引用 → Provider 多模态/文本内容块`。运行过程中不依赖网页控制台或本地 HTTP API。
