package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
)

const computerUseManifest = `{"schema_version":1,"id":"aiclaw.computer-use","name":"Computer Use",
	"permissions":["computer.control","filesystem.write"],
	"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`

type computerUseStub struct{ executed int }

func (p *computerUseStub) Tools() []pluginpkg.ToolSpec {
	return []pluginpkg.ToolSpec{{Name: "computer", Description: "control the screen"}}
}

func (p *computerUseStub) Execute(context.Context, string, string) (string, error) {
	p.executed++
	return `{"ok":true}`, nil
}

func registered(t *testing.T, ctx context.Context, dispatcher *LocalToolDispatcher, thread model.Thread, name string) bool {
	t.Helper()
	definitions, err := dispatcher.Definitions(ctx, thread)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

// A skill or MCP record carries its own Enabled flag, and disabling a plugin
// cascades to those flags. That cascade is not a guarantee: a record left
// enabled by a failed cascade, an older build or a direct database edit must
// still not reach a turn while its plugin is off.
func TestDisabledPluginSuppressesItsEnabledSkill(t *testing.T) {
	ctx, database, dispatcher, thread := newToolTestRuntime(t)
	plugin := &model.Plugin{
		UUID: "plugin-owner", Name: "Research Kit", InstallDir: t.TempDir(),
		Manifest: model.JSON(`{"name":"Research Kit"}`), Enabled: false,
	}
	if err := database.CreatePlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	skill := &model.Skill{
		UUID: "skill-owned", PluginUUID: plugin.UUID, Name: "Research",
		Description: "verify release artifacts", Instruction: "OWNED SKILL INSTRUCTIONS",
		Source: model.SkillSourceLocal, Enabled: true,
		ToolDefs: model.JSON(`[{"name":"owned_tool","description":"contributed by a plugin"}]`),
	}
	if err := database.UpsertSkill(ctx, skill); err != nil {
		t.Fatal(err)
	}

	if registered(t, ctx, dispatcher, thread, "owned_tool") {
		t.Fatal("a disabled plugin's skill tool was advertised")
	}
	messages, err := dispatcher.ContextMessages(ctx, thread)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "OWNED SKILL INSTRUCTIONS") || strings.Contains(message.Content, "Research") {
			t.Fatalf("a disabled plugin's skill reached the context: %q", message.Content)
		}
	}

	if err := database.SetPluginEnabled(ctx, plugin.UUID, true); err != nil {
		t.Fatal(err)
	}
	dispatcher.Reload()
	if !registered(t, ctx, dispatcher, thread, "owned_tool") {
		t.Fatal("enabling the plugin did not advertise its skill tool")
	}
}

// A stdio MCP server is a subprocess. While its plugin is off it must not be
// spawned at all, so this asserts on the side effect of running rather than on
// tool visibility: Manager.Connect logs and skips dial failures, which makes
// an unreachable endpoint indistinguishable from one that was never dialled.
func TestDisabledPluginNeverSpawnsItsMCPServer(t *testing.T) {
	ctx, database, dispatcher, thread := newToolTestRuntime(t)
	marker := filepath.Join(t.TempDir(), "spawned")
	plugin := &model.Plugin{UUID: "plugin-mcp", Name: "Spawner", InstallDir: t.TempDir(), Manifest: model.JSON(`{}`)}
	if err := database.CreatePlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	server := &model.MCPServer{
		UUID: "mcp-owned", PluginUUID: plugin.UUID, Name: "spawner",
		Transport: model.MCPTransportStdio, Endpoint: "/bin/sh",
		Args: model.JSON(`["-c","touch ` + marker + `"]`),
		Env:  model.JSON(`{}`), Headers: model.JSON(`{}`), Enabled: true,
	}
	if err := database.UpsertMCPServer(ctx, server); err != nil {
		t.Fatal(err)
	}

	if _, err := dispatcher.Definitions(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a disabled plugin's MCP server was spawned: %v", err)
	}

	if err := database.SetPluginEnabled(ctx, plugin.UUID, true); err != nil {
		t.Fatal(err)
	}
	dispatcher.Reload()
	if _, err := dispatcher.Definitions(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if !waitForFile(marker) {
		t.Fatal("enabling the plugin did not spawn its MCP server")
	}
}

// waitForFile polls for a marker written by a spawned subprocess.
func waitForFile(path string) bool {
	for range 100 {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// newComputerRuntime binds a stub provider for the bundled computer-use id.
func newComputerRuntime() (*pluginpkg.Runtime, error) {
	return pluginpkg.NewRuntime(map[string]pluginpkg.ToolProvider{"builtin:computer_use": &computerUseStub{}})
}

func TestEnabledPluginContributesNativeTool(t *testing.T) {
	ctx, database, _, thread := newToolTestRuntime(t)
	provider := &computerUseStub{}
	runtime, err := pluginpkg.NewRuntime(map[string]pluginpkg.ToolProvider{"builtin:computer_use": provider})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewLocalToolDispatcher(database,
		WithDispatcherRoot(t.TempDir()), WithSubAgentSampler(subAgentTestSampler{}),
		WithPluginRuntime(runtime))

	plugin := &model.Plugin{
		UUID: "plugin-computer", PluginID: "aiclaw.computer-use", Name: "Computer Use",
		InstallDir: t.TempDir(), Manifest: model.JSON(computerUseManifest), Enabled: false,
	}
	if err := database.CreatePlugin(ctx, plugin); err != nil {
		t.Fatal(err)
	}
	if registered(t, ctx, dispatcher, thread, "computer") {
		t.Fatal("a disabled plugin contributed a native tool")
	}

	if err := database.SetPluginEnabled(ctx, plugin.UUID, true); err != nil {
		t.Fatal(err)
	}
	dispatcher.Reload()
	if !registered(t, ctx, dispatcher, thread, "computer") {
		t.Fatal("an enabled plugin did not contribute its native tool")
	}
	result, err := dispatcher.Execute(ctx, thread, ToolCall{ID: "c1", Name: "computer", Arguments: `{"action":"screenshot"}`})
	if err != nil || result.Content != `{"ok":true}` {
		t.Fatalf("native tool execution failed: result=%+v err=%v", result, err)
	}
	if provider.executed != 1 {
		t.Fatalf("provider executions = %d", provider.executed)
	}
}
