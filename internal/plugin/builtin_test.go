package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/plugins/bundled"
)

const bundledComputerUse = `{"schema_version":1,"id":"aiclaw.computer-use","name":"Computer Use",
	"version":"0.1.0","permissions":["computer.control","filesystem.write"],
	"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`

func TestEnsureBuiltinsRecordsBundlesDisabled(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	bundles := fstest.MapFS{
		"computer-use/plugin.json": {Data: []byte(bundledComputerUse)},
	}

	if err := installer.EnsureBuiltins(context.Background(), bundles); err != nil {
		t.Fatal(err)
	}
	plugins, _ := store.ListPlugins(context.Background())
	if len(plugins) != 1 {
		t.Fatalf("plugins = %+v", plugins)
	}
	plugin := plugins[0]
	if plugin.Source != model.PluginSourceBuiltin || plugin.PluginID != "aiclaw.computer-use" {
		t.Fatalf("record = %+v", plugin)
	}
	// Shipping a capability is not granting it.
	if plugin.Enabled {
		t.Fatal("a bundled plugin must ship disabled")
	}
	if plugin.InstallDir != filepath.Join(installer.PluginsDir(), "computer-use") {
		t.Fatalf("install dir = %q", plugin.InstallDir)
	}
	if _, err := os.Stat(filepath.Join(plugin.InstallDir, "plugin.json")); err != nil {
		t.Fatalf("manifest was not synced: %v", err)
	}
}

func TestEnsureBuiltinsPreservesEnableChoiceAndRefreshesFiles(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	bundles := fstest.MapFS{"computer-use/plugin.json": {Data: []byte(bundledComputerUse)}}
	ctx := context.Background()
	if err := installer.EnsureBuiltins(ctx, bundles); err != nil {
		t.Fatal(err)
	}
	plugins, _ := store.ListPlugins(ctx)
	uuid := plugins[0].UUID
	store.plugins[uuid].Enabled = true

	// A stale file from an older version must not survive the upgrade.
	stale := filepath.Join(plugins[0].InstallDir, "stale.txt")
	mustWriteFile(t, stale, "old")

	upgraded := fstest.MapFS{
		"computer-use/plugin.json": {Data: []byte(strings.Replace(bundledComputerUse, `"version":"0.1.0"`, `"version":"0.2.0"`, 1))},
	}
	if err := installer.EnsureBuiltins(ctx, upgraded); err != nil {
		t.Fatal(err)
	}
	plugins, _ = store.ListPlugins(ctx)
	if len(plugins) != 1 {
		t.Fatalf("the upgrade created a second record: %+v", plugins)
	}
	if plugins[0].UUID != uuid {
		t.Fatalf("record identity changed across upgrade: %q -> %q", uuid, plugins[0].UUID)
	}
	if !plugins[0].Enabled {
		t.Fatal("the upgrade discarded the user's enable choice")
	}
	if plugins[0].Version != "0.2.0" {
		t.Fatalf("version = %q", plugins[0].Version)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a stale bundled file survived the upgrade: %v", err)
	}
}

func TestEnsureBuiltinsSkipsBundlesForOtherHosts(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	bundles := fstest.MapFS{
		"elsewhere/plugin.json": {Data: []byte(`{"schema_version":1,"id":"aiclaw.elsewhere","name":"Elsewhere",
			"runtime":{"os":["plan9"]}}`)},
	}
	// A bundle for another operating system must not fail startup.
	if err := installer.EnsureBuiltins(context.Background(), bundles); err != nil {
		t.Fatal(err)
	}
	if !store.empty() {
		t.Fatalf("a bundle for another host was recorded: %+v", store.plugins)
	}
	if _, err := os.Stat(filepath.Join(installer.PluginsDir(), "elsewhere")); !os.IsNotExist(err) {
		t.Fatalf("a bundle for another host left files behind: %v", err)
	}
}

func TestEnsureBuiltinsReportsDefectiveBundledManifest(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	// A manifest we ship ourselves is a defect, not user error: fail loudly.
	bundles := fstest.MapFS{
		"broken/plugin.json": {Data: []byte(`{"schema_version":1,"name":"Broken",
			"contributes":{"tools":[{"provider":"builtin:nope","names":["x"]}]}}`)},
	}
	if err := installer.EnsureBuiltins(context.Background(), bundles); err == nil {
		t.Fatal("a defective bundled manifest was accepted")
	}

	missingID := fstest.MapFS{"anon/plugin.json": {Data: []byte(`{"schema_version":1,"name":"Anonymous"}`)}}
	if err := installer.EnsureBuiltins(context.Background(), missingID); err == nil {
		t.Fatal("a bundled manifest without an id was accepted")
	}
}

func TestBuiltinPluginCannotBeUninstalled(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	ctx := context.Background()
	bundles := fstest.MapFS{"computer-use/plugin.json": {Data: []byte(bundledComputerUse)}}
	if err := installer.EnsureBuiltins(ctx, bundles); err != nil {
		t.Fatal(err)
	}
	plugins, _ := store.ListPlugins(ctx)
	if err := installer.Uninstall(ctx, plugins[0]); err == nil {
		t.Fatal("a bundled plugin was uninstalled; it would reappear on the next startup")
	}
	remaining, _ := store.ListPlugins(ctx)
	if len(remaining) != 1 {
		t.Fatalf("the refused uninstall still removed records: %+v", remaining)
	}
	if _, err := os.Stat(plugins[0].InstallDir); err != nil {
		t.Fatalf("the refused uninstall still removed files: %v", err)
	}
}

// The manifest actually shipped must satisfy every rule this package enforces,
// so a typo in it fails here rather than at a user's first startup.
func TestShippedBundlesResolve(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	if err := installer.EnsureBuiltins(context.Background(), bundled.FS()); err != nil {
		t.Fatal(err)
	}
	plugins, _ := store.ListPlugins(context.Background())
	if runtime.GOOS == "darwin" && len(plugins) == 0 {
		t.Fatal("no bundled plugin resolved on a host the shipped manifests support")
	}
	for _, plugin := range plugins {
		tools, channels, err := NativeContributions(plugin)
		if err != nil {
			t.Fatal(err)
		}
		if tools == 0 && channels == 0 {
			t.Fatalf("bundled plugin %q contributes nothing", plugin.PluginID)
		}
	}
}
