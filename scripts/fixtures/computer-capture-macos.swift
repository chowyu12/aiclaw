import AppKit

final class CaptureFixtureWindow: NSWindow {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

final class SemanticView: NSView {
    var value = "semantic-before"
    override func isAccessibilityElement() -> Bool { true }
    override func accessibilityRole() -> NSAccessibility.Role? { .textField }
    override func accessibilityLabel() -> String? { "Capture semantic fixture" }
    override func accessibilityValue() -> Any? { value }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.white.setFill(); dirtyRect.fill()
        ("AX-only changes do not redraw this text" as NSString).draw(at: NSPoint(x: 8, y: 8), withAttributes: [.font: NSFont.systemFont(ofSize: 14), .foregroundColor: NSColor.black])
    }
}

final class CaptureFixture: NSObject, NSApplicationDelegate {
    let commands = URL(fileURLWithPath: CommandLine.arguments[1])
    var previous: Data?
    var window: NSWindow!
    let field = NSTextField(string: "visual-before")
    let semantic = SemanticView(frame: NSRect(x: 20, y: 120, width: 440, height: 70))
    var timer: Timer?
    func applicationDidFinishLaunching(_ notification: Notification) {
        // No system title-bar/rounded-corner animation may contaminate the
        // AX-only assertion. This fixture paints a fixed opaque rectangle.
        let style: NSWindow.StyleMask = CommandLine.arguments.contains("--titled") ? [.titled, .closable, .resizable] : [.borderless]
        window = CaptureFixtureWindow(contentRect: NSRect(x: 220, y: 220, width: 480, height: 240), styleMask: style, backing: .buffered, defer: false)
        window.isOpaque = true; window.backgroundColor = .white; window.hasShadow = false
        window.title = "AIClaw stream fixture"
        window.isReleasedWhenClosed = false
        field.frame = NSRect(x: 20, y: 60, width: 440, height: 32)
        field.setAccessibilityLabel("Capture visible fixture")
        window.contentView!.addSubview(semantic); window.contentView!.addSubview(field)
        window.makeKeyAndOrderFront(nil); window.makeFirstResponder(nil)
        NSApp.activate(ignoringOtherApps: true)
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in self?.command() }
    }
    func command() {
        guard let data = try? Data(contentsOf: commands), data != previous,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        previous = data
        switch json["command"] as? String {
        case "semantic":
            semantic.value = json["value"] as? String ?? "semantic-after"
            NSAccessibility.post(element: semantic, notification: .valueChanged)
        case "visual":
            field.stringValue = json["value"] as? String ?? "visual-after"
            NSAccessibility.post(element: field, notification: .valueChanged)
        case "replace":
            let replacement = NSWindow(contentRect: window.frame, styleMask: [.titled], backing: .buffered, defer: false)
            replacement.title = "Replacement is not the pinned window"
            replacement.isReleasedWhenClosed = false
            window.close(); window = replacement; replacement.makeKeyAndOrderFront(nil)
        default: break
        }
    }
}

let application = NSApplication.shared
let delegate = CaptureFixture()
application.setActivationPolicy(.accessory)
application.delegate = delegate
application.run()
