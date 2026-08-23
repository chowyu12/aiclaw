# AIClaw Desktop

This directory contains AIClaw's native Wails desktop application.

```bash
# Run from the repository root
make dev
make test
make build
```

Development mode starts a native desktop window and uses Vite for frontend hot reload. Production builds are written to `desktop/build/bin/`. Application methods call Go directly through Wails bindings; the desktop application does not start an HTTP API.

The desktop application currently supports project and conversation management, provider model synchronization and editing, streaming chat and retries, Markdown and sanitized HTML response rendering, file and image attachments, web search, Computer Use, MCP, plugins, local memory management, and dark/light themes. Conversations may belong to a project or remain unassigned. The sidebar provides three levels of filtering: **All Conversations**, **Unassigned**, and individual projects. Removing a model from a provider updates only the current available-model list; it does not delete or rewrite historical conversations that reference that model. When the selected model is removed, the UI automatically selects the next available model.

The **Execution** panel above model responses shows safe, verifiable stage information, including analysis, response generation, and each tool's lifecycle. Tool requests and results come from the SQLite rollout and remain traceable after reopening a historical conversation. The UI does not expose the model's private, token-by-token chain of thought.

The composer toolbar supports native multi-file selection and drag-and-drop attachments. Images are sent as multimodal content blocks. PDF, DOCX, XLSX, PPTX, text, and source-code files are parsed locally before being added to context. Attachment copies are stored under `~/.aiclaw/attachments/`, and their references are stored in the SQLite rollout so they can be restored when reopening or retrying a conversation. Each request supports up to 10 attachments, with a maximum size of 20 MB per file.

Run the following commands for local acceptance testing:

```bash
make test
make dev
```

Under **Settings → Model Providers**, expand **Manage Models** and click the `×` next to a model chip to verify removal. Then create a conversation and confirm that the model picker has been updated. To test local memory, enter “Remember that my location is Shanghai,” create another conversation, and ask for your location. Provider output should appear incrementally instead of waiting for the complete response.

For attachment testing, select a PNG/JPEG image together with a Markdown/PDF document. Verify the thumbnail and file card before sending, reopen the conversation after sending to confirm that the attachments are restored, and click **Retry** to confirm that the original attachments are included again. You can also drag files directly into the composer; its border should highlight while files are being dragged over it.

See the Wails v2 installation requirements for platform dependencies. macOS requires a recent full Xcode SDK, Windows installers require NSIS, and Linux requires GTK3 and WebKitGTK.
