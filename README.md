# AIClaw

**English** · [简体中文](README_CN.md)

A local-first AI agent desktop app. The Electron host talks to a Go kernel (`claw-agent`)
over JSON-RPC on stdio. Model providers, plugins, search engines and chats all stay on your
machine, and the app runs no HTTP server. The interface is available in English and
Simplified Chinese. Switch it at the top of the **Settings** page; new installs start in English.

## What it does

- **Chat and act.** The model reads and writes files, runs commands and calls MCP tools on your
  machine.
  - It asks before running commands or calling external tools. Stricter and unattended
    approval modes are available, and dangerous commands are refused outright.
  - Context is compacted before it fills up.
  - The microphone button in the composer does voice input: speech is transcribed into the box
    so you can review it before sending.
- **Sub-agents** (modeled on Codex's multi_agents_v2).
  - Ask to "use sub-agents to do … in parallel" and the model delegates bounded side tasks while
    it keeps working on the critical path.
  - Results come back automatically for it to integrate.
  - Child chats nest under their parent in the sidebar, collapsed by default.
  - See `docs/design/multi-agent.md`.
- **Chat references** (modeled on Codex's task mentions). Type `@` in the composer to pick
  another chat; the model reads it with `read_thread` before answering. See
  `docs/design/thread-references.md`.
- **Chat management.** Groups, collapsing and search. Archived chats go to
  **Settings → Archived**, where you can restore them.
- **Scheduled tasks.**
  - Daily, weekdays, chosen weekdays, every N minutes/hours, or once.
  - When one comes due, AIClaw opens a new chat, runs it, and sends a system notification.
  - You can also just say "every weekday at 9am, summarize my unread email".
- **Office files.** Read Word, Excel and PowerPoint (docx/xlsx/pptx) and generate all three.
  The model can also make exact text replacements in a Word document and update individual
  sheets in an existing workbook.
- **Model providers.** Multiple OpenAI-compatible endpoints: OpenAI, Qwen, Kimi, OpenRouter,
  Claude/Gemini compatible endpoints, or self-hosted.
  - Each provider has its own key and model list, and you can switch per chat.
  - Keys are encrypted in the local database (AES-256-GCM; the key file `secret.key` sits next
    to it with mode 0600).
  - Keys never cross the protocol or reach logs, and the sandbox blocks reading both files.
- **Plugins.** Bundled with the app:
  - **Email**: list, read, save attachments, send and reply from your mailbox. Every send
    asks first. Company-domain mailboxes find their servers automatically via DNS.
  - **Computer use**: screenshots plus mouse and keyboard; enabling it grants the permission.
  - **WeChat**: QR login, multiple accounts.
  - **WeCom**: AI bot, multiple bots.

  You can also install your own plugins from a folder (skills, MCP servers).
  - Messages from external chats wait for your approval.
  - Once approved, they run in a restricted chat with read-only tools, grouped under the
    sidebar's collapsed **Channel chats** group.
- **Web search.** Tavily, SerpAPI or Alibaba Cloud IQS; enabling one gives the model a
  `web_search` tool.
- **Browser.**
  - The model gets a numbered list of interactive elements and navigates, clicks, types,
    selects, scrolls, extracts text and takes screenshots by index. This is browser-use-style
    DOM indexing on Electron's own Chromium, with no Playwright install.
  - Use the built-in window, or install the **AIClaw Browser Helper** extension to work in
    background tabs of your own Chrome/Edge with your existing logins.
  - Opening a URL asks first.
  - See `docs/design/browser.md`.
- **Multimodal.** Four jobs beyond chat each go to a model of your choice:
  - vision (reads images for chat models that can't),
  - speech-to-text,
  - text-to-speech,
  - image generation.

  To set them up:
  - Tag model capabilities on the **Providers** page, then pick a model per role in
    **Settings**.
  - A role you leave unset simply has no tool.
  - Capabilities can be auto-tagged from [models.dev](https://models.dev).
  - DashScope's compatible mode only does chat, so the kernel switches those three jobs to its
    native APIs.
- **Skills.**
  - The skills directory is `~/.agents/skills`. That's where `npx skills add -g` installs, and
    Codex, Cursor and others read it too.
  - `SKILL.md` skills from Claude Code, Codex, global npm packages and plugins are picked up as
    well.
  - Commands the model runs use your terminal's PATH, so CLIs installed via nvm or Homebrew
    are found.
- **Usage.** **Settings → Usage** shows tokens, model calls, tools and skills by day.

## Install

**macOS, one command.** It downloads the latest release, installs it into Applications,
removes the quarantine flag and opens the app:

```bash
curl -fsSL https://raw.githubusercontent.com/chowyu12/aiclaw/master/scripts/install-mac.sh | bash
```

Works on Apple Silicon and Intel. If you already downloaded the zip, `install-mac.sh -l`
installs the newest one in `~/Downloads`. The script does four things:
1. finds the package,
2. quits the running app,
3. replaces the `.app`,
4. runs `xattr -dr com.apple.quarantine`.

The package is unsigned, so Gatekeeper refuses it without that last step.

**Windows / Linux.** Download `AIClaw-<version>-win-x64.zip` or
`AIClaw-<version>-linux-x64.zip` from [Releases](https://github.com/chowyu12/aiclaw/releases),
unzip anywhere, and run it.

The app checks for updates itself. On macOS it replaces and relaunches in one click; on other
platforms it downloads to your Downloads folder and points you to it.

## Layout

```text
apps/desktop/           Electron host + Vue UI (shared/locales holds the English dictionary)
apps/browser-extension/ AIClaw Browser Helper extension
packages/agent-client/  TypeScript client for the kernel
tools/claw-agent/       Go kernel: loop / tools / MCP / skills / memory / chat store / sub-agents / JSON-RPC
internal/plugin         Plugin system (bundles, manifests, permissions, config, channels)
internal/plugins        Built-in plugins: email, computer use, WeChat, WeCom
internal/store          App database (SQLite, gorm): providers, plugins, search engines, channel bindings
scripts/                Smoke tests, packaging, icons, one-line installer
docs/                   Design docs; read docs/agent-loop.md before touching the loop
```

## Development

Requires Go 1.27+ and Node 22+.

```bash
make dev       # build and launch the app
make check     # full pre-merge check: format, vet, tests, types, both smoke suites
make scenarios # pre-release scenarios against a real model: chat / files / commands / approvals / vision / images / TTS / STT / search / concurrency
make help      # everything else
```

`make check` never calls a model and runs on every change. `make scenarios` uses the default
model and multimodal roles from Settings to do what a user would; it costs money and is slow,
so run it before releases only.

UI strings are written as the Chinese source text, e.g. `t("新建对话")`, and translated in
`apps/desktop/src/shared/locales/en/`. `scripts/test-i18n.ts` fails if any `t("…")` lacks an
English entry. The kernel follows the same convention: `i18n.D("…")` / `i18n.E("…")` from
`internal/i18n`, with English in `internal/i18n/en_*.go`. Text the model reads (system prompt,
tool descriptions) is written in English directly.

Data lives in several places:
- `~/.aiclaw/`: `aiclaw.db` app database, `plugins/`, and the global long-term memory
  `memory.md`.
- `~/.agents/skills`: skills.
- Electron's userData directory: the chat database and UI settings.

Memory has two layers:
- Things unrelated to a project go to the global `memory.md`.
- A project's conventions and lessons go to `<workspace>/.aiclaw/memory.md`, which travels
  with the repo. The `remember` tool writes there by default when a workspace is set.

## Packaging and releases

`make package` uses `@electron/packager` to build all four packages (macOS arm64/x64,
Windows x64, Linux x64) on one machine; the kernel cross-compiles with `CGO_ENABLED=0`.

Pushing a `v*` tag triggers GitHub Actions:
1. tests,
2. packaging,
3. creating the Release. The tag annotation becomes the release notes, and SHA256SUMS is
   attached.

macOS packages are unsigned:
- If you install with the script above, there's nothing to do.
- If you unzip by hand, right-click → Open the first time, or run
  `xattr -dr com.apple.quarantine /Applications/AIClaw.app`.
- `make install-mac` builds locally and installs into Applications.
