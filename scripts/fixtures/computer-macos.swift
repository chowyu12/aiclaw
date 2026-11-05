import AppKit

class GesturePad: NSView {
    var drags = 0
    var scrolls = 0
    override func mouseDown(with event: NSEvent) {}
    override func mouseDragged(with event: NSEvent) { drags += 1; needsDisplay = true }
    override func scrollWheel(with event: NSEvent) { scrolls += 1; needsDisplay = true }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.controlBackgroundColor.setFill()
        bounds.fill()
        ("Gesture pad: drag \(drags), scroll \(scrolls)" as NSString).draw(at: NSPoint(x: 8, y: 45), withAttributes: [.foregroundColor: NSColor.labelColor])
    }
}

// Local acceptance fixture only: no networking or user files.
class Fixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    let input = NSTextField(frame: NSRect(x: 24, y: 392, width: 350, height: 28))
    let status = NSTextField(labelWithString: "No input submitted")
    let pad = GesturePad(frame: NSRect(x: 24, y: 20, width: 350, height: 110))
    let stepper = NSStepper(frame: NSRect(x: 350, y: 332, width: 24, height: 28))
    let rich = NSTextView(frame: NSRect(x: 0, y: 0, width: 350, height: 120))
    func applicationDidFinishLaunching(_ notification: Notification) {
        // Standard responder-chain shortcuts need an Edit menu in an AppKit app.
        let menu = NSMenu()
        let appItem = NSMenuItem()
        let appMenu = NSMenu(title: "AIClaw Fixture")
        appMenu.addItem(withTitle: "Quit", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(appItem)
        menu.setSubmenu(appMenu, for: appItem)
        let editItem = NSMenuItem(title: "Edit", action: nil, keyEquivalent: "")
        let edit = NSMenu(title: "Edit")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        menu.addItem(editItem)
        menu.setSubmenu(edit, for: editItem)
        NSApp.mainMenu = menu
        window = NSWindow(contentRect: NSRect(x: 120, y: 120, width: 400, height: 480),
                          styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "AIClaw Computer Acceptance Fixture"
        input.placeholderString = "Fixture input"
        input.setAccessibilityLabel("Fixture input")
        input.setAccessibilityIdentifier("fixture-input")
        let button = NSButton(title: "Submit fixture", target: self, action: #selector(submit))
        button.frame = NSRect(x: 24, y: 348, width: 160, height: 28)
        status.frame = NSRect(x: 24, y: 306, width: 350, height: 22)
        pad.setAccessibilityElement(true)
        pad.setAccessibilityRole(.group)
        pad.setAccessibilityLabel("Fixture gesture pad")
        stepper.minValue = 0
        stepper.maxValue = 100
        stepper.setAccessibilityLabel("Fixture stepper")
        let richContainer = NSScrollView(frame: NSRect(x: 24, y: 160, width: 350, height: 120))
        richContainer.borderType = .bezelBorder
        richContainer.documentView = rich
        rich.isRichText = true
        rich.setAccessibilityLabel("Fixture rich editor")
        window.contentView!.addSubview(input)
        window.contentView!.addSubview(button)
        window.contentView!.addSubview(status)
        window.contentView!.addSubview(pad)
        window.contentView!.addSubview(stepper)
        window.contentView!.addSubview(richContainer)
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(input)
    }
    @objc func submit() {
        window.makeFirstResponder(nil)
        status.stringValue = "Submitted: " + input.stringValue
        var bold = false
        rich.attributedString().enumerateAttribute(.font, in: NSRange(location: 0, length: rich.attributedString().length)) { value, _, _ in
            if let font = value as? NSFont, NSFontManager.shared.traits(of: font).contains(.boldFontMask) { bold = true }
        }
        let data = try! JSONSerialization.data(withJSONObject: ["value": input.stringValue, "drags": pad.drags, "scrolls": pad.scrolls, "stepper": stepper.integerValue, "rich": rich.string, "rich_bold": bold])
        try! data.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let fixture = Fixture()
app.delegate = fixture
app.run()
