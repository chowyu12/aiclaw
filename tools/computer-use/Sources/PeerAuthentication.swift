import Darwin
import Foundation
import Security
import CryptoKit

// The requirement is compiled into this executable, never supplied by the
// connecting client, environment, launch arguments or mutable resource files.
enum ComputerPeerAuthentication {
    static func accepts(_ descriptor: Int32) -> Bool {
        guard let guest = peer(descriptor) else { return false }
        var requirement: SecRequirement?
        guard SecRequirementCreateWithString(computerTrustedHostRequirement as CFString, [], &requirement) == errSecSuccess,
              let requirement else { return false }
        return SecCodeCheckValidity(guest, SecCSFlags(rawValue: kSecCSStrictValidate), requirement) == errSecSuccess
    }

    // Read-only CLI: authenticate the listener on the original host connection.
    // Inheriting this FD does not change its kernel-recorded client identity.
    static func verifiesListener(_ descriptor: Int32, expectedSHA256: String) -> Bool {
        guard descriptor == 3, let url = Bundle.main.executableURL,
              let bytes = try? Data(contentsOf: url),
              SHA256.hash(data: bytes).map({ String(format: "%02x", $0) }).joined() == expectedSHA256,
              let guest = peer(descriptor) else { return false }
        var own: SecCode?
        guard SecCodeCopySelf([], &own) == errSecSuccess, let own else { return false }
        func identity(_ code: SecCode) -> (Data, String)? {
            guard SecCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), nil) == errSecSuccess else { return nil }
            var info: CFDictionary?
            // This public C API accepts a Code or StaticCode object.
            guard SecCodeCopySigningInformation(unsafeBitCast(code, to: SecStaticCode.self), SecCSFlags(rawValue: kSecCSSigningInformation | kSecCSDynamicInformation), &info) == errSecSuccess,
                  let info = info as? [String: Any],
                  let hash = info[kSecCodeInfoUnique as String] as? Data, !hash.isEmpty,
                  let executable = info[kSecCodeInfoMainExecutable as String] as? URL else { return nil }
            return (hash, executable.resolvingSymlinksInPath().standardizedFileURL.path)
        }
        guard let ownIdentity = identity(own), let peerIdentity = identity(guest),
              ownIdentity.1 == peerIdentity.1,
              ownIdentity.1 == url.resolvingSymlinksInPath().standardizedFileURL.path else { return false }
        // LaunchServices and a Rosetta host can choose different slices of the
        // same universal artifact. Bind both running hashes to verified slices.
        var hashes = Set<Data>()
        for architecture in ["arm64", "x86_64"] {
            var code: SecStaticCode?
            guard SecStaticCodeCreateWithPathAndAttributes(url as CFURL, [], [kSecCodeAttributeArchitecture: architecture] as CFDictionary, &code) == errSecSuccess,
                  let code else { continue }
            guard SecStaticCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), nil) == errSecSuccess else { return false }
            var info: CFDictionary?
            guard SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
                  let info = info as? [String: Any], let hash = info[kSecCodeInfoUnique as String] as? Data else { return false }
            hashes.insert(hash)
        }
        return hashes.contains(ownIdentity.0) && hashes.contains(peerIdentity.0)
    }

    private static func peer(_ descriptor: Int32) -> SecCode? {
        var uid: uid_t = 0, gid: gid_t = 0
        guard getpeereid(descriptor, &uid, &gid) == 0, uid == geteuid() else { return nil }
        var token = audit_token_t()
        var size = socklen_t(MemoryLayout<audit_token_t>.size)
        guard getsockopt(descriptor, SOL_LOCAL, LOCAL_PEERTOKEN, &token, &size) == 0,
              size == MemoryLayout<audit_token_t>.size else { return nil }
        let audit = withUnsafeBytes(of: &token) { Data($0) }
        var guest: SecCode?
        guard SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributeAudit: audit] as CFDictionary, [], &guest) == errSecSuccess,
              let guest else { return nil }
        return guest
    }
}
