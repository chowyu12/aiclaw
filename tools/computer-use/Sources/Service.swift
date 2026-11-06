import AppKit
import ApplicationServices
import CoreServices
import CryptoKit
import Darwin
import Foundation

private let protocolVersion = "AIClawComputerUseIPC-1"
private let frameLimit = 16 * 1024 * 1024

@MainActor
private final class DesktopService: NSObject, NSApplicationDelegate {
    private var listener: UnixListener?
    private var operations: OrderedOperations!
    private let captures = CaptureOperations()
    private let processExecutableSHA256: String
    private let migrationOnly = CommandLine.arguments.contains("--migration-only")
    private var migrationDirectory: URL?
    private var migrationHasHost = false

    init(executableURL: URL) throws {
        processExecutableSHA256 = SHA256.hash(data: try Data(contentsOf: executableURL)).map { String(format: "%02x", $0) }.joined()
        super.init()
        operations = OrderedOperations(service: self)
    }

    func enqueue(_ request: NativeRequest) {
        if migrationOnly && !["ComputerUseIPCHandshakeRequest", "ComputerUseIPCLegacyMigrationRequest"].contains(request.type) {
            Task { @MainActor in
                defer { request.connection.release() }
                guard request.connection.isOpen else { return }
                do { try await request.connection.send(["id": request.id, "error": ["message": "The migration-only helper cannot perform desktop operations"]]) }
                catch { request.connection.close() }
            }
            return
        }
        if migrationOnly { migrationHasHost = true }
        if CaptureOperations.handles(request.type) { captures.enqueue(request) }
        else { operations.enqueue(request) }
    }
    func disconnected(_ connection: NativeConnection) {
        operations.cancel(connection); captures.cancel(connection)
        if migrationOnly { NSApp.terminate(nil) }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        let arguments = CommandLine.arguments
        guard let index = arguments.firstIndex(of: "--socket"), arguments.indices.contains(index + 1) else {
            NSApp.terminate(nil)
            return
        }
        do {
            guard !migrationOnly || computerHostAuthenticationProfile == "production" else { throw LegacyMigrationError.unverified }
            migrationDirectory = URL(fileURLWithPath: arguments[index + 1]).deletingLastPathComponent()
            listener = try UnixListener(path: arguments[index + 1], service: self)
            listener?.start()
            if migrationOnly {
                Task { @MainActor [weak self] in
                    try? await Task.sleep(nanoseconds: 10_000_000_000)
                    guard !Task.isCancelled, let self, !self.migrationHasHost else { return }
                    NSApp.terminate(nil)
                }
            }
        } catch {
            // Never log requests, application titles, screenshots or user input.
            fputs("AIClaw Computer Use could not start its local IPC listener.\n", stderr)
            NSApp.terminate(nil)
        }
    }

    func applicationWillTerminate(_ notification: Notification) { listener?.close(); operations.cancelAll(); captures.cancelAll() }

    func dispatch(_ type: String, args: [String: Any], session: ComputerSession) async throws -> [String: Any] {
        if migrationOnly && !["ComputerUseIPCHandshakeRequest", "ComputerUseIPCLegacyMigrationRequest"].contains(type) {
            throw ComputerError("The migration-only helper cannot perform desktop operations")
        }
        switch type {
        case "ComputerUseIPCHandshakeRequest":
            return ["protocolVersion": protocolVersion, "serverVersion": "1", "processExecutableSHA256": processExecutableSHA256, "tools": migrationOnly ? [] : try toolManifest(), "capabilities": ["nativeCapture": !migrationOnly, "migrationOnly": migrationOnly]]
        case "ComputerUseIPCLegacyMigrationRequest":
            guard migrationOnly, computerHostAuthenticationProfile == "production", let migrationDirectory else { throw LegacyMigrationError.unverified }
            return try await LegacyComputerMigration.retireIdle(in: migrationDirectory)
        case "ComputerUseIPCPermissionStatusRequest", "ComputerUseIPCPermissionSetupRequest":
            let accessibility = AXIsProcessTrusted(), capture = CGPreflightScreenCaptureAccess()
            let showSetup = type == "ComputerUseIPCPermissionSetupRequest" && (!accessibility || !capture)
            if showSetup { PermissionWindow.shared.show() }
            return try nativeJSON(["accessibilityTrusted": accessibility, "screenCaptureGranted": capture, "setupWindowShown": showSetup])
        case "ComputerUseIPCListAppsRequest", "ComputerUseIPCFindAppsRequest":
            let inventory = try discover(args)
            for entry in inventory["apps"] as? [[String: Any]] ?? [] {
                if let id = entry["id"] as? String, let path = entry["path"] as? String {
                    session.discoveredApplications[id] = URL(fileURLWithPath: path)
                }
            }
            return try nativeJSON(inventory)
        case "ComputerUseIPCAppStartRequest":
            let app = try appIdentifier(args)
            session.invalidate()
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.activates = false
            guard let url = session.discoveredApplications[app] ?? NSWorkspace.shared.urlForApplication(withBundleIdentifier: app) else {
                throw ComputerError("Application bundle identifier is not registered with LaunchServices")
            }
            guard Bundle(url: url)?.bundleIdentifier == app else { throw ComputerError("Discovered application identity changed; discover it again before launch") }
            let running: NSRunningApplication = try await withCheckedThrowingContinuation { continuation in
                NSWorkspace.shared.openApplication(at: url, configuration: configuration) { running, error in
                    if let error { continuation.resume(throwing: error) }
                    else if let running { continuation.resume(returning: running) }
                    else { continuation.resume(throwing: ComputerError("Launch was requested but no running application was returned")) }
                }
            }
            return try nativeJSON(["app": app, "pid": running.processIdentifier, "is_running": !running.isTerminated, "verified": !running.isTerminated, "message": "Application process was launched. Observe its window before input; launch is not proof of the intended UI state."])
        case "ComputerUseIPCAppStopRequest":
            let app = try appIdentifier(args)
            session.invalidate()
            let running = NSRunningApplication.runningApplications(withBundleIdentifier: app)
            var accepted = true
            for application in running { if !application.terminate() { accepted = false } }
            for _ in 0..<30 {
                if NSRunningApplication.runningApplications(withBundleIdentifier: app).isEmpty {
                    return try nativeJSON(["app": app, "is_running": false, "verified": true, "quit_requested": accepted])
                }
                try await Task.sleep(nanoseconds: 100_000_000)
            }
            return try nativeJSON(["app": app, "is_running": true, "verified": false, "quit_requested": accepted, "message": "Normal quit was requested but exit is unconfirmed. The app may be awaiting a save dialog. Observe it; no force termination was performed."])
        case "ComputerUseIPCAppModifyRequest":
            let app = try appIdentifier(args)
            session.invalidate()
            guard let running = NSRunningApplication.runningApplications(withBundleIdentifier: app).first else { throw ComputerError("Application is not running") }
            let operation = args["modification"] as? String ?? "activate"
            let accepted: Bool
            switch operation {
            case "activate": accepted = running.activate(options: [])
            case "deactivate": accepted = running.hide()
            default: throw ComputerError("App modification requires modification activate or deactivate")
            }
            return try nativeJSON(["app": app, "modification": operation, "accepted": accepted, "is_active": running.isActive, "is_hidden": running.isHidden, "delivery": operation == "deactivate" ? "hide" : "activate"])
        case "ComputerUseIPCFrontmostWindowRequest":
            try requirePermissions(screenCapture: false)
            guard let frontmost = NSWorkspace.shared.frontmostApplication, let app = frontmost.bundleIdentifier else { return try nativeJSON(["window": NSNull()]) }
            _ = try appIdentifier(["app": app])
            let target = try await session.resolveTarget(app: app)
            return try nativeJSON(["app": app, "pid": target.pid, "window_id": target.windowID, "frame": ["x": target.frame.minX, "y": target.frame.minY, "width": target.frame.width, "height": target.frame.height]])
        case "ComputerUseIPCAppGetSkyshotRequest":
            try requirePermissions(screenCapture: false)
            _ = try appIdentifier(args)
            return try await session.observe(args)
        case "ComputerUseIPCAppQueryRequest":
            try requirePermissions(screenCapture: false)
            _ = try appIdentifier(args)
            return try await session.query(args)
        case "ComputerUseIPCAppPerformActionRequest":
            try requirePermissions(screenCapture: false)
            _ = try appIdentifier(args)
            return try await session.perform(args)
        case "ComputerUseIPCCodexTurnEndedRequest":
            session.invalidate()
            return nativeText("Turn observations cleared")
        default: throw ComputerError("Unsupported Computer Use IPC request")
        }
    }

    private func toolManifest() throws -> [[String: Any]] {
        guard let path = Bundle.main.url(forResource: "Tools", withExtension: "json"),
              let specs = try JSONSerialization.jsonObject(with: Data(contentsOf: path)) as? [[String: Any]] else {
            throw ComputerError("Computer Use tool manifest is missing or invalid")
        }
        return specs.compactMap { spec in
            if let platforms = spec["platforms"] as? [String], !platforms.contains("darwin") { return nil }
            guard let name = spec["action"] as? String, var schema = spec["schema"] as? [String: Any] else { return nil }
            if var properties = schema["properties"] as? [String: Any] { properties.removeValue(forKey: "observation_id"); properties.removeValue(forKey: "verify_after"); schema["properties"] = properties }
            if let required = schema["required"] as? [String] { schema["required"] = required.filter { $0 != "observation_id" } }
            return ["name": name, "inputSchema": schema]
        }
    }

    private func discover(_ args: [String: Any]) throws -> [String: Any] {
        let text = (args["text"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let limit = (args["limit"] as? Int) ?? 100
        guard (1...100).contains(limit) else { throw ComputerError("Application discovery limit must be 1–100") }
        var apps: [String: [String: Any]] = [:]
        let windows = (CGWindowListCopyWindowInfo([.optionAll, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []).filter {
            ($0[kCGWindowLayer as String] as? NSNumber)?.intValue == 0
        }
        func add(_ url: URL, running: NSRunningApplication? = nil) {
            guard let bundle = Bundle(url: url), let id = bundle.bundleIdentifier else { return }
            let name = running?.localizedName ?? bundle.object(forInfoDictionaryKey: "CFBundleDisplayName") as? String ?? bundle.object(forInfoDictionaryKey: "CFBundleName") as? String ?? id
            if !text.isEmpty && !name.lowercased().contains(text) && !id.lowercased().contains(text) { return }
            let existing = apps[id]?["is_running"] as? Bool ?? false
            var record: [String: Any] = ["id": id, "display_name": name, "path": url.path, "is_running": running != nil || existing]
            if let running {
                record["windows"] = windows.compactMap { entry -> [String: Any]? in
                    guard (entry[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == running.processIdentifier,
                          let windowID = entry[kCGWindowNumber as String] as? NSNumber,
                          let bounds = entry[kCGWindowBounds as String] as? [String: Any],
                          let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary) else { return nil }
                    return ["window_id": windowID.uint32Value, "title": entry[kCGWindowName as String] as? String ?? "", "frame": ["x": frame.minX, "y": frame.minY, "width": frame.width, "height": frame.height]]
                }
            }
            apps[id] = record
        }
        // Spotlight is a bounded source, not proof that unindexed apps are absent.
        let query = NSMetadataQuery()
        query.searchScopes = [NSMetadataQueryLocalComputerScope]
        query.predicate = NSPredicate(format: "kMDItemContentType == %@", "com.apple.application-bundle")
        let deadline = Date().addingTimeInterval(2)
        var indexComplete = false
        if query.start() {
            while query.isGathering && Date() < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
            query.disableUpdates()
            indexComplete = !query.isGathering
            for item in query.results {
                if let item = item as? NSMetadataItem, let path = item.value(forAttribute: kMDItemPath as String) as? String { add(URL(fileURLWithPath: path)) }
            }
            query.stop()
        }
        for running in NSWorkspace.shared.runningApplications { if let url = running.bundleURL { add(url, running: running) } }
        let matches = apps.values.sorted {
            let left = $0["is_running"] as? Bool ?? false, right = $1["is_running"] as? Bool ?? false
            if left != right { return left }
            return ($0["display_name"] as? String ?? "") < ($1["display_name"] as? String ?? "")
        }
        return ["apps": Array(matches.prefix(limit)), "total": matches.count, "truncated": matches.count > limit || !indexComplete, "index_complete": indexComplete, "source": "Bounded Spotlight application index plus NSWorkspace running applications; unindexed applications may be absent"]
    }
}

@MainActor
private func appIdentifier(_ args: [String: Any]) throws -> String {
    guard let app = args["app"] as? String else { throw ComputerError("An exact application bundle identifier is required") }
    return try validateComputerApp(app)
}

private struct NativeRequest {
    let id: Any
    let type: String
    let arguments: [String: Any]
    let connection: NativeConnection
}

// The reader keeps consuming EOF while a request is queued or awaiting an AX/
// capture operation. Closing a peer immediately cancels its active task. The
// descriptor stays alive until all jobs/writes release the connection object.
// Mutable connection state is guarded by lock; session is MainActor-only.
private final class NativeConnection: @unchecked Sendable {
    let id = UUID()
    let descriptor: Int32
    private let lock = NSLock()
    private var closed = false
    private var outstanding = 0
    private var task: Task<Void, Never>?
    private var captureTask: Task<Void, Never>?
    private let writes = DispatchQueue(label: "aiclaw.computer-use.responses", qos: .userInitiated)
    @MainActor let session = ComputerSession()

    @MainActor init(_ descriptor: Int32) {
        self.descriptor = descriptor
        var timeout = timeval(tv_sec: 5, tv_usec: 0)
        setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
    }

    deinit { Darwin.close(descriptor) }

    var isOpen: Bool { lock.lock(); defer { lock.unlock() }; return !closed }

    func reserve() -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard !closed && outstanding < 16 else { return false }
        outstanding += 1
        return true
    }

    func release() { lock.lock(); outstanding -= 1; lock.unlock() }

    func setTask(_ value: Task<Void, Never>?) {
        lock.lock(); task = value; let cancel = closed; lock.unlock()
        if cancel { value?.cancel() }
    }

    func setCaptureTask(_ value: Task<Void, Never>?) {
        lock.lock(); captureTask = value; let cancel = closed; lock.unlock()
        if cancel { value?.cancel() }
    }

    func close() {
        lock.lock()
        guard !closed else { lock.unlock(); return }
        closed = true
        let active = task
        let capture = captureTask
        lock.unlock()
        active?.cancel()
        capture?.cancel()
        Darwin.shutdown(descriptor, SHUT_RDWR)
    }

    func send(_ object: [String: Any]) async throws {
        var data = try JSONSerialization.data(withJSONObject: object)
        guard data.count <= frameLimit else { throw ComputerError("IPC response exceeds the frame limit") }
        data.append(10)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            writes.async { [self] in
                let sent = data.withUnsafeBytes { bytes -> Bool in
                    var offset = 0
                    while offset < bytes.count {
                        guard isOpen else { return false }
                        let count = Darwin.send(descriptor, bytes.baseAddress!.advanced(by: offset), bytes.count - offset, 0)
                        if count < 0 && errno == EINTR { continue }
                        if count <= 0 { return false }
                        offset += count
                    }
                    return true
                }
                if sent { continuation.resume() }
                else { continuation.resume(throwing: ComputerError("IPC peer disconnected or stopped reading")) }
            }
        }
    }
}

// Stream waits run independently of the globally ordered input lane. Each
// connection owns one stream and one active start/next; stop can interrupt it.
@MainActor
private final class CaptureOperations {
    private final class State {
        var capture: NativeWindowCapture?
        var active: Task<Void, Never>?
        var activeID: UUID?
        var cleanup: Task<Void, Never>?
        var stopping = false
    }
    // Entries include streams still starting or stopping, so asynchronous
    // startup cannot race past the global resource limit.
    private var states: [UUID: State] = [:]

    static func handles(_ type: String) -> Bool {
        ["ComputerUseIPCAppStartCaptureRequest", "ComputerUseIPCAppNextCaptureUpdateRequest", "ComputerUseIPCAppStopCaptureRequest"].contains(type)
    }

    func enqueue(_ request: NativeRequest) {
        let connection = request.connection
        guard connection.isOpen else { connection.release(); return }
        do {
            switch request.type {
            case "ComputerUseIPCAppStartCaptureRequest":
                for (connectionID, existing) in states where existing.capture?.stopped == true && !existing.stopping {
                    _ = cleanup(existing, connectionID: connectionID)
                }
                guard states[connection.id] == nil else { throw ComputerError("Stop the connection's existing capture before starting another") }
                guard states.count < 4 else { throw ComputerError("Native capture limit reached (4 streams, including startup and shutdown)") }
                let state = State()
                states[connection.id] = state
                run(request, state: state) {
                    let capture = try await NativeWindowCapture.start(request.arguments)
                    guard !Task.isCancelled, connection.isOpen, !state.stopping else {
                        await capture.stop()
                        throw CancellationError()
                    }
                    state.capture = capture
                    return try await capture.next(afterSequence: 0, waitMilliseconds: 0)
                }
            case "ComputerUseIPCAppNextCaptureUpdateRequest":
                let state = try state(for: request)
                guard !state.stopping, state.active == nil, let capture = state.capture else { throw ComputerError("Capture is starting, stopping or already has a pending update request") }
                let after = try integer(request.arguments, "after_sequence", fallback: 0, maximum: Int.max)
                let wait = try integer(request.arguments, "wait_ms", fallback: 10_000, maximum: 10_000)
                run(request, state: state) { try await capture.next(afterSequence: after, waitMilliseconds: wait) }
            case "ComputerUseIPCAppStopCaptureRequest":
                let state = try state(for: request, allowStarting: true)
                let captureID = state.capture?.id
                let stopped = cleanup(state, connectionID: connection.id)
                Task { @MainActor in
                    defer { connection.release() }
                    await stopped.value
                    guard connection.isOpen else { return }
                    do { try await connection.send(["id": request.id, "result": try nativeJSON(["stopped": true, "capture_id": captureID.map { $0 as Any } ?? NSNull()])]) }
                    catch { connection.close() }
                }
            default: throw ComputerError("Unsupported native capture request")
            }
        } catch {
            Task { @MainActor in
                defer { connection.release() }
                guard connection.isOpen else { return }
                do { try await connection.send(["id": request.id, "error": ["message": error.localizedDescription]]) }
                catch { connection.close() }
            }
        }
    }

    private func state(for request: NativeRequest, allowStarting: Bool = false) throws -> State {
        guard let state = states[request.connection.id] else { throw ComputerError("No native capture belongs to this connection") }
        if allowStarting && state.capture == nil && request.arguments["capture_id"] == nil { return state }
        guard let id = request.arguments["capture_id"] as? String, id == state.capture?.id else { throw ComputerError("Capture identifier does not belong to this connection") }
        return state
    }

    private func integer(_ args: [String: Any], _ name: String, fallback: Int, maximum: Int) throws -> Int {
        guard let raw = args[name] else { return fallback }
        guard let value = raw as? NSNumber, CFGetTypeID(value) != CFBooleanGetTypeID(), value.doubleValue.isFinite,
              value.doubleValue.rounded() == value.doubleValue, value.doubleValue >= 0,
              value.doubleValue <= Double(maximum), value.doubleValue < Double(Int.max) else { throw ComputerError("Invalid native capture \(name)") }
        return value.intValue
    }

    private func run(_ request: NativeRequest, state: State, operation: @escaping @MainActor () async throws -> [String: Any]) {
        let connection = request.connection, taskID = UUID()
        state.activeID = taskID
        let task = Task { @MainActor in
            defer {
                if state.activeID == taskID {
                    state.active = nil; state.activeID = nil
                    connection.setCaptureTask(nil)
                }
                if state.capture?.stopped == true && !state.stopping && states[connection.id] === state {
                    _ = cleanup(state, connectionID: connection.id)
                }
                if state.capture == nil && !state.stopping && states[connection.id] === state { states.removeValue(forKey: connection.id) }
                connection.release()
            }
            let response: [String: Any]
            do {
                try Task.checkCancellation()
                let result = try await operation()
                try Task.checkCancellation()
                response = ["id": request.id, "result": result]
            } catch {
                response = ["id": request.id, "error": ["message": error is CancellationError ? "Native capture request cancelled" : error.localizedDescription]]
            }
            guard connection.isOpen else { return }
            do { try await connection.send(response) }
            catch { connection.close() }
        }
        state.active = task
        connection.setCaptureTask(task)
    }

    private func cleanup(_ state: State, connectionID: UUID) -> Task<Void, Never> {
        if let cleanup = state.cleanup { return cleanup }
        state.stopping = true
        state.active?.cancel()
        state.capture?.cancel()
        let active = state.active
        let task = Task { @MainActor in
            await state.capture?.stop()
            await active?.value
            await state.capture?.stop()
            if states[connectionID] === state { states.removeValue(forKey: connectionID) }
        }
        state.cleanup = task
        return task
    }

    func cancel(_ connection: NativeConnection) {
        if let state = states[connection.id] { _ = cleanup(state, connectionID: connection.id) }
    }

    func cancelAll() {
        for (connectionID, state) in states { _ = cleanup(state, connectionID: connectionID) }
    }
}

// MainActor alone is reentrant at every await. This queue owns the complete
// request lifetime, including action sleeps and read-back, across every client.
@MainActor
private final class OrderedOperations {
    private weak var service: DesktopService?
    private var pending: [NativeRequest] = []
    private var active: (connection: UUID, task: Task<Void, Never>)?

    init(service: DesktopService) { self.service = service }

    func enqueue(_ request: NativeRequest) {
        guard request.connection.isOpen else { request.connection.release(); return }
        pending.append(request)
        drain()
    }

    func cancel(_ connection: NativeConnection) {
        pending.removeAll { request in
            if request.connection.id == connection.id { request.connection.release(); return true }
            return false
        }
        if active?.connection == connection.id { active?.task.cancel() }
        connection.session.invalidate()
    }

    func cancelAll() {
        for request in pending { request.connection.close(); request.connection.release() }
        pending.removeAll()
        active?.task.cancel()
    }

    private func drain() {
        guard active == nil else { return }
        while !pending.isEmpty {
            let request = pending.removeFirst()
            let connection = request.connection
            guard connection.isOpen else { connection.release(); continue }
            let task = Task { @MainActor [weak self] in
                guard let self else { connection.release(); return }
                defer {
                    connection.setTask(nil)
                    connection.release()
                    self.active = nil
                    self.drain()
                }
                do {
                    try Task.checkCancellation()
                    guard connection.isOpen, let service = self.service else { throw CancellationError() }
                    let response: [String: Any]
                    do {
                        response = ["id": request.id, "result": try await service.dispatch(request.type, args: request.arguments, session: connection.session)]
                    } catch is CancellationError { throw CancellationError() }
                    catch {
                        var failure: [String: Any] = ["message": error.localizedDescription]
                        if let migration = error as? LegacyMigrationError { failure["code"] = migration.code }
                        response = ["id": request.id, "error": failure]
                    }
                    try Task.checkCancellation()
                    guard connection.isOpen else { throw CancellationError() }
                    try await connection.send(response)
                } catch {
                    connection.close()
                    self.cancel(connection)
                }
            }
            active = (connection.id, task)
            connection.setTask(task)
            return
        }
    }
}

private final class UnixListener {
    private var descriptor: Int32
    private let path: String
    private var socketInode: ino_t = 0
    private weak var service: DesktopService?
    private let slots = DispatchSemaphore(value: 32)
    private let lock = NSLock()
    private var connections: [UUID: NativeConnection] = [:]

    @MainActor
    init(path: String, service: DesktopService) throws {
        let bytes = Array(path.utf8)
        guard path.hasPrefix("/"), bytes.count < 104 else { throw ComputerError("IPC socket path must be absolute and shorter than 104 bytes") }
        let parent = URL(fileURLWithPath: path).deletingLastPathComponent().path
        try FileManager.default.createDirectory(atPath: parent, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var info = stat()
        guard lstat(parent, &info) == 0, info.st_uid == geteuid(), (info.st_mode & S_IFMT) == S_IFDIR, (info.st_mode & 0o022) == 0 else { throw ComputerError("IPC socket directory must be an owned, non-writable-by-others directory") }
        if lstat(path, &info) == 0 {
            guard info.st_uid == geteuid(), (info.st_mode & S_IFMT) == S_IFSOCK else { throw ComputerError("Refusing to replace an unowned or non-socket IPC path") }
            let probe = socket(AF_UNIX, SOCK_STREAM, 0)
            var address = Self.address(path)
            let result = withUnsafePointer(to: &address) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(probe, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
            let refused = errno == ECONNREFUSED
            Darwin.close(probe)
            guard result < 0 && refused else { throw ComputerError("IPC socket is already in use") }
            guard unlink(path) == 0 else { throw ComputerError("Cannot remove stale IPC socket") }
        }
        descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw ComputerError("Cannot create IPC socket") }
        self.path = path
        self.service = service
        var address = Self.address(path)
        let bound = withUnsafePointer(to: &address) { pointer in pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
        guard bound == 0 else {
            Darwin.close(descriptor)
            throw ComputerError("Cannot bind local IPC socket")
        }
        guard chmod(path, 0o600) == 0, listen(descriptor, 16) == 0, lstat(path, &info) == 0 else {
            Darwin.close(descriptor)
            unlink(path)
            throw ComputerError("Cannot secure local IPC socket")
        }
        socketInode = info.st_ino
    }

    private static func address(_ path: String) -> sockaddr_un {
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.initializeMemory(as: UInt8.self, repeating: 0)
            raw.copyBytes(from: Array(path.utf8) + [0])
        }
        return address
    }

    func start() {
        signal(SIGPIPE, SIG_IGN)
        DispatchQueue.global(qos: .userInitiated).async { [self] in
            while true {
                let client = accept(descriptor, nil, nil)
                if client < 0 { if errno == EINTR { continue }; return }
                guard ComputerPeerAuthentication.accepts(client), slots.wait(timeout: .now()) == .success else { Darwin.close(client); continue }
                let ready = DispatchSemaphore(value: 0)
                var connection: NativeConnection?
                DispatchQueue.main.async { connection = NativeConnection(client); ready.signal() }
                ready.wait()
                guard let connection else { Darwin.close(client); slots.signal(); continue }
                lock.lock(); connections[connection.id] = connection; lock.unlock()
                DispatchQueue.global(qos: .userInitiated).async { [self] in read(connection) }
            }
        }
    }

    private func read(_ connection: NativeConnection) {
        defer {
            connection.close()
            DispatchQueue.main.async { [weak service] in service?.disconnected(connection) }
            lock.lock(); connections.removeValue(forKey: connection.id); lock.unlock()
            slots.signal()
        }
        var buffer = Data()
        var chunk = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let count = recv(connection.descriptor, &chunk, chunk.count, 0)
            if count < 0 && errno == EINTR { continue }
            if count <= 0 { return }
            buffer.append(contentsOf: chunk.prefix(count))
            while let newline = buffer.firstIndex(of: 10) {
                let line = buffer.prefix(upTo: newline)
                if line.count > frameLimit { return }
                buffer.removeSubrange(...newline)
                guard !line.isEmpty else { continue }
                guard let object = try? JSONSerialization.jsonObject(with: line) as? [String: Any], let id = object["id"], (id is String || id is NSNumber), let type = object["requestType"] as? String, let arguments = object["request"] as? [String: Any], connection.reserve() else { return }
                let request = NativeRequest(id: id, type: type, arguments: arguments, connection: connection)
                DispatchQueue.main.async { [weak service] in
                    guard let service else { connection.release(); connection.close(); return }
                    service.enqueue(request)
                }
            }
            if buffer.count > frameLimit { return }
        }
    }

    func close() {
        Darwin.shutdown(descriptor, SHUT_RDWR)
        Darwin.close(descriptor)
        lock.lock(); let clients = Array(connections.values); lock.unlock()
        for client in clients { client.close() }
        var info = stat()
        if lstat(path, &info) == 0, info.st_ino == socketInode, info.st_uid == geteuid() { unlink(path) }
    }
}

@main
struct ComputerServiceMain {
    @MainActor static func main() {
        if CommandLine.arguments.contains("--verify-listener-fd") {
            let args = CommandLine.arguments
            guard args.count == 5, args[1] == "--verify-listener-fd", args[2] == "3", args[3] == "--expected-executable-sha256",
                  ComputerPeerAuthentication.verifiesListener(3, expectedSHA256: args[4]) else { exit(77) }
            exit(0)
        }
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        guard let executableURL = Bundle.main.executableURL, let service = try? DesktopService(executableURL: executableURL) else {
            fputs("AIClaw Computer Use cannot establish its executable identity.\n", stderr)
            return
        }
        app.delegate = service
        withExtendedLifetime(service) { app.run() }
    }
}
