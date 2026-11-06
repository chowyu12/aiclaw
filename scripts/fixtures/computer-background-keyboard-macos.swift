// Two separately launched, disposable apps. Setup activates each app once;
// after the sentinel is frontmost, an eventual PID-delivery probe must not
// activate the target or route input to a global event tap.
import AppKit

final class KeyboardReceiptView: NSTextView {
    var keyDownCodes: [UInt16] = []
    var keyUpCodes: [UInt16] = []
    var onEvent: (() -> Void)?
    override func keyDown(with event: NSEvent) {
        keyDownCodes.append(event.keyCode); keyDownCodes = Array(keyDownCodes.suffix(256))
        super.keyDown(with: event); onEvent?()
    }
    override func keyUp(with event: NSEvent) {
        keyUpCodes.append(event.keyCode); keyUpCodes = Array(keyUpCodes.suffix(256))
        super.keyUp(with: event); onEvent?()
    }
}

final class BackgroundKeyboardFixture: NSObject, NSApplicationDelegate {
    let role = CommandLine.arguments[1]
    let receipt = URL(fileURLWithPath: CommandLine.arguments[2])
    let commands = URL(fileURLWithPath: CommandLine.arguments[3])
    let editor = KeyboardReceiptView(frame: NSRect(x: 20, y: 60, width: 440, height: 120))
    let other = NSTextView(frame: NSRect(x: 20, y: 15, width: 440, height: 35))
    let secure = NSSecureTextField(string: "fixture-only-password")
    var window: NSWindow!
    var secondWindow: NSWindow?
    var receivedDown: [UInt16] = []
    var receivedUp: [UInt16] = []
    var eventSources: [[String: Int]] = []
    var monitor: Any?
    var timer: Timer?
    var previousReceipt: Data?
    var previousCommand: Data?
    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: role == "target" ? 200 : 730, y: 250, width: 480, height: 220), styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "AIClaw background keyboard \(role) fixture"
        window.isReleasedWhenClosed = false
        editor.string = role == "target" ? "seed" : "sentinel"
        editor.setAccessibilityLabel("Background keyboard \(role) editor")
        editor.setAccessibilityIdentifier("background-keyboard-\(role)-editor")
        editor.font = .systemFont(ofSize: 16)
        editor.isRichText = false
        other.string = "Other field"
        other.setAccessibilityIdentifier("background-keyboard-other-editor")
        secure.frame = editor.frame; secure.isHidden = true
        secure.setAccessibilityLabel("Secure fixture field")
        window.contentView!.addSubview(editor); window.contentView!.addSubview(other); window.contentView!.addSubview(secure)
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .keyUp]) { [weak self] event in
            self?.eventSources.append(["down": event.type == .keyDown ? 1 : 0, "code": Int(event.keyCode), "source_pid": Int(event.cgEvent?.getIntegerValueField(.eventSourceUnixProcessID) ?? -1)])
            self?.eventSources = Array(self?.eventSources.suffix(256) ?? [])
            if event.type == .keyDown { self?.receivedDown.append(event.keyCode); self?.receivedDown = Array(self?.receivedDown.suffix(256) ?? []) }
            if event.type == .keyUp { self?.receivedUp.append(event.keyCode); self?.receivedUp = Array(self?.receivedUp.suffix(256) ?? []) }
            return event
        }
        editor.onEvent = { [weak self] in self?.writeReceipt() }
        window.makeKeyAndOrderFront(nil); window.makeFirstResponder(editor)
        editor.setSelectedRange(NSRange(location: (editor.string as NSString).length, length: 0))
        NSApp.activate(ignoringOtherApps: true)
        timer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in self?.command(); self?.writeReceipt() }
        writeReceipt()
    }
    func command() {
        guard let data = try? Data(contentsOf: commands), data != previousCommand,
              let command = try? JSONSerialization.jsonObject(with: data) as? [String: String] else { return }
        previousCommand = data
        switch command["command"] {
        case "reset-editor":
            secure.isHidden = true; editor.isHidden = false
            editor.unmarkText(); editor.string = role == "sentinel" ? "sentinel" : "target-value"
            window.makeFirstResponder(editor); editor.setSelectedRange(NSRange(location: role == "sentinel" ? 3 : 5, length: 0))
        case "change-value": editor.string += "manual"
        case "change-caret": editor.setSelectedRange(NSRange(location: 0, length: 0))
        case "focus-other": window.makeFirstResponder(other)
        case "focus-editor":
            secure.isHidden = true; editor.isHidden = false
            window.makeFirstResponder(editor)
        case "secure":
            editor.isHidden = true; secure.isHidden = false
            window.makeFirstResponder(secure)
        case "second-window":
            let second = NSWindow(contentRect: NSRect(x: 300, y: 300, width: 300, height: 150), styleMask: [.titled, .closable], backing: .buffered, defer: false)
            second.title = "Other background keyboard window"; second.isReleasedWhenClosed = false
            let field = NSTextView(frame: NSRect(x: 10, y: 10, width: 280, height: 120)); field.string = "second-window"
            second.contentView!.addSubview(field); second.makeKeyAndOrderFront(nil); second.makeFirstResponder(field)
            secondWindow = second
        case "activate": NSApp.activate(ignoringOtherApps: true)
        case "move": window.setFrameOrigin(NSPoint(x: window.frame.minX + 20, y: window.frame.minY))
        case "close": window.close()
        default: break
        }
    }
    func writeReceipt() {
        let range = editor.selectedRange()
        let marked = editor.markedRange()
        let object: [String: Any] = [
            "role": role, "pid": getpid(), "window_id": window.windowNumber,
            "is_active": NSApp.isActive, "editor_is_first_responder": window.firstResponder === editor,
            "text": editor.string, "other_text": other.string, "selection": [range.location, range.length],
            "has_marked_text": editor.hasMarkedText(), "marked_range": [marked.location, marked.length],
            "key_down": editor.keyDownCodes.map(Int.init), "key_up": editor.keyUpCodes.map(Int.init),
            "events_down": receivedDown.map(Int.init), "events_up": receivedUp.map(Int.init),
            "event_sources": eventSources,
            "secure_unchanged": secure.stringValue == "fixture-only-password",
            "is_visible": window.isVisible,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]), data != previousReceipt else { return }
        previousReceipt = data; try? data.write(to: receipt, options: .atomic)
    }
}

let application = NSApplication.shared
let delegate = BackgroundKeyboardFixture()
application.setActivationPolicy(.accessory)
application.delegate = delegate
application.run()
