import AppKit

// Owned acceptance fixture. No network, clipboard, user files or text editing.
final class KeyReceiptView: NSView {
    let receipt: URL
    private var events: [[String: Any]] = []
    init(receipt: URL) { self.receipt = receipt; super.init(frame: NSRect(x: 0, y: 0, width: 460, height: 220)) }
    required init?(coder: NSCoder) { fatalError("Not used") }
    override var acceptsFirstResponder: Bool { true }
    func record(_ kind: String, _ event: NSEvent) {
        events.append(["kind": kind, "keyCode": Int(event.keyCode), "repeat": event.isARepeat, "at_ms": Date().timeIntervalSince1970 * 1000])
        save()
    }
    func save() {
        guard let data = try? JSONSerialization.data(withJSONObject: ["ready": true, "pid": ProcessInfo.processInfo.processIdentifier, "events": events]) else { return }
        try? data.write(to: receipt, options: .atomic)
    }
    override func keyDown(with event: NSEvent) { record("down", event) }
    override func keyUp(with event: NSEvent) { record("up", event) }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill(); bounds.fill()
        ("Temporary keyboard-release acceptance fixture" as NSString).draw(at: NSPoint(x: 20, y: 110), withAttributes: [.foregroundColor: NSColor.labelColor])
    }
}

final class KeyReleaseFixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    func applicationDidFinishLaunching(_ notification: Notification) {
        let view = KeyReceiptView(receipt: URL(fileURLWithPath: CommandLine.arguments[1]))
        view.setAccessibilityElement(true)
        view.setAccessibilityRole(.group)
        view.setAccessibilityLabel("Keyboard receipt fixture")
        window = NSWindow(contentRect: NSRect(x: 160, y: 180, width: 460, height: 220), styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "Temporary Key Release Fixture"
        window.contentView = view
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(view)
        view.save()
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = KeyReleaseFixture()
app.delegate = delegate
app.run()
