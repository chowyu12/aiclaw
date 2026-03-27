//go:build darwin

package desktop

import (
	"fmt"
	"os/exec"
	"strings"
)

func platformScreenshotFallback(path string, region *rect) error {
	args := []string{"-x"}
	if region != nil {
		args = append(args, "-R", fmt.Sprintf("%d,%d,%d,%d", region.X, region.Y, region.Width, region.Height))
	}
	args = append(args, path)
	if err := run("screencapture", args...); err != nil {
		return fmt.Errorf("%w\n\nHint: grant Screen Recording permission — System Settings → Privacy & Security → Screen Recording → enable your terminal app, then restart aiclaw", err)
	}
	return nil
}

func platformClick(x, y int, button string, clicks int) error {
	cgoClick(x, y, button, clicks)
	return nil
}

func platformTypeText(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	return run("osascript", "-e",
		`tell application "System Events" to keystroke "v" using command down`)
}

func platformKeyPress(key string) error {
	script := buildAppleScriptKeyPress(key)
	return run("osascript", "-e", script)
}

func platformScroll(x, y, dx, dy int) error {
	if x != 0 || y != 0 {
		cgoMouseMove(x, y)
	}
	cgoScroll(dy, dx)
	return nil
}

func platformMouseMove(x, y int) error {
	cgoMouseMove(x, y)
	return nil
}

func platformListWindows() ([]windowInfo, error) {
	script := `
tell application "System Events"
    set windowList to ""
    repeat with proc in (every process whose visible is true)
        set appName to name of proc
        try
            repeat with w in (every window of proc)
                set windowList to windowList & appName & "|||" & name of w & linefeed
            end repeat
        end try
    end repeat
    return windowList
end tell
`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	var windows []windowInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 2)
		w := windowInfo{Name: parts[0]}
		if len(parts) > 1 {
			w.Title = parts[1]
		}
		windows = append(windows, w)
	}
	return windows, nil
}

func platformFocusWindow(name string) error {
	escaped := strings.ReplaceAll(name, "\"", "\\\"")
	script := fmt.Sprintf(`tell application "%s" to activate`, escaped)
	if err := run("osascript", "-e", script); err != nil {
		script = fmt.Sprintf(`
tell application "System Events"
    set targetProc to first process whose name contains "%s"
    set frontmost of targetProc to true
end tell
`, escaped)
		return run("osascript", "-e", script)
	}
	return nil
}

func buildAppleScriptKeyPress(key string) string {
	lower := strings.ToLower(key)

	parts := strings.Split(lower, "+")
	var modifiers []string
	mainKey := parts[len(parts)-1]

	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "cmd", "command":
			modifiers = append(modifiers, "command down")
		case "ctrl", "control":
			modifiers = append(modifiers, "control down")
		case "alt", "option":
			modifiers = append(modifiers, "option down")
		case "shift":
			modifiers = append(modifiers, "shift down")
		}
	}

	modStr := ""
	if len(modifiers) > 0 {
		modStr = " using {" + strings.Join(modifiers, ", ") + "}"
	}

	if code, ok := macKeyCode(strings.TrimSpace(mainKey)); ok {
		return fmt.Sprintf(`tell application "System Events" to key code %d%s`, code, modStr)
	}
	return fmt.Sprintf(`tell application "System Events" to keystroke "%s"%s`, strings.TrimSpace(mainKey), modStr)
}

func macKeyCode(key string) (int, bool) {
	codes := map[string]int{
		"return": 36, "enter": 36, "tab": 48, "space": 49, "delete": 51,
		"backspace": 51, "escape": 53, "esc": 53,
		"up": 126, "down": 125, "left": 123, "right": 124,
		"home": 115, "end": 119, "pageup": 116, "pagedown": 121,
		"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96,
		"f6": 97, "f7": 98, "f8": 100, "f9": 101, "f10": 109,
		"f11": 103, "f12": 111,
	}
	code, ok := codes[key]
	return code, ok
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
