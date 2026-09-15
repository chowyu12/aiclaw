package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// memStore records writes so installation and rollback can be asserted without
// a database. It is mutex-guarded because the channel host reads it from its
// own goroutines, the way the real store is used.
type memStore struct {
	mu sync.Mutex

	plugins  map[string]*model.Plugin
	skills   map[string]*model.Skill
	servers  map[string]*model.MCPServer
	config   map[string]*model.PluginConfig
	bindings map[string]*model.ChannelBinding
	failOn   string
}

func newMemStore() *memStore {
	return &memStore{
		plugins:  map[string]*model.Plugin{},
		skills:   map[string]*model.Skill{},
		servers:  map[string]*model.MCPServer{},
		config:   map[string]*model.PluginConfig{},
		bindings: map[string]*model.ChannelBinding{},
	}
}

func configKey(pluginUUID, key string) string { return pluginUUID + "\x00" + key }

func (s *memStore) ListPluginConfig(_ context.Context, pluginUUID string) ([]model.PluginConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]model.PluginConfig, 0, len(s.config))
	for _, item := range s.config {
		if item.PluginUUID == pluginUUID {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (s *memStore) SetPluginConfig(_ context.Context, item *model.PluginConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn == "config" {
		return fmt.Errorf("set plugin config failed")
	}
	stored := *item
	s.config[configKey(item.PluginUUID, item.Key)] = &stored
	return nil
}

func (s *memStore) DeletePluginConfig(_ context.Context, pluginUUID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.config, configKey(pluginUUID, key))
	return nil
}

func (s *memStore) DeleteChannelBindings(_ context.Context, pluginUUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.bindings {
		if item.PluginUUID == pluginUUID {
			delete(s.bindings, key)
		}
	}
	return nil
}

func (s *memStore) DeletePluginConfigs(_ context.Context, pluginUUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.config {
		if item.PluginUUID == pluginUUID {
			delete(s.config, key)
		}
	}
	return nil
}

func (s *memStore) CreatePlugin(_ context.Context, plugin *model.Plugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn == "plugin" {
		return fmt.Errorf("create plugin failed")
	}
	s.plugins[plugin.UUID] = plugin
	return nil
}

func (s *memStore) UpsertPlugin(_ context.Context, plugin *model.Plugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn == "plugin" {
		return fmt.Errorf("upsert plugin failed")
	}
	s.plugins[plugin.UUID] = plugin
	return nil
}

func (s *memStore) ListPlugins(_ context.Context) ([]model.Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]model.Plugin, 0, len(s.plugins))
	for _, plugin := range s.plugins {
		items = append(items, *plugin)
	}
	return items, nil
}

func (s *memStore) DeletePlugin(_ context.Context, pluginUUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plugins, pluginUUID)
	return nil
}

func (s *memStore) UpsertSkill(_ context.Context, skill *model.Skill) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn == "skill" {
		return fmt.Errorf("upsert skill failed")
	}
	s.skills[skill.UUID] = skill
	return nil
}

func (s *memStore) DeletePluginSkills(_ context.Context, pluginUUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for uuid, skill := range s.skills {
		if skill.PluginUUID == pluginUUID {
			delete(s.skills, uuid)
		}
	}
	return nil
}

func (s *memStore) UpsertMCPServer(_ context.Context, server *model.MCPServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn == "mcp" {
		return fmt.Errorf("upsert mcp failed")
	}
	s.servers[server.UUID] = server
	return nil
}

func (s *memStore) DeletePluginMCP(_ context.Context, pluginUUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for uuid, server := range s.servers {
		if server.PluginUUID == pluginUUID {
			delete(s.servers, uuid)
		}
	}
	return nil
}

func (s *memStore) empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.plugins) == 0 && len(s.skills) == 0 && len(s.servers) == 0 &&
		len(s.config) == 0 && len(s.bindings) == 0
}

func researchBundle(t *testing.T) string {
	return writeBundle(t, map[string]string{
		".codex-plugin/plugin.json":     `{"name":"Research Kit","description":"Local research","version":"1.2.0"}`,
		"skills/research/manifest.json": `{"name":"Research","main":"main.py","permissions":["process.execute"],"tools":[{"name":"research_echo"}]}`,
		"skills/research/SKILL.md":      "---\nname: Research\ndescription: Research carefully\n---\nCite evidence.",
		"skills/research/main.py":       "print('ok')",
		"mcp.json":                      `{"mcpServers":{"echo":{"command":"/bin/echo","args":["ready"]}}}`,
	})
}

func TestInstallRecordsContributionsDisabled(t *testing.T) {
	store := newMemStore()
	root := t.TempDir()
	installer := NewInstaller(store, root)

	plugin, counts, err := installer.Install(context.Background(), researchBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if counts.Skills != 1 || counts.MCP != 1 || counts.Tools != 0 || counts.Channels != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if plugin.Enabled {
		t.Fatal("a freshly installed plugin must stay disabled until the user enables it")
	}
	if plugin.Source != model.PluginSourceLocal {
		t.Fatalf("source = %q", plugin.Source)
	}
	if !strings.HasPrefix(plugin.InstallDir, installer.PluginsDir()) {
		t.Fatalf("install dir %q is outside %q", plugin.InstallDir, installer.PluginsDir())
	}
	if _, err := os.Stat(filepath.Join(plugin.InstallDir, "skills", "research", "main.py")); err != nil {
		t.Fatalf("bundle files were not copied: %v", err)
	}
	for _, skill := range store.skills {
		if skill.Enabled || skill.PluginUUID != plugin.UUID {
			t.Fatalf("skill record = %+v", skill)
		}
		if skill.InstallDir != filepath.Join(plugin.InstallDir, "skills", "research") {
			t.Fatalf("skill install dir = %q", skill.InstallDir)
		}
	}
	for _, server := range store.servers {
		if server.Enabled || server.Transport != model.MCPTransportStdio || server.Endpoint != "/bin/echo" {
			t.Fatalf("mcp record = %+v", server)
		}
	}
}

func TestInstallUsesStreamableHTTPForURLServers(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	source := writeBundle(t, map[string]string{
		"plugin.json": `{"name":"Remote MCP"}`,
		"mcp.json":    `{"mcpServers":{"remote":{"url":"https://example.invalid/mcp","headers":{"X-Token":"t"}}}}`,
	})
	if _, _, err := installer.Install(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	for _, server := range store.servers {
		if server.Transport != model.MCPTransportStreamableHTTP || server.Endpoint != "https://example.invalid/mcp" {
			t.Fatalf("remote server = %+v", server)
		}
		if string(server.Args) != "[]" {
			t.Fatalf("omitted args should fall back to an empty list, got %q", server.Args)
		}
	}
}

func TestInstallRollsBackFilesAndRecordsOnPersistFailure(t *testing.T) {
	for _, failOn := range []string{"plugin", "skill", "mcp"} {
		t.Run(failOn, func(t *testing.T) {
			store := newMemStore()
			store.failOn = failOn
			installer := NewInstaller(store, t.TempDir())

			if _, _, err := installer.Install(context.Background(), researchBundle(t)); err == nil {
				t.Fatal("persistence failure was not reported")
			}
			if !store.empty() {
				t.Fatalf("records survived rollback: %+v %+v %+v", store.plugins, store.skills, store.servers)
			}
			entries, err := os.ReadDir(installer.PluginsDir())
			if err == nil && len(entries) != 0 {
				t.Fatalf("plugin files survived rollback: %v", entries)
			}
		})
	}
}

func TestInstallRejectedBundleLeavesNoFiles(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	source := writeBundle(t, map[string]string{
		"plugin.json":                 `{"name":"Broken"}`,
		"skills/broken/manifest.json": `{"name":"Broken","main":"main.py","tools":[{"name":"broken_tool"}]}`,
		"skills/broken/main.py":       "print('no permission')",
	})
	if _, _, err := installer.Install(context.Background(), source); err == nil {
		t.Fatal("invalid bundle was installed")
	}
	if !store.empty() {
		t.Fatalf("invalid bundle left records: %+v", store.plugins)
	}
	// Validation runs before the copy, so the plugin directory is never created.
	if _, err := os.Stat(installer.PluginsDir()); !os.IsNotExist(err) {
		t.Fatalf("invalid bundle created the plugin directory: %v", err)
	}
}

func TestUninstallRemovesRecordsAndManagedFilesOnly(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	plugin, _, err := installer.Install(context.Background(), researchBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	installDir := plugin.InstallDir
	if err := installer.Uninstall(context.Background(), *plugin); err != nil {
		t.Fatal(err)
	}
	if !store.empty() {
		t.Fatalf("records survived uninstall: %+v %+v %+v", store.plugins, store.skills, store.servers)
	}
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Fatalf("plugin files survived uninstall: %v", err)
	}

	// A record pointing outside the managed directory must lose its records
	// without its files being touched.
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "keep.txt"), "keep")
	unmanaged := model.Plugin{UUID: "u", InstallDir: outside}
	if err := installer.Uninstall(context.Background(), unmanaged); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("uninstall deleted files outside the plugin directory: %v", err)
	}
}

func TestInstalledPermissionsReDerivesFromRecords(t *testing.T) {
	plugin := model.Plugin{
		UUID: "p1",
		Manifest: model.JSON(`{"schema_version":1,"name":"Kit","permissions":["network.access"],
			"contributes":{"channels":[{"id":"wecom","provider":"builtin:wecom"}]}}`),
	}
	skillItems := []model.Skill{
		{PluginUUID: "p1", Permissions: model.JSON(`["process.execute"]`)},
		{PluginUUID: "other", Permissions: model.JSON(`["filesystem.read"]`)},
	}
	servers := []model.MCPServer{{PluginUUID: "p1", Transport: model.MCPTransportStreamableHTTP}}

	permissions, err := InstalledPermissions(plugin, skillItems, servers)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(permissions, ",")
	want := "channel.receive,channel.send,filesystem.write,network.access,process.execute,secrets.read"
	if got != want {
		t.Fatalf("permissions = %q, want %q", got, want)
	}
}
