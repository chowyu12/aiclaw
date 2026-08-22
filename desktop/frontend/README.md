# AIClaw Desktop Frontend

Vue 3 + TypeScript + Vite 前端，通过 Wails 生成的绑定直接调用本地 Go 方法。

```bash
npm ci
npm run build
```

附件交互位于对话输入框：支持原生多选、Wails 文件拖放、图片缩略图、文档卡片、发送前移除，以及历史会话恢复。前端只保存附件 UUID；文件复制、格式识别、大小限制、文本提取和 SQLite 关联均由 Go 桥接负责。

修改 Go 绑定方法或返回结构后，在 `desktop/` 目录运行：

```bash
wails generate module
```

不要手动编辑 `wailsjs/go/` 中的生成文件。
