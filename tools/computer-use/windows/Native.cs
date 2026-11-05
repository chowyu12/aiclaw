// AIClaw-owned Win32 binding. UI Automation and orchestration live in Service.ps1.
using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using System.Threading;

namespace AIClawComputerUse {
    public static class Native {
        [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] public struct Point { public int X, Y; }
        [StructLayout(LayoutKind.Sequential)] struct Mouse { public int X, Y; public uint Data, Flags, Time; public UIntPtr Extra; }
        [StructLayout(LayoutKind.Sequential)] struct KeyInput { public ushort VK, Scan; public uint Flags, Time; public UIntPtr Extra; }
        [StructLayout(LayoutKind.Explicit)] struct InputData { [FieldOffset(0)] public Mouse Mouse; [FieldOffset(0)] public KeyInput Key; }
        [StructLayout(LayoutKind.Sequential)] struct Input { public uint Type; public InputData Data; }
        public class Window {
            public long ID; public int PID; public string Title, Name, Executable, Birth;
            public Rect Frame;
        }
        delegate bool EnumCallback(IntPtr window, IntPtr data);
        [DllImport("user32.dll")] static extern bool EnumWindows(EnumCallback callback, IntPtr data);
        [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr window);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetWindowText(IntPtr window, StringBuilder text, int count);
        [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
        [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out Rect rect);
        [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
        [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr window, int command);
        [DllImport("user32.dll")] public static extern bool GetCursorPos(out Point point);
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
        [DllImport("user32.dll")] static extern IntPtr WindowFromPoint(Point point);
        [DllImport("user32.dll")] static extern IntPtr GetAncestor(IntPtr window, uint flags);
        [DllImport("user32.dll", SetLastError = true)] static extern uint SendInput(uint count, Input[] input, int size);
        [DllImport("user32.dll")] static extern bool PrintWindow(IntPtr window, IntPtr dc, uint flags);
        [DllImport("user32.dll", SetLastError = true)] public static extern bool PostMessage(IntPtr window, uint message, IntPtr w, IntPtr l);
        [DllImport("user32.dll")] public static extern uint GetClipboardSequenceNumber();
        [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
        [DllImport("advapi32.dll", SetLastError = true)] static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);
        [DllImport("advapi32.dll", SetLastError = true)] static extern bool GetTokenInformation(IntPtr token, int kind, IntPtr buffer, int length, out int returned);
        static readonly ConcurrentQueue<string> Frames = new ConcurrentQueue<string>();
        static string SelfSID() { using (var identity = WindowsIdentity.GetCurrent()) { return identity.User.Value; } }
        static readonly string UserSID = SelfSID();
        public static volatile bool ParentEnded;
        public static void BeginInput() {
            var thread = new Thread(() => {
                try {
                    string line;
                    while ((line = Console.In.ReadLine()) != null) {
                        if (line.Length > 16 * 1024 * 1024 || Frames.Count >= 16) return;
                        Frames.Enqueue(line);
                    }
                } finally { ParentEnded = true; }
            });
            thread.IsBackground = true; thread.Start();
        }
        public static bool ReadFrame(out string frame) { return Frames.TryDequeue(out frame); }
        public static bool SameUser(int pid) {
            IntPtr process = OpenProcess(0x1000, false, (uint)pid), token = IntPtr.Zero, buffer = IntPtr.Zero;
            if (process == IntPtr.Zero) return false;
            try {
                if (!OpenProcessToken(process, 8, out token)) return false;
                int length;
                GetTokenInformation(token, 1, IntPtr.Zero, 0, out length);
                if (length <= 0 || length > 65536) return false;
                buffer = Marshal.AllocHGlobal(length);
                if (!GetTokenInformation(token, 1, buffer, length, out length)) return false;
                return new SecurityIdentifier(Marshal.ReadIntPtr(buffer)).Value == UserSID;
            } finally {
                if (buffer != IntPtr.Zero) Marshal.FreeHGlobal(buffer);
                if (token != IntPtr.Zero) CloseHandle(token);
                CloseHandle(process);
            }
        }
        public static Window[] Windows(int pid) {
            var result = new List<Window>();
            EnumCallback callback = (window, data) => {
                uint owner;
                GetWindowThreadProcessId(window, out owner);
                if (!IsWindowVisible(window) || (pid != 0 && owner != pid) || !SameUser((int)owner)) return true;
                var title = new StringBuilder(2048); GetWindowText(window, title, title.Capacity);
                Rect rect;
                if (title.Length == 0 || !GetWindowRect(window, out rect) || rect.Right <= rect.Left || rect.Bottom <= rect.Top) return true;
                try {
                    using (var process = Process.GetProcessById((int)owner)) {
                        result.Add(new Window { ID = window.ToInt64(), PID = (int)owner, Title = title.ToString(), Name = process.ProcessName,
                            Executable = process.MainModule.FileName, Birth = process.StartTime.ToUniversalTime().Ticks.ToString(), Frame = rect });
                    }
                } catch (InvalidOperationException) { } catch (System.ComponentModel.Win32Exception) { }
                return true;
            };
            EnumWindows(callback, IntPtr.Zero); GC.KeepAlive(callback); return result.ToArray();
        }
        public static int WindowPID(long window) { uint pid; GetWindowThreadProcessId(new IntPtr(window), out pid); return (int)pid; }
        public static bool SameRect(Rect a, Rect b) { return a.Left == b.Left && a.Top == b.Top && a.Right == b.Right && a.Bottom == b.Bottom; }
        public static bool PointBelongsTo(long window, int x, int y) { return GetAncestor(WindowFromPoint(new Point { X = x, Y = y }), 2).ToInt64() == window; }
        static void Emit(Input input) {
            if (SendInput(1, new Input[] { input }, Marshal.SizeOf(typeof(Input))) != 1)
                throw new InvalidOperationException("Input was blocked or incompletely dispatched (including possible UIPI restrictions); do not replay");
        }
        public static void Key(ushort key, bool down) { Emit(new Input { Type = 1, Data = new InputData { Key = new KeyInput { VK = key, Flags = down ? 0u : 2u } } }); }
        public static void Unicode(ushort unit, bool down) { Emit(new Input { Type = 1, Data = new InputData { Key = new KeyInput { Scan = unit, Flags = 4u | (down ? 0u : 2u) } } }); }
        public static void Button(int button, bool down) {
            uint flag = button == 1 ? (down ? 2u : 4u) : button == 2 ? (down ? 0x20u : 0x40u) : (down ? 8u : 0x10u);
            Emit(new Input { Data = new InputData { Mouse = new Mouse { Flags = flag } } });
        }
        public static void Wheel(int delta, bool horizontal) { Emit(new Input { Data = new InputData { Mouse = new Mouse { Flags = horizontal ? 0x1000u : 0x800u, Data = unchecked((uint)delta) } } }); }
        public static byte[] Capture(long window) {
            Rect rect;
            if (!GetWindowRect(new IntPtr(window), out rect)) throw new InvalidOperationException("Window is no longer available");
            int width = rect.Right - rect.Left, height = rect.Bottom - rect.Top;
            if (width <= 0 || height <= 0 || (long)width * height > 64000000) throw new InvalidOperationException("Window capture dimensions are unsupported");
            using (var bitmap = new Bitmap(width, height, PixelFormat.Format32bppRgb)) {
                using (var graphics = Graphics.FromImage(bitmap)) {
                    IntPtr dc = graphics.GetHdc();
                    try { if (!PrintWindow(new IntPtr(window), dc, 2)) throw new InvalidOperationException("Target-only PrintWindow capture failed; no desktop-screen fallback was used"); }
                    finally { graphics.ReleaseHdc(dc); }
                }
                double scale = Math.Min(1.0, 1280.0 / Math.Max(width, height));
                using (var scaled = new Bitmap(bitmap, Math.Max(1, (int)Math.Round(width * scale)), Math.Max(1, (int)Math.Round(height * scale))))
                using (var stream = new MemoryStream()) { scaled.Save(stream, ImageFormat.Png); return stream.ToArray(); }
            }
        }
    }
}
