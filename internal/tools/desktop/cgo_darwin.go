//go:build darwin

package desktop

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <unistd.h>

void cgo_mouse_click(int x, int y, int button, int clicks) {
	CGEventType downType = kCGEventLeftMouseDown;
	CGEventType upType   = kCGEventLeftMouseUp;
	CGMouseButton mb     = kCGMouseButtonLeft;
	if (button == 1) {
		downType = kCGEventRightMouseDown;
		upType   = kCGEventRightMouseUp;
		mb       = kCGMouseButtonRight;
	}
	CGPoint pt = CGPointMake(x, y);
	for (int i = 0; i < clicks; i++) {
		CGEventRef down = CGEventCreateMouseEvent(NULL, downType, pt, mb);
		if (clicks == 2) CGEventSetIntegerValueField(down, kCGMouseEventClickState, i + 1);
		CGEventPost(kCGHIDEventTap, down);
		CFRelease(down);

		CGEventRef up = CGEventCreateMouseEvent(NULL, upType, pt, mb);
		if (clicks == 2) CGEventSetIntegerValueField(up, kCGMouseEventClickState, i + 1);
		CGEventPost(kCGHIDEventTap, up);
		CFRelease(up);

		if (i < clicks - 1) usleep(50000);
	}
}

void cgo_mouse_move(int x, int y) {
	CGPoint pt = CGPointMake(x, y);
	CGEventRef ev = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, pt, kCGMouseButtonLeft);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

void cgo_scroll(int dy, int dx) {
	CGEventRef ev = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, dy, dx);
	CGEventPost(kCGHIDEventTap, ev);
	CFRelease(ev);
}

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

func cgoClick(x, y int, button string, clicks int) {
	btn := 0
	if button == "right" {
		btn = 1
	}
	C.cgo_mouse_click(C.int(x), C.int(y), C.int(btn), C.int(clicks))
}

func cgoMouseMove(x, y int) {
	C.cgo_mouse_move(C.int(x), C.int(y))
}

func cgoScroll(dy, dx int) {
	C.cgo_scroll(C.int(dy), C.int(dx))
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
