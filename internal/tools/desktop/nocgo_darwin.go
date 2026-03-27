//go:build darwin && !cgo

package desktop

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	cgFlagCmd   = 0x00100000
	cgFlagShift = 0x00020000
	cgFlagAlt   = 0x00080000
	cgFlagCtrl  = 0x00040000
)

func cgoClick(x, y int, button string, clicks int) {
	btn := "1"
	if button == "right" {
		btn = "3"
	}
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var pt = $.CGPointMake(%d, %d);
var btn = %s;
var downType, upType, mb;
if (btn === 3) {
  downType = $.kCGEventRightMouseDown; upType = $.kCGEventRightMouseUp; mb = $.kCGMouseButtonRight;
} else {
  downType = $.kCGEventLeftMouseDown; upType = $.kCGEventLeftMouseUp; mb = $.kCGMouseButtonLeft;
}
for (var i = 0; i < %d; i++) {
  var down = $.CGEventCreateMouseEvent($(), downType, pt, mb);
  $.CGEventSetIntegerValueField(down, $.kCGMouseEventClickState, i+1);
  $.CGEventPost($.kCGHIDEventTap, down);
  $.CFRelease(down);
  delay(0.005);
  var up = $.CGEventCreateMouseEvent($(), upType, pt, mb);
  $.CGEventSetIntegerValueField(up, $.kCGMouseEventClickState, i+1);
  $.CGEventPost($.kCGHIDEventTap, up);
  $.CFRelease(up);
  if (i < %d - 1) delay(0.05);
}
`, x, y, btn, clicks, clicks)
	_ = exec.Command("osascript", "-l", "JavaScript", "-e", script).Run()
}

func cgoMouseMove(x, y int) {
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var pt = $.CGPointMake(%d, %d);
var ev = $.CGEventCreateMouseEvent($(), $.kCGEventMouseMoved, pt, $.kCGMouseButtonLeft);
$.CGEventPost($.kCGHIDEventTap, ev);
$.CFRelease(ev);
`, x, y)
	_ = exec.Command("osascript", "-l", "JavaScript", "-e", script).Run()
}

func cgoScroll(dy, dx int) {
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var ev = $.CGEventCreateScrollWheelEvent($(), $.kCGScrollEventUnitPixel, 2, %d, %d);
$.CGEventPost($.kCGHIDEventTap, ev);
$.CFRelease(ev);
`, dy, dx)
	_ = exec.Command("osascript", "-l", "JavaScript", "-e", script).Run()
}

func cgoKeyTap(keycode, flags int) {
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var src = $.CGEventSourceCreate($.kCGEventSourceStateHIDSystemState);
var down = $.CGEventCreateKeyboardEvent(src, %d, true);
var up = $.CGEventCreateKeyboardEvent(src, %d, false);
if (%d) { $.CGEventSetFlags(down, %d); $.CGEventSetFlags(up, %d); }
$.CGEventPost($.kCGHIDEventTap, down);
delay(0.005);
$.CGEventPost($.kCGHIDEventTap, up);
$.CFRelease(down); $.CFRelease(up); $.CFRelease(src);
`, keycode, keycode, flags, flags, flags)
	_ = exec.Command("osascript", "-l", "JavaScript", "-e", script).Run()
}

func cgoTypeUnicode(ch rune) {
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var src = $.CGEventSourceCreate($.kCGEventSourceStateHIDSystemState);
var down = $.CGEventCreateKeyboardEvent(src, 0, true);
var up = $.CGEventCreateKeyboardEvent(src, 0, false);
var buf = $.NSString.alloc.initWithString("%s");
// Use pbcopy + Cmd-V as fallback for unicode
`, string(ch))
	_ = exec.Command("osascript", "-l", "JavaScript", "-e", script).Run()
}

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
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e",
		`ObjC.import('CoreGraphics'); var d=$.CGMainDisplayID(); var pw=$.CGDisplayPixelsWide(d); var b=$.CGDisplayBounds(d); Math.round(pw/b.size.width)`).Output()
	if err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 1 {
			return v
		}
	}
	return 1
}

func getScreenSize() (int, int) {
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e",
		`ObjC.import('CoreGraphics'); var b=$.CGDisplayBounds($.CGMainDisplayID()); b.size.width+','+b.size.height`).Output()
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ",")
		if len(parts) == 2 {
			w, _ := strconv.Atoi(parts[0])
			h, _ := strconv.Atoi(parts[1])
			if w > 0 && h > 0 {
				return w, h
			}
		}
	}
	return 1920, 1080
}
