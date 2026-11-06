import AppKit
import ApplicationServices
import CoreImage
import CoreMedia
import CryptoKit
import ScreenCaptureKit

struct NativeCaptureOptions {
    let app: String
    let windowID: UInt32?
    let intervalMilliseconds: Int

    init(_ args: [String: Any]) throws {
        guard let app = args["app"] as? String, !app.isEmpty else { throw ComputerError("Capture requires an exact app identifier") }
        self.app = app
        func integer(_ name: String, fallback: Int, minimum: Int, maximum: Int) throws -> Int {
            guard let raw = args[name] else { return fallback }
            guard let value = raw as? NSNumber, CFGetTypeID(value) != CFBooleanGetTypeID(),
                  value.doubleValue.isFinite, value.doubleValue.rounded() == value.doubleValue,
                  value.doubleValue >= Double(minimum), value.doubleValue <= Double(maximum) else {
                throw ComputerError("\(name) is outside the capture limits")
            }
            return value.intValue
        }
        windowID = args["window_id"] == nil ? nil : UInt32(try integer("window_id", fallback: 1, minimum: 1, maximum: Int(UInt32.max)))
        intervalMilliseconds = try integer("interval_ms", fallback: 1000, minimum: 500, maximum: 5000)
    }
}

func nativeCaptureSize(_ frame: CGRect) throws -> CGSize {
    guard frame.size.width.isFinite, frame.size.height.isFinite, frame.size.width > 0, frame.size.height > 0 else { throw ComputerError("Capture window geometry is invalid") }
    let scale = min(1, 1280 / max(frame.width, frame.height))
    return CGSize(width: max(1, (frame.width * scale).rounded()), height: max(1, (frame.height * scale).rounded()))
}

// The stream has three native buffers and at most one frame being encoded/read
// back by the main actor. Dropped frames are never queued for later delivery.
private final class NativeCaptureOutput: NSObject, SCStreamOutput, SCStreamDelegate {
    let context = CIContext(options: [.cacheIntermediates: false])
    var onFrame: ((Data, CGSize) -> Void)?
    var onFailure: ((Error) -> Void)?
    private let lock = NSLock()
    private var pending = false
    private var closed = false
    private var previousHash: SHA256.Digest?

    func completeFrame() { lock.lock(); pending = false; lock.unlock() }
    func close() { lock.lock(); closed = true; lock.unlock() }

    func stream(_ stream: SCStream, didStopWithError error: Error) { onFailure?(error) }

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of outputType: SCStreamOutputType) {
        guard outputType == .screen, sampleBuffer.isValid,
              let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
              let rawStatus = attachments.first?[.status] as? Int, let status = SCFrameStatus(rawValue: rawStatus) else { return }
        // Some streams begin with a temporary blank/started frame. Startup's
        // bounded first-frame wait handles that; never reuse an old image once
        // an established capture becomes blank or suspended.
        if status == .blank && previousHash == nil { return }
        if status == .blank || status == .suspended || status == .stopped {
            lock.lock(); let notify = !closed; closed = true; lock.unlock()
            if notify { onFailure?(ComputerError("Pinned window capture became unavailable or suspended; start a new capture")) }; return
        }
        guard status == .complete, let buffer = sampleBuffer.imageBuffer else { return }
        lock.lock()
        guard !closed, !pending else { lock.unlock(); return }
        pending = true; lock.unlock()
        let size = CGSize(width: CVPixelBufferGetWidth(buffer), height: CVPixelBufferGetHeight(buffer))
        guard size.width <= 1280, size.height <= 1280,
              let image = context.createCGImage(CIImage(cvPixelBuffer: buffer), from: CGRect(origin: .zero, size: size)),
              let data = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]), data.count <= 8 * 1024 * 1024 else {
            completeFrame(); onFailure?(ComputerError("Native capture frame exceeds the bounded image format")); return
        }
        let digest = SHA256.hash(data: data)
        if previousHash == digest { completeFrame(); return }
        previousHash = digest
        onFrame?(data, size)
    }
}

private let nativeCaptureAXCallback: AXObserverCallback = { _, element, notification, context in
    guard let context else { return }
    let capture = Unmanaged<NativeWindowCapture>.fromOpaque(context).takeUnretainedValue()
    // This source is registered exclusively on CFRunLoopGetMain(). Avoid a
    // separate unbounded Task allocation for every accessibility notification.
    MainActor.assumeIsolated { capture.accessibilityChanged(element, notification as String) }
}

/// A connection-owned, pinned-window stream. It never supplies action authority
/// and must be stopped on pause/disconnect; next() must bypass the input queue.
@MainActor
final class NativeWindowCapture {
    let id = UUID().uuidString
    private(set) var sequence = 0
    private(set) var stopped = false
    private let options: NativeCaptureOptions
    private let observation = ComputerSession()
    private var target: ComputerTarget
    private let running: NSRunningApplication
    private var stream: SCStream?
    private var output: NativeCaptureOutput?
    private var observer: AXObserver?
    private var registered: [(AXUIElement, String)] = []
    private var editableRegistrations: [(AXUIElement, String)] = []
    private var terminationObserver: NSObjectProtocol?
    private var axTask: Task<Void, Never>?
    private var axEventCount = 0
    private var image: (data: Data, size: CGSize)?
    private var result: [String: Any]?
    private var snapshotHash: SHA256.Digest?
    private var failure: Error?
    private var waiting: CheckedContinuation<[String: Any], Error>?
    private var waitingID: UUID?
    private var waitTimeout: Task<Void, Never>?
    private var refreshing = false
    private var refreshAgain = false
    private var imageGeneration = 0

    private init(options: NativeCaptureOptions, target: ComputerTarget, running: NSRunningApplication) {
        self.options = options; self.target = target; self.running = running
    }

    static func start(_ args: [String: Any]) async throws -> NativeWindowCapture {
        let options = try NativeCaptureOptions(args)
        _ = try validateComputerApp(options.app)
        try requirePermissions()
        let target = try await ComputerSession().resolveTarget(app: options.app, windowID: options.windowID)
        guard let running = NSRunningApplication(processIdentifier: target.pid), !running.isTerminated else { throw ComputerError("Capture target exited before startup") }
        let capture = NativeWindowCapture(options: options, target: target, running: running)
        do {
            try await capture.begin()
            try Task.checkCancellation()
            _ = try await capture.next(afterSequence: 0, waitMilliseconds: 2000)
            return capture
        } catch { await capture.stop(); throw error }
    }

    private func configuration(for frame: CGRect) throws -> SCStreamConfiguration {
        let size = try nativeCaptureSize(frame), config = SCStreamConfiguration()
        config.width = Int(size.width); config.height = Int(size.height)
        config.minimumFrameInterval = CMTime(value: Int64(options.intervalMilliseconds), timescale: 1000)
        config.queueDepth = 3; config.showsCursor = false; config.capturesAudio = false
        config.pixelFormat = kCVPixelFormatType_32BGRA; config.ignoreShadowsSingleWindow = true
        return config
    }

    private func begin() async throws {
        let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: false)
        guard let window = content.windows.first(where: { $0.windowID == target.windowID && $0.owningApplication?.processID == target.pid }) else { throw ComputerError("Pinned capture window is unavailable") }
        let output = NativeCaptureOutput()
        output.onFrame = { [weak self, weak output] data, size in
            Task { @MainActor in
                defer { output?.completeFrame() }
                guard let self, !self.stopped else { return }
                do { try await self.receivedFrame(data, size) } catch { self.fail(error) }
            }
        }
        output.onFailure = { [weak self] error in Task { @MainActor in self?.fail(error) } }
        let stream = SCStream(filter: SCContentFilter(desktopIndependentWindow: window), configuration: try configuration(for: target.frame), delegate: output)
        try stream.addStreamOutput(output, type: .screen, sampleHandlerQueue: DispatchQueue(label: "aiclaw.capture.\(id)", qos: .utility))
        self.output = output; self.stream = stream
        var observer: AXObserver?
        guard AXObserverCreate(target.pid, nativeCaptureAXCallback, &observer) == .success, let observer else { throw ComputerError("Cannot create the native accessibility observer") }
        self.observer = observer
        CFRunLoopAddSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(observer), .commonModes)
        let app = AXUIElementCreateApplication(target.pid)
        for name in [kAXFocusedUIElementChangedNotification, kAXWindowCreatedNotification] { register(app, name, editable: false) }
        for name in [kAXMovedNotification, kAXResizedNotification, kAXTitleChangedNotification, kAXUIElementDestroyedNotification, kAXLayoutChangedNotification] { register(target.window, name, editable: false) }
        terminationObserver = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didTerminateApplicationNotification, object: nil, queue: .main) { [weak self] note in
            guard let application = note.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
            Task { @MainActor in if let self, application.processIdentifier == self.target.pid { self.fail(ComputerError("Pinned capture application exited")) } }
        }
        try await stream.startCapture()
    }

    private func register(_ element: AXUIElement, _ name: String, editable: Bool) {
        guard let observer, AXObserverAddNotification(observer, element, name as CFString, Unmanaged.passUnretained(self).toOpaque()) == .success else { return }
        if editable { editableRegistrations.append((element, name)) } else { registered.append((element, name)) }
    }

    private func updateEditableObservers() {
        guard let observer else { return }
        for (element, name) in editableRegistrations { AXObserverRemoveNotification(observer, element, name as CFString) }
        editableRegistrations.removeAll()
        let roles: Set<String> = ["AXTextField", "AXTextArea", "AXComboBox", "AXCheckBox", "AXSlider", "AXPopUpButton"]
        for key in observation.elements.keys.sorted().filter({ roles.contains(observation.elementRoles[$0] ?? "") }).prefix(64) {
            guard let element = observation.elements[key] else { continue }
            register(element, kAXValueChangedNotification, editable: true)
            register(element, kAXSelectedTextChangedNotification, editable: true)
        }
    }

    private func validateTarget() async throws -> ComputerTarget {
        guard !stopped else { throw failure ?? ComputerError("Native capture stopped") }
        guard !running.isTerminated else { throw ComputerError("Pinned capture application exited") }
        try requirePermissions()
        let current = try await observation.resolveTarget(app: target.app, windowID: target.windowID)
        try Task.checkCancellation()
        guard !stopped else { throw failure ?? ComputerError("Native capture stopped") }
        guard current.pid == target.pid, current.windowID == target.windowID, CFEqual(current.window, target.window) else { throw ComputerError("Pinned capture target changed; start a new capture") }
        return current
    }

    private func receivedFrame(_ data: Data, _ size: CGSize) async throws {
        let current = try await validateTarget(), expected = try nativeCaptureSize(current.frame)
        if expected != size {
            try await stream?.updateConfiguration(configuration(for: current.frame)); return
        }
        target = current; image = (data, size); imageGeneration += 1
        try await refresh()
    }

    fileprivate func accessibilityChanged(_ element: AXUIElement, _ notification: String) {
        guard !stopped else { return }
        if notification == kAXUIElementDestroyedNotification && CFEqual(element, target.window) { fail(ComputerError("Pinned capture window closed")); return }
        axEventCount += 1
        guard axTask == nil else { return }
        axTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(self?.options.intervalMilliseconds ?? 1000) * 1_000_000)
            guard let self, !Task.isCancelled, !self.stopped else { return }
            self.axTask = nil
            do {
                let current = try await self.validateTarget()
                if current.frame.size != self.target.frame.size {
                    try await self.stream?.updateConfiguration(self.configuration(for: current.frame))
                    self.image = nil
                }
                self.target = current
                try await self.refresh()
            } catch { self.fail(error) }
        }
    }

    private func refresh() async throws {
        if refreshing { refreshAgain = true; return }
        refreshing = true
        defer {
            refreshing = false
            if refreshAgain && !stopped {
                refreshAgain = false
                Task { @MainActor in do { try await self.refresh() } catch { self.fail(error) } }
            }
        }
        guard let image, !stopped else { return }
        let generation = imageGeneration
        let baseline = try await observation.observe(["app": target.app, "window_id": target.windowID, "include_ax": true, "include_screenshot": false, "text_limit": "max", "max_tree_nodes": 800])
        if generation != imageGeneration { refreshAgain = true; return }
        guard !stopped, let content = baseline["content"] as? [[String: Any]], let text = content.first?["text"] as? String,
              let separator = text.firstIndex(of: "\n"), var metadata = try JSONSerialization.jsonObject(with: Data(text[..<separator].utf8)) as? [String: Any] else { return }
        let current = try await validateTarget()
        guard try nativeCaptureSize(current.frame) == image.size else { return }
        target = current
        let lines = String(text[text.index(after: separator)...])
        let normalized = lines.replacingOccurrences(of: "(?m)^(\\s*)\\[\\d+\\] ", with: "$1", options: .regularExpression)
        let frame: [String: Double] = ["x": target.frame.minX, "y": target.frame.minY, "width": target.frame.width, "height": target.frame.height]
        let geometry = try JSONSerialization.data(withJSONObject: frame, options: [.sortedKeys])
        let digest = SHA256.hash(data: image.data + Data(normalized.utf8) + geometry)
        updateEditableObservers()
        guard digest != snapshotHash else { return }
        snapshotHash = digest; sequence += 1
        metadata["window_frame"] = frame; metadata["image_width"] = image.size.width; metadata["image_height"] = image.size.height
        metadata["capture_id"] = id; metadata["capture_sequence"] = sequence; metadata["capture_backend"] = "screencapturekit-stream"
        metadata["ax_event_count"] = axEventCount; metadata["ax_observer_registrations"] = registered.count + editableRegistrations.count
        let header = try JSONSerialization.data(withJSONObject: metadata, options: [.sortedKeys])
        result = ["content": [["type": "text", "text": String(decoding: header, as: UTF8.self) + "\n" + lines], ["type": "image", "mimeType": "image/png", "data": image.data.base64EncodedString()]]]
        completeWait()
    }

    func next(afterSequence: Int, waitMilliseconds: Int) async throws -> [String: Any] {
        try Task.checkCancellation()
        guard afterSequence >= 0, afterSequence <= sequence, (0...10_000).contains(waitMilliseconds) else { throw ComputerError("Capture wait arguments are outside their limits") }
        guard !stopped else { throw failure ?? ComputerError("Native capture stopped") }
        guard waiting == nil else { throw ComputerError("Only one native capture wait may be active") }
        do {
            _ = try await validateTarget()
            if sequence > afterSequence || waitMilliseconds == 0 {
                guard let result else { throw ComputerError("Native capture has not produced a frame") }; return result
            }
            let requestID = UUID()
            let update = try await withTaskCancellationHandler(operation: {
                try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<[String: Any], Error>) in
                    waiting = continuation; waitingID = requestID
                    waitTimeout = Task { @MainActor [weak self] in
                        try? await Task.sleep(nanoseconds: UInt64(waitMilliseconds) * 1_000_000)
                        guard !Task.isCancelled else { return }; self?.completeWait(expectedID: requestID)
                    }
                }
            }, onCancel: { Task { @MainActor [weak self] in self?.completeWait(error: CancellationError(), expectedID: requestID) } })
            // A quiet stream may wake only on timeout. Never return its cached
            // frame after target loss or a permission change during that wait.
            _ = try await validateTarget()
            return update
        } catch is CancellationError { throw CancellationError() }
        catch { fail(error); throw error }
    }

    private func completeWait(error: Error? = nil, expectedID: UUID? = nil) {
        if let expectedID, waitingID != expectedID { return }
        guard let continuation = waiting else { return }
        waiting = nil; waitingID = nil; waitTimeout?.cancel(); waitTimeout = nil
        if let error { continuation.resume(throwing: error) }
        else if let result { continuation.resume(returning: result) }
        else { continuation.resume(throwing: ComputerError("Native stream did not deliver its initial frame")) }
    }

    private func fail(_ error: Error) {
        guard !stopped else { return }
        cancel(error)
        Task { @MainActor in await stop() }
    }

    /// Synchronous invalidation precedes awaiting the native stream shutdown.
    func cancel(_ error: Error = ComputerError("Native capture stopped")) {
        guard !stopped else { return }
        stopped = true; failure = error; output?.close(); axTask?.cancel(); axTask = nil
        if let observer {
            for (element, name) in registered + editableRegistrations { AXObserverRemoveNotification(observer, element, name as CFString) }
            CFRunLoopRemoveSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(observer), .commonModes)
        }
        observer = nil; registered.removeAll(); editableRegistrations.removeAll()
        if let terminationObserver { NSWorkspace.shared.notificationCenter.removeObserver(terminationObserver) }
        terminationObserver = nil; image = nil; result = nil; observation.invalidate()
        completeWait(error: error)
    }

    func stop() async {
        cancel()
        let stream = stream, output = output
        self.stream = nil; self.output = nil
        try? await stream?.stopCapture()
        if let stream, let output { try? stream.removeStreamOutput(output, type: .screen) }
    }
}
