package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/appservice"
)

// The allowlist must name real methods. A typo has to fail here rather than
// when a user clicks the button that needs it.
func TestAllowlistNamesRealMethods(t *testing.T) {
	host := appservice.NewHost(appservice.Options{Root: t.TempDir()})
	dispatcher, err := NewDispatcher(host.Service())
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.Commands()) != len(allowed) {
		t.Fatalf("commands = %d, allowlist = %d", len(dispatcher.Commands()), len(allowed))
	}
}

func TestNewDispatcherRejectsAnUnknownName(t *testing.T) {
	original := allowed
	t.Cleanup(func() { allowed = original })
	allowed = append(append([]string{}, original...), "NoSuchMethod")

	host := appservice.NewHost(appservice.Options{Root: t.TempDir()})
	if _, err := NewDispatcher(host.Service()); err == nil || !strings.Contains(err.Error(), "NoSuchMethod") {
		t.Fatalf("a bogus command name was accepted: %v", err)
	}
}

// The sidecar's surface must be exactly what the interface compiles against.
// Wails generated its bindings from every exported method; this list is the
// review point that replaces that, so drift between the two is a defect.
func TestAllowlistMatchesTheBoundInterfaceSurface(t *testing.T) {
	path := filepath.Join("..", "..", "desktop", "frontend", "wailsjs", "go", "appservice", "Service.d.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("generated bindings are absent (%v); run `wails generate module` to check drift", err)
	}
	bound := regexp.MustCompile(`(?m)^export function ([A-Za-z]+)`).FindAllStringSubmatch(string(data), -1)
	want := make([]string, 0, len(bound))
	for _, match := range bound {
		want = append(want, match[1])
	}
	got := append([]string{}, allowed...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Fatalf("allowlist drifted from the interface surface\n only in bindings: %v\n only in allowlist: %v",
			difference(want, got), difference(got, want))
	}
}

func difference(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, value := range b {
		set[value] = true
	}
	var out []string
	for _, value := range a {
		if !set[value] {
			out = append(out, value)
		}
	}
	return out
}

// An unlisted command must be refused even though the method exists and is
// exported — that refusal is the whole point of the allowlist.
func TestUnlistedCommandIsRefused(t *testing.T) {
	host := appservice.NewHost(appservice.Options{Root: t.TempDir()})
	dispatcher, err := NewDispatcher(host.Service())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Start", "Stop", "SetEmitter", "Root", "Nonexistent"} {
		if _, err := dispatcher.Call(name, nil); err == nil {
			t.Fatalf("command %q was served", name)
		}
	}
}

func TestCallDecodesPositionalArguments(t *testing.T) {
	host := appservice.NewHost(appservice.Options{Root: t.TempDir()})
	dispatcher, err := NewDispatcher(host.Service())
	if err != nil {
		t.Fatal(err)
	}
	// Status takes no arguments and cannot fail, so it exercises the plumbing
	// without a started service.
	result, err := dispatcher.Call("Status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(string); !ok {
		t.Fatalf("Status returned %T", result)
	}

	// Argument count is checked before any call is attempted.
	if _, err := dispatcher.Call("SetPluginConfig", raw(`"uuid"`, `"key"`)); err == nil {
		t.Fatal("a call with too few arguments was accepted")
	}
	// A malformed argument names its position so the host can find the bug.
	_, err = dispatcher.Call("AddProviderModel", raw(`"not-a-number"`, `"model"`))
	if err == nil || !strings.Contains(err.Error(), "argument 1") {
		t.Fatalf("argument decoding error = %v", err)
	}
}

func raw(values ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		out = append(out, json.RawMessage(value))
	}
	return out
}
