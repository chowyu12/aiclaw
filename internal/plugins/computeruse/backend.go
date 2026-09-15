// Package computeruse implements the native tool provider behind the bundled
// `aiclaw.computer-use` plugin: screen capture and keyboard/mouse synthesis.
//
// The capability is delivered as a plugin, not a builtin tool, so it is absent
// from every turn until the user installs and enables the bundle and grants
// `computer.control`. See docs/design/plugin-system.md.
package computeruse

import (
	"context"
	"errors"
	"image"
)

// ErrUnsupported reports that this host has no input synthesis backend.
var ErrUnsupported = errors.New("computer use is not supported on this platform")

// Backend is the host-specific half of the provider: capture and input
// synthesis. The osascript backend is the no-dependency implementation; a
// cgo/CGEvent backend can replace it without touching the tool contract.
type Backend interface {
	// Name identifies the backend in tool output so a caller can tell which
	// implementation answered.
	Name() string
	// ScreenSize reports the main display size in the same coordinate space
	// clicks use. On a Retina display this is points, while a screenshot's
	// pixels are a multiple of it — the provider reports both so the model
	// never scales coordinates off a screenshot's pixel dimensions.
	ScreenSize(ctx context.Context) (image.Point, error)
	// Screenshot captures the main display into path as PNG.
	Screenshot(ctx context.Context, path string) error
	// Click synthesizes a single left click at a point.
	Click(ctx context.Context, at image.Point) error
	// TypeText synthesizes keystrokes for literal text.
	TypeText(ctx context.Context, text string) error
	// Key presses one key with optional modifiers.
	Key(ctx context.Context, modifiers []string, key string) error
}
