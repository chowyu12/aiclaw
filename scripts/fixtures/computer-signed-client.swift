import Darwin
import Foundation

// Dedicated test host. The test signs this temporary binary, exercises only
// test-owned sockets, and deletes the signed fixture at the end.
signal(SIGPIPE, SIG_IGN)
let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
var address = sockaddr_un()
address.sun_family = sa_family_t(AF_UNIX)
address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
withUnsafeMutableBytes(of: &address.sun_path) { raw in
    raw.initializeMemory(as: UInt8.self, repeating: 0)
    raw.copyBytes(from: Array(CommandLine.arguments[1].utf8) + [0])
}
let connected = withUnsafePointer(to: &address) { pointer in
    pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
}
guard connected == 0 else { exit(72) }
var timeout = timeval(tv_sec: 10, tv_usec: 0)
setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
let requests = try JSONSerialization.jsonObject(with: Data(CommandLine.arguments[2].utf8)) as! [[String: Any]]
var responses: [Any] = []
for request in requests {
    var payload = try JSONSerialization.data(withJSONObject: request)
    payload.append(10)
    let sent = payload.withUnsafeBytes { bytes -> Bool in
        var offset = 0
        while offset < bytes.count {
            let count = Darwin.send(descriptor, bytes.baseAddress!.advanced(by: offset), bytes.count - offset, 0)
            if count <= 0 { return false }
            offset += count
        }
        return true
    }
    guard sent else { exit(73) }
    var data = Data(), buffer = [UInt8](repeating: 0, count: 65536)
    while !data.contains(10) {
        let count = recv(descriptor, &buffer, buffer.count, 0)
        if count <= 0 { exit(74) }
        data.append(contentsOf: buffer.prefix(count))
    }
    responses.append(try JSONSerialization.jsonObject(with: data))
}
Darwin.close(descriptor)
print(String(decoding: try JSONSerialization.data(withJSONObject: responses), as: UTF8.self))
