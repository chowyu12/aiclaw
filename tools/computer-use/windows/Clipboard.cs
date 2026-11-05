// Raw clipboard transaction: never deserialize clipboard objects or caller code.
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Windows.Forms;

namespace AIClawComputerUse {
    public sealed class ClipboardTransaction : IDisposable {
        [DllImport("user32.dll", SetLastError=true)] static extern bool OpenClipboard(IntPtr owner);
        [DllImport("user32.dll")] static extern bool CloseClipboard();
        [DllImport("user32.dll")] static extern bool EmptyClipboard();
        [DllImport("user32.dll")] static extern IntPtr GetClipboardOwner();
        [DllImport("user32.dll", SetLastError=true)] static extern uint EnumClipboardFormats(uint previous);
        [DllImport("user32.dll")] static extern IntPtr GetClipboardData(uint format);
        [DllImport("user32.dll")] static extern IntPtr SetClipboardData(uint format, IntPtr data);
        [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern uint RegisterClipboardFormat(string name);
        [DllImport("kernel32.dll", SetLastError=true)] static extern void SetLastError(uint error);
        [DllImport("kernel32.dll")] static extern UIntPtr GlobalSize(IntPtr data);
        [DllImport("kernel32.dll")] static extern IntPtr GlobalAlloc(uint flags, UIntPtr size);
        [DllImport("kernel32.dll")] static extern IntPtr GlobalLock(IntPtr data);
        [DllImport("kernel32.dll")] static extern bool GlobalUnlock(IntPtr data);
        [DllImport("kernel32.dll")] static extern IntPtr GlobalFree(IntPtr data);
        [DllImport("ole32.dll")] static extern IntPtr OleDuplicateData(IntPtr source, ushort format, uint flags);
        [DllImport("gdi32.dll")] static extern int GetObject(IntPtr handle, int size, out BitmapInfo bitmap);
        [DllImport("gdi32.dll")] static extern uint GetPaletteEntries(IntPtr handle, uint first, uint count, IntPtr entries);
        [DllImport("gdi32.dll")] static extern uint GetMetaFileBitsEx(IntPtr handle, uint size, IntPtr bytes);
        [DllImport("gdi32.dll")] static extern uint GetEnhMetaFileBits(IntPtr handle, uint size, IntPtr bytes);
        [DllImport("gdi32.dll", CharSet=CharSet.Unicode)] static extern IntPtr CopyEnhMetaFile(IntPtr handle, string file);
        [DllImport("gdi32.dll")] static extern bool DeleteObject(IntPtr handle);
        [DllImport("gdi32.dll")] static extern bool DeleteMetaFile(IntPtr handle);
        [DllImport("gdi32.dll")] static extern bool DeleteEnhMetaFile(IntPtr handle);
        [StructLayout(LayoutKind.Sequential)] struct BitmapInfo { public int Type, Width, Height, WidthBytes; public ushort Planes, BitsPixel; public IntPtr Bits; }
        [StructLayout(LayoutKind.Sequential)] struct MetaPicture { public int Mode, X, Y; public IntPtr Handle; }
        sealed class Item { public uint Format; public IntPtr Handle; }
        const long Capacity = 16 * 1024 * 1024;
        static Control owner;
        static Thread worker;
        static Exception startupFailure;
        static readonly ManualResetEvent ready = new ManualResetEvent(false);
        readonly List<Item> original = new List<Item>();
        uint generation;
        bool disposed;
        public bool Restored { get; private set; }
        public bool NewerCopyPreserved { get; private set; }

        static void OnOwner(Action action) {
            if (worker == null) {
                worker = new Thread(() => {
                    try {
                        owner = new Control(); var handle = owner.Handle; ready.Set();
                        Application.Run();
                    } catch (Exception error) { startupFailure = error; ready.Set(); }
                    finally { if (owner != null) owner.Dispose(); }
                });
                worker.SetApartmentState(ApartmentState.STA); worker.IsBackground = true; worker.Start();
            }
            if (!ready.WaitOne(5000) || startupFailure != null || owner == null || owner.IsDisposed)
                throw new InvalidOperationException("Clipboard owner could not initialize", startupFailure);
            owner.Invoke(action);
        }
        static void Open() {
            for (int attempt = 0; attempt < 50; attempt++) {
                if (OpenClipboard(owner.Handle)) return;
                Thread.Sleep(10);
            }
            throw new InvalidOperationException("Clipboard is busy; no automatic paste replay");
        }
        static uint Kind(uint format) { return format == 0x82 ? 2u : format == 0x83 ? 3u : format == 0x8e ? 14u : format; }
        static long Size(IntPtr handle, uint kind) {
            if (kind == 2) {
                BitmapInfo info;
                if (GetObject(handle, Marshal.SizeOf(typeof(BitmapInfo)), out info) == 0) throw new InvalidOperationException("Bitmap clipboard data is unavailable");
                return Math.Abs((long)info.Height) * Math.Abs((long)info.WidthBytes);
            }
            if (kind == 9) return GetPaletteEntries(handle, 0, 0, IntPtr.Zero) * 4L;
            if (kind == 14) return GetEnhMetaFileBits(handle, 0, IntPtr.Zero);
            if (kind == 3) {
                var pointer = GlobalLock(handle);
                if (pointer == IntPtr.Zero) throw new InvalidOperationException("Metafile clipboard data is unavailable");
                try { return GetMetaFileBitsEx(((MetaPicture)Marshal.PtrToStructure(pointer, typeof(MetaPicture))).Handle, 0, IntPtr.Zero); }
                finally { GlobalUnlock(handle); }
            }
            return checked((long)GlobalSize(handle).ToUInt64());
        }
        static void Release(Item item) {
            if (item.Handle == IntPtr.Zero) return;
            uint kind = Kind(item.Format);
            if (kind == 2 || kind == 9) DeleteObject(item.Handle);
            else if (kind == 14) DeleteEnhMetaFile(item.Handle);
            else {
                if (kind == 3) {
                    var pointer = GlobalLock(item.Handle);
                    if (pointer != IntPtr.Zero) {
                        try { DeleteMetaFile(((MetaPicture)Marshal.PtrToStructure(pointer, typeof(MetaPicture))).Handle); }
                        finally { GlobalUnlock(item.Handle); }
                    }
                }
                GlobalFree(item.Handle);
            }
            item.Handle = IntPtr.Zero;
        }
        static Item Bytes(uint format, byte[] bytes) {
            var handle = GlobalAlloc(0x42, new UIntPtr((uint)bytes.Length));
            if (handle == IntPtr.Zero) throw new InvalidOperationException("Clipboard allocation failed");
            var pointer = GlobalLock(handle);
            if (pointer == IntPtr.Zero) { GlobalFree(handle); throw new InvalidOperationException("Clipboard buffer cannot be written"); }
            try { Marshal.Copy(bytes, 0, pointer, bytes.Length); }
            finally { GlobalUnlock(handle); }
            return new Item { Format = format, Handle = handle };
        }
        static void Publish(List<Item> items) {
            if (!EmptyClipboard()) throw new InvalidOperationException("Clipboard replacement was refused");
            foreach (var item in items) {
                if (SetClipboardData(item.Format, item.Handle) == IntPtr.Zero) throw new InvalidOperationException("A clipboard format could not be restored; outcome unknown");
                item.Handle = IntPtr.Zero; // Ownership transferred to the system.
            }
        }
        static byte[] HTML(string fragment) {
            const string prefix = "<html><body><!--StartFragment-->", suffix = "<!--EndFragment--></body></html>";
            const string template = "Version:1.0\r\nStartHTML:{0:D10}\r\nEndHTML:{1:D10}\r\nStartFragment:{2:D10}\r\nEndFragment:{3:D10}\r\n";
            int header = Encoding.UTF8.GetByteCount(String.Format(template, 0, 0, 0, 0));
            int start = header + Encoding.UTF8.GetByteCount(prefix), end = start + Encoding.UTF8.GetByteCount(fragment);
            return Encoding.UTF8.GetBytes(String.Format(template, header, end + Encoding.UTF8.GetByteCount(suffix), start, end) + prefix + fragment + suffix + "\0");
        }
        static string PlainHTML(string html) {
            var timeout = TimeSpan.FromMilliseconds(200);
            var text = Regex.Replace(html, @"<(script|style)\b[^>]*>[\s\S]*?</\1\s*>", "", RegexOptions.IgnoreCase, timeout);
            text = Regex.Replace(text, @"<\s*(br\b[^>]*|/(p|div|li|h[1-6])\s*)>", "\n", RegexOptions.IgnoreCase, timeout);
            return System.Net.WebUtility.HtmlDecode(Regex.Replace(text, "<[^>]*>", "", RegexOptions.None, timeout));
        }
        public ClipboardTransaction(string text, string format) {
            if (text == null || text.Length > 2000000 || text.IndexOf('\0') >= 0 || (format != "text" && format != "html"))
                throw new ArgumentException("Windows paste currently requires bounded text or HTML without NUL");
            var payload = new List<Item>();
            try {
                payload.Add(Bytes(13, Encoding.Unicode.GetBytes((format == "html" ? PlainHTML(text) : text) + "\0")));
                if (format == "html") {
                    uint html = RegisterClipboardFormat("HTML Format");
                    if (html == 0) throw new InvalidOperationException("HTML clipboard format could not be registered");
                    payload.Add(Bytes(html, HTML(text)));
                }
                OnOwner(() => {
                    Open();
                    try {
                        uint previous = 0; long size = 0;
                        while (true) {
                            if (Native.ParentEnded) throw new InvalidOperationException("Desktop operation cancelled before paste");
                            SetLastError(0); uint formatID = EnumClipboardFormats(previous);
                            if (formatID == 0) {
                                if (Marshal.GetLastWin32Error() != 0) throw new InvalidOperationException("Clipboard format enumeration failed; no paste dispatched");
                                break;
                            }
                            previous = formatID;
                            if (original.Count >= 64 || formatID == 0x80 || (formatID >= 0x200 && formatID <= 0x3ff))
                                throw new InvalidOperationException("Clipboard contains too many or owner-managed formats; no paste dispatched");
                            IntPtr source = GetClipboardData(formatID); uint kind = Kind(formatID);
                            if (source == IntPtr.Zero) throw new InvalidOperationException("A clipboard format cannot be materialized; no paste dispatched");
                            long length = Size(source, kind); size = checked(size + length);
                            if (length <= 0 || size > Capacity) throw new InvalidOperationException("Clipboard exceeds retention capacity or has an unsupported handle; no paste dispatched");
                            IntPtr copy = kind == 14 ? CopyEnhMetaFile(source, null) : OleDuplicateData(source, (ushort)kind, 0);
                            if (copy == IntPtr.Zero) throw new InvalidOperationException("Clipboard format cannot be retained; no paste dispatched");
                            original.Add(new Item { Format = formatID, Handle = copy });
                        }
                        try { Publish(payload); }
                        catch { Publish(original); throw; }
                    } finally { CloseClipboard(); }
                    // Windows can advance the sequence when the write lock is
                    // closed. Capture the published generation after closing.
                    generation = Native.GetClipboardSequenceNumber();
                });
            } catch { foreach (var item in original) Release(item); throw; }
            finally { foreach (var item in payload) Release(item); }
        }
        public void VerifyCurrent() {
            OnOwner(() => {
                Open();
                try {
                    if (Native.GetClipboardSequenceNumber() != generation || GetClipboardOwner() != owner.Handle)
                        throw new InvalidOperationException("Clipboard changed before paste; no further keys dispatched");
                } finally { CloseClipboard(); }
            });
        }
        public void Dispose() {
            if (disposed) return; disposed = true;
            try {
                OnOwner(() => {
                    Open();
                    try {
                        if (Native.GetClipboardSequenceNumber() != generation || GetClipboardOwner() != owner.Handle) { NewerCopyPreserved = true; return; }
                        Publish(original); Restored = true;
                    } finally { CloseClipboard(); }
                });
            } finally { foreach (var item in original) Release(item); }
        }
        public static void Shutdown() {
            if (owner != null && !owner.IsDisposed) owner.BeginInvoke(new Action(() => Application.ExitThread()));
            if (worker != null) worker.Join(1000);
        }
    }
}
