import AppKit
import CoreGraphics

private func require(_ condition: Bool, _ message: String) throws {
    if !condition { throw ComputerError("Test failed: \(message)") }
}

private func expectRange(_ range: CFRange, _ location: Int, _ length: Int, _ message: String) throws {
    try require(range.location == location && range.length == length, message)
}

private func expectFailure(_ label: String, _ operation: () throws -> Void) throws {
    do { try operation() }
    catch is ComputerError { return }
    throw ComputerError("Test failed: \(label) was accepted")
}

@main
struct ActionsTests {
    @MainActor
    static func main() throws {
        let emoji = "👩🏽‍💻", combined = "e\u{301}"
        let text = "前\(emoji)\(combined)后"
        try expectRange(actionSelectionRange(text: text, target: emoji), 1, (emoji as NSString).length, "emoji uses UTF-16 range")
        try expectRange(actionSelectionRange(text: text, target: combined), 1 + (emoji as NSString).length, 2, "combining sequence retains both UTF-16 units")
        try expectRange(actionSelectionRange(text: text, target: emoji, selection: "cursorBefore"), 1, 0, "cursor before emoji")
        try expectRange(actionSelectionRange(text: text, target: emoji, selection: "cursorAfter"), 1 + (emoji as NSString).length, 0, "cursor after emoji")
        try expectRange(actionSelectionRange(text: "甲发送乙 丙发送丁", target: "发送", prefix: "丙", suffix: "丁"), 6, 2, "context identifies second repeated target")
        try expectRange(actionSelectionRange(text: "aaab", target: "aa", suffix: "b"), 1, 2, "context identifies overlapping occurrence")
        try expectRange(actionSelectionRange(text: "text", target: "text", prefix: "", suffix: ""), 0, 4, "empty context and string boundaries")
        try expectFailure("repeated target") { _ = try actionSelectionRange(text: "发送 发送", target: "发送") }
        try expectFailure("overlapping target") { _ = try actionSelectionRange(text: "aaa", target: "aa") }
        try expectFailure("unmatched text") { _ = try actionSelectionRange(text: text, target: "missing") }
        try expectFailure("unmatched prefix") { _ = try actionSelectionRange(text: "a发送", target: "发送", prefix: "b") }
        try expectFailure("unmatched suffix") { _ = try actionSelectionRange(text: "发送a", target: "发送", suffix: "b") }
        try expectFailure("empty target") { _ = try actionSelectionRange(text: text, target: "") }
        try expectFailure("empty source") { _ = try actionSelectionRange(text: "", target: "a") }
        try expectFailure("unknown selection mode") { _ = try actionSelectionRange(text: "abc", target: "b", selection: "guess") }
        let paste = try actionKey("super+v")
        try require(actionBackgroundMode(nil, operation: "type", input: [:]) == false, "foreground remains the default")
        for operation in ["setValue", "selectText", "performSecondaryAction"] {
            try require(actionBackgroundMode("background", operation: operation, input: [:]), "semantic background mode")
        }
        try require(actionBackgroundMode("background", operation: "click", input: ["elementID": "1"]), "semantic background click")
        for operation in ["pressKey", "type", "paste", "scroll", "drag"] {
            try expectFailure("global background input refused") { _ = try actionBackgroundMode("background", operation: operation, input: [:]) }
        }
        try require(actionBackgroundMode("background", operation: "pressKey", input: ["key": "Left"]), "PID key route still requires a live focused-field proof")
        try require(actionBackgroundMode("background", operation: "type", input: ["text": "中文🌦️"]), "PID Unicode route still requires a live focused-field proof")
        try expectFailure("background global shortcut refused") { _ = try actionBackgroundMode("background", operation: "pressKey", input: ["key": "super+a"]) }
        try expectFailure("background oversized text refused") { _ = try actionBackgroundMode("background", operation: "type", input: ["text": String(repeating: "a", count: 16001)]) }
        try require(computerBackgroundKeyboardEditable(role: "AXTextArea", subrole: "", protected: false, editable: true), "ordinary editable field eligible")
        for (role, subrole, protected, editable) in [("AXButton", "", false, true), ("AXTextField", "AXSecureTextField", false, true), ("AXTextArea", "", true, true), ("AXTextArea", "", false, false)] {
            try require(!computerBackgroundKeyboardEditable(role: role, subrole: subrole, protected: protected, editable: editable), "unproven or protected fields rejected")
        }
        guard let generation = computerProcessGeneration(getpid()) else { throw ComputerError("Own kernel process generation unavailable") }
        try require(computerProcessGeneration(0) == nil && computerProcessGeneration(-1) == nil, "unknown process generations cannot become input authority")
        let session = ComputerSession(), element = AXUIElementCreateApplication(getpid())
        let target = ComputerTarget(app: "com.example.fixture", pid: getpid(), windowID: 1, frame: CGRect(x: 0, y: 0, width: 1, height: 1), window: element)
        session.backgroundKeyboardFocus = ComputerKeyboardFocusProof(target: target, application: .current, processGeneration: generation, focusByAttribute: false, webContent: false, element: element, role: "AXTextArea", identity: [], textSnapshot: ComputerKeyboardTextSnapshot(digest: Data(), location: 0, length: 0))
        session.backgroundKeyboardUnavailableReason = "test"
        session.invalidate()
        try require(session.backgroundKeyboardFocus == nil && session.backgroundKeyboardUnavailableReason == nil, "observation invalidation revokes focused keyboard proof")
        let inserted = try actionBackgroundLiteralPlan(value: "A😀B", range: CFRange(location: 1, length: 2), text: "中文", role: "AXTextField")
        try require(inserted.0 == "A中文B" && inserted.1.location == 3 && inserted.1.length == 0, "plain insertion preserves surrounding text and computes UTF-16 caret")
        let emptyArea = try actionBackgroundLiteralPlan(value: "", range: CFRange(location: 0, length: 0), text: "🌦️", role: "AXTextArea")
        try require(emptyArea.0 == "🌦️" && emptyArea.1.location == ("🌦️" as NSString).length, "empty area literal insertion")
        try expectFailure("nonempty rich area cannot be flattened") { _ = try actionBackgroundLiteralPlan(value: "keep formatting", range: CFRange(location: 0, length: 0), text: "x", role: "AXTextArea") }
        for range in [CFRange(location: -1, length: 0), CFRange(location: 9, length: 0), CFRange(location: 2, length: 0), CFRange(location: 0, length: 2), CFRange(location: 0, length: Int.max)] {
            try expectFailure("invalid/surrogate-splitting selection refused") { _ = try actionBackgroundLiteralPlan(value: "A😀B", range: range, text: "x", role: "AXTextField") }
        }
        for input: [String: Any] in [["coordinate": [1, 2]], ["elementID": "1", "key": "shift"], ["elementID": "1", "duration": 1], ["elementID": "1", "clickCount": 2]] {
            try expectFailure("background click cannot fall back") { _ = try actionBackgroundMode("background", operation: "click", input: input) }
        }
        try expectFailure("background autosubmit refused before changing value") { _ = try actionBackgroundMode("background", operation: "setValue", input: ["autosubmitSearchFields": true]) }
        try expectFailure("unknown interaction mode") { _ = try actionBackgroundMode("guess", operation: "click", input: [:]) }
        try require(paste.0 == 9 && paste.1 == .maskCommand, "Command-V mapping")
        let chord = try actionKey("ctrl+alt+shift+Return")
        try require(chord.0 == 36 && chord.1 == [.maskControl, .maskAlternate, .maskShift], "modifier chord mapping")
        let uppercase = try actionKey("A")
        try require(uppercase.0 == 0 && uppercase.1 == .maskShift, "uppercase physical key includes Shift")
        let arrow = try actionKey("Left")
        try require(arrow.0 == 123 && arrow.1.isEmpty, "named arrow key mapping")
        try expectFailure("unknown modifier") { _ = try actionKey("guess+a") }
        try expectFailure("missing key") { _ = try actionKey("super+") }
        try expectFailure("unknown key") { _ = try actionKey("super+run_script") }
        try expectFailure("empty chord component") { _ = try actionKey("super++a") }
        let markdown = try computerMarkdown("# Heading\n\n**Bold** *italic* `code` [link](https://example.com)\n\n- one\n- two\n\n|a|b|\n|-|-|\n|1|2|")
        try require(markdown.html.contains("<strong>Bold</strong>") && markdown.html.contains("<em>italic</em>"), "Markdown inline styles survive HTML conversion")
        try require(markdown.html.contains("<h1>Heading</h1>") && markdown.html.contains("<ul><li>"), "Markdown headings and lists preserve structure")
        try require(markdown.html.contains("<table><tr><th>a</th><th>b</th></tr><tr><td>1</td><td>2</td></tr></table>"), "Markdown tables preserve cells")
        try require(markdown.html.contains("<a href=\"https://example.com\">link</a>"), "Markdown links preserve destinations")
        let escaped = try computerMarkdown("`<script>` 🌦️")
        try require(escaped.html.contains("&lt;script&gt;") && escaped.text.contains("🌦️"), "Markdown escapes markup and preserves Unicode")
        let html = try NSAttributedString(data: computerHTMLPasteData("<b>HTML富文本</b>"), options: [.documentType: NSAttributedString.DocumentType.html], documentAttributes: nil)
        try require(html.string.trimmingCharacters(in: .whitespacesAndNewlines) == "HTML富文本", "AppKit interprets clipboard HTML fragments as UTF-8")
        print("PASS: native action pure-logic tests (UTF-16, combining text, repeated/overlapping matches, context, cursor placement, key modifiers and invalid inputs); no app input or permissions used.")
    }
}
