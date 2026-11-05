"""Runs only inside its private D-Bus/Xvfb desktop; never targets host apps."""
import base64
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time
from x11 import X11
import gi
gi.require_version("GdkPixbuf", "2.0")
from gi.repository import GdkPixbuf

here = Path(__file__).resolve().parent
manifest = sys.argv[1]
with tempfile.TemporaryDirectory(prefix="aiclaw-linux-acceptance-") as temp:
    target = subprocess.Popen([sys.executable, str(here / "fixture.py"), f"{temp}/receipt.json", "AIClaw Target Fixture"], stdout=subprocess.PIPE)
    sentinel = subprocess.Popen([sys.executable, str(here / "fixture.py"), f"{temp}/sentinel.json", "AIClaw Sentinel Fixture"], stdout=subprocess.PIPE)
    service = subprocess.Popen([sys.executable, str(here / "service.py"), manifest], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
    sequence = 0
    def call(kind, request=None):
        global sequence
        sequence += 1
        service.stdin.write(json.dumps({"id": sequence, "requestType": kind, "request": request or {}}) + "\n"); service.stdin.flush()
        if not select.select([service.stdout], [], [], 15)[0]: raise RuntimeError("Native IPC response timed out")
        line = service.stdout.readline()
        if not line: raise RuntimeError(f"Native IPC exited ({service.poll()})")
        frame = json.loads(line)
        if frame.get("error"): raise RuntimeError(frame["error"]["message"])
        return frame["result"]
    def payload(result): return json.loads(result["content"][0]["text"])
    def query(name, role=""):
        result = payload(call("ComputerUseIPCAppQueryRequest", {"app": str(target.pid), "text": name, "role": role, "exact": True}))
        assert not result["truncated"], result
        assert len(result["matches"]) == 1, result
        return str(result["matches"][0]["index"])
    def action(name, values, background=False):
        return call("ComputerUseIPCAppPerformActionRequest", {"app": str(target.pid), "interactionMode": "background" if background else "foreground", "action": {name: values}})
    try:
        for fixture in (target, sentinel):
            assert select.select([fixture.stdout], [], [], 10)[0] and fixture.stdout.readline() == b"READY\n"
        handshake = call("ComputerUseIPCHandshakeRequest")
        assert handshake["protocolVersion"] == "AIClawComputerUseIPC-1"
        for attempt in range(30):
            discovery = payload(call("ComputerUseIPCFindAppsRequest", {"text": "AIClaw Target Fixture"}))
            if any(item["id"] == str(target.pid) for item in discovery["apps"]): break
            time.sleep(0.1)
        assert any(item["id"] == str(target.pid) for item in discovery["apps"]), discovery
        x = X11()
        sentinel_window = x.windows(sentinel.pid)[0]["window_id"]
        x.activate(sentinel_window, lambda: False)
        foreground, pointer = x.active(), x.pointer()
        value = "Linux 后台 🌦️"
        action("setValue", {"elementID": query("Fixture input"), "value": value}, True)
        action("selectText", {"elementID": query("Fixture input"), "text": value, "selection": "text"}, True)
        action("click", {"elementID": query("Submit fixture")}, True)
        assert json.loads(Path(f"{temp}/receipt.json").read_text())["value"] == value
        assert x.active() == foreground and x.pointer() == pointer
        limited = call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": True, "include_screenshot": False, "text_limit": 1})["content"][0]["text"]
        header, rendered = limited.split("\n", 1)
        assert json.loads(header)["truncated"] and len(rendered) == 1
        shallow = call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": True, "include_screenshot": False, "text_limit": "max", "max_tree_depth": 1})["content"][0]["text"]
        assert json.loads(shallow.split("\n", 1)[0])["truncated"]
        observed = call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": True})
        png = next(item for item in observed["content"] if item["type"] == "image")
        image = base64.b64decode(png["data"])
        assert image.startswith(b"\x89PNG\r\n\x1a\n")
        loader = GdkPixbuf.PixbufLoader.new_with_type("png"); loader.write(image); loader.close()
        pixbuf = loader.get_pixbuf(); offset = 2 * pixbuf.get_rowstride() + 2 * pixbuf.get_n_channels()
        assert tuple(pixbuf.get_pixels()[offset:offset + 3]) == (204, 51, 68), "capture must contain the red target, not the blue foreground sentinel"
        assert x.active() == foreground and x.pointer() == pointer
        if os.environ.get("AICLAW_COMPUTER_EVIDENCE_DIR"):
            Path(os.environ["AICLAW_COMPUTER_EVIDENCE_DIR"], "linux-background-window.png").write_bytes(image)
        # Each subsequent input uses a newly observed target; never replay it.
        call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": False})
        action("pressKey", {"key": "Right"})
        query("Fixture input")
        caret = payload(call("ComputerUseIPCAppQueryRequest", {"app": str(target.pid), "text": "Fixture input", "exact": True}))["matches"][0]["caret_offset"]
        assert caret == len(value), (caret, len(value))
        call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": False})
        addition = " +输入 🌤️"
        action("type", {"text": addition})
        gesture = payload(call("ComputerUseIPCAppQueryRequest", {"app": str(target.pid), "text": "Fixture gesture pad", "exact": True}))["matches"][0]["frame"]
        state = call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": False})
        geometry = json.loads(state["content"][0]["text"].split("\n")[0])["window_frame"]
        start = [gesture["x"] - geometry["x"] + 20, gesture["y"] - geometry["y"] + 20]
        end = [start[0] + 80, start[1] + 40]
        action("drag", {"from": start, "to": end})
        call("ComputerUseIPCAppGetSkyshotRequest", {"app": str(target.pid), "include_ax": False})
        action("scroll", {"coordinate": end, "direction": "down", "pages": 1})
        action("click", {"elementID": query("Submit fixture")})
        receipt = json.loads(Path(f"{temp}/receipt.json").read_text())
        assert receipt["value"] == value + addition, receipt
        assert receipt["drags"] > 0 and receipt["scrolls"] > 0, receipt
        print(json.dumps({"passed": True, "backend": "x11-atspi", "native_ipc": True, "discovery": True, "background_value": value, "selection": True, "click_receipt": True, "foreground_unchanged": True, "pointer_unchanged": True, "window_only_png": True, "occluded_target_color_verified": True, "unicode_insertion": True, "key_read_back": True, **receipt}, ensure_ascii=False))
    finally:
        service.stdin.close()
        try: service.wait(timeout=4)
        except subprocess.TimeoutExpired: service.terminate(); service.wait(timeout=4)
        for fixture in (target, sentinel):
            fixture.terminate(); fixture.wait(timeout=4)
