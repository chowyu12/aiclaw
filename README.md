# AIClaw

AIClaw is a local-first native desktop AI application. It uses Wails to provide native windows for macOS, Windows, and Linux. All projects, conversations, model settings, and plugin configurations are stored in local SQLite. The application does not start an HTTP service and is not a command-line chat program.

## Current Capabilities

- Manage projects and conversations from a unified sidebar. A conversation may belong to a project or remain unassigned, and it can be moved between those states at any time.
- Switch between dark and light themes with one click, with the last selected theme persisted locally.
- Select a provider and model directly in each conversation without creating or maintaining agents.
- Synchronize model lists from provider APIs, search and add candidate models, or add and remove model names manually. Removing a model configuration does not delete historical conversations.
- Stream incremental provider responses in real time and retry the most recent model response.
- Render model responses as GFM Markdown and sanitized HTML, including headings, lists, tables, blockquotes, code blocks, links, and images. HTML is sanitized against an allowlist before display.
- Show a collapsible execution panel above each response with context analysis, response generation, and tool pending/running/success/failure states. Tool steps are stored in the rollout and restored with conversation history. The UI does not display or fabricate the model's private, token-by-token chain of thought.
- Add or drag local files and images into the composer, similar to Codex. Attachments can be previewed and removed before sending, restored from conversation history afterward, and retained when retrying.
- Send JPEG, PNG, WebP, and GIF files as native multimodal image blocks. Extract PDF, DOCX, XLSX, PPTX, text, source code, and common configuration-file content locally before adding it to model context.
- Use a fully local memory system stored in SQLite, with cross-conversation retrieval, explicit memories, candidate review, approval, and forgetting. Memory use and memory generation can be disabled independently.
- Enable web search by default for new conversations and manage search services under **Settings → Web Search**.
- Manage providers, Computer Use, MCP, and plugins from Settings.
- Use the built-in `browser` Computer Use tool with models that support tool calling.
- Discover, copy, install, enable, and disable local plugin directories.
- Load `SKILL.md` instructions, JavaScript/Python tools, and MCP servers from plugins.
- Store conversations, messages, projects, and configuration in `~/.aiclaw/aiclaw.db`.
- Build universal macOS applications, Windows applications/installers, and Linux packages through GitHub Actions when a version tag is published, including SHA-256 checksum files.

## Run the Desktop Application from Source

You need Go, Node.js, Wails v2.11.0, and the desktop build dependencies for your platform.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
make dev
```

`make dev` starts a native Wails development window. It is not a browser application; the Vite URL is used only for frontend hot reload inside Wails.

Build the production application:

```bash
make test
make build
```

On macOS, the application is usually written to `desktop/build/bin/AIClaw.app`. macOS builds require a complete, recent Xcode SDK. Old standalone Command Line Tools may not link the system frameworks required by Wails.

## First-Time Setup

1. Open **Settings → Model Providers**, add an API endpoint and key, then synchronize and search for models or add model names manually. Click the `×` next to an added model to remove it from the available-model list.
2. To use external web search, add and enable a search service under **Settings → Web Search**.
3. Return to the conversation and click the model button above the composer to select a provider and model.
4. New conversations are unassigned by default. Open a project in the sidebar before creating a conversation to assign it to that project, or change an existing conversation's assignment later. **Unassigned** in the sidebar contains only standalone conversations.
5. Click **Attach** in the composer toolbar or drag files directly into the composer. You can send attachments by themselves or add instructions describing what AIClaw should do with them.

AIClaw does not bundle or host model credentials. API keys are stored in the local database.

### Model Removal and Conversation History

The provider model list represents the models currently available for selection. Removing a model only removes it from this list. Saved projects, conversations, messages, and the model names recorded with historical conversations remain intact. If you remove the currently selected model, AIClaw selects the next available model from the same provider. If no models remain, add one before sending another message.

### Local Memory and Streaming Responses

Under **Settings → Local Memory**, you can independently control whether new conversations use memories and whether conversations may generate memories. You can also review or forget existing entries. An explicit request such as “Remember that my location is Shanghai” is saved directly as a local memory, and relevant memories are injected into future conversations when needed. Memories and review records are written only to `~/.aiclaw/aiclaw.db`.

Provider responses are displayed incrementally over a streaming connection. **Retry** under the latest model response starts a new request from the corresponding user message and replaces the response using the same streaming flow.

### Files and Images

Attachments are copied into AIClaw's private local directory before being associated with the conversation rollout. Remote providers never receive the original local path. Each request supports up to 10 attachments, with a maximum size of 20 MB per file.

- **Images:** JPEG, PNG, WebP, and GIF. Image understanding depends on whether the selected model supports vision input.
- **Documents:** PDF, DOCX, XLSX, and PPTX.
- **Text:** Markdown, JSON/JSONL, CSV/TSV, XML/YAML, and common source-code, script, and configuration formats.
- **Safe fallback:** Document content is parsed locally and capped before being injected. Unsupported binary files are rejected before sending instead of being passed to the model as unreadable text.

Removing an attachment before sending also deletes its staged copy. Sent attachments belong to conversation history and cannot be silently removed from an individual message. Unsent staged attachments older than 24 hours are cleaned automatically when the application starts.

## Plugin Format

Choose a local directory under **Settings → Plugins** to install it. AIClaw recognizes the following structure:

```text
example-plugin/
  .codex-plugin/plugin.json   # or plugin.json in the plugin root
  skills/
    research/
      SKILL.md
      manifest.json           # may declare a JS/Python tool entry point
      main.py                 # or a JavaScript entry point
  mcp.json                    # .mcp.json is also supported
```

The plugin manifest must provide at least a name:

```json
{
  "name": "Research Kit",
  "description": "Local research helpers",
  "version": "1.0.0"
}
```

MCP files use the common `mcpServers` structure. They may configure a local `command`/`args`/`env` combination or a remote `url`/`headers` combination. Disabling a plugin also disables its associated skills and MCP servers.

## Local Data

```text
~/.aiclaw/
  aiclaw.db
  attachments/
  plugins/
  logs/
```

Deleting a project archives its conversations instead of physically deleting their content from the database.

Project assignment is optional conversation-grouping metadata. It does not affect the provider, model, messages, or local memories. Moving a conversation from a project to **Unassigned** only clears its project UUID; it does not archive or delete the conversation.

A provider cannot be deleted while a conversation still references it. This restriction does not apply when removing an individual model from a provider because doing so does not alter historical conversation records.

## Release Application Packages

Pushing a `v*` tag triggers `.github/workflows/release.yml` and produces:

- `AIClaw-macos-universal.zip`
- `AIClaw-windows-amd64.zip`
- `AIClaw-linux-amd64.tar.gz`
- `SHA256SUMS.txt`

The workflow runs Go, desktop bridge, and frontend tests before building all three platforms in parallel and uploading the artifacts to the corresponding GitHub Release. Manually running the workflow performs validation and generates Actions artifacts; only a version-tag run creates a Release.

CI writes tags such as `v1.2.3` into application metadata for each platform and performs ad-hoc signing and integrity checks for the macOS bundle. For public distribution, configuring an Apple Developer ID, notarization, and a Windows code-signing certificate is still recommended.

## Technical Structure

```text
desktop/                 Native Wails window, Go bindings, and Vue frontend
internal/core/           Local conversations, model sampling, and tool dispatch
internal/store/gormstore SQLite persistence
internal/tools/          File, command, browser, and web tools
internal/skills/         Skill parsing and JS/Python execution
internal/mcp/            MCP client and tool bridge
```

The desktop conversation path is `Wails UI → Go desktop bridge → local core session → Provider/MCP/Skill tools → SQLite rollout`. The attachment path is `original file → private local copy and SQLite metadata → rollout attachment reference → provider multimodal/text content block`. Neither path depends on a web console or a local HTTP API at runtime.
