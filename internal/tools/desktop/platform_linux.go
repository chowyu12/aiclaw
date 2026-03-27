//go:build linux

package desktop

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func platformScreenshotFallback(path string, region *rect) error {
	if cmdAvailable("scrot") {
		args := []string{"-o", path}
		if region != nil {
			args = []string{"-a", fmt.Sprintf("%d,%d,%d,%d", region.X, region.Y, region.Width, region.Height), "-o", path}
		}
		return run("scrot", args...)
	}
	if cmdAvailable("import") {
		args := []string{"-window", "root", path}
		if region != nil {
			crop := fmt.Sprintf("%dx%d+%d+%d", region.Width, region.Height, region.X, region.Y)
			args = []string{"-window", "root", "-crop", crop, path}
		}
		return run("import", args...)
	}
	return fmt.Errorf("no fallback screenshot tool found, install scrot or imagemagick")
}

func platformClick(x, y int, button string, clicks int) error {
	requireCmd("xdotool")
	xs, ys := strconv.Itoa(x), strconv.Itoa(y)
	btn := "1"
	if button == "right" {
		btn = "3"
	} else if button == "middle" {
		btn = "2"
	}
	args := []string{"mousemove", "--sync", xs, ys, "click", "--repeat", strconv.Itoa(clicks), btn}
	return run("xdotool", args...)
}

func platformTypeText(text string) error {
	requireCmd("xdotool")
	return run("xdotool", "type", "--clearmodifiers", text)
}

func platformKeyPress(key string) error {
	requireCmd("xdotool")
	xKey := translateKeyLinux(key)
	return run("xdotool", "key", "--clearmodifiers", xKey)
}

func platformScroll(x, y, _, dy int) error {
	requireCmd("xdotool")
	if x != 0 || y != 0 {
		_ = run("xdotool", "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y))
	}
	btn := "5" // down
	if dy > 0 {
		btn = "4" // up
	}
	count := dy
	if count < 0 {
		count = -count
	}
	if count == 0 {
		count = 3
	}
	for range count {
		if err := run("xdotool", "click", btn); err != nil {
			return err
		}
	}
	return nil
}

func platformMouseMove(x, y int) error {
	requireCmd("xdotool")
	return run("xdotool", "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y))
}

func platformListWindows() ([]windowInfo, error) {
	requireCmd("xdotool")
	out, err := exec.Command("xdotool", "search", "--onlyvisible", "--name", "").Output()
	if err != nil {
		return nil, err
	}
	var windows []windowInfo
	for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		wid = strings.TrimSpace(wid)
		if wid == "" {
			continue
		}
		nameOut, _ := exec.Command("xdotool", "getwindowname", wid).Output()
		classOut, _ := exec.Command("xdotool", "getwindowclassname", wid).Output()
		title := strings.TrimSpace(string(nameOut))
		name := strings.TrimSpace(string(classOut))
		if title == "" && name == "" {
			continue
		}
		windows = append(windows, windowInfo{Name: name, Title: title})
	}
	return windows, nil
}

func platformFocusWindow(name string) error {
	requireCmd("xdotool")
	out, err := exec.Command("xdotool", "search", "--onlyvisible", "--name", name).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("window not found: %s", name)
	}
	wid := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	return run("xdotool", "windowactivate", "--sync", wid)
}

func translateKeyLinux(key string) string {
	lower := strings.ToLower(key)
	lower = strings.ReplaceAll(lower, "cmd+", "super+")
	lower = strings.ReplaceAll(lower, "command+", "super+")
	lower = strings.ReplaceAll(lower, "option+", "alt+")
	replacer := map[string]string{
		"enter": "Return", "return": "Return", "tab": "Tab",
		"space": "space", "escape": "Escape", "esc": "Escape",
		"backspace": "BackSpace", "delete": "Delete",
		"up": "Up", "down": "Down", "left": "Left", "right": "Right",
		"home": "Home", "end": "End", "pageup": "Page_Up", "pagedown": "Page_Down",
	}
	parts := strings.Split(lower, "+")
	mainKey := parts[len(parts)-1]
	if mapped, ok := replacer[mainKey]; ok {
		parts[len(parts)-1] = mapped
	}
	return strings.Join(parts, "+")
}

func cmdAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func requireCmd(name string) {
	if !cmdAvailable(name) {
		panic(fmt.Sprintf("required command not found: %s (install with: sudo apt install %s)", name, name))
	}
}

func platformFindElements(_, _ string) ([]uiElement, error) {
	return nil, fmt.Errorf("find_element not yet implemented on Linux; use screenshot + coordinates instead")
}

func getScaleFactor() int { return 1 }

func getScreenSize() (int, int) {
	out, err := exec.Command("xdpyinfo").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "dimensions:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					dim := strings.Split(parts[1], "x")
					if len(dim) == 2 {
						w, _ := strconv.Atoi(dim[0])
						h, _ := strconv.Atoi(dim[1])
						if w > 0 && h > 0 {
							return w, h
						}
					}
				}
			}
		}
	}
	return 1920, 1080
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
