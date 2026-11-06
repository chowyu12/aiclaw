import AppKit
import Darwin
import Foundation
import Security

struct LegacyMigrationError: LocalizedError {
    let code: String
    var errorDescription: String? {
        code == "COMPUTER_LEGACY_BUSY"
            ? "The older desktop service is busy. Close the older AIClaw instance and check again."
            : "The older desktop service cannot be verified safely. Close the older AIClaw instance and check again."
    }
    static let busy = LegacyMigrationError(code: "COMPUTER_LEGACY_BUSY")
    static let unverified = LegacyMigrationError(code: "COMPUTER_LEGACY_UNVERIFIED")
}

// This runs only inside a production-authenticated, migration-only service,
// before replacing the stable installation. No caller-provided target path.
@MainActor
final class LegacyComputerMigration {
    private static let helperID = "com.github.chowyu12.aiclaw.computer-use"
    private struct Identity: Equatable {
        let pid: pid_t
        let startSeconds: UInt64
        let startMicroseconds: UInt64
        let hash: Data
    }
    private struct Sockets: Equatable {
        let descriptors: [Int32]
        let paths: [String]
        let busy: Bool
    }

    static func retireIdle(in directory: URL) async throws -> [String: Any] {
        guard computerHostAuthenticationProfile == "production" else { throw LegacyMigrationError.unverified }
        let root = directory.resolvingSymlinksInPath().standardizedFileURL
        let bundle = root.appendingPathComponent("AIClaw Computer Use.app", isDirectory: true)
        let executable = bundle.appendingPathComponent("Contents/MacOS/AIClawComputerUse").resolvingSymlinksInPath().standardizedFileURL.path
        var own: SecCode?
        guard SecCodeCopySelf([], &own) == errSecSuccess, let own else { throw LegacyMigrationError.unverified }
        let ownHash = try signature(own).hash
        var retired = 0
        for application in NSRunningApplication.runningApplications(withBundleIdentifier: helperID) where application.processIdentifier != getpid() {
            guard let launched = application.executableURL,
                  launched.resolvingSymlinksInPath().standardizedFileURL.path == executable else { continue }
            if application.isTerminated { continue }
            // Refuse linked/unowned installation roots before inspecting or
            // terminating anything. An unlinked historical vnode is diagnostic,
            // never a reason to guess that a same-name process is safe to stop.
            var file = stat()
            guard lstat(bundle.path, &file) == 0, file.st_uid == geteuid(),
                  (file.st_mode & S_IFMT) == S_IFDIR, (file.st_mode & 0o022) == 0,
                  executable.hasPrefix(bundle.path + "/") else { throw LegacyMigrationError.unverified }
            let original = try identity(application, executable: executable)
            if original.hash == ownHash { continue }
            var stable: Sockets?
            var idle = false
            for _ in 0..<20 {
                try Task.checkCancellation()
                if application.isTerminated { idle = true; break }
                guard try identity(application, executable: executable) == original else { throw LegacyMigrationError.unverified }
                let current = try sockets(original.pid, root: root.path)
                if !current.busy, current == stable { idle = true; break }
                stable = current.busy ? nil : current
                try await Task.sleep(nanoseconds: 100_000_000)
            }
            guard idle else { throw LegacyMigrationError.busy }
            if application.isTerminated { continue }
            // Revalidate immediately before the normal quit request. Accepted
            // or disconnected-but-retained Unix FDs both count as busy: the old
            // input implementation retains its FD until cancellation releases.
            try Task.checkCancellation()
            guard try identity(application, executable: executable) == original,
                  let stable, !stable.busy, try sockets(original.pid, root: root.path) == stable else { throw LegacyMigrationError.busy }
            guard application.terminate() else { throw LegacyMigrationError.busy }
            for _ in 0..<40 {
                if application.isTerminated { break }
                try await Task.sleep(nanoseconds: 50_000_000)
            }
            guard application.isTerminated else { throw LegacyMigrationError.busy }
            for path in stable.paths { try requireClosed(path) }
            retired += 1
        }
        return try nativeJSON(["migration_complete": true, "retired_idle_residents": retired, "scope": "same-user same-installation idle resident processes; does not revoke older signed software"])
    }

    private static func identity(_ application: NSRunningApplication, executable: String) throws -> Identity {
        let pid = application.processIdentifier
        let before = try process(pid)
        guard application.bundleIdentifier == helperID, let launched = application.executableURL,
              launched.resolvingSymlinksInPath().standardizedFileURL.path == executable else { throw LegacyMigrationError.unverified }
        var bytes = [CChar](repeating: 0, count: 4 * Int(MAXPATHLEN))
        guard proc_pidpath(pid, &bytes, UInt32(bytes.count)) > 0,
              URL(fileURLWithPath: String(cString: bytes)).resolvingSymlinksInPath().standardizedFileURL.path == executable else { throw LegacyMigrationError.unverified }
        var code: SecCode?
        guard SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributePid: NSNumber(value: pid)] as CFDictionary, [], &code) == errSecSuccess,
              let code else { throw LegacyMigrationError.unverified }
        let signed = try signature(code)
        guard signed.path == executable else { throw LegacyMigrationError.unverified }
        let after = try process(pid)
        guard before.pbi_start_tvsec == after.pbi_start_tvsec, before.pbi_start_tvusec == after.pbi_start_tvusec else { throw LegacyMigrationError.unverified }
        return Identity(pid: pid, startSeconds: before.pbi_start_tvsec, startMicroseconds: before.pbi_start_tvusec, hash: signed.hash)
    }

    private static func signature(_ code: SecCode) throws -> (hash: Data, path: String) {
        guard SecCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), nil) == errSecSuccess else { throw LegacyMigrationError.unverified }
        var info: CFDictionary?
        // Apple's C API explicitly accepts Code or StaticCode; the Swift import
        // declares only StaticCode. Keep the dynamic object for running CDHash.
        guard SecCodeCopySigningInformation(unsafeBitCast(code, to: SecStaticCode.self), SecCSFlags(rawValue: kSecCSSigningInformation | kSecCSDynamicInformation), &info) == errSecSuccess,
              let info = info as? [String: Any], info[kSecCodeInfoIdentifier as String] as? String == helperID,
              let hash = info[kSecCodeInfoUnique as String] as? Data, !hash.isEmpty,
              let path = info[kSecCodeInfoMainExecutable as String] as? URL else { throw LegacyMigrationError.unverified }
        return (hash, path.resolvingSymlinksInPath().standardizedFileURL.path)
    }

    private static func process(_ pid: pid_t) throws -> proc_bsdinfo {
        var info = proc_bsdinfo()
        guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, Int32(MemoryLayout<proc_bsdinfo>.size)) == MemoryLayout<proc_bsdinfo>.size,
              info.pbi_pid == UInt32(pid), info.pbi_uid == geteuid(), info.pbi_ruid == getuid() else { throw LegacyMigrationError.unverified }
        return info
    }

    private static func sockets(_ pid: pid_t, root: String) throws -> Sockets {
        let length = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, nil, 0)
        let stride = MemoryLayout<proc_fdinfo>.size
        guard length > 0, length < 1024 * 1024 else { throw LegacyMigrationError.unverified }
        var descriptors = [proc_fdinfo](repeating: proc_fdinfo(), count: Int(length) / stride + 32)
        let received = descriptors.withUnsafeMutableBytes { proc_pidinfo(pid, PROC_PIDLISTFDS, 0, $0.baseAddress, Int32($0.count)) }
        guard received > 0, Int(received) < descriptors.count * stride, Int(received) % stride == 0 else { throw LegacyMigrationError.unverified }
        var unix: [Int32] = [], paths: [String] = [], busy = false
        for fd in descriptors.prefix(Int(received) / stride) where fd.proc_fdtype == UInt32(PROX_FDTYPE_SOCKET) {
            var info = socket_fdinfo()
            guard proc_pidfdinfo(pid, fd.proc_fd, PROC_PIDFDSOCKETINFO, &info, Int32(MemoryLayout<socket_fdinfo>.size)) == MemoryLayout<socket_fdinfo>.size else { throw LegacyMigrationError.unverified }
            guard info.psi.soi_family == AF_UNIX, info.psi.soi_type == SOCK_STREAM else { continue }
            unix.append(fd.proc_fd)
            if Int32(info.psi.soi_options) & SO_ACCEPTCONN == 0 { busy = true; continue }
            if info.psi.soi_qlen != 0 || info.psi.soi_incqlen != 0 { busy = true }
            var address = info.psi.soi_proto.pri_un.unsi_addr.ua_sun.sun_path
            let bytes = withUnsafeBytes(of: &address) { Array($0.prefix(while: { $0 != 0 })) }
            guard let path = String(bytes: bytes, encoding: .utf8), path.hasPrefix("/") else { throw LegacyMigrationError.unverified }
            let canonical = URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
            guard canonical.hasPrefix(root + "/") else { throw LegacyMigrationError.unverified }
            paths.append(path)
        }
        return Sockets(descriptors: unix.sorted(), paths: paths.sorted(), busy: busy)
    }

    private static func requireClosed(_ path: String) throws {
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw LegacyMigrationError.unverified }
        defer { Darwin.close(descriptor) }
        guard fcntl(descriptor, F_SETFL, O_NONBLOCK) == 0 else { throw LegacyMigrationError.unverified }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: address.sun_path) else { throw LegacyMigrationError.unverified }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.initializeMemory(as: UInt8.self, repeating: 0); raw.copyBytes(from: bytes + [0])
        }
        let connected = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard connected < 0, errno == ECONNREFUSED || errno == ENOENT else { throw LegacyMigrationError.busy }
    }
}
