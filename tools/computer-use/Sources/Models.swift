import AppKit
import ApplicationServices
import CoreGraphics

struct ComputerError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}

func nativeText(_ text: String) -> [String: Any] {
    ["content": [["type": "text", "text": text]]]
}

func nativeJSON(_ object: Any) throws -> [String: Any] {
    let data = try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys])
    return nativeText(String(decoding: data, as: UTF8.self))
}

struct ComputerTarget {
    let app: String
    let pid: pid_t
    let windowID: CGWindowID
    let frame: CGRect
    let window: AXUIElement
}

@MainActor
final class ComputerSession {
    var elements: [Int: AXUIElement] = [:]
    var elementRoles: [Int: String] = [:]
    var elementIdentities: [Int: [String]] = [:]
    var nextElementID = 1
    var target: ComputerTarget?
    var epoch = 0
    var imageSize: CGSize?
    var discoveredApplications: [String: URL] = [:]

    func invalidate() {
        elements.removeAll()
        elementRoles.removeAll()
        elementIdentities.removeAll()
        target = nil
        imageSize = nil
        epoch += 1
    }
}

@MainActor
func requirePermissions(screenCapture: Bool = true) throws {
    if !AXIsProcessTrusted() || (screenCapture && !CGPreflightScreenCaptureAccess()) {
        PermissionWindow.shared.show()
        throw ComputerError("AIClaw Computer Use needs Accessibility and Screen Recording. Open its permission setup window, grant the required access in System Settings, then observe again. No input was dispatched.")
    }
}

@MainActor
final class PermissionWindow: NSObject {
    static let shared = PermissionWindow()
    private var window: NSWindow?

    func show() {
        if window == nil {
            let panel = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 230), styleMask: [.titled, .closable], backing: .buffered, defer: false)
            panel.title = "AIClaw Computer Use Permissions"
            panel.isReleasedWhenClosed = false
            let title = NSTextField(labelWithString: "Allow AIClaw Computer Use to control applications")
            title.font = .boldSystemFont(ofSize: 16)
            let detail = NSTextField(wrappingLabelWithString: "Enable AIClaw Computer Use in Privacy & Security → Accessibility and Screen Recording. Permissions belong to this independent helper application. AIClaw does not grant access automatically. After changing access, retry the observation.")
            let accessibility = NSButton(title: "Open Accessibility Settings", target: self, action: #selector(openAccessibility))
            let capture = NSButton(title: "Open Screen Recording Settings", target: self, action: #selector(openCapture))
            let stack = NSStackView(views: [title, detail, accessibility, capture])
            stack.orientation = .vertical
            stack.alignment = .leading
            stack.spacing = 14
            stack.translatesAutoresizingMaskIntoConstraints = false
            panel.contentView!.addSubview(stack)
            NSLayoutConstraint.activate([
                stack.leadingAnchor.constraint(equalTo: panel.contentView!.leadingAnchor, constant: 20),
                stack.trailingAnchor.constraint(equalTo: panel.contentView!.trailingAnchor, constant: -20),
                stack.topAnchor.constraint(equalTo: panel.contentView!.topAnchor, constant: 20),
            ])
            panel.center()
            window = panel
        }
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func openAccessibility() {
        _ = AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary)
        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")!)
    }

    @objc private func openCapture() {
        // A preflight check does not register this app in System Settings. Ask
        // only after the user clicks this explicit setup button; macOS owns consent.
        _ = CGRequestScreenCaptureAccess()
        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture")!)
    }
}

@MainActor
func validateComputerApp(_ app: String) throws -> String {
    guard app.range(of: "^[A-Za-z0-9-]+(?:\\.[A-Za-z0-9-]+)+$", options: .regularExpression) != nil else { throw ComputerError("An exact application bundle identifier is required") }
    let lower = app.lowercased()
    if lower.hasPrefix("com.github.chowyu12.aiclaw") || lower.hasPrefix("com.aiclaw.computeruse") || lower == Bundle.main.bundleIdentifier?.lowercased() || lower == "com.electron" || lower.hasPrefix("com.github.electron") {
        throw ComputerError("Refusing to control AIClaw, its helper or approval controls")
    }
    return app
}
