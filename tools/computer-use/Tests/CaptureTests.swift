import AppKit

@main
struct CaptureTests {
    static func main() throws {
        let options = try NativeCaptureOptions(["app": "com.example.capture", "window_id": 123, "interval_ms": 500])
        precondition(options.windowID == 123 && options.intervalMilliseconds == 500)
        let defaults = try NativeCaptureOptions(["app": "com.example.capture"])
        precondition(defaults.intervalMilliseconds == 1000)
        for args: [String: Any] in [[:], ["app": ""], ["app": "com.example.capture", "window_id": true], ["app": "com.example.capture", "window_id": 0], ["app": "com.example.capture", "window_id": 1.5], ["app": "com.example.capture", "interval_ms": 499], ["app": "com.example.capture", "interval_ms": 5001]] {
            do { _ = try NativeCaptureOptions(args); fatalError("Invalid capture options were accepted") } catch {}
        }
        let large = try nativeCaptureSize(CGRect(x: 0, y: 0, width: 4000, height: 2000))
        let small = try nativeCaptureSize(CGRect(x: 0, y: 0, width: 800, height: 400))
        precondition(large == CGSize(width: 1280, height: 640))
        precondition(small == CGSize(width: 800, height: 400))
        for dimensions in [CGSize(width: 0, height: 100), CGSize(width: CGFloat.infinity, height: 100), CGSize(width: 1, height: -1)] {
            do { _ = try nativeCaptureSize(CGRect(origin: .zero, size: dimensions)); fatalError("Invalid capture geometry was accepted") } catch {}
        }
        print("PASS: native capture option and image bounds; live stream and AX events require the separate fixture acceptance.")
    }
}
