import AppKit

// Local acceptance fixture only: no networking or user files.
class Fixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    let input = NSTextField(frame: NSRect(x: 24, y: 92, width: 350, height: 28))
    let status = NSTextField(labelWithString: "No input submitted")
    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 120, y: 120, width: 400, height: 180),
                          styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "AIClaw Computer Acceptance Fixture"
        input.placeholderString = "Fixture input"
        input.setAccessibilityLabel("Fixture input")
        input.setAccessibilityIdentifier("fixture-input")
        let button = NSButton(title: "Submit fixture", target: self, action: #selector(submit))
        button.frame = NSRect(x: 24, y: 48, width: 160, height: 28)
        status.frame = NSRect(x: 24, y: 14, width: 350, height: 22)
        window.contentView!.addSubview(input)
        window.contentView!.addSubview(button)
        window.contentView!.addSubview(status)
        window.orderFront(nil)
    }
    @objc func submit() {
        window.makeFirstResponder(nil)
        status.stringValue = "Submitted: " + input.stringValue
        let data = try! JSONSerialization.data(withJSONObject: ["value": input.stringValue])
        try! data.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let fixture = Fixture()
app.delegate = fixture
app.run()
