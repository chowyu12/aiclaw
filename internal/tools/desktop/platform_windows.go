//go:build windows

package desktop

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func platformScreenshotFallback(path string, _ *rect) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bmp = New-Object System.Drawing.Bitmap($bounds.Width, $bounds.Height)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
$bmp.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose()
$bmp.Dispose()
`, strings.ReplaceAll(path, "'", "''"))
	return runPS(script)
}

func platformClick(x, y int, button string, clicks int) error {
	flags := "0x0002, 0x0004" // MOUSEEVENTF_LEFTDOWN, MOUSEEVENTF_LEFTUP
	if button == "right" {
		flags = "0x0008, 0x0010"
	}
	script := fmt.Sprintf(`
Add-Type -MemberDefinition '
[DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
[DllImport("user32.dll")] public static extern void mouse_event(int dwFlags, int dx, int dy, int dwData, int dwExtraInfo);
' -Name WinAPI -Namespace Desktop
[Desktop.WinAPI]::SetCursorPos(%d, %d)
Start-Sleep -Milliseconds 50
for ($i = 0; $i -lt %d; $i++) {
    foreach ($f in @(%s)) { [Desktop.WinAPI]::mouse_event($f, 0, 0, 0, 0) }
    Start-Sleep -Milliseconds 50
}
`, x, y, clicks, flags)
	return runPS(script)
}

func platformTypeText(text string) error {
	escaped := strings.ReplaceAll(text, "'", "''")
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait('%s')
`, escaped)
	return runPS(script)
}

func platformKeyPress(key string) error {
	sendKey := translateKeyWindows(key)
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait('%s')
`, sendKey)
	return runPS(script)
}

func platformScroll(x, y, _, dy int) error {
	if x != 0 || y != 0 {
		_ = platformMouseMove(x, y)
	}
	script := fmt.Sprintf(`
Add-Type -MemberDefinition '
[DllImport("user32.dll")] public static extern void mouse_event(int dwFlags, int dx, int dy, int dwData, int dwExtraInfo);
' -Name WinAPI -Namespace Desktop
[Desktop.WinAPI]::mouse_event(0x0800, 0, 0, %d, 0)
`, dy*120)
	return runPS(script)
}

func platformMouseMove(x, y int) error {
	script := fmt.Sprintf(`
Add-Type -MemberDefinition '
[DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
' -Name WinAPI -Namespace Desktop
[Desktop.WinAPI]::SetCursorPos(%d, %d)
`, x, y)
	return runPS(script)
}

func platformListWindows() ([]windowInfo, error) {
	script := `
Get-Process | Where-Object { $_.MainWindowTitle -ne '' } | ForEach-Object {
    [PSCustomObject]@{ name = $_.ProcessName; title = $_.MainWindowTitle }
} | ConvertTo-Json -Compress
`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var windows []windowInfo
	if raw[0] == '[' {
		if err := json.Unmarshal([]byte(raw), &windows); err != nil {
			return nil, fmt.Errorf("parse windows: %w", err)
		}
	} else {
		var w windowInfo
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, fmt.Errorf("parse window: %w", err)
		}
		windows = append(windows, w)
	}
	return windows, nil
}

func platformFocusWindow(name string) error {
	escaped := strings.ReplaceAll(name, "'", "''")
	script := fmt.Sprintf(`
Add-Type -MemberDefinition '
[DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
' -Name WinAPI -Namespace Desktop
$p = Get-Process | Where-Object { $_.MainWindowTitle -like '*%s*' -and $_.MainWindowHandle -ne 0 } | Select-Object -First 1
if ($p) { [Desktop.WinAPI]::SetForegroundWindow($p.MainWindowHandle) } else { throw "Window not found: %s" }
`, escaped, escaped)
	return runPS(script)
}

func translateKeyWindows(key string) string {
	lower := strings.ToLower(key)
	parts := strings.Split(lower, "+")
	var mods []string
	mainKey := parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "ctrl", "control":
			mods = append(mods, "^")
		case "alt", "option":
			mods = append(mods, "%")
		case "shift":
			mods = append(mods, "+")
		}
	}

	specials := map[string]string{
		"enter": "{ENTER}", "return": "{ENTER}", "tab": "{TAB}",
		"escape": "{ESC}", "esc": "{ESC}", "backspace": "{BACKSPACE}",
		"delete": "{DELETE}", "space": " ",
		"up": "{UP}", "down": "{DOWN}", "left": "{LEFT}", "right": "{RIGHT}",
		"home": "{HOME}", "end": "{END}", "pageup": "{PGUP}", "pagedown": "{PGDN}",
		"f1": "{F1}", "f2": "{F2}", "f3": "{F3}", "f4": "{F4}",
		"f5": "{F5}", "f6": "{F6}", "f7": "{F7}", "f8": "{F8}",
		"f9": "{F9}", "f10": "{F10}", "f11": "{F11}", "f12": "{F12}",
	}
	mk := strings.TrimSpace(mainKey)
	if mapped, ok := specials[mk]; ok {
		mk = mapped
	}
	return strings.Join(mods, "") + mk
}

func platformFindElements(_, _ string) ([]uiElement, error) {
	return nil, fmt.Errorf("find_element not yet implemented on Windows; use screenshot + coordinates instead")
}

func getScaleFactor() int { return 1 }

func getScreenSize() (int, int) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
$s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
Write-Output "$($s.Width)x$($s.Height)"
`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), "x")
		if len(parts) == 2 {
			w, _ := strconv.Atoi(parts[0])
			h, _ := strconv.Atoi(parts[1])
			if w > 0 && h > 0 {
				return w, h
			}
		}
	}
	return 1920, 1080
}

func runPS(script string) error {
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
