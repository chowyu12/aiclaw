// Package appservice is the shell-agnostic desktop application layer. It owns
// every operation the user interface can invoke, and knows nothing about how
// that interface is hosted: the Wails shell and the Electron sidecar are both
// adapters over this one implementation.
//
// Two capabilities genuinely belong to the host and cannot live here. Emitting
// events needs a channel to the running interface, and a native file picker
// needs a window to attach to; a headless sidecar has neither. Both are
// injected. See docs/design/electron-migration.md.
package appservice

import (
	"context"
	"fmt"
)

// Emitter delivers an event to the user interface. A host without a live
// interface may drop events.
type Emitter func(name string, data ...any)

// FilePicker describes a native file selection request.
type FilePicker struct {
	Title string
	// Filters are display-name/pattern pairs in the host's own format, e.g.
	// "*.png;*.jpg". An empty list means any file.
	Filters []FileFilter
}

type FileFilter struct {
	DisplayName string
	Pattern     string
}

// Dialogs are the native pickers only the host can show. A headless host
// returns ErrNoDialogs so the caller reports "not available here" rather than
// hanging or pretending the user cancelled.
type Dialogs interface {
	PickFiles(ctx context.Context, request FilePicker) ([]string, error)
	PickDirectory(ctx context.Context, title string) (string, error)
}

// ErrNoDialogs reports a host that cannot show native dialogs.
var ErrNoDialogs = fmt.Errorf("native dialogs are not available in this host")

// NoDialogs is the Dialogs implementation for a headless host. The Electron
// shell replaces it by forwarding these requests to its main process, which is
// the only part of that architecture owning a window.
type NoDialogs struct{}

func (NoDialogs) PickFiles(context.Context, FilePicker) ([]string, error) {
	return nil, ErrNoDialogs
}

func (NoDialogs) PickDirectory(context.Context, string) (string, error) {
	return "", ErrNoDialogs
}

// Host owns a Service's lifecycle.
//
// The lifecycle is deliberately not on Service itself: a host binds Service to
// its user interface, and anything exported there becomes callable from the
// renderer. Start and Stop reachable from the interface would let a page shut
// the application's core down.
type Host struct{ service *Service }

// NewHost builds a service for a host. Nothing is opened until Start runs, so
// a host can construct it before it has a context.
func NewHost(options Options) *Host {
	service := &Service{
		rootOverride: options.Root,
		dialogs:      options.Dialogs,
		previewURL:   options.PreviewURL,
	}
	if options.Emit != nil {
		service.emit = options.Emit
	}
	if service.dialogs == nil {
		service.dialogs = NoDialogs{}
	}
	return &Host{service: service}
}

// Service is what gets bound to the user interface.
func (h *Host) Service() *Service { return h.service }

// SetEmitter installs the event channel. A host whose interface only exists
// after startup calls this from its own startup hook.
func (h *Host) SetEmitter(emit Emitter) {
	if emit != nil {
		h.service.emit = emit
	}
}

// Start opens the data directory, database, plugins and tool runtime. A
// failure is also recorded on the service so every subsequent call reports it
// rather than panicking on a half-built service.
func (h *Host) Start(ctx context.Context) error {
	h.service.startup(ctx)
	if h.service.err != "" {
		return fmt.Errorf("start desktop service: %s", h.service.err)
	}
	return nil
}

// Stop releases the channels, background turns and database.
func (h *Host) Stop() { h.service.shutdown(context.Background()) }

// Root is the data directory in use.
func (h *Host) Root() string { return h.service.root }

// PreviewURLFunc builds the URL the interface loads to show an image
// attachment.
//
// How a preview reaches the page is the host's business. The Wails shell
// inlines the bytes as a data URI, which works because its bindings are
// in-process. A host that talks over a pipe cannot afford that: an attachment
// is capped at 20MB, which is roughly 27MB once base64-encoded, and sending
// that as one protocol message would stall every other message behind it. Such
// a host serves the file over its own scheme instead and returns a URL here.
type PreviewURLFunc func(file PreviewFile) (string, bool)

// PreviewFile describes the image a preview URL is wanted for.
type PreviewFile struct {
	// UUID identifies a stored attachment; empty for a loose output file.
	UUID string
	// Path is the file on disk.
	Path string
	// ContentType is the image's media type.
	ContentType string
	// Size is the file's length in bytes.
	Size int64
}

// Options configure a Service for one host.
type Options struct {
	// Root overrides the data directory. Empty means ~/.aiclaw.
	Root string
	// Emit delivers events to the interface; nil drops them.
	Emit Emitter
	// Dialogs shows native pickers; nil means none are available.
	Dialogs Dialogs
	// PreviewURL builds image preview URLs. Nil inlines them as data URIs,
	// which is what an in-process host wants.
	PreviewURL PreviewURLFunc
}
