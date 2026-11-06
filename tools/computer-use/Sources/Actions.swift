import AppKit
import ApplicationServices
import CoreGraphics

private func actionString(_ args: [String: Any], _ key: String) throws -> String {
    guard let value = args[key] as? String else { throw ComputerError("\(key) must be a string") }
    return value
}

private func actionNumber(_ args: [String: Any], _ key: String, default fallback: Double) throws -> Double {
    guard let raw = args[key] else { return fallback }
    guard let number = raw as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(), number.doubleValue.isFinite else {
        throw ComputerError("\(key) must be a finite number")
    }
    return number.doubleValue
}

private func actionAttribute(_ element: AXUIElement, _ attribute: String) throws -> CFTypeRef {
    var result: CFTypeRef?
    let error = AXUIElementCopyAttributeValue(element, attribute as CFString, &result)
    guard error == .success, let result else { throw ComputerError("Cannot read \(attribute): AX error \(error.rawValue)") }
    return result
}

private func actionSet(_ element: AXUIElement, _ attribute: String, _ value: CFTypeRef) throws {
    try Task.checkCancellation()
    var settable = DarwinBoolean(false)
    let checked = AXUIElementIsAttributeSettable(element, attribute as CFString, &settable)
    guard checked == .success, settable.boolValue else { throw ComputerError("\(attribute) is not settable for this element") }
    try Task.checkCancellation()
    let error = AXUIElementSetAttributeValue(element, attribute as CFString, value)
    guard error == .success else { throw ComputerError("Setting \(attribute) failed: AX error \(error.rawValue). Outcome may be unknown; observe again.") }
}

private func actionElementCenter(_ element: AXUIElement) throws -> CGPoint {
    let position = try actionAttribute(element, kAXPositionAttribute)
    let size = try actionAttribute(element, kAXSizeAttribute)
    guard CFGetTypeID(position) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { throw ComputerError("Element has no usable geometry") }
    var point = CGPoint.zero, dimensions = CGSize.zero
    guard AXValueGetValue(unsafeBitCast(position, to: AXValue.self), .cgPoint, &point),
          AXValueGetValue(unsafeBitCast(size, to: AXValue.self), .cgSize, &dimensions), dimensions.width > 0, dimensions.height > 0 else {
        throw ComputerError("Element has no usable geometry")
    }
    return CGPoint(x: point.x + dimensions.width / 2, y: point.y + dimensions.height / 2)
}

private let actionModifiers: [String: CGEventFlags] = [
    "super": .maskCommand, "cmd": .maskCommand, "command": .maskCommand, "meta": .maskCommand,
    "ctrl": .maskControl, "control": .maskControl, "alt": .maskAlternate, "option": .maskAlternate, "shift": .maskShift,
]
private let actionKeys: [String: CGKeyCode] = [
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9,
    "b": 11, "q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17,
    "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25,
    "7": 26, "-": 27, "8": 28, "0": 29, "]": 30, "o": 31, "u": 32, "[": 33,
    "i": 34, "p": 35, "return": 36, "enter": 36, "l": 37, "j": 38, "'": 39, "k": 40,
    ";": 41, "\\": 42, ",": 43, "/": 44, "n": 45, "m": 46, ".": 47, "tab": 48,
    "space": 49, "`": 50, "backspace": 51, "delete": 51, "escape": 53, "esc": 53,
    "f17": 64, "f18": 79, "f19": 80, "f20": 90, "f5": 96, "f6": 97, "f7": 98,
    "f3": 99, "f8": 100, "f9": 101, "f11": 103, "f13": 105, "f16": 106, "f14": 107,
    "f10": 109, "f12": 111, "f15": 113, "home": 115, "pageup": 116, "forwarddelete": 117,
    "end": 119, "f2": 120, "pagedown": 121, "f1": 122, "left": 123, "right": 124, "down": 125, "up": 126,
]

func actionKey(_ raw: String) throws -> (CGKeyCode, CGEventFlags) {
    let pieces = raw.lowercased().split(separator: "+", omittingEmptySubsequences: false).map(String.init)
    guard let name = pieces.last, let code = actionKeys[name] else { throw ComputerError("Unsupported key; use a key name or type for literal Unicode text") }
    var flags: CGEventFlags = []
    for modifier in pieces.dropLast() {
        guard let flag = actionModifiers[modifier] else { throw ComputerError("Unsupported key modifier: \(modifier)") }
        flags.insert(flag)
    }
    if raw.count == 1, raw != raw.lowercased() { flags.insert(.maskShift) }
    return (code, flags)
}

func actionSelectionRange(text: String, target: String, prefix: String? = nil, suffix: String? = nil, selection: String = "text") throws -> CFRange {
    guard !target.isEmpty else { throw ComputerError("Selection text cannot be empty") }
    guard ["text", "cursorBefore", "cursorAfter", "cursor_before", "cursor_after"].contains(selection) else { throw ComputerError("Selection must be text, cursorBefore or cursorAfter") }
    let source = text as NSString
    let prefixLength = (prefix as NSString?)?.length ?? 0, suffixLength = (suffix as NSString?)?.length ?? 0
    var selected: NSRange?, cursor = 0
    while cursor < source.length {
        let match = source.range(of: target, options: [.literal], range: NSRange(location: cursor, length: source.length - cursor))
        if match.location == NSNotFound { break }
        let prefixMatches = prefix == nil || (match.location >= prefixLength && source.substring(with: NSRange(location: match.location - prefixLength, length: prefixLength)) == prefix!)
        let end = match.location + match.length
        let suffixMatches = suffix == nil || (source.length - end >= suffixLength && source.substring(with: NSRange(location: end, length: suffixLength)) == suffix!)
        if prefixMatches && suffixMatches {
            guard selected == nil else { throw ComputerError("Selection text is ambiguous; use prefix/suffix for repeated text") }
            selected = match
        }
        cursor = match.location + 1
    }
    guard var range = selected else { throw ComputerError("Selection text was not found with the requested context") }
    if selection == "cursorBefore" || selection == "cursor_before" { range.length = 0 }
    if selection == "cursorAfter" || selection == "cursor_after" { range.location += range.length; range.length = 0 }
    return CFRange(location: range.location, length: range.length)
}

// Strict background mode has no global-input fallback. Keep this preflight pure
// so unsupported requests are rejected before any accessibility action.
func actionBackgroundMode(_ raw: Any?, operation: String, input: [String: Any]) throws -> Bool {
    let mode = raw ?? "foreground"
    guard let mode = mode as? String, ["foreground", "background"].contains(mode) else { throw ComputerError("interactionMode must be foreground or background") }
    guard mode == "background" else { return false }
    switch operation {
    case "click":
        guard input["elementID"] is String, input["coordinate"] == nil,
              try actionNumber(input, "clickCount", default: 1) == 1,
              try actionNumber(input, "duration", default: 0) == 0,
              (input["mouseButton"] as? String ?? "left") == "left",
              (input["key"] as? String ?? "").isEmpty else { throw ComputerError("Background click requires a single unmodified semantic left click; no global input was sent") }
    case "setValue":
        guard input["autosubmitSearchFields"] as? Bool != true else { throw ComputerError("Background setValue cannot autosubmit; no input was sent") }
    case "selectText", "performSecondaryAction": break
    case "pressKey":
        let (_, flags) = try actionKey(actionString(input, "key"))
        guard flags.subtracting(.maskShift).isEmpty else { throw ComputerError("Background keys currently support unmodified or Shift keys only; no input was sent") }
    case "type":
        let text = try actionString(input, "text")
        guard text.utf16.count <= 16000 else { throw ComputerError("Background text exceeds 16000 UTF-16 units; no input was sent") }
    default: throw ComputerError("Background \(operation) is not supported; no foreground fallback or global input was sent")
    }
    return true
}

func actionBackgroundLiteralPlan(value: String, range: CFRange, text: String, role: String) throws -> (String, CFRange) {
    let units = Array(value.utf16)
    guard units.count <= 2_000_000, text.utf16.count <= 16000,
          range.location >= 0, range.length >= 0, range.location <= units.count, range.length <= units.count - range.location else { throw ComputerError("Background literal text selection is outside the current value") }
    func boundary(_ index: Int) -> Bool {
        index == 0 || index == units.count || !((0xD800...0xDBFF).contains(units[index - 1]) && (0xDC00...0xDFFF).contains(units[index]))
    }
    guard boundary(range.location), boundary(range.location + range.length) else { throw ComputerError("Background literal text selection splits a UTF-16 surrogate pair") }
    guard role == "AXTextField" || (role == "AXTextArea" && value.isEmpty && range.location == 0 && range.length == 0) else {
        throw ComputerError("Background AX literal insertion cannot replace a nonempty rich text area; use an explicit set_value or foreground workflow")
    }
    return ((value as NSString).replacingCharacters(in: NSRange(location: range.location, length: range.length), with: text), CFRange(location: range.location + text.utf16.count, length: 0))
}

@MainActor
extension ComputerSession {
    private func actionElement(_ args: [String: Any], target: ComputerTarget) throws -> AXUIElement {
        let raw = try actionString(args, "elementID")
        guard let id = Int(raw), id >= 0 else { throw ComputerError("elementID must be a non-negative integer string") }
        let element = try resolveElement(id, target: target)
        AXUIElementSetMessagingTimeout(element, 2)
        return element
    }

    private func actionPoint(_ raw: Any?, target: ComputerTarget, observed: ComputerTarget) throws -> CGPoint {
        guard let values = raw as? [NSNumber], values.count == 2,
              values.allSatisfy({ CFGetTypeID($0) != CFBooleanGetTypeID() && $0.doubleValue.isFinite }),
              let pixels = imageSize, pixels.width > 0, pixels.height > 0,
              target.frame == observed.frame else {
            throw ComputerError("Coordinates need a fresh screenshot of the same window and geometry")
        }
        let x = values[0].doubleValue, y = values[1].doubleValue
        guard x >= 0, y >= 0, x < pixels.width, y < pixels.height else { throw ComputerError("Coordinates are outside the observed screenshot") }
        return CGPoint(x: target.frame.minX + x / pixels.width * target.frame.width,
                       y: target.frame.minY + y / pixels.height * target.frame.height)
    }

    private func actionActivate(_ target: ComputerTarget) async throws {
        try Task.checkCancellation()
        guard let application = NSRunningApplication(processIdentifier: target.pid), !application.isTerminated,
              application.bundleIdentifier == target.app else { throw ComputerError("Observed application process changed; observe again") }
        AXUIElementPerformAction(target.window, kAXRaiseAction as CFString)
        guard application.activate(options: []) else { throw ComputerError("Could not activate the target application; no global input was dispatched") }
        for _ in 0..<25 {
            if NSWorkspace.shared.frontmostApplication?.processIdentifier == target.pid {
                let focused = try await resolveTarget(app: target.app)
                guard focused.pid == target.pid, focused.windowID == target.windowID, focused.frame == target.frame else {
                    throw ComputerError("Target window or geometry changed during activation; observe again before global input")
                }
                return
            }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        throw ComputerError("The target application did not become frontmost; no global input was dispatched")
    }

    private func actionCheckFocus(_ target: ComputerTarget) throws {
        try Task.checkCancellation()
        guard NSWorkspace.shared.frontmostApplication?.processIdentifier == target.pid else { throw ComputerError("User changed the foreground application; input may be partial. Observe again before continuing") }
        let application = AXUIElementCreateApplication(target.pid)
        let focused = try actionAttribute(application, kAXFocusedWindowAttribute)
        guard CFGetTypeID(focused) == AXUIElementGetTypeID(), CFEqual(focused, target.window) else { throw ComputerError("User changed the focused window; input may be partial. Observe again before continuing") }
    }

    private func actionKeyboard(_ code: CGKeyCode, flags: CGEventFlags, duration: Double = 0) async throws {
        try Task.checkCancellation()
        guard let down = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: true),
              let up = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: false) else { throw ComputerError("Could not construct keyboard events") }
        down.flags = flags; up.flags = flags
        down.post(tap: .cghidEventTap)
        // Release the key even if the caller is cancelled while holding it.
        do { if duration > 0 { try await Task.sleep(nanoseconds: UInt64(duration * 1_000_000_000)) } }
        catch { up.post(tap: .cghidEventTap); throw error }
        up.post(tap: .cghidEventTap)
    }

    private func actionBackgroundKeyboardCheck(_ proof: ComputerKeyboardFocusProof, target: ComputerTarget, foreground: pid_t?, pointer: CGPoint?, clipboard: Int) throws {
        try Task.checkCancellation()
        guard AXIsProcessTrusted(), !proof.application.isTerminated,
              let running = NSRunningApplication(processIdentifier: target.pid), !running.isTerminated,
              running.bundleIdentifier == target.app, computerProcessGeneration(target.pid) == proof.processGeneration,
              proof.target.pid == target.pid, proof.target.windowID == target.windowID,
              proof.target.frame == target.frame, CFEqual(proof.target.window, target.window),
              let foreground, foreground != target.pid, pointer != nil,
              NSWorkspace.shared.frontmostApplication?.processIdentifier == foreground,
              CGEvent(source: nil)?.location == pointer, NSPasteboard.general.changeCount == clipboard else {
            throw ComputerError("Background keyboard target or user context changed; input may be partial. Observe again; no foreground fallback was used")
        }
        let application = AXUIElementCreateApplication(target.pid)
        if let focusedWindow = try? actionAttribute(application, kAXFocusedWindowAttribute) {
            guard CFGetTypeID(focusedWindow) == AXUIElementGetTypeID(), CFEqual(focusedWindow, target.window) else { throw ComputerError("Background keyboard focused window changed; observe again") }
        }
        if let focused = try? actionAttribute(application, kAXFocusedUIElementAttribute) {
            guard CFGetTypeID(focused) == AXUIElementGetTypeID(), CFEqual(focused, proof.element) else { throw ComputerError("Background keyboard focused editable field changed; observe again") }
        } else {
            guard proof.focusByAttribute, let focused = (try? actionAttribute(proof.element, kAXFocusedAttribute)) as? NSNumber,
                  CFGetTypeID(focused) == CFBooleanGetTypeID(), focused.boolValue else { throw ComputerError("Background keyboard focused editable field is no longer proven; observe again") }
        }
        let position = try actionAttribute(target.window, kAXPositionAttribute), size = try actionAttribute(target.window, kAXSizeAttribute)
        guard CFGetTypeID(position) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { throw ComputerError("Background keyboard window geometry is unavailable") }
        var origin = CGPoint.zero, dimensions = CGSize.zero
        guard AXValueGetValue(unsafeBitCast(position, to: AXValue.self), .cgPoint, &origin),
              AXValueGetValue(unsafeBitCast(size, to: AXValue.self), .cgSize, &dimensions), CGRect(origin: origin, size: dimensions) == proof.target.frame else {
            throw ComputerError("Background keyboard window moved or resized; observe again")
        }
        let role = (try? actionAttribute(proof.element, kAXRoleAttribute)) as? String ?? ""
        let subrole = (try? actionAttribute(proof.element, kAXSubroleAttribute)) as? String ?? ""
        let protected = ((try? actionAttribute(proof.element, "AXProtectedContent")) as? NSNumber)?.boolValue ?? false
        var settable = DarwinBoolean(false)
        let editable = AXUIElementIsAttributeSettable(proof.element, kAXValueAttribute as CFString, &settable) == .success && settable.boolValue
        guard role == proof.role, computerBackgroundKeyboardEditable(role: role, subrole: subrole, protected: protected, editable: editable),
              [kAXTitleAttribute, kAXDescriptionAttribute, kAXIdentifierAttribute].map({ (try? actionAttribute(proof.element, $0)) as? String ?? "" }) == proof.identity else {
            throw ComputerError("Background keyboard editable field identity changed or is protected; observe again")
        }
        var parent: AXUIElement? = proof.element
        for _ in 0..<128 {
            guard let element = parent else { break }
            if CFEqual(element, target.window) { return }
            guard let value = try? actionAttribute(element, kAXParentAttribute), CFGetTypeID(value) == AXUIElementGetTypeID() else { break }
            parent = unsafeBitCast(value, to: AXUIElement.self)
        }
        throw ComputerError("Background keyboard field no longer belongs to the pinned window")
    }

    private func actionBackgroundKeyboard(_ target: ComputerTarget, code: CGKeyCode, flags: CGEventFlags = [], duration: Double = 0, text: String? = nil) async throws {
        guard let proof = backgroundKeyboardFocus, let source = CGEventSource(stateID: .privateState) else {
            throw ComputerError("Background keyboard requires a fresh screenshot proving the focused editable field; no input was sent")
        }
        let foreground = NSWorkspace.shared.frontmostApplication?.processIdentifier, pointer = CGEvent(source: nil)?.location
        let clipboard = NSPasteboard.general.changeCount
        func check() throws { try actionBackgroundKeyboardCheck(proof, target: target, foreground: foreground, pointer: pointer, clipboard: clipboard) }
        var dispatched = false
        func send(_ units: [UInt16]?) async throws {
            try check()
            if !dispatched {
                guard computerKeyboardTextSnapshot(proof.element) == proof.textSnapshot else { throw ComputerError("Focused text or selection changed since screenshot; no input was sent. Observe again") }
            }
            guard let down = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: true),
                  let up = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: false) else { throw ComputerError("Could not construct PID-scoped keyboard events") }
            down.flags = flags; up.flags = []
            if let units {
                units.withUnsafeBufferPointer {
                    down.keyboardSetUnicodeString(stringLength: units.count, unicodeString: $0.baseAddress)
                    up.keyboardSetUnicodeString(stringLength: units.count, unicodeString: $0.baseAddress)
                }
            }
            down.setIntegerValueField(.keyboardEventAutorepeat, value: 0)
            dispatched = true
            down.postToPid(target.pid)
            defer {
                // Always release on cancellation, but never into a reused PID.
                if !proof.application.isTerminated, computerProcessGeneration(target.pid) == proof.processGeneration { up.postToPid(target.pid) }
            }
            let deadline = ProcessInfo.processInfo.systemUptime + duration
            while ProcessInfo.processInfo.systemUptime < deadline {
                try await Task.sleep(nanoseconds: UInt64(min(0.05, max(0, deadline - ProcessInfo.processInfo.systemUptime)) * 1_000_000_000))
                try check()
            }
        }
        if let text {
            let units = Array(text.utf16)
            var offset = 0
            while offset < units.count {
                var end = min(offset + 16, units.count)
                if end < units.count, (0xD800...0xDBFF).contains(units[end - 1]) { end -= 1 }
                try await send(Array(units[offset..<end])); offset = end
                try await Task.sleep(nanoseconds: 8_000_000)
            }
        } else { try await send(nil) }
        try check()
    }

    private func actionBackgroundLiteral(_ target: ComputerTarget, text: String) async throws {
        guard let proof = backgroundKeyboardFocus, proof.webContent else { throw ComputerError("Background literal insertion needs a proven focused web field") }
        let foreground = NSWorkspace.shared.frontmostApplication?.processIdentifier, pointer = CGEvent(source: nil)?.location
        let clipboard = NSPasteboard.general.changeCount
        func check() throws { try actionBackgroundKeyboardCheck(proof, target: target, foreground: foreground, pointer: pointer, clipboard: clipboard) }
        func selection() throws -> CFRange {
            let value = try actionAttribute(proof.element, kAXSelectedTextRangeAttribute)
            guard CFGetTypeID(value) == AXValueGetTypeID() else { throw ComputerError("Focused field does not expose a text selection") }
            var range = CFRange()
            guard AXValueGetValue(unsafeBitCast(value, to: AXValue.self), .cfRange, &range) else { throw ComputerError("Focused field selection is unreadable") }
            return range
        }
        try check()
        var writable = DarwinBoolean(false)
        guard AXUIElementIsAttributeSettable(proof.element, kAXSelectedTextRangeAttribute as CFString, &writable) == .success, writable.boolValue,
              let before = try actionAttribute(proof.element, kAXValueAttribute) as? String else { throw ComputerError("Background literal insertion needs readable/writable plain text and selection") }
        let beforeRange = try selection(), plan = try actionBackgroundLiteralPlan(value: before, range: beforeRange, text: text, role: proof.role)
        try check()
        let verifiedRange = try selection()
        guard (try actionAttribute(proof.element, kAXValueAttribute) as? String) == before,
              verifiedRange.location == beforeRange.location, verifiedRange.length == beforeRange.length else { throw ComputerError("Focused text or selection changed before background insertion; no input was sent") }
        guard computerKeyboardTextSnapshot(proof.element) == proof.textSnapshot else { throw ComputerError("Focused text or selection changed since screenshot; no input was sent. Observe again") }
        // Adapter selection happens before any key event. This path never
        // follows a failed/uncertain PID dispatch and never replays the write.
        try actionSet(proof.element, kAXValueAttribute, plan.0 as CFString)
        var caret = plan.1
        guard let value = AXValueCreate(.cfRange, &caret) else { throw ComputerError("Background text was written but selection construction failed; outcome is unknown") }
        try actionSet(proof.element, kAXSelectedTextRangeAttribute, value)
        for _ in 0..<10 {
            try check()
            let after = try selection()
            if (try actionAttribute(proof.element, kAXValueAttribute) as? String) == plan.0,
               after.location == plan.1.location, after.length == 0 { return }
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        throw ComputerError("Background literal insertion was dispatched but text/selection read-back differs; outcome is unknown, do not replay")
    }

    private func actionClick(_ args: [String: Any], target: ComputerTarget, observed: ComputerTarget, background: Bool) async throws -> String {
        let count = try actionNumber(args, "clickCount", default: 1)
        let duration = try actionNumber(args, "duration", default: 0)
        guard count >= 1, count <= 3, count.rounded() == count, duration >= 0, duration <= 10 else { throw ComputerError("clickCount must be 1–3 and duration 0–10 seconds") }
        let buttonName = (args["mouseButton"] as? String ?? "left").lowercased()
        guard ["left", "right", "middle"].contains(buttonName) else { throw ComputerError("mouseButton must be left, right or middle") }
        var flags: CGEventFlags = []
        if let key = args["key"] as? String {
            for modifier in key.lowercased().split(separator: "+") {
                guard let flag = actionModifiers[String(modifier)] else { throw ComputerError("Click key must contain only modifier names") }
                flags.insert(flag)
            }
        }
        let element = args["elementID"] == nil ? nil : try actionElement(args, target: target)
        if let element, count == 1, duration == 0, flags.isEmpty, buttonName == "left" {
            try Task.checkCancellation()
            let result = AXUIElementPerformAction(element, kAXPressAction as CFString)
            if result == .success { return "Click dispatched via AXPress" }
            guard result == .actionUnsupported else { throw ComputerError("AXPress failed (\(result.rawValue)); outcome may be unknown. Observe again; input was not replayed.") }
        }
        guard !background else { throw ComputerError("Background AXPress is unsupported by this control; no foreground fallback or global input was sent") }
        let point = try element.map(actionElementCenter) ?? actionPoint(args["coordinate"], target: target, observed: observed)
        guard target.frame.contains(point) else { throw ComputerError("Target point is outside the observed window") }
        try await actionActivate(target)
        let button: CGMouseButton = buttonName == "left" ? .left : buttonName == "right" ? .right : .center
        let downType: CGEventType = button == .left ? .leftMouseDown : button == .right ? .rightMouseDown : .otherMouseDown
        let upType: CGEventType = button == .left ? .leftMouseUp : button == .right ? .rightMouseUp : .otherMouseUp
        for click in 1...Int(count) {
            try actionCheckFocus(target)
            guard let down = CGEvent(mouseEventSource: nil, mouseType: downType, mouseCursorPosition: point, mouseButton: button),
                  let up = CGEvent(mouseEventSource: nil, mouseType: upType, mouseCursorPosition: point, mouseButton: button) else { throw ComputerError("Could not construct mouse events") }
            down.flags = flags; up.flags = flags
            down.setIntegerValueField(.mouseEventClickState, value: Int64(click)); up.setIntegerValueField(.mouseEventClickState, value: Int64(click))
            down.post(tap: .cghidEventTap)
            do { if duration > 0 { try await Task.sleep(nanoseconds: UInt64(duration * 1_000_000_000)) } }
            catch { up.post(tap: .cghidEventTap); throw error }
            up.post(tap: .cghidEventTap)
        }
        return "Click dispatched via foreground global input; the real pointer may move"
    }

    private func actionSelect(_ args: [String: Any], target: ComputerTarget) throws {
        let element = try actionElement(args, target: target)
        guard let value = try actionAttribute(element, kAXValueAttribute) as? String else { throw ComputerError("Text selection requires an exposed string value") }
        var applied = try actionSelectionRange(text: value, target: actionString(args, "text"), prefix: args["prefix"] as? String, suffix: args["suffix"] as? String, selection: args["selection"] as? String ?? "text")
        guard let encoded = AXValueCreate(.cfRange, &applied) else { throw ComputerError("Could not encode selection range") }
        guard (try actionAttribute(element, kAXValueAttribute) as? String) == value else { throw ComputerError("Text changed during selection; query again") }
        try actionSet(element, kAXSelectedTextRangeAttribute, encoded)
        let readback = try actionAttribute(element, kAXSelectedTextRangeAttribute)
        var actual = CFRange()
        guard CFGetTypeID(readback) == AXValueGetTypeID(), AXValueGetValue(unsafeBitCast(readback, to: AXValue.self), .cfRange, &actual), actual.location == applied.location, actual.length == applied.length else {
            throw ComputerError("Text selection could not be verified; observe again, do not replay input")
        }
    }

    private func actionPaste(_ args: [String: Any], target: ComputerTarget) async throws -> String {
        let text = try actionString(args, "text"), format = args["format"] as? String ?? "text"
        guard text.utf16.count <= 2_000_000, ["text", "md", "html"].contains(format) else { throw ComputerError("Paste requires text ≤2000000 UTF-16 units and format text/md/html") }
        let payload = NSPasteboardItem()
        payload.setString(text, forType: .string)
        if format == "html" { payload.setData(computerHTMLPasteData(text), forType: .html) }
        if format == "md" {
            let rendered = try computerMarkdown(text)
            payload.setString(rendered.html, forType: .html)
            payload.setString(rendered.text, forType: .string)
        }
        try await actionActivate(target)
        let clipboard = NSPasteboard.general
        let previous = try (clipboard.pasteboardItems ?? []).map { item -> NSPasteboardItem in
            let saved = NSPasteboardItem()
            for type in item.types {
                guard let data = item.data(forType: type), saved.setData(data, forType: type) else { throw ComputerError("Original clipboard could not be preserved; no paste was dispatched") }
            }
            return saved
        }
        clipboard.clearContents()
        guard clipboard.writeObjects([payload]) else {
            clipboard.clearContents()
            if !previous.isEmpty { _ = clipboard.writeObjects(previous) }
            throw ComputerError("Could not prepare pasteboard; no paste was dispatched")
        }
        let written = clipboard.changeCount
        func restore() -> String {
            guard clipboard.changeCount == written else { return "a concurrent copy was preserved" }
            clipboard.clearContents()
            return previous.isEmpty || clipboard.writeObjects(previous) ? "original pasteboard restored" : "pasteboard restoration failed; do not replay input"
        }
        do {
            try actionCheckFocus(target)
            try await actionKeyboard(9, flags: .maskCommand)
            // The target consumes the pasteboard asynchronously. Keep it available
            // across event delivery; the host's next observation checks actual effect.
            try await Task.sleep(nanoseconds: 300_000_000)
        } catch {
            throw ComputerError("Paste outcome may be unknown: \(error.localizedDescription). \(restore()). Observe again; do not replay input.")
        }
        return "Paste dispatched via foreground Command-V (\(format)); \(restore())"
    }

    func perform(_ args: [String: Any]) async throws -> [String: Any] {
        try requirePermissions(screenCapture: false)
        let app = try actionString(args, "app")
        guard let observed = target, observed.app == app else { throw ComputerError("Observe this application before performing an action") }
        let current = try await resolveTarget(app: app, windowID: observed.windowID)
        guard current.pid == observed.pid, current.windowID == observed.windowID else { throw ComputerError("Application process or window changed; observe again") }
        guard let action = args["action"] as? [String: Any], action.count == 1, let operation = action.keys.first,
              let input = action[operation] as? [String: Any] else { throw ComputerError("action must contain exactly one named operation object") }
        let fields: [String: Set<String>] = [
            "click": ["elementID", "coordinate", "clickCount", "mouseButton", "key", "duration"],
            "performSecondaryAction": ["elementID", "action"], "setValue": ["elementID", "value", "autosubmitSearchFields"],
            "selectText": ["elementID", "text", "prefix", "suffix", "selection"],
            "scroll": ["elementID", "coordinate", "direction", "pages"], "drag": ["from", "to"],
            "pressKey": ["key", "duration"], "type": ["text"], "paste": ["text", "format"],
        ]
        guard let allowed = fields[operation], Set(input.keys).isSubset(of: allowed) else { throw ComputerError("Unsupported action or action argument") }
        let background = try actionBackgroundMode(args["interactionMode"], operation: operation, input: input)
        for key in ["elementID", "mouseButton", "key", "prefix", "suffix", "selection", "format"] where input[key] != nil {
            guard input[key] is String else { throw ComputerError("\(key) must be a string") }
        }
        if let value = input["autosubmitSearchFields"] {
            guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else { throw ComputerError("autosubmitSearchFields must be a boolean") }
        }
        if input["elementID"] != nil, input["coordinate"] != nil { throw ComputerError("Choose an elementID or coordinate, not both") }
        defer { invalidate() }
        let foreground = NSWorkspace.shared.frontmostApplication?.processIdentifier
        let pointer = CGEvent(source: nil)?.location
        let clipboardGeneration = background ? NSPasteboard.general.changeCount : nil
        var receipt = "\(operation) dispatched"
        switch operation {
        case "click": receipt = try await actionClick(input, target: current, observed: observed, background: background)
        case "performSecondaryAction":
            let element = try actionElement(input, target: current), name = try actionString(input, "action")
            var available: CFArray?
            guard AXUIElementCopyActionNames(element, &available) == .success, (available as? [String] ?? []).contains(name) else { throw ComputerError("Requested secondary action is not exposed by this control") }
            try Task.checkCancellation()
            let status = AXUIElementPerformAction(element, name as CFString)
            guard status == .success else { throw ComputerError("Secondary action failed (\(status.rawValue)); outcome may be unknown") }
        case "setValue":
            let element = try actionElement(input, target: current), value = try actionString(input, "value")
            try actionSet(element, kAXValueAttribute, value as CFString)
            guard (try actionAttribute(element, kAXValueAttribute) as? String) == value else { throw ComputerError("Set value was dispatched but read-back differs; observe again") }
            if input["autosubmitSearchFields"] as? Bool == true {
                let subrole = (try? actionAttribute(element, kAXSubroleAttribute)) as? String
                guard subrole == "AXSearchField" else { throw ComputerError("Value was set, but autosubmit requires an AXSearchField; no submit key was sent") }
                try await actionActivate(current)
                try actionSet(element, kAXFocusedAttribute, kCFBooleanTrue)
                try await actionKeyboard(36, flags: [])
            }
        case "selectText": try actionSelect(input, target: current); receipt = "Text selection applied and UTF-16 range verified"
        case "pressKey":
            let (code, flags) = try actionKey(actionString(input, "key"))
            let duration = try actionNumber(input, "duration", default: 0)
            guard duration >= 0, duration <= 10 else { throw ComputerError("duration must be 0–10 seconds") }
            if background {
                try await actionBackgroundKeyboard(current, code: code, flags: flags, duration: duration)
                receipt += " via public PID-scoped background keyboard input"
            } else {
                try await actionActivate(current); try await actionKeyboard(code, flags: flags, duration: duration)
                receipt += " via foreground global keyboard input"
            }
        case "type":
            let text = try actionString(input, "text")
            guard text.utf16.count <= 2_000_000 else { throw ComputerError("Text exceeds 2000000 UTF-16 units") }
            if background {
                if backgroundKeyboardFocus?.webContent == true {
                    try await actionBackgroundLiteral(current, text: text)
                    receipt += " via AX literal insertion; focused plain value and UTF-16 selection verified, not keyboard event delivery"
                } else {
                    try await actionBackgroundKeyboard(current, code: 0, text: text)
                    receipt += " via public PID-scoped background Unicode keyboard input; actual consumption must be observed"
                }
                break
            }
            try await actionActivate(current)
            let textUnits = Array(text.utf16)
            var offset = 0
            while offset < textUnits.count {
                try actionCheckFocus(current)
                var end = min(offset + 16, textUnits.count)
                if end < textUnits.count, (0xD800...0xDBFF).contains(textUnits[end - 1]) { end -= 1 }
                let units = Array(textUnits[offset..<end])
                guard let down = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true), let up = CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false) else { throw ComputerError("Could not construct Unicode events; input may be partial") }
                units.withUnsafeBufferPointer { buffer in
                    down.keyboardSetUnicodeString(stringLength: units.count, unicodeString: buffer.baseAddress)
                    up.keyboardSetUnicodeString(stringLength: units.count, unicodeString: buffer.baseAddress)
                }
                down.flags = []; up.flags = []; down.post(tap: .cghidEventTap); up.post(tap: .cghidEventTap)
                offset = end
                if offset % 1024 == 0 { try Task.checkCancellation(); await Task.yield() }
            }
            receipt += " via foreground Unicode keyboard events; input may be partial until observed"
        case "paste": receipt = try await actionPaste(input, target: current)
        case "scroll":
            let direction = try actionString(input, "direction"), pages = try actionNumber(input, "pages", default: 1)
            guard ["up", "down", "left", "right"].contains(direction), pages > 0, pages <= 100 else { throw ComputerError("Scroll needs a direction up/down/left/right and pages >0 and ≤100") }
            let point: CGPoint
            if input["elementID"] != nil { point = try actionElementCenter(actionElement(input, target: current)) }
            else if input["coordinate"] != nil { point = try actionPoint(input["coordinate"], target: current, observed: observed) }
            else { point = CGPoint(x: current.frame.midX, y: current.frame.midY) }
            guard current.frame.contains(point) else { throw ComputerError("Scroll target is outside the observed window") }
            let vertical = direction == "up" || direction == "down"
            let amount = min(Double(Int32.max), (vertical ? current.frame.height : current.frame.width) * pages)
            let delta = Int32(amount.rounded()) * ((direction == "down" || direction == "right") ? -1 : 1)
            guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 2, wheel1: vertical ? delta : 0, wheel2: vertical ? 0 : delta, wheel3: 0) else { throw ComputerError("Could not construct scroll event") }
            try await actionActivate(current); try actionCheckFocus(current); event.flags = []; event.location = point; event.post(tap: .cghidEventTap)
            receipt += " via foreground global scroll input"
        case "drag":
            let from = try actionPoint(input["from"], target: current, observed: observed), to = try actionPoint(input["to"], target: current, observed: observed)
            try await actionActivate(current)
            guard let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: from, mouseButton: .left), let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: to, mouseButton: .left) else { throw ComputerError("Could not construct drag events") }
            down.flags = []; up.flags = []
            try actionCheckFocus(current)
            down.post(tap: .cghidEventTap)
            do {
                for step in 1...20 {
                    try actionCheckFocus(current)
                    let progress = Double(step) / 20
                    let point = CGPoint(x: from.x + (to.x - from.x) * progress, y: from.y + (to.y - from.y) * progress)
                    guard let event = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left) else { throw ComputerError("Could not construct drag event; outcome may be partial") }
                    event.flags = []; event.post(tap: .cghidEventTap); try await Task.sleep(nanoseconds: 10_000_000)
                }
            } catch { up.post(tap: .cghidEventTap); throw error }
            up.post(tap: .cghidEventTap)
            receipt += " via foreground global mouse input; the real pointer may move"
        default: throw ComputerError("Unsupported action: \(operation)")
        }
        if background {
            try await Task.sleep(nanoseconds: 50_000_000)
            guard NSWorkspace.shared.frontmostApplication?.processIdentifier == foreground,
                  CGEvent(source: nil)?.location == pointer,
                  NSPasteboard.general.changeCount == clipboardGeneration else {
                throw ComputerError("Background action was dispatched but foreground, pointer or clipboard changed. Outcome is unknown; do not replay or restore focus automatically")
            }
            let method = operation == "pressKey" || (operation == "type" && backgroundKeyboardFocus?.webContent != true) ? "PID keyboard" : operation == "type" ? "AX literal" : "AX"
            receipt += " in strict \(method) background mode; foreground, pointer and clipboard unchanged at read-back"
        }
        return nativeText("\(receipt). Dispatch is not proof of the intended application result; observe again before continuing. Previous element IDs are invalid.")
    }
}
