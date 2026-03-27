//go:build darwin && cgo

package desktop

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <unistd.h>

static CGEventSourceRef _src() {
	return CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
}

void cgo_mouse_click(int x, int y, int button, int clicks) {
	CGEventType downType = kCGEventLeftMouseDown;
	CGEventType upType   = kCGEventLeftMouseUp;
	CGMouseButton mb     = kCGMouseButtonLeft;
	if (button == 1) {
		downType = kCGEventRightMouseDown;
		upType   = kCGEventRightMouseUp;
		mb       = kCGMouseButtonRight;
	} else if (button == 2) {
		downType = kCGEventOtherMouseDown;
		upType   = kCGEventOtherMouseUp;
		mb       = kCGMouseButtonCenter;
	}

	CGPoint pt = CGPointMake(x, y);
	CGEventSourceRef src = _src();

	for (int i = 0; i < clicks; i++) {
		CGEventRef down = CGEventCreateMouseEvent(src, downType, pt, mb);
		CGEventSetIntegerValueField(down, kCGMouseEventClickState, i + 1);
		CGEventPost(kCGHIDEventTap, down);
		CFRelease(down);

		usleep(5000);

		CGEventRef up = CGEventCreateMouseEvent(src, upType, pt, mb);
		CGEventSetIntegerValueField(up, kCGMouseEventClickState, i + 1);
		CGEventPost(kCGHIDEventTap, up);
		CFRelease(up);

		if (i < clicks - 1) usleep(50000);
	}
	CFRelease(src);
}

void cgo_mouse_move(int x, int y) {
	CGEventSourceRef src = _src();
	CGPoint pt = CGPointMake(x, y);

	CGEventRef get = CGEventCreate(NULL);
	CGPoint cur = CGEventGetLocation(get);
	CFRelease(get);

	CGEventRef ev = CGEventCreateMouseEvent(src, kCGEventMouseMoved, pt, kCGMouseButtonLeft);
	CGEventSetIntegerValueField(ev, kCGMouseEventDeltaX, (int64_t)(pt.x - cur.x));
	CGEventSetIntegerValueField(ev, kCGMouseEventDeltaY, (int64_t)(pt.y - cur.y));
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	CFRelease(src);
}

void cgo_scroll(int dy, int dx) {
	CGEventSourceRef src = _src();
	CGEventRef ev = CGEventCreateScrollWheelEvent(src, kCGScrollEventUnitPixel, 2, dy, dx);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
	CFRelease(src);
}

// ─── Keyboard ───

void cgo_key_tap(int keycode, int flags) {
	CGEventSourceRef src = _src();
	CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)keycode, true);
	CGEventRef up   = CGEventCreateKeyboardEvent(src, (CGKeyCode)keycode, false);
	if (flags) {
		CGEventSetFlags(down, (CGEventFlags)flags);
		CGEventSetFlags(up,   (CGEventFlags)flags);
	}
	CGEventPost(kCGHIDEventTap, down);
	usleep(5000);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
	CFRelease(src);
}

void cgo_type_unicode(unsigned int charCode) {
	CGEventSourceRef src = _src();
	UniChar ch = (UniChar)charCode;
	CGEventRef down = CGEventCreateKeyboardEvent(src, 0, true);
	CGEventRef up   = CGEventCreateKeyboardEvent(src, 0, false);
	CGEventKeyboardSetUnicodeString(down, 1, &ch);
	CGEventKeyboardSetUnicodeString(up,   1, &ch);
	CGEventPost(kCGHIDEventTap, down);
	usleep(3000);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
	CFRelease(src);
}

// ─── Display info ───

double cgo_get_scale_factor() {
	CGDirectDisplayID display = CGMainDisplayID();
	size_t pw = CGDisplayPixelsWide(display);
	CGRect bounds = CGDisplayBounds(display);
	if (bounds.size.width > 0) {
		return (double)pw / bounds.size.width;
	}
	return 1.0;
}

void cgo_get_screen_size(int* w, int* h) {
	CGRect bounds = CGDisplayBounds(CGMainDisplayID());
	*w = (int)bounds.size.width;
	*h = (int)bounds.size.height;
}
*/
import "C"

import "strings"

func cgoClick(x, y int, button string, clicks int) {
	btn := 0
	switch button {
	case "right":
		btn = 1
	case "middle":
		btn = 2
	}
	C.cgo_mouse_click(C.int(x), C.int(y), C.int(btn), C.int(clicks))
}

func cgoMouseMove(x, y int) {
	C.cgo_mouse_move(C.int(x), C.int(y))
}

func cgoScroll(dy, dx int) {
	C.cgo_scroll(C.int(dy), C.int(dx))
}

// cgoKeyTap presses and releases a key with optional modifier flags.
func cgoKeyTap(keycode, flags int) {
	C.cgo_key_tap(C.int(keycode), C.int(flags))
}

// cgoTypeUnicode types a single Unicode character via CGEvent.
func cgoTypeUnicode(ch rune) {
	C.cgo_type_unicode(C.uint(ch))
}

// ─── Key code mapping ───

const (
	cgFlagCmd   = 0x00100000 // kCGEventFlagMaskCommand
	cgFlagShift = 0x00020000 // kCGEventFlagMaskShift
	cgFlagAlt   = 0x00080000 // kCGEventFlagMaskAlternate
	cgFlagCtrl  = 0x00040000 // kCGEventFlagMaskControl
)

//nolint:unused
var macVKCodes = map[string]int{
	"a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3, "g": 5,
	"h": 4, "i": 34, "j": 38, "k": 40, "l": 37, "m": 46, "n": 45,
	"o": 31, "p": 35, "q": 12, "r": 15, "s": 1, "t": 17, "u": 32,
	"v": 9, "w": 13, "x": 7, "y": 16, "z": 6,
	"0": 29, "1": 18, "2": 19, "3": 20, "4": 21, "5": 23, "6": 22,
	"7": 26, "8": 28, "9": 25,
	"-": 27, "=": 24, "[": 33, "]": 30, "\\": 42,
	";": 41, "'": 39, ",": 43, ".": 47, "/": 44, "`": 50,
	"return": 36, "enter": 36, "tab": 48, "space": 49,
	"delete": 51, "backspace": 51, "escape": 53, "esc": 53,
	"up": 126, "down": 125, "left": 123, "right": 124,
	"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96,
	"f6": 97, "f7": 98, "f8": 100, "f9": 101, "f10": 109,
	"f11": 103, "f12": 111,
}

// parseMacKey parses a key expression like "cmd+v", "ctrl+shift+a", "enter"
// and returns (keycode, flags, ok).
func parseMacKey(key string) (int, int, bool) {
	lower := strings.ToLower(key)
	parts := strings.Split(lower, "+")
	flags := 0
	mainKey := strings.TrimSpace(parts[len(parts)-1])
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "cmd", "command":
			flags |= cgFlagCmd
		case "ctrl", "control":
			flags |= cgFlagCtrl
		case "alt", "option":
			flags |= cgFlagAlt
		case "shift":
			flags |= cgFlagShift
		}
	}
	code, ok := macVKCodes[mainKey]
	return code, flags, ok
}

func getScaleFactor() int {
	s := float64(C.cgo_get_scale_factor())
	if s < 1.5 {
		return 1
	}
	return int(s + 0.5)
}

func getScreenSize() (int, int) {
	var w, h C.int
	C.cgo_get_screen_size(&w, &h)
	return int(w), int(h)
}
