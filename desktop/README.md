# AIClaw Desktop

这是 AIClaw 的 Wails 原生桌面应用模块。

```bash
# 仓库根目录
make dev
make test
make build
```

开发模式会启动原生桌面窗口，并用 Vite 提供前端热更新。生产构建输出到 `desktop/build/bin/`；应用业务方法通过 Wails 绑定直接调用 Go，不启动 HTTP API。

桌面端当前支持项目/会话管理、Provider 模型同步与增删、流式对话及重试、文件与图片附件、联网搜索、Computer Use、MCP、插件、本地记忆管理和亮暗主题。会话可以归属项目或保持未归属；左侧提供“全部会话”“未归属”和各项目三个层级的筛选。删除 Provider 下的模型只更新当前可用模型列表，不会删除或改写引用该模型的历史会话；删除当前选中模型后，界面会自动选择下一个可用模型。

输入框工具栏的“附件”支持原生多选和拖放。图片以多模态内容块发送；PDF、DOCX、XLSX、PPTX、文本和代码文件在本机解析后加入上下文。附件副本保存在 `~/.aiclaw/attachments/`，引用保存在 SQLite Rollout 中，因此重开会话和重试都能恢复。单次最多 10 个、单个最大 20MB。

本地验收可运行：

```bash
make test
make dev
```

在“设置 → 模型 Provider”展开“管理模型”，点击模型标签右侧的 `×` 验证删除；随后新建对话确认模型选择器已更新。可再输入“请记住，我的位置是上海”，新建另一会话询问位置，以验证本地记忆；Provider 输出应逐段显示而不是等待完整响应。

附件验收建议同时选择一张 PNG/JPEG 和一个 Markdown/PDF，在发送前检查缩略图与文件卡片，发送后重开会话确认附件仍显示，再点击“重试”确认模型请求仍包含原附件。也可把文件直接拖到输入框，拖入时输入框应显示高亮边框。

平台依赖请参考 Wails v2 的安装要求。macOS 需要较新的完整 Xcode SDK，Windows 安装器需要 NSIS，Linux 需要 GTK3 与 WebKitGTK。
