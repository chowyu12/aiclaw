import AppKit
import CoreGraphics

if CommandLine.arguments.contains("--probe") {
    let front = NSWorkspace.shared.frontmostApplication
    let point = CGEvent(source: nil)!.location
    let data: [String: Any] = ["pid": front?.processIdentifier ?? 0, "bundle": front?.bundleIdentifier ?? "", "x": point.x, "y": point.y]
    print(String(data: try! JSONSerialization.data(withJSONObject: data), encoding: .utf8)!)
    exit(0)
}
final class Fixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    let field = NSTextField(frame: NSRect(x: 25, y: 100, width: 350, height: 28))
    let output = CommandLine.arguments[1]
    func write(_ committed: Bool = false) {
        let data: [String: Any] = ["pid": ProcessInfo.processInfo.processIdentifier, "window_id": window.windowNumber,
            "value": field.stringValue, "committed": committed]
        try? JSONSerialization.data(withJSONObject: data).write(to: URL(fileURLWithPath: output), options: .atomic)
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        window = NSWindow(contentRect: NSRect(x: 650, y: 300, width: 430, height: 190), styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "AIClaw Background Target Fixture"
        field.stringValue = "before"
        field.setAccessibilityLabel("Fixture Input")
        window.contentView!.addSubview(field)
        let button = NSButton(title: "Commit Fixture", target: self, action: #selector(commit))
        button.frame = NSRect(x: 25, y: 40, width: 180, height: 30)
        window.contentView!.addSubview(button)
        window.orderFront(nil) // Never activates the app or changes key focus.
        write()
    }
    @objc func commit() { write(true) }
}
let delegate = Fixture()
NSApplication.shared.delegate = delegate
NSApplication.shared.run()
