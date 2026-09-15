package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
)

// ProviderName is the identifier the bundled manifest references.
const ProviderName = "builtin:computer_use"

// ToolName is the single tool this provider contributes.
const ToolName = "computer"

const (
	maxTypeLength = 4096
	maxWaitSecond = 10.0
)

// Provider implements the native computer-use tool.
//
// The advertised actions are exactly what the active backend can do. The
// osascript backend has no coordinate-addressed right click, double click,
// mouse move, drag or wheel scroll, so those actions are not advertised: a
// tool that silently approximates a gesture is worse than one that says it
// cannot perform it. A CGEvent backend can add them behind the same contract.
type Provider struct {
	backend Backend
	root    string
	now     func() time.Time
}

type Option func(*Provider)

// WithBackend replaces the host backend, used by tests to avoid synthesizing
// real input on a developer's machine.
func WithBackend(backend Backend) Option {
	return func(p *Provider) { p.backend = backend }
}

// WithClock fixes screenshot filenames in tests.
func WithClock(now func() time.Time) Option {
	return func(p *Provider) { p.now = now }
}

// New builds the provider. Screenshots are written under <root>/screenshots.
func New(root string, options ...Option) *Provider {
	provider := &Provider{backend: newBackend(), root: strings.TrimSpace(root), now: time.Now}
	for _, option := range options {
		option(provider)
	}
	return provider
}

func (p *Provider) Tools() []pluginpkg.ToolSpec {
	return []pluginpkg.ToolSpec{{
		Name: ToolName,
		Description: "Observe and control this computer's screen. Call action=screenshot first to see the " +
			"current screen, then act on what the image shows. Coordinates are in screen points with the " +
			"origin at the top left; a screenshot's pixel dimensions may be a multiple of the point size on " +
			"a high-density display, so always take coordinates from the reported screen size, never from " +
			"the image's pixel dimensions. Only the actions listed here are available: this backend cannot " +
			"right-click, double-click, drag, move the pointer without clicking, or scroll with a wheel.",
		Schema: model.JSON(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["screenshot", "screen_size", "click", "type", "key", "wait"],
					"description": "screenshot: capture the main display to a PNG file. screen_size: report the click coordinate space. click: single left click at x,y. type: enter literal text into the focused field. key: press one key with optional modifiers. wait: pause for the UI to settle."
				},
				"x": {"type": "integer", "description": "Click x in screen points, required for action=click."},
				"y": {"type": "integer", "description": "Click y in screen points, required for action=click."},
				"text": {"type": "string", "description": "Literal text for action=type."},
				"keys": {"type": "string", "description": "Key for action=key, with optional modifiers joined by '+', e.g. \"cmd+s\", \"return\", \"escape\", \"cmd+shift+4\"."},
				"seconds": {"type": "number", "description": "Pause length for action=wait, at most 10."}
			},
			"required": ["action"]
		}`),
	}}
}

type request struct {
	Action  string  `json:"action"`
	X       *int    `json:"x"`
	Y       *int    `json:"y"`
	Text    string  `json:"text"`
	Keys    string  `json:"keys"`
	Seconds float64 `json:"seconds"`
}

func (p *Provider) Execute(ctx context.Context, name, arguments string) (string, error) {
	if strings.TrimSpace(name) != ToolName {
		return "", fmt.Errorf("computer-use provider does not implement tool %q", name)
	}
	var input request
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return "", fmt.Errorf("parse %s arguments: %w", ToolName, err)
	}
	switch strings.TrimSpace(input.Action) {
	case "screenshot":
		return p.screenshot(ctx)
	case "screen_size":
		return p.screenSize(ctx)
	case "click":
		return p.click(ctx, input)
	case "type":
		return p.typeText(ctx, input)
	case "key":
		return p.key(ctx, input)
	case "wait":
		return p.wait(ctx, input)
	case "":
		return "", fmt.Errorf("action is required")
	default:
		return "", fmt.Errorf("unsupported action %q; this backend supports screenshot, screen_size, click, type, key and wait", input.Action)
	}
}

func (p *Provider) screenshot(ctx context.Context) (string, error) {
	directory := filepath.Join(p.root, "screenshots")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "screen-"+p.now().UTC().Format("20060102-150405.000")+".png")
	if err := p.backend.Screenshot(ctx, path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("screenshot was not written: %w", err)
	}
	payload := map[string]any{
		// __type/path keep the established file-result shape so the desktop
		// layer can surface the capture as an attachment.
		"__type":  "file",
		"path":    path,
		"mime":    "image/png",
		"backend": p.backend.Name(),
		"bytes":   info.Size(),
	}
	if size, err := p.backend.ScreenSize(ctx); err == nil {
		payload["screen_size"] = map[string]int{"width": size.X, "height": size.Y}
	}
	if width, height, err := pngDimensions(path); err == nil {
		payload["image_pixels"] = map[string]int{"width": width, "height": height}
	}
	payload["description"] = "Screen capture of the main display. Click coordinates use screen_size, not image_pixels."
	return encode(payload)
}

func (p *Provider) screenSize(ctx context.Context) (string, error) {
	size, err := p.backend.ScreenSize(ctx)
	if err != nil {
		return "", err
	}
	return encode(map[string]any{
		"width": size.X, "height": size.Y, "unit": "points", "backend": p.backend.Name(),
	})
}

func (p *Provider) click(ctx context.Context, input request) (string, error) {
	if input.X == nil || input.Y == nil {
		return "", fmt.Errorf("x and y are required for action=click")
	}
	at := image.Point{X: *input.X, Y: *input.Y}
	if at.X < 0 || at.Y < 0 {
		return "", fmt.Errorf("click coordinates must not be negative, got %d,%d", at.X, at.Y)
	}
	// An off-screen click lands somewhere unintended instead of failing, so
	// it is rejected while the screen size is known.
	if size, err := p.backend.ScreenSize(ctx); err == nil && size.X > 0 && size.Y > 0 {
		if at.X >= size.X || at.Y >= size.Y {
			return "", fmt.Errorf("click at %d,%d is outside the %dx%d screen", at.X, at.Y, size.X, size.Y)
		}
	}
	if err := p.backend.Click(ctx, at); err != nil {
		return "", err
	}
	return encode(map[string]any{"ok": true, "action": "click", "x": at.X, "y": at.Y})
}

func (p *Provider) typeText(ctx context.Context, input request) (string, error) {
	if input.Text == "" {
		return "", fmt.Errorf("text is required for action=type")
	}
	if len(input.Text) > maxTypeLength {
		return "", fmt.Errorf("text is %d bytes, which exceeds the %d byte limit", len(input.Text), maxTypeLength)
	}
	if err := p.backend.TypeText(ctx, input.Text); err != nil {
		return "", err
	}
	return encode(map[string]any{"ok": true, "action": "type", "length": len(input.Text)})
}

func (p *Provider) key(ctx context.Context, input request) (string, error) {
	modifiers, key, err := parseCombo(input.Keys)
	if err != nil {
		return "", err
	}
	if err := p.backend.Key(ctx, modifiers, key); err != nil {
		return "", err
	}
	return encode(map[string]any{"ok": true, "action": "key", "keys": input.Keys})
}

func (p *Provider) wait(ctx context.Context, input request) (string, error) {
	seconds := input.Seconds
	if seconds <= 0 {
		seconds = 1
	}
	if seconds > maxWaitSecond {
		return "", fmt.Errorf("seconds is %.1f, which exceeds the %.0f second limit", seconds, maxWaitSecond)
	}
	timer := time.NewTimer(time.Duration(seconds * float64(time.Second)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}
	return encode(map[string]any{"ok": true, "action": "wait", "seconds": seconds})
}

// parseCombo splits "cmd+shift+s" into validated modifiers and one key. Every
// part must be known: an unrecognized name would otherwise be typed as text or
// dropped silently.
func parseCombo(combo string) ([]string, string, error) {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return nil, "", fmt.Errorf("keys is required for action=key")
	}
	parts := strings.Split(combo, "+")
	key := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	if key == "" {
		return nil, "", fmt.Errorf("keys %q does not name a key", combo)
	}
	modifiers := make([]string, 0, len(parts)-1)
	seen := make(map[string]bool, len(parts))
	for _, part := range parts[:len(parts)-1] {
		name, ok := modifierNames[strings.ToLower(strings.TrimSpace(part))]
		if !ok {
			return nil, "", fmt.Errorf("unknown modifier %q in keys %q", part, combo)
		}
		if !seen[name] {
			modifiers = append(modifiers, name)
			seen[name] = true
		}
	}
	if _, named := keyCodes[key]; !named && len([]rune(key)) != 1 {
		return nil, "", fmt.Errorf("unknown key %q in keys %q", key, combo)
	}
	return modifiers, key, nil
}

func encode(payload map[string]any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
