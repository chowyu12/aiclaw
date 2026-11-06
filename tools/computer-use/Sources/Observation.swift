import AppKit
import ApplicationServices
import ScreenCaptureKit

private func observationAttribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var value: CFTypeRef?
    return AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success ? value : nil
}

private func observationElement(_ element: AXUIElement, _ name: String) -> AXUIElement? {
    guard let value = observationAttribute(element, name), CFGetTypeID(value) == AXUIElementGetTypeID() else { return nil }
    return unsafeBitCast(value, to: AXUIElement.self)
}

private func observationString(_ element: AXUIElement, _ name: String) -> String {
    guard let value = observationAttribute(element, name) else { return "" }
    if let text = value as? String { return text }
    if let number = value as? NSNumber { return number.stringValue }
    return ""
}

private func observationFrame(_ element: AXUIElement) -> CGRect? {
    guard let position = observationAttribute(element, kAXPositionAttribute),
          let size = observationAttribute(element, kAXSizeAttribute),
          CFGetTypeID(position) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero, dimensions = CGSize.zero
    guard AXValueGetValue(unsafeBitCast(position, to: AXValue.self), .cgPoint, &point),
          AXValueGetValue(unsafeBitCast(size, to: AXValue.self), .cgSize, &dimensions),
          dimensions.width.isFinite, dimensions.height.isFinite,
          point.x.isFinite, point.y.isFinite else { return nil }
    return CGRect(origin: point, size: dimensions)
}

private func observationSameFrame(_ first: CGRect, _ second: CGRect) -> Bool {
    abs(first.minX - second.minX) < 2 && abs(first.minY - second.minY) < 2 &&
    abs(first.width - second.width) < 2 && abs(first.height - second.height) < 2
}

private func observationFrameJSON(_ frame: CGRect) -> [String: Double] {
    ["x": frame.minX, "y": frame.minY, "width": frame.width, "height": frame.height]
}

private func observationInteger(_ args: [String: Any], _ name: String, default fallback: Int, maximum: Int) throws -> Int {
    guard let raw = args[name] else { return fallback }
    guard let number = raw as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
          number.doubleValue.isFinite, number.doubleValue.rounded() == number.doubleValue,
          number.doubleValue >= 1, number.doubleValue <= Double(maximum) else {
        throw ComputerError("\(name) must be an integer between 1 and \(maximum)")
    }
    return number.intValue
}

private func observationWindowID(_ args: [String: Any]) throws -> UInt32? {
    guard args["window_id"] != nil else { return nil }
    let value = try observationInteger(args, "window_id", default: 1, maximum: Int(UInt32.max))
    return UInt32(value)
}

private func observationBool(_ args: [String: Any], _ name: String, default fallback: Bool) throws -> Bool {
    guard let value = args[name] else { return fallback }
    guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else {
        throw ComputerError("\(name) must be a boolean")
    }
    return number.boolValue
}

private func observationChildren(_ element: AXUIElement, maximum: Int) -> ([AXUIElement], Bool) {
    var count: CFIndex = 0
    guard AXUIElementGetAttributeValueCount(element, kAXChildrenAttribute as CFString, &count) == .success, count > 0 else { return ([], false) }
    guard maximum > 0 else { return ([], true) }
    var values: CFArray?
    let requested = min(count, maximum)
    guard AXUIElementCopyAttributeValues(element, kAXChildrenAttribute as CFString, 0, requested, &values) == .success,
          let children = values as? [AXUIElement] else { return ([], true) }
    return (children, requested < count)
}

private func observationActions(_ element: AXUIElement) -> [String] {
    var names: CFArray?
    guard AXUIElementCopyActionNames(element, &names) == .success else { return [] }
    return names as? [String] ?? []
}

private func observationValueSettable(_ element: AXUIElement) -> Bool {
    var settable = DarwinBoolean(false)
    return AXUIElementIsAttributeSettable(element, kAXValueAttribute as CFString, &settable) == .success && settable.boolValue
}

private struct ObservationNode {
    let element: AXUIElement
    let depth: Int
}

extension ComputerSession {
    func resolveTarget(app: String, windowID: UInt32? = nil) async throws -> ComputerTarget {
        try requirePermissions(screenCapture: false)
        let identifier = try validateComputerApp(app)
        let apps = NSRunningApplication.runningApplications(withBundleIdentifier: identifier).filter { !$0.isTerminated }
        guard apps.count == 1, let running = apps.first, running.processIdentifier != getpid() else {
            throw ComputerError("The exact target app must already be running as one process; use start_app or select an unambiguous bundle identifier")
        }
        let application = AXUIElementCreateApplication(running.processIdentifier)
        AXUIElementSetMessagingTimeout(application, 0.2)
        guard let windows = observationAttribute(application, kAXWindowsAttribute) as? [AXUIElement], !windows.isEmpty else {
            throw ComputerError("Target app exposes no accessible windows")
        }
        let metadata = (CGWindowListCopyWindowInfo(.optionAll, kCGNullWindowID) as? [[String: Any]] ?? []).filter {
            ($0[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == running.processIdentifier &&
            ($0[kCGWindowLayer as String] as? NSNumber)?.intValue == 0
        }
        var candidates: [(AXUIElement, CGRect, CGWindowID)] = []
        for window in windows {
            guard let frame = observationFrame(window), frame.width > 0, frame.height > 0 else { continue }
            let title = observationString(window, kAXTitleAttribute)
            for entry in metadata {
                guard let id = entry[kCGWindowNumber as String] as? NSNumber,
                      windowID == nil || windowID == id.uint32Value,
                      let bounds = entry[kCGWindowBounds as String] as? [String: Any],
                      let cgFrame = CGRect(dictionaryRepresentation: bounds as CFDictionary), observationSameFrame(frame, cgFrame) else { continue }
                if let name = entry[kCGWindowName as String] as? String, !name.isEmpty, !title.isEmpty, name != title { continue }
                candidates.append((window, frame, id.uint32Value))
            }
        }
        if windowID == nil {
            if let focused = observationElement(application, kAXFocusedWindowAttribute) ?? observationElement(application, kAXMainWindowAttribute) {
                candidates = candidates.filter { CFEqual($0.0, focused) }
            }
        }
        guard candidates.count == 1, let (window, frame, id) = candidates.first else {
            throw ComputerError("Cannot uniquely align the accessible window with a native window ID; choose a window_id from app discovery and observe again")
        }
        return ComputerTarget(app: identifier, pid: running.processIdentifier, windowID: id, frame: frame, window: window)
    }

    func resolveElement(_ id: Int, target current: ComputerTarget) throws -> AXUIElement {
        guard let previous = target, previous.pid == current.pid, previous.windowID == current.windowID,
              previous.app == current.app, observationSameFrame(previous.frame, current.frame),
              let element = elements[id], let role = elementRoles[id] else {
            throw ComputerError("Element observation is stale or belongs to another window; observe or query again")
        }
        var pid: pid_t = 0
        guard AXUIElementGetPid(element, &pid) == .success, pid == current.pid,
              observationString(element, kAXRoleAttribute) == role,
              elementIdentities[id] == [kAXTitleAttribute, kAXDescriptionAttribute, kAXIdentifierAttribute].map({ observationString(element, $0) }) else {
            throw ComputerError("Element process or role changed; observe or query again")
        }
        var ancestor: AXUIElement? = element
        for _ in 0..<128 {
            guard let node = ancestor else { break }
            if CFEqual(node, current.window) { return element }
            ancestor = observationElement(node, kAXParentAttribute)
        }
        throw ComputerError("Element no longer belongs to the observed window; observe or query again")
    }

    private func beginObservation(_ current: ComputerTarget) {
        if target?.pid != current.pid || target?.windowID != current.windowID || target.map({ !observationSameFrame($0.frame, current.frame) }) == true { imageSize = nil }
        elements.removeAll()
        elementRoles.removeAll()
        elementIdentities.removeAll()
        backgroundKeyboardFocus = nil
        backgroundKeyboardUnavailableReason = nil
        epoch += 1
        target = current
    }

    private func keyboardFocusProof(_ current: ComputerTarget, completeAX: Bool) -> ComputerKeyboardFocusProof? {
        func unavailable(_ reason: String) -> ComputerKeyboardFocusProof? { backgroundKeyboardUnavailableReason = reason; return nil }
        guard let running = NSRunningApplication(processIdentifier: current.pid), !running.isTerminated, let generation = computerProcessGeneration(current.pid),
              running.bundleIdentifier == current.app else { return unavailable("process_generation_unavailable") }
        let application = AXUIElementCreateApplication(current.pid)
        if let focusedWindow = observationElement(application, kAXFocusedWindowAttribute), !CFEqual(focusedWindow, current.window) { return unavailable("different_focused_window") }
        let element: AXUIElement, byAttribute: Bool
        if let focused = observationElement(application, kAXFocusedUIElementAttribute) { element = focused; byAttribute = false }
        else {
            guard completeAX else { return unavailable("focused_field_unavailable") }
            // Some frameworks omit the application-level focused element but
            // expose the public keyboard-focus flag on the actual AX node.
            // A complete observation must prove exactly one eligible node.
            let candidates = elements.values.filter { candidate in
                guard let focused = observationAttribute(candidate, kAXFocusedAttribute) as? NSNumber,
                      CFGetTypeID(focused) == CFBooleanGetTypeID(), focused.boolValue else { return false }
                return computerBackgroundKeyboardEditable(role: observationString(candidate, kAXRoleAttribute),
                    subrole: observationString(candidate, kAXSubroleAttribute),
                    protected: (observationAttribute(candidate, "AXProtectedContent") as? NSNumber)?.boolValue ?? false,
                    editable: observationValueSettable(candidate))
            }
            guard candidates.count == 1, let candidate = candidates.first else { return unavailable(candidates.isEmpty ? "focused_field_unavailable" : "ambiguous_focused_fields") }
            element = candidate; byAttribute = true
        }
        let role = observationString(element, kAXRoleAttribute), subrole = observationString(element, kAXSubroleAttribute)
        let protected = (observationAttribute(element, "AXProtectedContent") as? NSNumber)?.boolValue ?? false
        guard computerBackgroundKeyboardEditable(role: role, subrole: subrole, protected: protected, editable: observationValueSettable(element)) else { return unavailable("field_not_editable_or_protected") }
        var parent: AXUIElement? = element, belongs = false, webContent = false
        for _ in 0..<128 {
            guard let node = parent else { break }
            if observationString(node, kAXRoleAttribute) == "AXWebArea" { webContent = true }
            if CFEqual(node, current.window) { belongs = true; break }
            parent = observationElement(node, kAXParentAttribute)
        }
        guard belongs else { return unavailable("focused_field_outside_window") }
        guard let textSnapshot = computerKeyboardTextSnapshot(element) else { return unavailable("text_or_selection_unavailable_or_too_large") }
        return ComputerKeyboardFocusProof(target: current, application: running, processGeneration: generation, focusByAttribute: byAttribute, webContent: webContent, element: element, role: role,
            identity: [kAXTitleAttribute, kAXDescriptionAttribute, kAXIdentifierAttribute].map { observationString(element, $0) }, textSnapshot: textSnapshot)
    }

    private func retainObservationElement(_ element: AXUIElement, role: String) -> Int {
        let id = nextElementID
        nextElementID += 1
        elements[id] = element
        elementRoles[id] = role
        elementIdentities[id] = [kAXTitleAttribute, kAXDescriptionAttribute, kAXIdentifierAttribute].map { observationString(element, $0) }
        return id
    }

    private func screenshot(_ current: ComputerTarget) async throws -> [String: Any] {
        try requirePermissions(screenCapture: true)
        let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
        guard let window = content.windows.first(where: { $0.windowID == current.windowID && $0.owningApplication?.processID == current.pid }) else {
            throw ComputerError("The selected window is no longer capturable; observe again")
        }
        let config = SCStreamConfiguration()
        let scale = min(1, 1280 / max(current.frame.width, current.frame.height))
        config.width = max(1, Int((current.frame.width * scale).rounded()))
        config.height = max(1, Int((current.frame.height * scale).rounded()))
        config.showsCursor = false
        config.ignoreShadowsSingleWindow = true
        let image = try await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(desktopIndependentWindow: window), configuration: config)
        guard let latest = observationFrame(current.window), observationSameFrame(current.frame, latest) else {
            throw ComputerError("Window moved during capture; observe again")
        }
        guard let png = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]) else { throw ComputerError("Failed to encode window screenshot") }
        imageSize = CGSize(width: image.width, height: image.height)
        return ["type": "image", "mimeType": "image/png", "data": png.base64EncodedString()]
    }

    func observe(_ args: [String: Any]) async throws -> [String: Any] {
        guard let app = args["app"] as? String else { throw ComputerError("app is required") }
        let includeAX = try observationBool(args, "include_ax", default: true)
        let includeScreenshot = try observationBool(args, "include_screenshot", default: true)
        guard includeAX || includeScreenshot else { throw ComputerError("Observe must include accessibility or screenshot data") }
        let maximum = try observationInteger(args, "max_tree_nodes", default: 1200, maximum: 5000)
        let depthLimit = try observationInteger(args, "max_tree_depth", default: 64, maximum: 128)
        let textLimit = args["text_limit"] as? String == "max" ? 100_000 : try observationInteger(args, "text_limit", default: 500, maximum: 100_000)
        do {
            let current = try await resolveTarget(app: app, windowID: observationWindowID(args))
            beginObservation(current)
            var image: [String: Any]?
            var lines: [String] = [], truncated = false, renderedUnits = 0
            if includeAX {
                var queue = [ObservationNode(element: current.window, depth: 0)], offset = 0
                var seen: [CFHashCode: [AXUIElement]] = [:]
                while offset < queue.count {
                    let node = queue[offset]; offset += 1
                    let hash = CFHash(node.element)
                    if seen[hash, default: []].contains(where: { CFEqual($0, node.element) }) { continue }
                    seen[hash, default: []].append(node.element)
                    AXUIElementSetMessagingTimeout(node.element, 0.1)
                    let role = observationString(node.element, kAXRoleAttribute)
                    if !role.isEmpty {
                        let id = retainObservationElement(node.element, role: role)
                        let title = observationString(node.element, kAXTitleAttribute)
                        let description = observationString(node.element, kAXDescriptionAttribute)
                        let value = observationString(node.element, kAXValueAttribute)
                        let summary = [title, description, value].filter { !$0.isEmpty }.joined(separator: " | ")
                        if summary.utf16.count > textLimit { truncated = true }
                        let remaining = max(0, 250_000 - renderedUnits)
                        if remaining == 0 { truncated = true; break }
                        if summary.utf16.count > remaining { truncated = true }
                        let bounded = String(decoding: summary.utf16.prefix(min(textLimit, remaining)), as: UTF16.self).replacingOccurrences(of: "\n", with: "\\n").replacingOccurrences(of: "\r", with: "\\r")
                        let actions = observationActions(node.element).joined(separator: ",")
                        let details = "settable=\(observationValueSettable(node.element)) actions=[\(actions)]"
                        let line = String(repeating: "  ", count: node.depth) + "[\(id)] \(role) \(bounded) \(details)"
                        lines.append(line)
                        renderedUnits += line.utf16.count
                    }
                    let children = observationChildren(node.element, maximum: max(0, maximum - queue.count))
                    if children.1 { truncated = true }
                    if node.depth < depthLimit { queue.append(contentsOf: children.0.map { ObservationNode(element: $0, depth: node.depth + 1) }) }
                    else if !children.0.isEmpty { truncated = true }
                    if offset % 32 == 0 { try Task.checkCancellation(); await Task.yield() }
                }
            }
            if includeScreenshot {
                let before = keyboardFocusProof(current, completeAX: includeAX && !truncated)
                image = try await screenshot(current)
                let after = keyboardFocusProof(current, completeAX: includeAX && !truncated)
                if let before, let after, CFEqual(before.element, after.element),
                   before.processGeneration == after.processGeneration, before.target.pid == after.target.pid,
                   before.target.windowID == after.target.windowID, before.target.frame == after.target.frame,
                   before.role == after.role, before.identity == after.identity, before.textSnapshot == after.textSnapshot {
                    backgroundKeyboardFocus = after
                } else if before != nil || after != nil { backgroundKeyboardUnavailableReason = "focus_changed_during_capture" }
            }
            var metadata: [String: Any] = ["app": current.app, "app_name": NSRunningApplication(processIdentifier: current.pid)?.localizedName ?? current.app, "pid": current.pid, "window_id": current.windowID,
                "window_frame": observationFrameJSON(current.frame), "image_width": imageSize?.width ?? 0,
                "image_height": imageSize?.height ?? 0, "truncated": truncated, "background_keyboard_ready": backgroundKeyboardFocus != nil]
            if let focus = backgroundKeyboardFocus {
                metadata["background_keyboard_focus"] = ["role": focus.role, "source": focus.focusByAttribute ? "unique_observed_AXFocused" : "AXFocusedUIElement", "web_content": focus.webContent, "identity": focus.identity.map { String(decoding: $0.utf16.prefix(500), as: UTF16.self) }]
            }
            if let reason = backgroundKeyboardUnavailableReason { metadata["background_keyboard_unavailable"] = reason }
            let data = try JSONSerialization.data(withJSONObject: metadata, options: [.sortedKeys])
            var content: [[String: Any]] = [["type": "text", "text": String(decoding: data, as: UTF8.self) + "\n" + lines.joined(separator: "\n")]]
            if let image { content.append(image) }
            return ["content": content]
        } catch { invalidate(); throw error }
    }

    func query(_ args: [String: Any]) async throws -> [String: Any] {
        guard let app = args["app"] as? String else { throw ComputerError("app is required") }
        for key in ["text", "role"] where args[key] != nil && !(args[key] is String) { throw ComputerError("\(key) must be a string") }
        let text = (args["text"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let wantedRole = (args["role"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty || !wantedRole.isEmpty else { throw ComputerError("query requires text or role") }
        guard text.utf16.count <= 1000 else { throw ComputerError("query text exceeds 1000 UTF-16 code units") }
        let exact = try observationBool(args, "exact", default: false)
        let limit = try observationInteger(args, "limit", default: 20, maximum: 100)
        let maximum = try observationInteger(args, "max_nodes", default: 500, maximum: 5000)
        do {
            let current = try await resolveTarget(app: app, windowID: observationWindowID(args))
            beginObservation(current)
            let deadline = ProcessInfo.processInfo.systemUptime + 2
            var queue = [ObservationNode(element: current.window, depth: 0)], offset = 0
            var seen: [CFHashCode: [AXUIElement]] = [:], matches: [[String: Any]] = []
            var truncated = false, reason = "complete", visited = 0
            while offset < queue.count {
                if ProcessInfo.processInfo.systemUptime >= deadline { truncated = true; reason = "deadline"; break }
                let node = queue[offset]; offset += 1
                let hash = CFHash(node.element)
                if seen[hash, default: []].contains(where: { CFEqual($0, node.element) }) { continue }
                seen[hash, default: []].append(node.element)
                visited += 1
                AXUIElementSetMessagingTimeout(node.element, 0.1)
                let role = observationString(node.element, kAXRoleAttribute)
                let source = [observationString(node.element, kAXTitleAttribute), observationString(node.element, kAXDescriptionAttribute), observationString(node.element, kAXValueAttribute)]
                if source.contains(where: { $0.utf16.count > 1000 }) { truncated = true; reason = "text_limit" }
                let values = source.map { String(decoding: $0.utf16.prefix(1000), as: UTF16.self) }
                let textMatches = text.isEmpty || values.enumerated().contains { index, value in
                    exact ? source[index].utf16.count <= 1000 && value.compare(text, options: [.caseInsensitive]) == .orderedSame : value.range(of: text, options: [.caseInsensitive]) != nil
                }
                if !role.isEmpty && (wantedRole.isEmpty || wantedRole == role) && textMatches {
                    let id = retainObservationElement(node.element, role: role)
                    var match: [String: Any] = ["index": id, "role": role, "title": values[0], "description": values[1], "value": values[2],
                        "settable": observationValueSettable(node.element), "actions": observationActions(node.element)]
                    if let frame = observationFrame(node.element) { match["frame"] = observationFrameJSON(frame) }
                    matches.append(match)
                    if matches.count == limit { truncated = true; reason = "limit"; break }
                }
                let children = observationChildren(node.element, maximum: max(0, maximum - queue.count))
                if children.1 { truncated = true; reason = "max_nodes" }
                if node.depth < 128 { queue.append(contentsOf: children.0.map { ObservationNode(element: $0, depth: node.depth + 1) }) }
                else if !children.0.isEmpty { truncated = true; reason = "max_depth" }
                if offset % 32 == 0 { try Task.checkCancellation(); await Task.yield() }
            }
            return try nativeJSON(["app": current.app, "app_name": NSRunningApplication(processIdentifier: current.pid)?.localizedName ?? current.app, "pid": current.pid, "matches": matches, "truncated": truncated, "stop_reason": reason,
                "visited_nodes": visited, "window_id": current.windowID, "window_frame": observationFrameJSON(current.frame),
                "limit": limit, "max_nodes": maximum, "image_width": imageSize?.width ?? 0, "image_height": imageSize?.height ?? 0])
        } catch { invalidate(); throw error }
    }
}
