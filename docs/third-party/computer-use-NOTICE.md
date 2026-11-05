# Bundled Open Computer Use

AIClaw bundles the unmodified native runtime from `open-computer-use` 1.1.3:
https://github.com/iFurySt/open-computer-use

The exact npm archive and its SHA-512 integrity are recorded in package-lock.json.
Only the current platform's runtime is shipped, along with its LICENSE (MIT) and
THIRD_PARTY_NOTICES.md. The macOS app retains the upstream name, Bundle ID,
Developer ID signature and bundled resources. AIClaw communicates over MCP and
does not link the runtime into Electron.

Upstream notices include code derived from Cua Driver and yabai under MIT, and
libwebp under BSD-3-Clause. Those notices remain with the distributed runtime.
AIClaw no longer bundles the Cua Driver SDK or its MPL-covered UniFFI bindings.
