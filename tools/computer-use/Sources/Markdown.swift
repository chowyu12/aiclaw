import Foundation

func computerHTMLPasteData(_ source: String) -> Data {
    // Pasteboard HTML fragments need an explicit charset; otherwise AppKit may
    // interpret UTF-8 bytes using a legacy locale encoding.
    Data(("<meta charset=\"utf-8\">" + source).utf8)
}

private func markdownEscape(_ text: String) -> String {
    text.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;")
        .replacingOccurrences(of: ">", with: "&gt;").replacingOccurrences(of: "\"", with: "&quot;")
}

// Foundation supplies Markdown syntax and semantic block/inline runs. Serialize
// those runs directly: NSAttributedString's HTML exporter drops Markdown's bold,
// heading, list and table intents when no explicit AppKit fonts were attached.
func computerMarkdown(_ source: String) throws -> (text: String, html: String) {
    let value = try AttributedString(markdown: source)
    var html = "<!doctype html><html><head><meta charset=\"utf-8\"></head><body>"
    var plain = ""
    var stack: [(id: Int, close: String)] = []
    for run in value.runs {
        let blocks = Array((run.presentationIntent?.components ?? []).reversed())
        var shared = 0
        while shared < min(stack.count, blocks.count), stack[shared].id == blocks[shared].identity { shared += 1 }
        for block in stack.dropFirst(shared).reversed() { html += block.close }
        stack.removeSubrange(shared...)
        let opened = blocks.dropFirst(shared)
        let newRow = opened.contains { if case .tableRow = $0.kind { return true }; if case .tableHeaderRow = $0.kind { return true }; return false }
        let newCell = opened.contains { if case .tableCell = $0.kind { return true }; return false }
        let newParagraph = opened.contains { if case .paragraph = $0.kind { return true }; if case .header = $0.kind { return true }; return false }
        if !plain.isEmpty {
            if newRow || newParagraph { if !plain.hasSuffix("\n") { plain += "\n" } }
            else if newCell { plain += "\t" }
        }
        for block in opened {
            let tags: (String, String)
            switch block.kind {
            case .paragraph: tags = ("<p>", "</p>")
            case .header(let level): let level = min(6, max(1, level)); tags = ("<h\(level)>", "</h\(level)>")
            case .orderedList: tags = ("<ol>", "</ol>")
            case .unorderedList: tags = ("<ul>", "</ul>")
            case .listItem(let ordinal):
                tags = ("<li>", "</li>")
                let ordered = blocks.contains { if case .orderedList = $0.kind { return true }; return false }
                plain += ordered ? "\(ordinal). " : "• "
            case .codeBlock: tags = ("<pre><code>", "</code></pre>")
            case .blockQuote: tags = ("<blockquote>", "</blockquote>")
            case .table: tags = ("<table>", "</table>")
            case .tableHeaderRow, .tableRow: tags = ("<tr>", "</tr>")
            case .tableCell:
                let header = blocks.contains { if case .tableHeaderRow = $0.kind { return true }; return false }
                tags = header ? ("<th>", "</th>") : ("<td>", "</td>")
            case .thematicBreak: tags = ("<hr>", "")
            @unknown default: tags = ("<span>", "</span>")
            }
            html += tags.0
            stack.append((block.identity, tags.1))
        }
        let text = String(value.characters[run.range])
        plain += text
        var content = markdownEscape(text)
        if let intent = run.inlinePresentationIntent {
            if intent.contains(.code) { content = "<code>\(content)</code>" }
            if intent.contains(.stronglyEmphasized) { content = "<strong>\(content)</strong>" }
            if intent.contains(.emphasized) { content = "<em>\(content)</em>" }
            if intent.contains(.strikethrough) { content = "<s>\(content)</s>" }
        }
        if let url = run.link { content = "<a href=\"\(markdownEscape(url.absoluteString))\">\(content)</a>" }
        html += content
    }
    for block in stack.reversed() { html += block.close }
    return (plain, html + "</body></html>")
}
