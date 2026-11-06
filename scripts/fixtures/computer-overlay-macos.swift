import AppKit
import CoreGraphics

// Isolated acceptance window. Only its own command file can move it.
final class OverlayFixture: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    var sequence = -1
    func applicationDidFinishLaunching(_ notification: Notification) {
        window = NSWindow(contentRect: NSRect(x: 120, y: 120, width: 700, height: 420), styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "AIClaw overlay acceptance fixture"
        window.backgroundColor = NSColor(calibratedRed: 0.08, green: 0.36, blue: 0.48, alpha: 1)
        let label = NSTextField(labelWithString: "Overlay acceptance fixture · no user data")
        label.textColor = .white; label.frame = NSRect(x: 30, y: 180, width: 580, height: 35)
        window.contentView?.addSubview(label)
        window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
        Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in self?.tick() }
        tick()
    }
    func tick() {
        if let data = try? Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1])),
           let command = try? JSONSerialization.jsonObject(with: data) as? [String: Double],
           let next = command["sequence"], Int(next) != sequence,
           let x = command["x"], let y = command["y"], let width = command["width"], let height = command["height"] {
            sequence = Int(next)
            let primaryTop = NSScreen.screens.first?.frame.maxY ?? 0
            window.setFrame(NSRect(x: x, y: primaryTop - y - height, width: width, height: height), display: true)
        }
        let entries = CGWindowListCopyWindowInfo([.optionIncludingWindow], CGWindowID(window.windowNumber)) as? [[String: Any]]
        guard let bounds = entries?.first?[kCGWindowBounds as String] as? [String: Any],
              let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary),
              let data = try? JSONSerialization.data(withJSONObject: ["pid": ProcessInfo.processInfo.processIdentifier, "windowId": window.windowNumber, "sequence": sequence,
                "frame": ["x": frame.minX, "y": frame.minY, "width": frame.width, "height": frame.height]]) else { return }
        try? data.write(to: URL(fileURLWithPath: CommandLine.arguments[2]), options: .atomic)
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let fixture = OverlayFixture(); app.delegate = fixture; app.run()
