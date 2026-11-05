"""Small X11/XComposite/XTest binding. No shell commands or third-party driver."""
import ctypes as C
import ctypes.util
import os
import time


class XImage(C.Structure):
    _fields_ = [("width", C.c_int), ("height", C.c_int), ("xoffset", C.c_int),
                ("format", C.c_int), ("data", C.c_void_p), ("byte_order", C.c_int),
                ("bitmap_unit", C.c_int), ("bitmap_bit_order", C.c_int),
                ("bitmap_pad", C.c_int), ("depth", C.c_int), ("bytes_per_line", C.c_int),
                ("bits_per_pixel", C.c_int), ("red_mask", C.c_ulong),
                ("green_mask", C.c_ulong), ("blue_mask", C.c_ulong)]


class Visual(C.Structure):
    _fields_ = [("extension", C.c_void_p), ("id", C.c_ulong), ("kind", C.c_int),
                ("red_mask", C.c_ulong), ("green_mask", C.c_ulong), ("blue_mask", C.c_ulong),
                ("bits_per_rgb", C.c_int), ("map_entries", C.c_int)]


class WindowAttributes(C.Structure):
    _fields_ = [(name, C.c_int) for name in ("x", "y", "width", "height", "border_width", "depth")] + [
        ("visual", C.POINTER(Visual)), ("root", C.c_ulong), ("kind", C.c_int),
        ("bit_gravity", C.c_int), ("win_gravity", C.c_int), ("backing_store", C.c_int),
        ("backing_planes", C.c_ulong), ("backing_pixel", C.c_ulong), ("save_under", C.c_int),
        ("colormap", C.c_ulong), ("map_installed", C.c_int), ("map_state", C.c_int),
        ("all_event_masks", C.c_long), ("your_event_mask", C.c_long), ("do_not_propagate_mask", C.c_long),
        ("override_redirect", C.c_int), ("screen", C.c_void_p)]


class MessageData(C.Union):
    _fields_ = [("longs", C.c_long * 5), ("bytes", C.c_char * 20)]


class ClientMessage(C.Structure):
    _fields_ = [("type", C.c_int), ("serial", C.c_ulong), ("send_event", C.c_int),
                ("display", C.c_void_p), ("window", C.c_ulong), ("message_type", C.c_ulong),
                ("format", C.c_int), ("data", MessageData)]


class Event(C.Union):
    _fields_ = [("message", ClientMessage), ("pad", C.c_long * 24)]


class X11:
    def __init__(self):
        self.x = C.CDLL(ctypes.util.find_library("X11") or "libX11.so.6")
        self.xt = C.CDLL(ctypes.util.find_library("Xtst") or "libXtst.so.6")
        self.composite = C.CDLL(ctypes.util.find_library("Xcomposite") or "libXcomposite.so.1")
        def bind(lib, name, result, arguments):
            fn = getattr(lib, name); fn.restype = result; fn.argtypes = arguments
            return fn
        p, u, i, ul = C.c_void_p, C.c_uint, C.c_int, C.c_ulong
        bind(self.x, "XOpenDisplay", p, [C.c_char_p])
        bind(self.x, "XCloseDisplay", i, [p])
        bind(self.x, "XDefaultRootWindow", ul, [p])
        bind(self.x, "XDefaultScreen", i, [p])
        bind(self.x, "XInternAtom", ul, [p, C.c_char_p, i])
        bind(self.x, "XGetWindowProperty", i, [p, ul, ul, C.c_long, C.c_long, i, ul, C.POINTER(ul), C.POINTER(i), C.POINTER(ul), C.POINTER(ul), C.POINTER(p)])
        bind(self.x, "XFree", i, [p])
        bind(self.x, "XGetGeometry", i, [p, ul, C.POINTER(ul), C.POINTER(i), C.POINTER(i), C.POINTER(u), C.POINTER(u), C.POINTER(u), C.POINTER(u)])
        bind(self.x, "XTranslateCoordinates", i, [p, ul, ul, i, i, C.POINTER(i), C.POINTER(i), C.POINTER(ul)])
        bind(self.x, "XMapWindow", i, [p, ul]); bind(self.x, "XRaiseWindow", i, [p, ul])
        bind(self.x, "XIconifyWindow", i, [p, ul, i])
        bind(self.x, "XSendEvent", i, [p, ul, i, C.c_long, C.POINTER(Event)])
        bind(self.x, "XFlush", i, [p]); bind(self.x, "XSync", i, [p, i])
        bind(self.x, "XGetImage", C.POINTER(XImage), [p, ul, i, i, u, u, ul, i])
        bind(self.x, "XGetWindowAttributes", i, [p, ul, C.POINTER(WindowAttributes)])
        bind(self.x, "XDestroyImage", i, [C.POINTER(XImage)])
        bind(self.x, "XFreePixmap", i, [p, ul])
        bind(self.x, "XStringToKeysym", ul, [C.c_char_p])
        bind(self.x, "XKeysymToKeycode", u, [p, ul])
        bind(self.x, "XQueryPointer", i, [p, ul, C.POINTER(ul), C.POINTER(ul), C.POINTER(i), C.POINTER(i), C.POINTER(i), C.POINTER(i), C.POINTER(u)])
        bind(self.xt, "XTestFakeKeyEvent", i, [p, u, i, ul])
        bind(self.xt, "XTestFakeButtonEvent", i, [p, u, i, ul])
        bind(self.xt, "XTestFakeMotionEvent", i, [p, i, i, i, ul])
        bind(self.composite, "XCompositeNameWindowPixmap", ul, [p, ul])
        bind(self.composite, "XCompositeRedirectWindow", None, [p, ul, i])
        bind(self.composite, "XCompositeUnredirectWindow", None, [p, ul, i])
        self.display = self.x.XOpenDisplay(None)
        if not self.display:
            raise RuntimeError("No X11 display is available. Wayland requires a portal-backed driver and is not handled by this X11 service.")
        self.root = self.x.XDefaultRootWindow(self.display)
        self.error = 0
        self.redirected = None
        self.error_callback = C.CFUNCTYPE(i, p, p)(self._on_error)
        bind(self.x, "XSetErrorHandler", p, [C.c_void_p])(C.cast(self.error_callback, p))

    def _on_error(self, _display, _event):
        self.error = 1
        return 0

    def atom(self, name):
        return self.x.XInternAtom(self.display, name.encode(), False)

    def property(self, window, name):
        actual, count, remaining, fmt, data = C.c_ulong(), C.c_ulong(), C.c_ulong(), C.c_int(), C.c_void_p()
        self.error = 0
        status = self.x.XGetWindowProperty(self.display, window, self.atom(name), 0, 100_000, False, 0,
                                          C.byref(actual), C.byref(fmt), C.byref(count), C.byref(remaining), C.byref(data))
        if status or self.error or not data.value:
            return []
        try:
            if fmt.value == 32:
                return list(C.cast(data, C.POINTER(C.c_ulong))[:count.value])
            if fmt.value == 8:
                return C.string_at(data, count.value).decode("utf-8", "replace")
            return []
        finally:
            self.x.XFree(data)

    def owner(self, window):
        values = self.property(window, "_NET_WM_PID")
        return int(values[0]) if values else 0

    def windows(self, pid=None):
        result = []
        for window in self.property(self.root, "_NET_CLIENT_LIST"):
            owner = self.owner(window)
            if owner and (pid is None or owner == pid):
                try:
                    if os.stat(f"/proc/{owner}").st_uid != os.getuid():
                        continue
                    result.append({"window_id": window, "pid": owner, "title": self.property(window, "_NET_WM_NAME") or "", "frame": self.frame(window)})
                except (OSError, RuntimeError):
                    continue
        return result

    def frame(self, window):
        root, child = C.c_ulong(), C.c_ulong()
        x, y, sx, sy = C.c_int(), C.c_int(), C.c_int(), C.c_int()
        w, h, border, depth = C.c_uint(), C.c_uint(), C.c_uint(), C.c_uint()
        self.error = 0
        ok = self.x.XGetGeometry(self.display, window, C.byref(root), C.byref(x), C.byref(y), C.byref(w), C.byref(h), C.byref(border), C.byref(depth))
        if not ok or self.error or not w.value or not h.value:
            raise RuntimeError("Window is no longer available")
        self.x.XTranslateCoordinates(self.display, window, self.root, 0, 0, C.byref(sx), C.byref(sy), C.byref(child))
        return {"x": sx.value, "y": sy.value, "width": w.value, "height": h.value}

    def active(self):
        values = self.property(self.root, "_NET_ACTIVE_WINDOW")
        return values[0] if values else 0

    def message(self, window, kind, values):
        event = Event(); event.message.type = 33; event.message.send_event = True
        event.message.display = self.display; event.message.window = window
        event.message.message_type = self.atom(kind); event.message.format = 32
        for index, value in enumerate(values):
            event.message.data.longs[index] = value
        self.x.XSendEvent(self.display, self.root, False, (1 << 19) | (1 << 20), C.byref(event))
        self.x.XFlush(self.display)

    def activate(self, window, cancelled):
        self.x.XMapWindow(self.display, window); self.x.XRaiseWindow(self.display, window)
        self.message(window, "_NET_ACTIVE_WINDOW", [2, 0, 0])
        for _ in range(50):
            if cancelled():
                raise RuntimeError("Desktop operation cancelled before input")
            if self.active() == window:
                return
            time.sleep(0.01)
        raise RuntimeError("The target window did not become active; no global input was sent")

    def pointer(self):
        root, child = C.c_ulong(), C.c_ulong()
        x, y, local_x, local_y, mask = C.c_int(), C.c_int(), C.c_int(), C.c_int(), C.c_uint()
        self.x.XQueryPointer(self.display, self.root, C.byref(root), C.byref(child), C.byref(x), C.byref(y), C.byref(local_x), C.byref(local_y), C.byref(mask))
        return x.value, y.value

    def motion(self, x, y):
        self.xt.XTestFakeMotionEvent(self.display, -1, round(x), round(y), 0); self.x.XFlush(self.display)

    def button(self, button, pressed):
        self.xt.XTestFakeButtonEvent(self.display, button, pressed, 0); self.x.XFlush(self.display)

    def keycode(self, name):
        symbol = self.x.XStringToKeysym(name.encode("ascii"))
        code = self.x.XKeysymToKeycode(self.display, symbol)
        if not code:
            raise RuntimeError(f"Key is unavailable in the current X11 keymap: {name}")
        return code

    def key(self, code, pressed):
        self.xt.XTestFakeKeyEvent(self.display, code, pressed, 0); self.x.XFlush(self.display)

    def pixels(self, window):
        frame = self.frame(window)
        if self.redirected != window:
            if self.redirected:
                self.composite.XCompositeUnredirectWindow(self.display, self.redirected, 0)
                self.x.XSync(self.display, False)
            self.error = 0
            # A compositing WM may redirect only its decoration parent. Request
            # automatic backing storage for the exact client window ourselves.
            self.composite.XCompositeRedirectWindow(self.display, window, 0)
            self.x.XSync(self.display, False)
            if self.error:
                raise RuntimeError("XComposite cannot redirect the selected window; no root-screen fallback is used")
            self.redirected = window
            time.sleep(0.05)
        self.error = 0
        pixmap = self.composite.XCompositeNameWindowPixmap(self.display, window)
        self.x.XSync(self.display, False)
        if self.error or not pixmap:
            raise RuntimeError("Target-only XComposite backing storage is unavailable; no root-screen fallback is used")
        image = None
        try:
            image = self.x.XGetImage(self.display, pixmap, 0, 0, frame["width"], frame["height"], C.c_ulong(-1), 2)
            if not image or self.error:
                raise RuntimeError("The target window backing pixmap cannot be captured")
            info = image.contents
            attributes = WindowAttributes()
            if not self.x.XGetWindowAttributes(self.display, window, C.byref(attributes)) or self.error or not attributes.visual:
                raise RuntimeError("The window visual is no longer available")
            visual = attributes.visual.contents
            # Pixmap images have no associated visual and may report zero masks.
            # Use the actual target window's visual, never an assumed screen format.
            if (info.bits_per_pixel, info.byte_order, visual.red_mask, visual.green_mask, visual.blue_mask) != (32, 0, 0xff0000, 0xff00, 0xff):
                raise RuntimeError("Unsupported X11 image format; no guessed conversion was attempted")
            raw = C.string_at(info.data, info.bytes_per_line * info.height)
            rgba = bytearray(info.width * info.height * 4)
            for row in range(info.height):
                source = raw[row * info.bytes_per_line:row * info.bytes_per_line + info.width * 4]
                start = row * info.width * 4
                rgba[start:start + info.width * 4:4] = source[2::4]
                rgba[start + 1:start + info.width * 4:4] = source[1::4]
                rgba[start + 2:start + info.width * 4:4] = source[0::4]
                rgba[start + 3:start + info.width * 4:4] = b"\xff" * info.width
            return frame, bytes(rgba)
        finally:
            if image:
                self.x.XDestroyImage(image)
            self.x.XFreePixmap(self.display, pixmap)
