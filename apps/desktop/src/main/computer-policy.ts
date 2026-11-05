/** AIClaw-authored guidance, not instructions recovered from Codex or claims that
 * a particular UI version exposes a fixed accessibility selector.
 */
const common = [
  "Use exact app and window identities; resolve the intended control from a fresh query instead of guessing indexes or coordinates.",
  "Prefer verified AX set_value and semantic actions in strict background mode where supported. Keyboard, pointer and clipboard workflows require foreground mode.",
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
];

export function computerAppPolicy(app: string) {
  const policy = policies.find(policy => policy.apps.includes(app));
  return {
    app, policy_id: policy?.id ?? "generic", version: 1, source: "AIClaw curated strategy", guidance_only: true,
    selector_policy: "Resolve controls from current observations; no hard-coded indexes or locale-specific labels.",
    instructions: [...common, ...(policy?.instructions ?? ["No app-specific strategy is registered. Inspect available roles, actions and settable attributes before selecting a workflow."])],
    sources: policy?.sources ?? [],
    background_actions: ["single unmodified AXPress click", "set_value without autosubmit", "select_text", "exposed secondary action"],
    unsupported_background: ["coordinate click", "keyboard", "paste", "scroll", "drag"],
  };
}
