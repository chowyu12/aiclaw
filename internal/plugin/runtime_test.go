package plugin

import (
	"context"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

// stubProvider stands in for a bundled native tool implementation. It exposes
// one more tool than any manifest advertises, so the manifest's role as the
// consent surface can be asserted.
type stubProvider struct{ calls []string }

func (p *stubProvider) Tools() []ToolSpec {
	return []ToolSpec{
		{Name: "computer", Description: "control the screen", Schema: model.JSON(`{"type":"object"}`)},
		{Name: "computer_debug", Description: "undeclared extra tool"},
	}
}

func (p *stubProvider) Execute(_ context.Context, name, arguments string) (string, error) {
	p.calls = append(p.calls, name+":"+arguments)
	return "done", nil
}

const computerManifest = `{"schema_version":1,"id":"aiclaw.computer-use","name":"Computer Use",
	"permissions":["computer.control","filesystem.write"],
	"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`

func TestNewRuntimeRejectsUndeclaredProvider(t *testing.T) {
	if _, err := NewRuntime(map[string]ToolProvider{"builtin:nope": &stubProvider{}}); err == nil {
		t.Fatal("undeclared provider was bound")
	}
	if _, err := NewRuntime(map[string]ToolProvider{"builtin:computer_use": nil}); err == nil {
		t.Fatal("nil implementation was bound")
	}
}

func TestActiveToolsOnlyForEnabledPluginsAndAdvertisedNames(t *testing.T) {
	provider := &stubProvider{}
	runtime, err := NewRuntime(map[string]ToolProvider{"builtin:computer_use": provider})
	if err != nil {
		t.Fatal(err)
	}

	disabled := model.Plugin{UUID: "p1", Name: "Computer Use", Manifest: model.JSON(computerManifest)}
	if tools, err := runtime.ActiveTools([]model.Plugin{disabled}); err != nil || len(tools) != 0 {
		t.Fatalf("a disabled plugin contributed tools: %+v err=%v", tools, err)
	}

	enabled := disabled
	enabled.Enabled = true
	tools, err := runtime.ActiveTools([]model.Plugin{enabled})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Spec.Name != "computer" {
		t.Fatalf("active tools = %+v", tools)
	}
	if tools[0].Owner != "aiclaw.computer-use" {
		t.Fatalf("owner = %q", tools[0].Owner)
	}
	if _, err := tools[0].Provider.Execute(context.Background(), "computer", `{"action":"screenshot"}`); err != nil {
		t.Fatal(err)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("provider calls = %v", provider.calls)
	}
}

func TestActiveToolsSkipsUnimplementedProviderAndMissingPermission(t *testing.T) {
	// No implementations: a manifest referencing a declared-but-absent
	// provider must resolve to nothing rather than fail the turn.
	empty, err := NewRuntime(nil)
	if err != nil {
		t.Fatal(err)
	}
	enabled := model.Plugin{UUID: "p1", Enabled: true, Manifest: model.JSON(computerManifest)}
	tools, err := empty.ActiveTools([]model.Plugin{enabled})
	if err != nil || len(tools) != 0 {
		t.Fatalf("unimplemented provider produced tools: %+v err=%v", tools, err)
	}

	// The provider exists, but this manifest never declared the permission it
	// needs, so the tool must not be advertised.
	runtime, err := NewRuntime(map[string]ToolProvider{"builtin:computer_use": &stubProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	underDeclared := model.Plugin{UUID: "p2", Enabled: true, Manifest: model.JSON(
		`{"schema_version":1,"name":"Sneaky","permissions":["filesystem.write"],
			"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`)}
	tools, err = runtime.ActiveTools([]model.Plugin{underDeclared})
	if err != nil || len(tools) != 0 {
		t.Fatalf("tool was advertised without its permission: %+v err=%v", tools, err)
	}
}

func TestOwnerActiveGuardsOwnedRecordsOnly(t *testing.T) {
	enabled := EnabledSet([]model.Plugin{
		{UUID: "on", Enabled: true},
		{UUID: "off"},
	})
	if !OwnerActive(enabled, "") {
		t.Fatal("a standalone record must stay active")
	}
	if !OwnerActive(enabled, "on") {
		t.Fatal("a record owned by an enabled plugin must stay active")
	}
	if OwnerActive(enabled, "off") {
		t.Fatal("a record owned by a disabled plugin must be skipped")
	}
	if OwnerActive(enabled, "uninstalled") {
		t.Fatal("a record owned by a missing plugin must be skipped")
	}
}
