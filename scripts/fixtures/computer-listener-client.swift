import Darwin
import Foundation

signal(SIGPIPE, SIG_IGN)
let fd = socket(AF_UNIX, SOCK_STREAM, 0)
var address = sockaddr_un()
address.sun_family = sa_family_t(AF_UNIX)
address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: Array(CommandLine.arguments[1].utf8) + [0]) }
let connected = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
guard connected == 0 else { exit(72) }
if CommandLine.arguments[4] != "unchecked" {
    var actions: posix_spawn_file_actions_t?
    guard posix_spawn_file_actions_init(&actions) == 0 else { exit(75) }
    defer { posix_spawn_file_actions_destroy(&actions) }
    guard posix_spawn_file_actions_adddup2(&actions, fd, 3) == 0 else { exit(75) }
    let verifier = [CommandLine.arguments[2], "--verify-listener-fd", "3", "--expected-executable-sha256", CommandLine.arguments[3]]
    let arguments = CommandLine.arguments[4] == "verify-x86_64" ? ["/usr/bin/arch", "-x86_64"] + verifier : verifier
    let strings = arguments.map { strdup($0) }
    defer { strings.forEach { free($0) } }
    var pointers = strings + [nil], pid: pid_t = 0, status: Int32 = 0
    let result = pointers.withUnsafeMutableBufferPointer { posix_spawn(&pid, arguments[0], &actions, nil, $0.baseAddress!, environ) }
    guard result == 0, waitpid(pid, &status, 0) == pid, status == 0 else { exit(77) }
}
let request = Array("{\"id\":1,\"requestType\":\"ComputerUseIPCHandshakeRequest\",\"request\":{}}\n".utf8)
_ = request.withUnsafeBytes { send(fd, $0.baseAddress, $0.count, 0) }
var timeout = timeval(tv_sec: 3, tv_usec: 0)
setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
var buffer = [UInt8](repeating: 0, count: 65536)
var data = Data()
while !data.contains(10) {
    let count = recv(fd, &buffer, buffer.count, 0)
    guard count > 0 else { exit(74) }
    data.append(contentsOf: buffer.prefix(count))
}
guard let frame = try JSONSerialization.jsonObject(with: data) as? [String: Any],
      let result = frame["result"] as? [String: Any], result["processExecutableSHA256"] as? String == CommandLine.arguments[3] else { exit(74) }
close(fd)
print("listener accepted")
