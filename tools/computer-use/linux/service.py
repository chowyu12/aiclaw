#!/usr/bin/python3
"""AIClaw's X11/AT-SPI desktop service. Requests arrive only on parent stdio."""
import base64
import collections
import hashlib
import json
import math
import os
from pathlib import Path
import queue
import sys
import threading
import time

# Load only the sibling binding from our shipped service directory, never cwd.
sys.path.insert(0, str(Path(__file__).resolve().parent))
import gi
gi.require_version("Atspi", "2.0")
gi.require_version("GdkPixbuf", "2.0")
from gi.repository import Atspi, GdkPixbuf, GLib, Gio
from x11 import X11
from clipboard import Clipboard, plain_html

PROTOCOL = "AIClawComputerUseIPC-1"
SUPPORTED = {"list_apps", "find_apps", "permission_status", "frontmost_window", "get_app_state", "query", "set_value", "click", "perform_secondary_action", "select_text", "modify_app", "stop_app", "start_app", "press_key", "scroll", "drag", "type_text", "paste"}
CANCELLED = threading.Event()


def text(value):
    return {"content": [{"type": "text", "text": value if isinstance(value, str) else json.dumps(value, ensure_ascii=False)}]}


def integer(value, default, low, high):
    value = default if value is None else value
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        raise ValueError(f"Expected an integer from {low} to {high}")
    return value


def number(value, default, low, high):
    value = default if value is None else value
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not low <= value <= high:
        raise ValueError(f"Expected a finite number from {low} to {high}")
    return value


def pump():
    context = GLib.MainContext.default()
    for _ in range(10):
        if not context.pending():
            break
        context.iteration(False)


def delay(seconds):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        if CANCELLED.is_set():
            raise RuntimeError("Desktop operation cancelled; outcome may be partial")
        pump(); time.sleep(min(0.01, max(0, until - time.monotonic())))


class Service:
    def __init__(self, manifest):
        if os.environ.get("XDG_SESSION_TYPE") == "wayland":
            raise RuntimeError("Wayland needs an explicit portal-backed desktop session; this service supports X11 only")
        # libatspi can abort with SIGTRAP when its bus launcher is missing.
        # Validate/activate the accessibility bus before entering native AT-SPI.
        try:
            bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
            bus.call_sync("org.a11y.Bus", "/org/a11y/bus", "org.a11y.Bus", "GetAddress", None, GLib.VariantType.new("(s)"), Gio.DBusCallFlags.NONE, 2000, None)
        except GLib.Error as error:
            raise RuntimeError("Requires an AT-SPI accessibility bus: install at-spi2-core and run in a desktop D-Bus session. " + error.message) from error
        self.x = X11()
        self.clipboard = Clipboard()
        Atspi.init()
        Atspi.set_timeout(1000, 2000)
        self.tools = []
        for spec in json.loads(Path(manifest).read_text()):
            if spec["action"] in SUPPORTED:
                schema = spec["schema"]
                for field in ("observation_id", "verify_after"):
                    schema["properties"].pop(field, None)
                schema["required"] = [field for field in schema.get("required", []) if field != "observation_id"]
                if spec["action"] == "paste": schema["properties"]["format"]["enum"] = ["text", "html"]
                self.tools.append({"name": spec["action"], "inputSchema": schema})
        self.target = None; self.elements = {}; self.counter = 0; self.image_size = None
        self.launches = {}
        self.source_hash = hashlib.sha256(b"".join(Path(__file__).with_name(name).read_bytes() for name in ("service.py", "x11.py", "clipboard.py"))).hexdigest()

    def paste(self, values, target):
        value, fmt = values.get("text"), values.get("format", "text")
        if not isinstance(value, str) or len(value) > 2_000_000 or "\0" in value or fmt not in ("text", "html"):
            raise ValueError("X11 paste currently supports text and HTML, not Markdown")
        original, generation = self.clipboard.snapshot()
        payload = {"UTF8_STRING": ("UTF8_STRING", 8, (plain_html(value) if fmt == "html" else value).encode("utf-8"))}
        if fmt == "html": payload["text/html"] = ("text/html", 8, value.encode("utf-8"))
        generation = self.clipboard.replace_if_unchanged(payload, generation)
        if generation is None: raise RuntimeError("Clipboard changed before paste; no input was dispatched")
        try:
            self.global_focus(target)
            codes = [self.x.keycode(name) for name in ("Control_L", "v")]
            held = []
            try:
                for code in codes:
                    self.global_focus(target)
                    if self.clipboard.generation() != generation: raise RuntimeError("Clipboard changed before paste; no further keys dispatched")
                    self.x.key(code, True); held.append(code)
            finally:
                for code in reversed(held): self.x.key(code, False)
            delay(0.3)
        finally:
            self.clipboard.replace_if_unchanged(original, generation, restored=True)

    def invalidate(self):
        self.target = None; self.elements.clear(); self.image_size = None

    def installed_apps(self, needle):
        result = []
        for info in Gio.AppInfo.get_all():
            identifier = info.get_id()
            if not isinstance(info, Gio.DesktopAppInfo) or not identifier or not info.should_show(): continue
            name = info.get_display_name() or identifier
            if needle and needle not in name.casefold() and needle not in identifier.casefold(): continue
            filename = info.get_filename()
            if not filename: continue
            try: digest = hashlib.sha256(Path(filename).read_bytes()).hexdigest()
            except OSError: continue
            key = "launch:" + identifier
            self.launches[key] = (info, filename, digest)
            result.append({"id": key, "launch_id": key, "display_name": name, "is_running": False, "desktop_file": filename, "windows": []})
        return result

    def launch(self, args):
        key = args.get("app")
        if key not in self.launches: raise RuntimeError("Use an exact launch_id discovered on this connection; arbitrary names, paths or commands are not accepted")
        info, filename, expected = self.launches[key]
        if hashlib.sha256(Path(filename).read_bytes()).hexdigest() != expected: raise RuntimeError("The discovered desktop entry changed; discover it again before launch")
        executable = Path(info.get_executable() or "").name.casefold()
        if executable in ("aiclaw", "electron", "claw-agent", "aiclawcomputeruse"): raise RuntimeError("Refusing to launch AIClaw or its helper/approval controls")
        self.invalidate(); launched = {}
        def collect(_info, child_pid, _data):
            try: launched[child_pid] = Path(f"/proc/{child_pid}/stat").read_text().rsplit(") ", 1)[1].split()[19]
            except OSError: pass
        flags = GLib.SpawnFlags.SEARCH_PATH | GLib.SpawnFlags.STDOUT_TO_DEV_NULL | GLib.SpawnFlags.STDERR_TO_DEV_NULL
        if not info.launch_uris_as_manager([], None, flags, None, None, collect, None): raise RuntimeError("Desktop application launch was refused")
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            pump()
            candidates = set()
            for window in self.x.windows():
                child_pid = window["pid"]
                if child_pid not in launched: continue
                try:
                    birth = Path(f"/proc/{child_pid}/stat").read_text().rsplit(") ", 1)[1].split()[19]
                    if birth == launched[child_pid]: candidates.add(child_pid)
                except OSError: pass
            if len(candidates) == 1:
                child_pid = next(iter(candidates))
                return text({"app": str(child_pid), "launch_id": key, "verified": True, "launch_requested": True})
            delay(0.05)
        return text({"launch_id": key, "verified": False, "launch_requested": True})

    def application(self, pid):
        desktop = Atspi.get_desktop(0)
        for index in range(desktop.get_child_count()):
            child = desktop.get_child_at_index(index)
            if child and child.get_process_id() == pid:
                return child
        raise RuntimeError("The selected process exposes no AT-SPI application; enable desktop accessibility and observe again")

    def nodes(self, root, maximum=1200, state=None, maximum_depth=64):
        state = state if state is not None else {}
        pending = collections.deque([(root, 0)])
        seen = set(); deadline = time.monotonic() + 2
        while pending and len(seen) < maximum and time.monotonic() < deadline:
            if CANCELLED.is_set():
                raise RuntimeError("Desktop operation cancelled")
            node, depth = pending.popleft()
            if not node or hash(node) in seen:
                continue
            seen.add(hash(node))
            yield node, depth
            count = node.get_child_count()
            if depth < maximum_depth:
                available = max(0, maximum - len(seen) - len(pending))
                if count > available: state["truncated"] = True
                for index in range(min(count, available)):
                    pending.append((node.get_child_at_index(index), depth + 1))
            elif count: state["truncated"] = True
        if pending: state["truncated"] = True

    def resolve(self, args):
        app = args.get("app")
        if not isinstance(app, str) or not app.isdecimal() or str(int(app)) != app:
            raise ValueError("Use an exact running PID from application discovery")
        pid = int(app)
        if pid in (os.getpid(), os.getppid()) or os.stat(f"/proc/{pid}").st_uid != os.getuid():
            raise RuntimeError("Refusing helper, parent or another user's process")
        windows = self.x.windows(pid)
        requested = args.get("window_id")
        if requested is not None:
            requested = integer(requested, 0, 1, 0xffffffff)
            windows = [window for window in windows if window["window_id"] == requested]
        if len(windows) != 1:
            raise RuntimeError("Select one explicit window_id from discovery; no unique target window is available")
        window = windows[0]; accessible = self.application(pid)
        matches = []
        for node, depth in self.nodes(accessible, 200):
            if depth > 2:
                continue
            component = node.get_component_iface()
            if not component or node.get_role_name() not in ("frame", "dialog", "window", "alert"):
                continue
            bounds = component.get_extents(Atspi.CoordType.SCREEN)
            frame = window["frame"]
            if abs(bounds.x - frame["x"]) <= 32 and abs(bounds.y - frame["y"]) <= 32 and abs(bounds.width - frame["width"]) <= 64 and abs(bounds.height - frame["height"]) <= 64:
                matches.append(node)
        if len(matches) != 1:
            raise RuntimeError("Cannot uniquely align the AT-SPI window and X11 window; no input was sent")
        birth = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()[19]
        return {"app": app, "app_name": accessible.get_name() or app, **window, "process_start_time": birth, "root": matches[0]}

    def attributes(self, node):
        role, name = node.get_role_name(), node.get_name() or ""
        value = ""
        accessible_text = node.get_text_iface()
        if accessible_text and role != "password text":
            if accessible_text.get_character_count() > 1000: self.value_truncated = True
            value = accessible_text.get_text(0, min(1000, accessible_text.get_character_count())) or ""
        action = node.get_action_iface()
        actions = [action.get_action_name(index) for index in range(action.get_n_actions())] if action else []
        component = node.get_component_iface()
        rect = component.get_extents(Atspi.CoordType.SCREEN) if component else None
        details = {"role": role, "title": name, "value": value, "actions": actions,
                "settable": bool(node.get_editable_text_iface()),
                "frame": {"x": rect.x, "y": rect.y, "width": rect.width, "height": rect.height} if rect else None,
                "identifier": node.get_accessible_id() or ""}
        if accessible_text and role != "password text": details["caret_offset"] = accessible_text.get_caret_offset()
        return details

    def retain(self, node, target):
        self.counter += 1
        attrs = self.attributes(node)
        fingerprint = tuple(json.dumps(attrs[key], sort_keys=True) for key in ("role", "title", "frame", "identifier"))
        self.elements[str(self.counter)] = (node, fingerprint, target["window_id"])
        return self.counter, attrs

    def element(self, raw, target):
        if not isinstance(raw, str) or raw not in self.elements:
            raise RuntimeError("Element index is stale or was not observed on this connection")
        node, fingerprint, window_id = self.elements[raw]
        attrs = self.attributes(node)
        current = tuple(json.dumps(attrs[key], sort_keys=True) for key in ("role", "title", "frame", "identifier"))
        if window_id != target["window_id"] or node.get_process_id() != target["pid"] or fingerprint != current or node.get_state_set().contains(Atspi.StateType.DEFUNCT):
            raise RuntimeError("Observed element identity or geometry changed; observe again")
        parent = node
        for _ in range(64):
            if parent == target["root"]:
                return node
            parent = parent.get_parent() if parent else None
        raise RuntimeError("Observed element no longer belongs to the selected window")

    def observe(self, args, query_only=False):
        self.invalidate()
        self.value_truncated = False
        target = self.resolve(args)
        self.target = target
        maximum = integer(args.get("max_nodes" if query_only else "max_tree_nodes"), 1200, 1, 5000)
        maximum_depth = 64 if query_only else min(64, integer(args.get("max_tree_depth"), 64, 1, 2**31-1))
        raw_text_limit = args.get("text_limit", 500)
        text_limit = 2_000_000 if raw_text_limit == "max" else integer(raw_text_limit, 500, 1, 2_000_000)
        matches, lines, visited = [], [], 0
        limit = integer(args.get("limit"), 20, 1, 100)
        needle, wanted_role = args.get("text", ""), args.get("role", "")
        if query_only and (not isinstance(needle, str) or not isinstance(wanted_role, str) or len(needle) > 1000 or len(wanted_role) > 1000 or not (needle.strip() or wanted_role.strip())):
            raise ValueError("Query needs text or role")
        traversal = {}
        if query_only or args.get("include_ax", True):
            for node, depth in self.nodes(target["root"], maximum, traversal, maximum_depth):
                visited += 1; index, attrs = self.retain(node, target)
                candidate = " | ".join((attrs["title"], node.get_description() or "", attrs["value"]))
                if query_only:
                    matched = candidate.casefold() == needle.casefold() if args.get("exact") else needle.casefold() in candidate.casefold()
                    # Exact matching applies independently to exposed text fields.
                    if args.get("exact"):
                        matched = any(needle.casefold() == part.casefold() for part in (attrs["title"], node.get_description() or "", attrs["value"]))
                    if (not needle or matched) and (not wanted_role or attrs["role"].casefold() == wanted_role.casefold()):
                        matches.append({"index": index, **attrs})
                        if len(matches) >= limit:
                            break
                else:
                    lines.append("  " * depth + f"[{index}] {attrs['role']} " + candidate.replace("\n", "\\n") + f" settable={attrs['settable']} actions={attrs['actions']}")
        metadata = {key: target[key] for key in ("app", "app_name", "pid", "window_id", "process_start_time")}
        # Bounded search is conservative: reaching a budget never proves absence.
        truncated = bool(traversal.get("truncated") or self.value_truncated or visited >= maximum or (query_only and len(matches) >= limit))
        if query_only:
            return text({**metadata, "window_frame": target["frame"], "matches": matches, "truncated": truncated, "visited": visited})
        rendered = "\n".join(lines)
        if len(rendered) > text_limit:
            rendered = rendered[:text_limit]; truncated = True
        content = []
        if args.get("include_screenshot", True):
            frame, rgba = self.x.pixels(target["window_id"])
            pixbuf = GdkPixbuf.Pixbuf.new_from_bytes(GLib.Bytes.new(rgba), GdkPixbuf.Colorspace.RGB, True, 8, frame["width"], frame["height"], frame["width"] * 4)
            scale = min(1, 1280 / max(frame["width"], frame["height"]))
            if scale < 1:
                pixbuf = pixbuf.scale_simple(max(1, round(frame["width"] * scale)), max(1, round(frame["height"] * scale)), GdkPixbuf.InterpType.BILINEAR)
            self.image_size = (pixbuf.get_width(), pixbuf.get_height())
            success, png = pixbuf.save_to_bufferv("png", [], [])
            if not success or self.x.frame(target["window_id"]) != frame:
                raise RuntimeError("Window changed during capture; observe again")
            content.append({"type": "image", "mimeType": "image/png", "data": base64.b64encode(png).decode()})
        metadata.update(window_frame=target["frame"], truncated=truncated, image_width=self.image_size[0] if self.image_size else 0, image_height=self.image_size[1] if self.image_size else 0)
        content.insert(0, {"type": "text", "text": json.dumps(metadata) + "\n" + rendered})
        return {"content": content}

    def global_focus(self, target):
        if CANCELLED.is_set() or self.x.active() != target["window_id"] or self.x.frame(target["window_id"]) != target["frame"]:
            raise RuntimeError("Focus, window geometry or cancellation changed; input may be partial, observe again")

    def insert_text(self, value, target):
        if not isinstance(value, str) or len(value) > 2_000_000:
            raise ValueError("Type needs text up to 2000000 characters")
        focused = [node for node, _depth in self.nodes(target["root"]) if node.get_state_set().contains(Atspi.StateType.FOCUSED) and node.get_editable_text_iface()]
        if len(focused) != 1:
            raise RuntimeError("No unique focused editable control is exposed; no guessed keyboard typing was attempted")
        node = focused[0]; accessible_text = node.get_text_iface(); editable = node.get_editable_text_iface()
        if not accessible_text or accessible_text.get_character_count() > 2_000_000:
            raise RuntimeError("Focused text is unavailable or exceeds the supported edit size")
        before = accessible_text.get_text(0, -1)
        start = end = accessible_text.get_caret_offset()
        if accessible_text.get_n_selections():
            selected = accessible_text.get_selection(0); start, end = selected.start_offset, selected.end_offset
        if not 0 <= start <= end <= len(before): raise RuntimeError("The current text selection is invalid")
        self.global_focus(target)
        if end > start and not editable.delete_text(start, end): raise RuntimeError("Selection deletion failed; observe before continuing")
        # AT-SPI positions are Unicode characters, but insertion length is UTF-8 bytes.
        if not editable.insert_text(start, value, len(value.encode("utf-8"))): raise RuntimeError("Text insertion failed; outcome may be partial, do not replay")
        if accessible_text.get_text(0, -1) != before[:start] + value + before[end:]: raise RuntimeError("Inserted text read-back differs; do not replay")
        accessible_text.set_caret_offset(start + len(value))

    def point(self, raw, target):
        if not isinstance(raw, list) or len(raw) != 2 or not self.image_size or any(isinstance(value, bool) or not isinstance(value, (int, float)) for value in raw):
            raise ValueError("Coordinates require a fresh screenshot of this window")
        if not 0 <= raw[0] < self.image_size[0] or not 0 <= raw[1] < self.image_size[1]:
            raise ValueError("Coordinates are outside the observed screenshot")
        frame = target["frame"]
        return frame["x"] + raw[0] / self.image_size[0] * frame["width"], frame["y"] + raw[1] / self.image_size[1] * frame["height"]

    def semantic(self, operation, values, target):
        node = self.element(values.get("elementID"), target)
        if operation == "setValue":
            value = values.get("value")
            if not isinstance(value, str) or len(value) > 2_000_000 or values.get("autosubmitSearchFields"):
                raise ValueError("setValue needs text without implicit autosubmit")
            editable = node.get_editable_text_iface()
            if not editable or not editable.set_text_contents(value):
                raise RuntimeError("The control does not expose a writable text interface")
            if node.get_text_iface().get_text(0, -1) != value:
                raise RuntimeError("Set value read-back differs; do not replay input")
        elif operation == "selectText":
            accessible_text = node.get_text_iface()
            if not accessible_text:
                raise RuntimeError("The control exposes no text selection interface")
            value = accessible_text.get_text(0, -1)
            needle = values.get("text")
            if not isinstance(needle, str) or not needle or len(value) > 2_000_000:
                raise ValueError("Selection needs non-empty text within the supported size")
            candidates, offset = [], 0
            while True:
                start = value.find(needle, offset)
                if start < 0:
                    break
                end = start + len(needle)
                if ("prefix" not in values or value[:start].endswith(values["prefix"])) and ("suffix" not in values or value[end:].startswith(values["suffix"])):
                    candidates.append((start, end))
                offset = start + 1
            if len(candidates) != 1:
                raise RuntimeError("Selection is absent or ambiguous; use prefix and suffix")
            start, end = candidates[0]; mode = values.get("selection", "text")
            if mode == "text":
                ok = accessible_text.set_selection(0, start, end) if accessible_text.get_n_selections() else accessible_text.add_selection(start, end)
                selected = accessible_text.get_selection(0)
                if not ok or (selected.start_offset, selected.end_offset) != (start, end):
                    raise RuntimeError("Text selection read-back differs; do not replay")
            elif mode in ("cursorBefore", "cursorAfter"):
                position = start if mode == "cursorBefore" else end
                if not accessible_text.set_caret_offset(position) or accessible_text.get_caret_offset() != position:
                    raise RuntimeError("Caret position read-back differs")
            else:
                raise ValueError("Unknown selection mode")
        else:
            action = node.get_action_iface()
            names = [action.get_action_name(index) for index in range(action.get_n_actions())] if action else []
            requested = values.get("action") if operation == "performSecondaryAction" else next((name for name in names if name in ("click", "press", "activate")), None)
            if requested not in names:
                return False
            if not action.do_action(names.index(requested)):
                raise RuntimeError("Semantic action failed; outcome is unknown")
        return True

    def perform(self, args):
        if not self.target or args.get("app") != self.target["app"]:
            raise RuntimeError("Observe this app before input")
        previous = self.target
        target = self.resolve({"app": previous["app"], "window_id": previous["window_id"]})
        if target["frame"] != previous["frame"] or target["pid"] != previous["pid"] or target["process_start_time"] != previous["process_start_time"]:
            self.invalidate(); raise RuntimeError("Observed target moved or changed; observe again")
        action = args.get("action", {})
        if not isinstance(action, dict) or len(action) != 1:
            raise ValueError("Exactly one action is required")
        operation, values = next(iter(action.items()))
        mode = args.get("interactionMode", "foreground")
        if mode not in ("foreground", "background"):
            raise ValueError("Unknown interaction mode")
        background = mode == "background"
        if background and (operation not in ("click", "setValue", "selectText", "performSecondaryAction") or (operation == "click" and ("elementID" not in values or values.get("clickCount", 1) != 1 or values.get("mouseButton", "left") != "left" or values.get("key") or values.get("duration", 0))) or values.get("autosubmitSearchFields")):
            raise RuntimeError("Background action is not supported; no foreground fallback or global input was sent")
        active, pointer = self.x.active(), self.x.pointer()
        clipboard_generation = self.clipboard.generation() if background else None
        try:
            if operation in ("setValue", "selectText", "performSecondaryAction") or (operation == "click" and "elementID" in values and values.get("clickCount", 1) == 1 and not values.get("key") and not values.get("duration") and values.get("mouseButton", "left") == "left"):
                if self.semantic(operation, values, target):
                    delay(0.05)
                    if background and (self.x.active() != active or self.x.pointer() != pointer or self.clipboard.generation() != clipboard_generation):
                        raise RuntimeError("Background action was dispatched but foreground, pointer or clipboard changed; outcome is unknown")
                    return text("Semantic action dispatched; read-back verified where available. Old element IDs are invalid.")
                if background or operation != "click":
                    raise RuntimeError("The semantic action is unavailable; no global input was sent")
            self.x.activate(target["window_id"], CANCELLED.is_set)
            self.global_focus(target)
            if operation == "click":
                if "elementID" in values:
                    frame = self.attributes(self.element(values["elementID"], target))["frame"]
                    point = frame["x"] + frame["width"] / 2, frame["y"] + frame["height"] / 2
                else:
                    point = self.point(values.get("coordinate"), target)
                button = {"left": 1, "middle": 2, "right": 3}.get(values.get("mouseButton", "left"))
                count = integer(values.get("clickCount"), 1, 1, 3)
                if not button or values.get("key"):
                    raise ValueError("This X11 click path requires a supported button without modifiers")
                self.x.motion(*point)
                for _ in range(count):
                    self.global_focus(target); self.x.button(button, True)
                    try: delay(number(values.get("duration"), 0, 0, 10))
                    finally: self.x.button(button, False)
            elif operation == "pressKey":
                parts = values.get("key", "").split("+")
                aliases = {"super": "Super_L", "ctrl": "Control_L", "alt": "Alt_L", "shift": "Shift_L", "Enter": "Return", "Left": "Left", "Right": "Right", "Up": "Up", "Down": "Down", "Space": "space", "Backspace": "BackSpace"}
                codes = [self.x.keycode(aliases.get(part, part)) for part in parts]
                held = []
                try:
                    for code in codes:
                        self.global_focus(target); self.x.key(code, True); held.append(code)
                    delay(number(values.get("duration"), 0, 0, 10))
                finally:
                    for code in reversed(held): self.x.key(code, False)
            elif operation == "scroll":
                point = self.point(values["coordinate"], target) if "coordinate" in values else (target["frame"]["x"] + target["frame"]["width"] / 2, target["frame"]["y"] + target["frame"]["height"] / 2)
                if "elementID" in values:
                    frame = self.attributes(self.element(values["elementID"], target))["frame"]
                    point = frame["x"] + frame["width"] / 2, frame["y"] + frame["height"] / 2
                button = {"up": 4, "down": 5, "left": 6, "right": 7}.get(values.get("direction"))
                if not button: raise ValueError("Unknown scroll direction")
                self.x.motion(*point)
                for _ in range(max(1, round(number(values.get("pages"), 1, 0.01, 100) * 10))):
                    self.global_focus(target); self.x.button(button, True); self.x.button(button, False)
            elif operation == "drag":
                start, end = self.point(values.get("from"), target), self.point(values.get("to"), target)
                self.x.motion(*start); self.x.button(1, True)
                try:
                    for step in range(1, 21):
                        self.global_focus(target)
                        self.x.motion(start[0] + (end[0] - start[0]) * step / 20, start[1] + (end[1] - start[1]) * step / 20)
                        delay(0.01)
                finally: self.x.button(1, False)
            elif operation == "type": self.insert_text(values.get("text"), target)
            elif operation == "paste": self.paste(values, target)
            else:
                raise RuntimeError(f"Action {operation} has no validated X11 implementation; no fallback was used")
            return text("X11 input dispatched. Verify the intended effect; old element IDs are invalid.")
        finally:
            self.invalidate()

    def dispatch(self, kind, args):
        if kind == "ComputerUseIPCHandshakeRequest":
            return {"protocolVersion": PROTOCOL, "serverVersion": "1", "serviceSourceSHA256": self.source_hash, "tools": self.tools}
        if kind == "ComputerUseIPCPermissionStatusRequest":
            return text({"backend": "x11-atspi", "service_pid": os.getpid(), "runtime_directory": str(Path(__file__).resolve().parent), "session_type": "x11", "accessibilityTrusted": bool(Atspi.get_desktop(0)), "screenCaptureGranted": True, "requires_xcomposite": True, "wayland_supported": False})
        if kind in ("ComputerUseIPCListAppsRequest", "ComputerUseIPCFindAppsRequest"):
            apps = {}; needle = args.get("text", "").casefold()
            for window in self.x.windows():
                pid = window["pid"]
                if pid in (os.getpid(), os.getppid()): continue
                name = Path(f"/proc/{pid}/comm").read_text().strip()
                if needle and needle not in name.casefold() and needle not in window["title"].casefold(): continue
                record = apps.setdefault(str(pid), {"id": str(pid), "pid": pid, "display_name": name, "is_running": True, "windows": []})
                record["windows"].append(window)
            installed = self.installed_apps(needle)
            records = list(apps.values()) + installed
            limit = integer(args.get("limit"), 100, 1, 100)
            return text({"apps": records[:limit], "truncated": len(records) > limit, "discovery_scope": "running_and_desktop_entries"})
        if kind == "ComputerUseIPCAppStartRequest": return self.launch(args)
        if kind == "ComputerUseIPCFrontmostWindowRequest":
            window = self.x.active(); pid = self.x.owner(window) if window else 0
            return text({"app": str(pid), "pid": pid, "window_id": window} if pid else {"window": None})
        if kind in ("ComputerUseIPCAppGetSkyshotRequest", "ComputerUseIPCAppQueryRequest"):
            try: return self.observe(args, kind.endswith("QueryRequest"))
            except Exception: self.invalidate(); raise
        if kind == "ComputerUseIPCAppPerformActionRequest": return self.perform(args)
        if kind in ("ComputerUseIPCAppStopRequest", "ComputerUseIPCAppModifyRequest"):
            self.invalidate(); target = self.resolve(args)
            if kind.endswith("StopRequest"):
                self.x.message(target["window_id"], "_NET_CLOSE_WINDOW", [0, 2]); delay(0.2)
                try:
                    state = Path(f'/proc/{target["pid"]}/stat').read_text().rsplit(") ", 1)[1].split()
                    stopped = state[0] == "Z" or state[19] != target["process_start_time"]
                except FileNotFoundError: stopped = True
                return text({"app": target["app"], "verified": stopped, "normal_close_requested": True})
            if args.get("modification") == "activate": self.x.activate(target["window_id"], CANCELLED.is_set)
            elif args.get("modification") == "deactivate": self.x.x.XIconifyWindow(self.x.display, target["window_id"], self.x.x.XDefaultScreen(self.x.display)); self.x.x.XFlush(self.x.display)
            else: raise ValueError("Unknown app modification")
            return text({"app": target["app"], "modification": args["modification"]})
        raise ValueError("Unsupported desktop IPC request")


def main():
    service = Service(sys.argv[1])
    requests = queue.Queue(maxsize=16)
    def read_requests():
        try:
            while True:
                line = sys.stdin.buffer.readline(16 * 1024 * 1024 + 1)
                if not line: return
                if len(line) > 16 * 1024 * 1024 or not line.endswith(b"\n"): return
                requests.put(json.loads(line), timeout=1)
        finally: CANCELLED.set()
    threading.Thread(target=read_requests, daemon=True).start()
    while not CANCELLED.is_set():
        pump()
        try: frame = requests.get(timeout=0.02)
        except queue.Empty: continue
        try: response = {"id": frame["id"], "result": service.dispatch(frame["requestType"], frame.get("request", {}))}
        except Exception as error: response = {"id": frame.get("id"), "error": {"message": str(error)}}
        print(json.dumps(response, ensure_ascii=False), flush=True)
    # X11 selections are served by their owner. Retain only restored bytes after
    # EOF, without accepting requests or dispatching input, until the next copy.
    service.invalidate()
    service.x.x.XCloseDisplay(service.x.display)
    Atspi.exit()
    if service.clipboard.owns_restored():
        control = os.environ.get("AICLAW_COMPUTER_CONTROL_FD")
        if control == "3":
            try: os.write(3, b'{"event":"clipboard-retained"}\n')
            finally: os.close(3)
        while service.clipboard.owns_restored():
            pump(); time.sleep(0.02)


if __name__ == "__main__":
    main()
