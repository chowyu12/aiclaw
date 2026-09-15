// Package plugin owns the plugin bundle contract: manifest parsing, the
// contribution points a bundle may declare, permission derivation and local
// installation. It is the single place that decides what an installed bundle
// is allowed to contribute; the desktop layer only renders the result.
//
// See docs/design/plugin-system.md.
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// manifestCandidates are searched in order. The `.aiclaw-plugin` directory is
// the current location; the Codex-style and bare paths stay supported so
// bundles installed before this package keep working.
var manifestCandidates = []string{
	filepath.Join(".aiclaw-plugin", "plugin.json"),
	filepath.Join(".codex-plugin", "plugin.json"),
	"plugin.json",
}

// mcpCandidates hold `mcpServers` when the manifest does not declare them
// inline.
var mcpCandidates = []string{
	filepath.Join(".aiclaw-plugin", "mcp.json"),
	filepath.Join(".codex-plugin", "mcp.json"),
	".mcp.json",
	"mcp.json",
}

// SchemaVersion is the manifest revision this build understands.
const SchemaVersion = 1

// ErrUnsupportedHost reports a bundle that declares it cannot run here. A
// bundled plugin for another operating system is skipped on this one rather
// than failing startup, so this is a sentinel and not just a message.
var ErrUnsupportedHost = errors.New("plugin is not supported on this host")

type Manifest struct {
	SchemaVersion int                               `json:"schema_version,omitzero"`
	ID            string                            `json:"id,omitzero"`
	Name          string                            `json:"name"`
	Version       string                            `json:"version,omitzero"`
	Description   string                            `json:"description,omitzero"`
	Author        string                            `json:"author,omitzero"`
	Runtime       ManifestRuntime                   `json:"runtime,omitzero"`
	Permissions   []string                          `json:"permissions,omitzero"`
	Config        map[string]model.SkillConfigField `json:"config,omitzero"`
	Contributes   Contributions                     `json:"contributes,omitzero"`
}

type ManifestRuntime struct {
	OS            []string `json:"os,omitzero"`
	MinAppVersion string   `json:"min_app_version,omitzero"`
}

type Contributions struct {
	Skills     []SkillContribution        `json:"skills,omitzero"`
	MCPServers map[string]MCPContribution `json:"mcpServers,omitzero"`
	Tools      []ToolContribution         `json:"tools,omitzero"`
	Channels   []ChannelContribution      `json:"channels,omitzero"`
}

// SkillContribution points at a skill directory relative to the bundle root.
type SkillContribution struct {
	Dir string `json:"dir"`
}

type MCPContribution struct {
	Command string            `json:"command,omitzero"`
	URL     string            `json:"url,omitzero"`
	Args    []string          `json:"args,omitzero"`
	Env     map[string]string `json:"env,omitzero"`
	Headers map[string]string `json:"headers,omitzero"`
}

// ToolContribution advertises native tools executed by a bundled provider
// compiled into the application binary. Third-party bundles cannot use this
// contribution point; they ship native tools over MCP instead.
type ToolContribution struct {
	Provider string   `json:"provider"`
	Names    []string `json:"names"`
}

// ChannelContribution advertises a long-running inbound connector.
type ChannelContribution struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name,omitzero"`
}

// mcpFile is the standalone `mcp.json` shape.
type mcpFile struct {
	MCPServers map[string]MCPContribution `json:"mcpServers"`
}

// LoadManifest reads the bundle manifest from root, returning both the decoded
// manifest and the exact bytes so the record keeps what the author wrote. A
// bundle without any manifest is not an error: a synthetic manifest named after
// the directory is returned so directory-probing bundles keep installing.
func LoadManifest(root string) (*Manifest, model.JSON, error) {
	for _, candidate := range manifestCandidates {
		data, err := os.ReadFile(filepath.Join(root, candidate))
		if err != nil {
			continue
		}
		manifest := &Manifest{}
		if err := json.Unmarshal(data, manifest); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", candidate, err)
		}
		if strings.TrimSpace(manifest.Name) == "" {
			manifest.Name = filepath.Base(root)
		}
		return manifest, model.JSON(data), nil
	}
	return &Manifest{Name: filepath.Base(root)}, model.JSON(`{}`), nil
}

// loadMCPFile returns the `mcpServers` declared in a standalone file, used only
// when the manifest itself declares none.
func loadMCPFile(root string) (map[string]MCPContribution, error) {
	for _, candidate := range mcpCandidates {
		data, err := os.ReadFile(filepath.Join(root, candidate))
		if err != nil {
			continue
		}
		var file mcpFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parse %s: %w", candidate, err)
		}
		return file.MCPServers, nil
	}
	return nil, nil
}

// checkRuntime rejects a bundle that cannot run on this machine before any
// file is copied.
func (m *Manifest) checkRuntime() error {
	if m.SchemaVersion > SchemaVersion {
		return fmt.Errorf("plugin manifest schema version %d is newer than supported version %d", m.SchemaVersion, SchemaVersion)
	}
	if len(m.Runtime.OS) == 0 {
		return nil
	}
	for _, value := range m.Runtime.OS {
		if strings.EqualFold(strings.TrimSpace(value), runtime.GOOS) {
			return nil
		}
	}
	return fmt.Errorf("%w: it requires one of %s but this host is %s", ErrUnsupportedHost, strings.Join(m.Runtime.OS, ", "), runtime.GOOS)
}
