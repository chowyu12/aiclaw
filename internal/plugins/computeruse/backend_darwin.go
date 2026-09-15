//go:build darwin

package computeruse

import (
	"context"
	"fmt"
	"image"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// screenSizeTTL bounds how long a cached display size is trusted. Display
// configuration changes when a monitor is plugged in or resolution changes, so
// the size is re-read rather than cached for the process lifetime.
const screenSizeTTL = 30 * time.Second

// darwinBackend drives macOS through screencapture and System Events. Both
// need user-granted permissions — Screen Recording for capture, Accessibility
// for input — which the OS prompts for on first use and which this code cannot
// grant. A denied permission surfaces as a failed command, reported verbatim.
type darwinBackend struct {
	mu       sync.Mutex
	size     image.Point
	sizeRead time.Time
}

func newBackend() Backend { return &darwinBackend{} }

func (b *darwinBackend) Name() string { return "darwin/osascript" }

func (b *darwinBackend) ScreenSize(ctx context.Context) (image.Point, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.sizeRead.IsZero() && time.Since(b.sizeRead) < screenSizeTTL {
		return b.size, nil
	}
	output, err := runOsascript(ctx, nil,
		`tell application "Finder" to get bounds of window of desktop`)
	if err != nil {
		return image.Point{}, err
	}
	// Finder reports "left, top, right, bottom".
	fields := strings.Split(output, ",")
	if len(fields) != 4 {
		return image.Point{}, fmt.Errorf("unexpected desktop bounds %q", output)
	}
	right, rightErr := strconv.Atoi(strings.TrimSpace(fields[2]))
	bottom, bottomErr := strconv.Atoi(strings.TrimSpace(fields[3]))
	if rightErr != nil || bottomErr != nil {
		return image.Point{}, fmt.Errorf("unexpected desktop bounds %q", output)
	}
	b.size, b.sizeRead = image.Point{X: right, Y: bottom}, time.Now()
	return b.size, nil
}

func (b *darwinBackend) Screenshot(ctx context.Context, path string) error {
	// -x silences the shutter, -m limits capture to the main display so the
	// image shares one coordinate space with clicks, -t png fixes the format.
	command := exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-m", "-t", "png", path)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("screencapture failed (Screen Recording permission may be missing): %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (b *darwinBackend) Click(ctx context.Context, at image.Point) error {
	_, err := runOsascript(ctx,
		[]string{strconv.Itoa(at.X), strconv.Itoa(at.Y)},
		`on run argv`,
		`tell application "System Events" to click at {(item 1 of argv) as integer, (item 2 of argv) as integer}`,
		`end run`)
	return err
}

func (b *darwinBackend) TypeText(ctx context.Context, text string) error {
	_, err := runOsascript(ctx, []string{text},
		`on run argv`,
		`tell application "System Events" to keystroke (item 1 of argv)`,
		`end run`)
	return err
}

func (b *darwinBackend) Key(ctx context.Context, modifiers []string, key string) error {
	using := ""
	if len(modifiers) > 0 {
		clauses := make([]string, 0, len(modifiers))
		for _, modifier := range modifiers {
			clauses = append(clauses, modifier+" down")
		}
		using = " using {" + strings.Join(clauses, ", ") + "}"
	}
	// Modifiers and key codes come from validated lookup tables, never from
	// raw arguments, so composing them into the script cannot inject code.
	// Free-form text always travels through argv instead.
	if code, ok := keyCodes[key]; ok {
		_, err := runOsascript(ctx, nil,
			fmt.Sprintf(`tell application "System Events" to key code %d%s`, code, using))
		return err
	}
	_, err := runOsascript(ctx, []string{key},
		`on run argv`,
		fmt.Sprintf(`tell application "System Events" to keystroke (item 1 of argv)%s`, using),
		`end run`)
	return err
}

// runOsascript executes a script, passing every dynamic value as an argument
// rather than interpolating it, so caller text can never become code.
func runOsascript(ctx context.Context, args []string, lines ...string) (string, error) {
	command := []string{}
	for _, line := range lines {
		command = append(command, "-e", line)
	}
	if len(args) > 0 {
		command = append(command, "--")
		command = append(command, args...)
	}
	process := exec.CommandContext(ctx, "/usr/bin/osascript", command...)
	output, err := process.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		return "", fmt.Errorf("osascript failed (Accessibility permission may be missing): %w: %s", err, trimmed)
	}
	return trimmed, nil
}
