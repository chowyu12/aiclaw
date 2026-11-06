"""Native X11 measurement and optional owned Xvfb RandR monitor-layout probe."""
import ctypes as C
import ctypes.util
import json
import sys
from x11 import X11

x = X11()
if sys.argv[1] in ("frame", "outer"):
    window = int(sys.argv[2])
    frame = x.frame(window)
    extents = x.property(window, "_NET_FRAME_EXTENTS")
    if sys.argv[1] == "outer" and isinstance(extents, list) and len(extents) == 4:
        left, right, top, bottom = extents
        frame = {"x": frame["x"] - left, "y": frame["y"] - top, "width": frame["width"] + left + right, "height": frame["height"] + top + bottom}
    print(json.dumps(frame))
    raise SystemExit(0)

class Monitor(C.Structure):
    _fields_ = [("name", C.c_ulong), ("primary", C.c_int), ("automatic", C.c_int), ("noutput", C.c_int),
                ("x", C.c_int), ("y", C.c_int), ("width", C.c_int), ("height", C.c_int),
                ("mwidth", C.c_int), ("mheight", C.c_int), ("outputs", C.POINTER(C.c_ulong))]

r = C.CDLL(ctypes.util.find_library("Xrandr"))
r.XRRGetMonitors.argtypes = [C.c_void_p, C.c_ulong, C.c_int, C.POINTER(C.c_int)]
r.XRRGetMonitors.restype = C.POINTER(Monitor)
r.XRRFreeMonitors.argtypes = [C.POINTER(Monitor)]
if sys.argv[1] == "layout":
    r.XRRSetMonitor.argtypes = [C.c_void_p, C.c_ulong, C.POINTER(Monitor)]
    for name, primary, position in [("AIClawScaleLeft", 0, 0), ("AIClawScaleRight", 1, 1280)]:
        monitor = Monitor(x.atom(name), primary, 0, 0, position, 0, 1280, 1600, 340, 425, None)
        r.XRRSetMonitor(x.display, x.root, C.byref(monitor))
    x.x.XSync(x.display, False)

count = C.c_int()
monitors = r.XRRGetMonitors(x.display, x.root, False, C.byref(count))
try:
    print(json.dumps({"x11_error": x.error, "monitors": [{"primary": bool(monitors[index].primary), "x": monitors[index].x, "y": monitors[index].y, "width": monitors[index].width, "height": monitors[index].height} for index in range(count.value)]}))
finally:
    if monitors:
        r.XRRFreeMonitors(monitors)
