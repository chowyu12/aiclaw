"""Small owned GTK geometry fixture; no network, clipboard or user files."""
import gi

gi.require_version("Gtk", "3.0")
from gi.repository import Gtk, GLib

GLib.set_prgname("aiclaw-overlay-scale-fixture")

window = Gtk.Window(title="AIClaw Linux Scale Fixture")
window.set_default_size(900, 500)
entry = Gtk.Entry()
entry.get_accessible().set_name("Fixture input")
entry.set_text("Linux scale fixture")
window.add(entry)
window.connect("destroy", Gtk.main_quit)
window.show_all()

def place():
    window.move(600, 400)
    window.resize(900, 500)
    print("ready", flush=True)
    return False

GLib.timeout_add(250, place)
Gtk.main()
