/** AIClaw-authored guidance, not instructions recovered from Codex or claims that
 * a particular UI version exposes a fixed accessibility selector.
 */
const common = [
  "Use exact app and window identities; resolve the intended control from a fresh query instead of guessing indexes or coordinates.",
  "Prefer verified semantic set_value and actions in strict background mode where the backend reports support. Use background keyboard or text only within the reported focused-field constraints; pointer and clipboard workflows require foreground mode.",
  "Group deterministic query/action steps with sequential await in exec. Stop on ambiguous/truncated matches or errors; capture at visual decisions and completion.",
  "An observation subscription reports changes, not action authority. Query or observe explicitly before input, and verify the business effect rather than event dispatch.",
];
const policies = [
  {
    id: "finder", apps: ["com.apple.finder"],
    instructions: [
      "For a known folder path use Finder's Go to Folder UI rather than clicking through ancestors. In foreground mode Command-Shift-G opens that dialog; observe the dialog before locating its path field.",
      "Set the observed editable path field, then re-observe before confirming. Verify the resulting folder identity, not merely that the dialog disappeared.",
      "Distinguish selected items from the folder being displayed. A file operation is complete only when the expected item and destination are verified; do not infer success from a click.",
    ],
    sources: ["https://support.apple.com/guide/mac-help/go-directly-to-a-specific-folder-on-mac-mchlp1236/mac", "https://support.apple.com/en-us/102650"],
  },
  {
    id: "safari", apps: ["com.apple.Safari", "com.apple.SafariTechnologyPreview"],
    instructions: [
      "Keep browser chrome (tabs and Smart Search field) distinct from page content. Do not treat a page's text field as the browser address field.",
      "For navigation to a user-specified URL, foreground Command-L selects the Smart Search field. Observe it, enter the intended URL, re-observe and confirm; verify the loaded address and page content.",
      "A page may replace its accessibility tree during navigation. Re-query after each navigation or modal change; do not reuse previous page indexes.",
    ],
    sources: ["https://support.apple.com/guide/safari/keyboard-and-other-shortcuts-cpsh003/mac"],
  },
  {
    id: "textedit", apps: ["com.apple.TextEdit"],
    instructions: [
      "Identify the intended document window before editing; several documents can belong to the same process.",
      "Query the actual document text area and use exposed settable attributes or verified text selection. A toolbar search field is not the document body.",
      "Choose text versus Markdown/HTML paste according to the requested formatting and current document mode. Plain-text documents cannot preserve rich formatting. Never silently replace an entire document when only an insertion or selection was requested.",
      "Verify the edited text and formatting, and separately verify save completion and destination if saving was requested. Closing a window may leave an unsaved-changes dialog.",
    ],
    sources: ["https://support.apple.com/guide/textedit/format-with-fonts-and-styles-txte2be20f33/mac"],
  },
  {
    id: "chrome", apps: ["com.google.Chrome"],
    instructions: [
      "Keep the browser address bar, browser search, page search and page form fields distinct. Use a fresh observation of the intended browser window and profile before navigation.",
      "For an explicitly requested URL, foreground Command-L selects the address bar on macOS. Observe the resulting field, enter the URL, and verify the loaded address and intended page after navigation.",
      "Re-query after tab switches, navigation, dialogs and page updates. Do not apply old page element indexes to a newly loaded document or another tab.",
    ],
    sources: ["https://support.google.com/chrome/answer/157179", "https://support.google.com/chrome/a/answer/7591084"],
  },
  {
    // Confirmed from the installed Microsoft Edge Info.plist.
    id: "edge", apps: ["com.microsoft.edgemac"],
    instructions: [
      "Identify the intended Edge window, profile and tab before interacting. Distinguish browser chrome from page content and side-panel controls.",
      "For user-requested navigation on macOS, foreground Command-L selects the address bar. Observe and verify the destination after confirming it; address-bar suggestions are not proof that navigation completed.",
      "Re-query controls after navigation, tab changes or dialogs. Verify a form's intended effect before considering submission complete; never automatically replay an uncertain submission.",
    ],
    sources: ["https://support.microsoft.com/en-us/edge/keyboard-shortcuts-in-microsoft-edge"],
  },
  {
    id: "vscode", apps: ["com.microsoft.VSCode", "com.visualstudio.code.oss"],
    instructions: [
      "Distinguish the document editor from terminal, search, command palette and chat inputs. Confirm the intended workspace, file and editor group before editing.",
      "The macOS default Command-Shift-P opens the Command Palette, but user keybindings may differ. Observe the opened UI before selecting a command; palette search results do not authorize running shell commands or changing files.",
      "For a local edit, select the requested text or range and preserve surrounding content. Verify the edited text and file identity before saving. Command-S requests save by default; separately verify save completion and any destination dialog.",
      "If editor text is not exposed through accessibility, report the limitation rather than replacing the whole document or typing into whichever pane currently has focus.",
    ],
    sources: ["https://code.visualstudio.com/docs/configure/keybindings", "https://code.visualstudio.com/docs/editing/codebasics"],
  },
  {
    // Confirmed from the installed 企业微信.app Info.plist; labels and send
    // shortcuts are deliberately not assumed from a particular client build.
    id: "wecom", apps: ["com.tencent.WeWorkMac"],
    instructions: [
      "Treat global search results, contact details, the selected conversation and its message composer as different contexts. Re-observe after selecting a result and verify the conversation header and recipient identity.",
      "A matching display name or the first search result does not uniquely identify a recipient. Stop for ambiguity rather than opening a different person's conversation or guessing a control index.",
      "When drafting is requested, place text only in the observed message composer and leave it unsent. Do not use Enter as a harmless navigation key in a composer; send-key preferences can differ.",
      "Send only when the user explicitly requested sending, the recipient and text are verified, and normal action approval permits it. An uncertain send result must be inspected, never automatically retried.",
    ],
    sources: ["https://work.weixin.qq.com/help"],
  },
];

export function computerAppPolicy(app: string, platform: NodeJS.Platform = process.platform) {
  // Windows/Linux targets are process IDs. A PID or title does not establish
  // a trustworthy application identity, so never select a bundle-ID policy.
  const policy = platform === "darwin" ? policies.find(policy => policy.apps.includes(app)) : undefined;
  const supported = ["darwin", "win32", "linux"].includes(platform);
  const backend = platform === "darwin" ? "AX" : platform === "win32" ? "UIA and verified standard Win32 controls" : platform === "linux" ? "AT-SPI on X11" : "unavailable";
  return {
    app, platform, policy_id: policy?.id ?? "generic", version: 2, source: "AIClaw curated strategy", guidance_only: true,
    identity_match: policy ? "exact_bundle_id" : "none",
    selector_policy: "Resolve controls from current observations; no hard-coded indexes or locale-specific labels.",
    instructions: [...common, ...(policy?.instructions ?? ["No app-specific strategy is registered. Inspect available roles, actions and settable attributes before selecting a workflow."])],
    sources: policy?.sources ?? [],
    background_backend: backend,
    background_actions: supported ? [platform === "darwin" ? "single unmodified AXPress click" : "single unmodified semantic click", "set_value without autosubmit", "select_text", "exposed secondary action", ...(platform === "darwin" ? ["unmodified or Shift-only keys with current observed focused-field proof", "Cocoa PID text input and literal web text insertion into a plain field or empty textarea"] : [])] : [],
    background_constraints: [
      platform === "win32" ? "Background set_value and selection require a verified standard Edit control; background click fallback requires a verified standard Button. Other controls may be refused." : `Controls must expose the required ${backend} semantic action or writable attribute.`,
      "Unsupported background requests are rejected before global input, with no foreground fallback. Target apps may change focus themselves; changed foreground, pointer or clipboard makes the outcome unknown and must not trigger replay.",
      ...(platform === "darwin" ? ["Background keys and text require screenshot-bound proof of the exact focused editable element, window and process generation. Cmd/Ctrl/Alt shortcuts and nonempty rich web editors are refused; key delivery alone does not prove text was consumed."] : []),
      ...(platform === "linux" ? ["Wayland is not supported by the X11 backend."] : []),
    ],
    unsupported_background: ["coordinate click", ...(platform === "darwin" ? ["Cmd/Ctrl/Alt shortcuts", "nonempty rich web editor text"] : ["keyboard", "text input"]), "paste", "scroll", "drag", ...(!supported ? ["semantic click", "set_value", "select_text", "secondary action"] : [])],
  };
}
