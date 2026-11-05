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

    private func actionClick(_ args: [String: Any], target: ComputerTarget, observed: ComputerTarget) async throws -> String {
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
        for key in ["elementID", "mouseButton", "key", "prefix", "suffix", "selection", "format"] where input[key] != nil {
            guard input[key] is String else { throw ComputerError("\(key) must be a string") }
        }
        if let value = input["autosubmitSearchFields"] {
            guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else { throw ComputerError("autosubmitSearchFields must be a boolean") }
        }
        if input["elementID"] != nil, input["coordinate"] != nil { throw ComputerError("Choose an elementID or coordinate, not both") }
        defer { invalidate() }
        var receipt = "\(operation) dispatched"
        switch operation {
        case "click": receipt = try await actionClick(input, target: current, observed: observed)
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
            try await actionActivate(current); try await actionKeyboard(code, flags: flags, duration: duration)
            receipt += " via foreground global keyboard input"
        case "type":
            let text = try actionString(input, "text")
            guard text.utf16.count <= 2_000_000 else { throw ComputerError("Text exceeds 2000000 UTF-16 units") }
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
        return nativeText("\(receipt). Dispatch is not proof of the intended application result; observe again before continuing. Previous element IDs are invalid.")
    }
}
