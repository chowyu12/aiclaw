"""Temporary GTK acceptance app; writes receipts only to its supplied temp path."""
import json
from pathlib import Path
import sys
import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib
import os

GLib.set_prgname("aiclaw-linux-fixture")
window = Gtk.Window(title=sys.argv[2] if len(sys.argv) > 2 else "AIClaw Linux Fixture")
window.set_default_size(420, 420)
css = Gtk.CssProvider()
color = "#cc3344" if "Target" in window.get_title() else "#3355cc"
css.load_from_data(("window { background-color: " + color + "; }").encode())
window.get_style_context().add_provider(css, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
window.connect("destroy", Gtk.main_quit)
box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10, margin=16)
window.add(box)
entry = Gtk.Entry()
entry.get_accessible().set_name("Fixture input")
entry.set_placeholder_text("Fixture input")
button = Gtk.Button(label="Submit fixture")
status = Gtk.Label(label="No input submitted")
pad = Gtk.DrawingArea(); pad.set_size_request(340, 110)
pad.get_accessible().set_name("Fixture gesture pad")
pad.add_events(Gdk.EventMask.BUTTON_PRESS_MASK | Gdk.EventMask.BUTTON_RELEASE_MASK | Gdk.EventMask.POINTER_MOTION_MASK | Gdk.EventMask.SCROLL_MASK)
counts = {"drags": 0, "scrolls": 0}
def motion(_widget, event):
    if event.state & Gdk.ModifierType.BUTTON1_MASK: counts["drags"] += 1
    return False
def scroll(_widget, _event):
    counts["scrolls"] += 1
    return True
pad.connect("motion-notify-event", motion)
pad.connect("scroll-event", scroll)
editor = Gtk.TextView(); editor.set_size_request(340, 100)
editor.get_accessible().set_name("Fixture rich editor")
browser = None
if os.environ.get("AICLAW_FIXTURE_RICH") == "1":
    # This browser only renders the inline acceptance document, without network.
    gi.require_version("WebKit2", "4.1")
    from gi.repository import WebKit2
    browser = WebKit2.WebView()
    browser.get_settings().set_enable_javascript(True)
    browser.set_size_request(340, 160)
    browser.get_accessible().set_name("Fixture HTML editor")
    browser.load_html("<!doctype html><html><body><div id='editor' contenteditable='true' style='height:130px'>HTML fixture</div></body></html>", "about:blank")
    box.pack_start(browser, False, False, 0)
def submit(_button):
    status.set_text("Submitted: " + entry.get_text())
    values = {"value": entry.get_text(), **counts}
    def save(html=""):
        Path(sys.argv[1]).write_text(json.dumps({**values, "html": html}, ensure_ascii=False))
    if browser:
        def evaluated(view, result, _data):
            save(view.evaluate_javascript_finish(result).to_string())
        browser.evaluate_javascript("document.getElementById('editor').innerHTML", -1, None, None, None, evaluated, None)
    else: save()
button.connect("clicked", submit)
for widget in (entry, button, status, pad, editor): box.pack_start(widget, False, False, 0)
window.show_all()
entry.grab_focus()
if os.environ.get("AICLAW_FIXTURE_CLIPBOARD_SEED") == "1":
    from clipboard import Clipboard
    clipboard = Clipboard()
    clipboard.replace_if_unchanged({"UTF8_STRING": ("UTF8_STRING", 8, b"original clipboard"), "application/x-aiclaw-test": ("application/x-aiclaw-test", 8, b"\x00\xfforiginal")}, clipboard.generation())
if browser:
    def loaded(_view, event):
        if event == WebKit2.LoadEvent.FINISHED: print("READY", flush=True)
    browser.connect("load-changed", loaded)
else: GLib.idle_add(lambda: (print("READY", flush=True), False)[1])
Gtk.main()
