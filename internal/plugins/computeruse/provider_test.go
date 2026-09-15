package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeBackend records what the provider asked for. Tests never use the real
// backend: synthesizing clicks or capturing the screen of the machine running
// the tests is a side effect on someone's desktop, not test setup.
type fakeBackend struct {
	size        image.Point
	sizeErr     error
	clicks      []image.Point
	typed       []string
	keys        []string
	shotWritten image.Point
	shotErr     error
}

func (b *fakeBackend) Name() string { return "fake" }

func (b *fakeBackend) ScreenSize(context.Context) (image.Point, error) {
	return b.size, b.sizeErr
}

func (b *fakeBackend) Screenshot(_ context.Context, path string) error {
	if b.shotErr != nil {
		return b.shotErr
	}
	// Write a real PNG so the provider's dimension read is exercised.
	size := b.shotWritten
	if size.X == 0 {
		size = image.Point{X: 8, Y: 4}
	}
	canvas := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	canvas.Set(0, 0, color.RGBA{R: 1, A: 1})
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(file, canvas); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (b *fakeBackend) Click(_ context.Context, at image.Point) error {
	b.clicks = append(b.clicks, at)
	return nil
}

func (b *fakeBackend) TypeText(_ context.Context, text string) error {
	b.typed = append(b.typed, text)
	return nil
}

func (b *fakeBackend) Key(_ context.Context, modifiers []string, key string) error {
	b.keys = append(b.keys, strings.Join(append(modifiers, key), "+"))
	return nil
}

func newTestProvider(t *testing.T, backend *fakeBackend) *Provider {
	t.Helper()
	return New(t.TempDir(), WithBackend(backend),
		WithClock(func() time.Time { return time.Unix(0, 0).UTC() }))
}

func execute(t *testing.T, provider *Provider, arguments string) (map[string]any, error) {
	t.Helper()
	output, err := provider.Execute(context.Background(), ToolName, arguments)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("result is not JSON: %q", output)
	}
	return payload, nil
}

func TestToolAdvertisesOnlyWhatTheBackendCanDo(t *testing.T) {
	specs := New(t.TempDir()).Tools()
	if len(specs) != 1 || specs[0].Name != ToolName {
		t.Fatalf("tools = %+v", specs)
	}
	schema := string(specs[0].Schema)
	if !json.Valid([]byte(schema)) {
		t.Fatal("tool schema is not valid JSON")
	}
	// Gestures this backend cannot perform must not appear in the enum: a
	// tool that approximates a gesture is worse than one that refuses it.
	for _, absent := range []string{"right_click", "double_click", "scroll", "drag", "mouse_move"} {
		if strings.Contains(schema, `"`+absent+`"`) {
			t.Fatalf("schema advertises unsupported action %q", absent)
		}
	}
	for _, present := range []string{"screenshot", "screen_size", "click", "type", "key", "wait"} {
		if !strings.Contains(schema, `"`+present+`"`) {
			t.Fatalf("schema is missing action %q", present)
		}
	}
}

func TestScreenshotReportsPointsAndPixelsSeparately(t *testing.T) {
	// A Retina display: the capture is twice the click coordinate space.
	backend := &fakeBackend{size: image.Point{X: 1440, Y: 900}, shotWritten: image.Point{X: 2880, Y: 1800}}
	provider := newTestProvider(t, backend)

	payload, err := execute(t, provider, `{"action":"screenshot"}`)
	if err != nil {
		t.Fatal(err)
	}
	if payload["__type"] != "file" || payload["mime"] != "image/png" {
		t.Fatalf("screenshot is not reported as a file result: %+v", payload)
	}
	path, _ := payload["path"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("screenshot file missing: %v", err)
	}
	screen, _ := payload["screen_size"].(map[string]any)
	pixels, _ := payload["image_pixels"].(map[string]any)
	if screen["width"] != float64(1440) || pixels["width"] != float64(2880) {
		t.Fatalf("point and pixel dimensions were conflated: screen=%+v pixels=%+v", screen, pixels)
	}
}

func TestScreenshotFailureIsReportedNotSwallowed(t *testing.T) {
	backend := &fakeBackend{shotErr: fmt.Errorf("screencapture failed (Screen Recording permission may be missing)")}
	provider := newTestProvider(t, backend)
	if _, err := provider.Execute(context.Background(), ToolName, `{"action":"screenshot"}`); err == nil {
		t.Fatal("a failed capture was reported as success")
	}
}

func TestClickRejectsOffScreenAndNegativeCoordinates(t *testing.T) {
	backend := &fakeBackend{size: image.Point{X: 800, Y: 600}}
	provider := newTestProvider(t, backend)

	for _, arguments := range []string{
		`{"action":"click","x":800,"y":10}`,
		`{"action":"click","x":10,"y":600}`,
		`{"action":"click","x":-1,"y":10}`,
		`{"action":"click","y":10}`,
	} {
		if _, err := provider.Execute(context.Background(), ToolName, arguments); err == nil {
			t.Fatalf("invalid click was accepted: %s", arguments)
		}
	}
	if len(backend.clicks) != 0 {
		t.Fatalf("an invalid click reached the backend: %+v", backend.clicks)
	}

	// x=0,y=0 is a legitimate target and must survive the zero-value check.
	if _, err := execute(t, provider, `{"action":"click","x":0,"y":0}`); err != nil {
		t.Fatal(err)
	}
	if len(backend.clicks) != 1 || backend.clicks[0] != (image.Point{}) {
		t.Fatalf("clicks = %+v", backend.clicks)
	}
}

func TestClickProceedsWhenScreenSizeIsUnknown(t *testing.T) {
	// Losing the bounds probe must not block control: the guard is a bounds
	// check, not a precondition.
	backend := &fakeBackend{sizeErr: fmt.Errorf("no display")}
	provider := newTestProvider(t, backend)
	if _, err := execute(t, provider, `{"action":"click","x":10,"y":10}`); err != nil {
		t.Fatal(err)
	}
	if len(backend.clicks) != 1 {
		t.Fatalf("clicks = %+v", backend.clicks)
	}
}

func TestKeyParsesModifiersAndRejectsUnknownNames(t *testing.T) {
	backend := &fakeBackend{size: image.Point{X: 800, Y: 600}}
	provider := newTestProvider(t, backend)

	for _, arguments := range []string{
		`{"action":"key","keys":"cmd+s"}`,
		`{"action":"key","keys":"CMD+Shift+S"}`,
		`{"action":"key","keys":"return"}`,
		`{"action":"key","keys":"escape"}`,
	} {
		if _, err := execute(t, provider, arguments); err != nil {
			t.Fatalf("%s: %v", arguments, err)
		}
	}
	want := []string{"command+s", "command+shift+s", "return", "escape"}
	for index, key := range want {
		if backend.keys[index] != key {
			t.Fatalf("keys[%d] = %q, want %q", index, backend.keys[index], key)
		}
	}

	for _, arguments := range []string{
		`{"action":"key","keys":"hyper+s"}`,
		`{"action":"key","keys":"cmd+nosuchkey"}`,
		`{"action":"key"}`,
	} {
		if _, err := provider.Execute(context.Background(), ToolName, arguments); err == nil {
			t.Fatalf("invalid key combination was accepted: %s", arguments)
		}
	}
}

func TestTypeRequiresTextAndBoundsLength(t *testing.T) {
	backend := &fakeBackend{}
	provider := newTestProvider(t, backend)
	if _, err := provider.Execute(context.Background(), ToolName, `{"action":"type"}`); err == nil {
		t.Fatal("empty text was accepted")
	}
	long := strings.Repeat("a", maxTypeLength+1)
	if _, err := provider.Execute(context.Background(), ToolName, `{"action":"type","text":"`+long+`"}`); err == nil {
		t.Fatal("oversized text was accepted")
	}
	// Quotes and backslashes must survive: they reach the backend as an
	// argument, never as script text.
	tricky := `it's "quoted" \ and ` + "`backticked`"
	if _, err := execute(t, provider, `{"action":"type","text":`+mustJSONString(tricky)+`}`); err != nil {
		t.Fatal(err)
	}
	if len(backend.typed) != 1 || backend.typed[0] != tricky {
		t.Fatalf("typed = %q", backend.typed)
	}
}

func TestUnknownActionAndToolAreRejected(t *testing.T) {
	provider := newTestProvider(t, &fakeBackend{})
	if _, err := provider.Execute(context.Background(), ToolName, `{"action":"scroll","x":1,"y":1}`); err == nil {
		t.Fatal("an unsupported gesture was accepted")
	}
	if _, err := provider.Execute(context.Background(), ToolName, `{}`); err == nil {
		t.Fatal("a missing action was accepted")
	}
	if _, err := provider.Execute(context.Background(), "other_tool", `{"action":"screenshot"}`); err == nil {
		t.Fatal("a foreign tool name was accepted")
	}
}

func TestWaitIsBoundedAndCancellable(t *testing.T) {
	provider := newTestProvider(t, &fakeBackend{})
	if _, err := provider.Execute(context.Background(), ToolName, `{"action":"wait","seconds":60}`); err == nil {
		t.Fatal("an unbounded wait was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Execute(ctx, ToolName, `{"action":"wait","seconds":5}`); err == nil {
		t.Fatal("a cancelled wait did not return")
	}
}

func mustJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
