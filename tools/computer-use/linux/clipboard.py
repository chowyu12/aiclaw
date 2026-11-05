"""GTK3 clipboard transaction with atomic ownership checks and raw-format restore.

On X11 the owner must stay alive to serve selection bytes. After parent EOF the
service may retain only the restored clipboard, with no input/IPC capability,
until another application takes ownership. No clipboard history is written.
"""
import ctypes as C
import ctypes.util
import gi
gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
from gi.repository import Gtk, Gdk, GLib


class Target(C.Structure):
    _fields_ = [("name", C.c_char_p), ("flags", C.c_uint), ("info", C.c_uint)]


class Clipboard:
    def __init__(self):
        Gtk.init([])
        self.display = Gdk.Display.get_default()
        if not self.display.supports_selection_notification():
            raise RuntimeError("Safe clipboard restoration requires XFixes selection notifications")
        self.clipboard = Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD)
        self.events = 0; self.token = None; self.restored = False; self.holder = None
        self.clipboard.connect("owner-change", self._changed)
        capsule = C.pythonapi.PyCapsule_GetPointer
        capsule.restype = C.c_void_p; capsule.argtypes = [C.py_object, C.c_char_p]
        self.clipboard_pointer = capsule(self.clipboard.__gpointer__, None)
        display_pointer = capsule(self.display.__gpointer__, None)
        self.gtk = C.CDLL(ctypes.util.find_library("gtk-3"))
        self.gdk = C.CDLL(ctypes.util.find_library("gdk-3"))
        self.x = C.CDLL(ctypes.util.find_library("X11"))
        self.gdk.gdk_x11_display_get_xdisplay.restype = C.c_void_p
        self.gdk.gdk_x11_display_get_xdisplay.argtypes = [C.c_void_p]
        self.connection = self.gdk.gdk_x11_display_get_xdisplay(display_pointer)
        self.x.XInternAtom.restype = C.c_ulong
        self.x.XInternAtom.argtypes = [C.c_void_p, C.c_char_p, C.c_int]
        self.atom = self.x.XInternAtom(self.connection, b"CLIPBOARD", False)
        self.x.XGetSelectionOwner.restype = C.c_ulong
        self.x.XGetSelectionOwner.argtypes = [C.c_void_p, C.c_ulong]
        for name in ("XGrabServer", "XUngrabServer", "XFlush"):
            getattr(self.x, name).argtypes = [C.c_void_p]
        self.get_type = C.CFUNCTYPE(None, C.c_void_p, C.c_void_p, C.c_uint, C.c_void_p)
        self.clear_type = C.CFUNCTYPE(None, C.c_void_p, C.c_void_p)
        self.gtk.gtk_clipboard_set_with_data.restype = C.c_int
        self.gtk.gtk_clipboard_set_with_data.argtypes = [C.c_void_p, C.POINTER(Target), C.c_uint, self.get_type, self.clear_type, C.c_void_p]
        self.gdk.gdk_atom_intern.restype = C.c_void_p
        self.gdk.gdk_atom_intern.argtypes = [C.c_char_p, C.c_int]
        self.gtk.gtk_selection_data_set.argtypes = [C.c_void_p, C.c_void_p, C.c_int, C.c_void_p, C.c_int]

    def _changed(self, _clipboard, _event):
        self.events += 1

    def sync(self):
        self.display.sync()
        context = GLib.MainContext.default()
        while context.pending(): context.iteration(False)

    def generation(self):
        self.sync()
        return self.x.XGetSelectionOwner(self.connection, self.atom), self.events

    def snapshot(self):
        initial = self.generation()
        success, targets = self.clipboard.wait_for_targets()
        data = {}; size = 0
        if success:
            for atom in targets:
                name = atom.name()
                if name in ("TARGETS", "MULTIPLE", "TIMESTAMP", "SAVE_TARGETS", "DELETE"): continue
                if len(data) >= 64: raise RuntimeError("Clipboard has too many formats; no paste was dispatched")
                selection = self.clipboard.wait_for_contents(atom)
                if selection is None or selection.get_length() < 0: raise RuntimeError("Clipboard format cannot be retained; no paste was dispatched")
                size += selection.get_length()
                if size > 16 * 1024 * 1024: raise RuntimeError("Clipboard exceeds retention capacity; no paste was dispatched")
                data[name] = (selection.get_data_type().name(), selection.get_format(), bytes(selection.get_data() or b""))
        if self.generation() != initial: raise RuntimeError("Clipboard changed during snapshot; no paste was dispatched")
        return data, initial

    def _set(self, data, restored):
        token = object(); self.token = token
        values = list(data.items())
        names = [name.encode("utf-8") for name, _ in values]
        entries = (Target * len(values))(*(Target(name, 0, index) for index, name in enumerate(names)))
        def get_data(_clipboard, selection, index, _context):
            kind, fmt, raw = values[index][1]
            buffer = C.create_string_buffer(raw)
            atom = self.gdk.gdk_atom_intern(kind.encode("utf-8"), False)
            self.gtk.gtk_selection_data_set(selection, atom, fmt, buffer, len(raw))
        def clear_data(_clipboard, _context):
            if self.token is token: self.token = None; self.restored = False
        get_callback, clear_callback = self.get_type(get_data), self.clear_type(clear_data)
        previous = self.holder  # Keep old callbacks alive while GTK clears its old owner.
        self.holder = (get_callback, clear_callback, entries, names, values)
        if not self.gtk.gtk_clipboard_set_with_data(self.clipboard_pointer, entries, len(entries), get_callback, clear_callback, None):
            self.token = None; raise RuntimeError("Clipboard ownership could not be established")
        self.restored = restored
        del previous

    def replace_if_unchanged(self, data, expected, restored=False):
        self.x.XGrabServer(self.connection)
        try:
            if self.generation() != expected: return None
            if data: self._set(data, restored)
            else: self.clipboard.clear(); self.token = None; self.restored = False
        finally:
            self.x.XUngrabServer(self.connection); self.x.XFlush(self.connection)
        return self.generation()

    def owns_restored(self):
        return self.restored and self.token is not None and bool(self.generation()[0])
