import AppKit
import Darwin

// Isolated migration fixture: an older same-identity Unix service without peer
// authentication. It only answers a constant handshake, never desktop actions.
final class LegacyServiceFixture: NSObject, NSApplicationDelegate {
    var listener: Int32 = -1
    let path = CommandLine.arguments[1]
    func applicationDidFinishLaunching(_ notification: Notification) {
        signal(SIGPIPE, SIG_IGN)
        listener = socket(AF_UNIX, SOCK_STREAM, 0)
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.initializeMemory(as: UInt8.self, repeating: 0)
            raw.copyBytes(from: Array(path.utf8) + [0])
        }
        let bound = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard listener >= 0, bound == 0, chmod(path, 0o600) == 0, listen(listener, 8) == 0 else { exit(71) }
        DispatchQueue.global().async { [self] in
            while true {
                let client = accept(listener, nil, nil)
                if client < 0 { return }
                DispatchQueue.global().async {
                    defer { Darwin.close(client) }
                    var buffer = [UInt8](repeating: 0, count: 4096)
                    while recv(client, &buffer, buffer.count, 0) > 0 {
                        let reply = Array("{\"id\":1,\"result\":{\"protocolVersion\":\"AIClawComputerUseIPC-1\",\"legacy_fixture\":true}}\n".utf8)
                        _ = reply.withUnsafeBytes { Darwin.send(client, $0.baseAddress, $0.count, 0) }
                    }
                }
            }
        }
    }
    func applicationWillTerminate(_ notification: Notification) {
        Darwin.close(listener)
        unlink(path)
    }
}
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = LegacyServiceFixture()
app.delegate = delegate
app.run()
