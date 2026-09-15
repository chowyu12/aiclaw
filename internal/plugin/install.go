package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
)

// Store is the persistence contract of plugin installation. Skills and MCP
// servers are written through their own tables and stay linked by plugin UUID.
type Store interface {
	CreatePlugin(ctx context.Context, plugin *model.Plugin) error
	UpsertPlugin(ctx context.Context, plugin *model.Plugin) error
	ListPlugins(ctx context.Context) ([]model.Plugin, error)
	DeletePlugin(ctx context.Context, pluginUUID string) error
	UpsertSkill(ctx context.Context, skill *model.Skill) error
	DeletePluginSkills(ctx context.Context, pluginUUID string) error
	UpsertMCPServer(ctx context.Context, server *model.MCPServer) error
	DeletePluginMCP(ctx context.Context, pluginUUID string) error
	DeletePluginConfigs(ctx context.Context, pluginUUID string) error
	DeleteChannelBindings(ctx context.Context, pluginUUID string) error
}

// Counts reports what an installed bundle contributes.
type Counts struct {
	Skills   int `json:"skills"`
	MCP      int `json:"mcp"`
	Tools    int `json:"tools"`
	Channels int `json:"channels"`
}

// Installer installs bundles under <root>/plugins.
type Installer struct {
	store Store
	root  string
}

func NewInstaller(store Store, root string) *Installer {
	return &Installer{store: store, root: root}
}

// PluginsDir is the only directory the installer creates or removes files in.
func (i *Installer) PluginsDir() string { return filepath.Join(i.root, "plugins") }

// Install validates the bundle at source, copies it into the plugin directory
// and records its contributions. Everything is installed disabled: enabling a
// bundle is a separate, explicit act because it is what grants the declared
// permissions.
//
// Validation happens before any file is copied, so a rejected bundle leaves
// nothing behind. Once files are copied, a persistence failure rolls back both
// the records and the files.
func (i *Installer) Install(ctx context.Context, source string) (*model.Plugin, Counts, error) {
	resolved, err := Resolve(source)
	if err != nil {
		return nil, Counts{}, err
	}
	return i.install(ctx, resolved, model.PluginSourceLocal)
}

func (i *Installer) install(ctx context.Context, resolved *Resolved, origin model.PluginSource) (*model.Plugin, Counts, error) {
	manifest := resolved.Manifest
	pluginUUID := uuid.NewString()
	dest := filepath.Join(i.PluginsDir(), safeName(manifest.Name)+"-"+pluginUUID[:8])
	if err := copyDir(resolved.Root, dest); err != nil {
		_ = os.RemoveAll(dest)
		return nil, Counts{}, err
	}

	plugin := &model.Plugin{
		UUID: pluginUUID, PluginID: strings.TrimSpace(manifest.ID), Source: origin,
		Name: manifest.Name, Description: manifest.Description, Version: manifest.Version,
		InstallDir: dest, Manifest: resolved.Raw, Enabled: false,
	}
	counts := Counts{
		Skills: len(resolved.Skills), MCP: len(resolved.Servers),
		Channels: len(manifest.Contributes.Channels),
	}
	for _, contribution := range manifest.Contributes.Tools {
		counts.Tools += len(contribution.Names)
	}

	if err := i.store.CreatePlugin(ctx, plugin); err != nil {
		i.rollback(ctx, plugin.UUID, dest)
		return nil, Counts{}, err
	}
	if err := i.persistContributions(ctx, plugin, resolved); err != nil {
		i.rollback(ctx, plugin.UUID, dest)
		return nil, Counts{}, err
	}
	return plugin, counts, nil
}

// persistContributions writes the skill and MCP records a bundle contributes.
// The plugin record itself is written by the caller, which differs between a
// user install and a bundled sync.
func (i *Installer) persistContributions(ctx context.Context, plugin *model.Plugin, resolved *Resolved) error {
	for _, item := range resolved.Skills {
		skill := skills.InfoToSkill(item.Info, model.SkillSourceLocal, item.Info.Slug)
		skill.UUID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(plugin.UUID+"/"+item.Info.DirName)).String()
		skill.PluginUUID = plugin.UUID
		skill.Enabled = false
		skill.InstallDir = plugin.InstallDir
		if item.RelDir != "" {
			skill.InstallDir = filepath.Join(plugin.InstallDir, item.RelDir)
		}
		if err := i.store.UpsertSkill(ctx, skill); err != nil {
			return err
		}
	}
	for name, contribution := range resolved.Servers {
		server, err := mcpServerRecord(plugin.UUID, name, contribution)
		if err != nil {
			return err
		}
		if err := i.store.UpsertMCPServer(ctx, server); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall removes a bundle's records and, when the files live inside the
// managed plugin directory, its files.
//
// A bundled plugin ships with the binary and would reappear on the next
// startup, so it can only be disabled, never removed.
func (i *Installer) Uninstall(ctx context.Context, plugin model.Plugin) error {
	if plugin.Source == model.PluginSourceBuiltin {
		return fmt.Errorf("plugin %q ships with the application and can only be disabled", plugin.Name)
	}
	if err := i.store.DeletePluginSkills(ctx, plugin.UUID); err != nil {
		return err
	}
	if err := i.store.DeletePluginMCP(ctx, plugin.UUID); err != nil {
		return err
	}
	// Stored secrets and inbound authorizations must not outlive the bundle
	// they belong to.
	if err := i.store.DeletePluginConfigs(ctx, plugin.UUID); err != nil {
		return err
	}
	if err := i.store.DeleteChannelBindings(ctx, plugin.UUID); err != nil {
		return err
	}
	if err := i.store.DeletePlugin(ctx, plugin.UUID); err != nil {
		return err
	}
	if !i.managed(plugin.InstallDir) {
		return nil
	}
	if err := os.RemoveAll(filepath.Clean(plugin.InstallDir)); err != nil {
		return fmt.Errorf("remove plugin files: %w", err)
	}
	return nil
}

func (i *Installer) rollback(ctx context.Context, pluginUUID, dest string) {
	_ = i.store.DeletePluginSkills(ctx, pluginUUID)
	_ = i.store.DeletePluginMCP(ctx, pluginUUID)
	_ = i.store.DeletePluginConfigs(ctx, pluginUUID)
	_ = i.store.DeletePlugin(ctx, pluginUUID)
	if i.managed(dest) {
		_ = os.RemoveAll(dest)
	}
}

// managed reports whether dir is inside the installer's plugin directory. It
// is the guard that keeps uninstall and rollback from deleting anything else.
func (i *Installer) managed(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(i.PluginsDir()), filepath.Clean(dir))
	if err != nil {
		return false
	}
	return relative != "." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func mcpServerRecord(pluginUUID, name string, contribution MCPContribution) (*model.MCPServer, error) {
	transport, endpoint := model.MCPTransportStdio, strings.TrimSpace(contribution.Command)
	if url := strings.TrimSpace(contribution.URL); url != "" {
		transport, endpoint = model.MCPTransportStreamableHTTP, url
	}
	if endpoint == "" {
		return nil, fmt.Errorf("MCP server %q declares neither a command nor a url", name)
	}
	args, err := encodeJSON(contribution.Args, "[]")
	if err != nil {
		return nil, err
	}
	env, err := encodeJSON(contribution.Env, "{}")
	if err != nil {
		return nil, err
	}
	headers, err := encodeJSON(contribution.Headers, "{}")
	if err != nil {
		return nil, err
	}
	return &model.MCPServer{
		UUID:       uuid.NewSHA1(uuid.NameSpaceURL, []byte(pluginUUID+"/mcp/"+name)).String(),
		PluginUUID: pluginUUID, Name: name, Transport: transport, Endpoint: endpoint,
		Args: args, Env: env, Headers: headers, Enabled: false,
	}, nil
}

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "plugin"
	}
	return strings.Trim(b.String(), "-")
}

// copyDir copies a bundle tree, skipping symlinks and other irregular files so
// a bundle cannot reach outside itself once installed.
func copyDir(source, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(source, dest string, perm os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// encodeJSON renders an optional manifest map or list into the JSON column
// shape the MCP records use, falling back when the author omitted it.
func encodeJSON(value any, fallback string) (model.JSON, error) {
	if value == nil {
		return model.JSON(fallback), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || string(data) == "null" {
		return model.JSON(fallback), nil
	}
	return model.JSON(data), nil
}
