"""Temporary GTK acceptance app; writes receipts only to its supplied temp path."""
import json
from pathlib import Path
import sys
import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, Gdk, GLib

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
def submit(_button):
    status.set_text("Submitted: " + entry.get_text())
    Path(sys.argv[1]).write_text(json.dumps({"value": entry.get_text(), **counts}, ensure_ascii=False))
button.connect("clicked", submit)
for widget in (entry, button, status, pad, editor): box.pack_start(widget, False, False, 0)
window.show_all()
entry.grab_focus()
GLib.idle_add(lambda: (print("READY", flush=True), False)[1])
Gtk.main()
