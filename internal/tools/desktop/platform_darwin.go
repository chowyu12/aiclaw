//go:build darwin

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	cgoKeyTap(9, cgFlagCmd) // 'v' key + Cmd → paste
	return nil
}

func platformKeyPress(key string) error {
	if code, flags, ok := parseMacKey(key); ok {
		cgoKeyTap(code, flags)
		return nil
	}
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

func platformFindElements(appName, text string) ([]uiElement, error) {
	script := fmt.Sprintf(`
ObjC.import('stdlib');
var se = Application("System Events");
var proc;
var appArg = %q;
var textArg = %q;
var t0 = Date.now();

if (appArg !== "") {
    try { proc = se.processes.byName(appArg); proc.name(); }
    catch(e) {
        var procs = se.processes.whose({visible: true})();
        for (var i = 0; i < procs.length; i++) {
            if (procs[i].name().indexOf(appArg) >= 0) { proc = procs[i]; break; }
        }
        if (!proc) { JSON.stringify([]); $.exit(0); }
    }
} else {
    proc = se.processes.whose({frontmost: true})[0];
}

var interactiveRoles = {"AXTextField":1,"AXSearchField":1,"AXTextArea":1,"AXButton":1,"AXPopUpButton":1,"AXCheckBox":1,"AXRadioButton":1,"AXLink":1,"AXMenuItem":1,"AXComboBox":1};
var results = [];
var interactives = [];

function scan(elem, depth) {
    if (depth > 5 || results.length >= 15 || (Date.now() - t0) > 8000) return;
    try {
        var role = "", title = "", val = "", desc = "";
        try { role = elem.role(); } catch(e) {}
        try { title = elem.title() || ""; } catch(e) {}
        try { val = String(elem.value() || ""); } catch(e) {}
        try { desc = elem.description() || ""; } catch(e) {}

        var textMatch = false;
        if (textArg !== "") {
            if (title && title.indexOf(textArg) >= 0) textMatch = true;
            if (!textMatch && val && val.indexOf(textArg) >= 0) textMatch = true;
            if (!textMatch && desc && desc.indexOf(textArg) >= 0) textMatch = true;
        }

        if (textMatch) {
            var pos = [0,0], sz = [0,0];
            try { pos = elem.position(); } catch(e) {}
            try { sz = elem.size(); } catch(e) {}
            if (pos[0] >= 0 && pos[1] >= 0 && sz[0] > 1 && sz[1] > 1) {
                results.push({role:role, title:title, value:val, desc:desc, x:pos[0], y:pos[1], w:sz[0], h:sz[1]});
            }
        }

        if (interactiveRoles[role] && interactives.length < 20) {
            var pos2 = [0,0], sz2 = [0,0];
            try { pos2 = elem.position(); } catch(e) {}
            try { sz2 = elem.size(); } catch(e) {}
            if (pos2[0] >= 0 && pos2[1] >= 0 && sz2[0] > 5 && sz2[1] > 5) {
                interactives.push({role:role, title:title, value:val, desc:desc, x:pos2[0], y:pos2[1], w:sz2[0], h:sz2[1]});
            }
        }
    } catch(e) {}
    try {
        var children = elem.uiElements();
        for (var i = 0; i < children.length && results.length < 15 && (Date.now() - t0) < 8000; i++) {
            scan(children[i], depth + 1);
        }
    } catch(e) {}
}

try {
    var wins = proc.windows();
    for (var w = 0; w < wins.length && w < 2; w++) {
        scan(wins[w], 0);
    }
} catch(e) {}

JSON.stringify(results.length > 0 ? results : interactives);
`, appName, text)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("find_element timed out (10s) — try a more specific app name or text")
		}
		return nil, fmt.Errorf("osascript: %w", err)
	}

	var rawElements []struct {
		Role  string `json:"role"`
		Title string `json:"title"`
		Value string `json:"value"`
		Desc  string `json:"desc"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
		W     int    `json:"w"`
		H     int    `json:"h"`
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "undefined" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &rawElements); err != nil {
		return nil, fmt.Errorf("parse elements: %w: %s", err, raw)
	}

	var elements []uiElement
	for _, r := range rawElements {
		label := r.Title
		if label == "" {
			label = r.Desc
		}
		elements = append(elements, uiElement{
			Role:    r.Role,
			Title:   label,
			Value:   r.Value,
			ScreenX: r.X + r.W/2,
			ScreenY: r.Y + r.H/2,
			Width:   r.W,
			Height:  r.H,
		})
	}
	return elements, nil
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
