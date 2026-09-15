package plugin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
)

// ToolSpec is a model-visible native tool exposed by a bundled provider.
type ToolSpec struct {
	Name        string
	Description string
	Schema      model.JSON
}

// ToolProvider is a native tool implementation compiled into the binary. The
// bundled plugins register one each; third-party bundles cannot reach this
// interface and ship native tools over MCP instead.
type ToolProvider interface {
	Tools() []ToolSpec
	Execute(ctx context.Context, name, arguments string) (string, error)
}

// ActiveTool is one native tool an enabled plugin currently contributes.
type ActiveTool struct {
	// Owner identifies the contributing bundle for the tool registry source.
	Owner    string
	Spec     ToolSpec
	Provider ToolProvider
}

// Runtime resolves the native tools that installed plugins contribute. It is
// constructed once with the provider implementations this build carries, and
// answers per-turn queries from the plugin records.
type Runtime struct {
	providers map[string]ToolProvider
}

// NewRuntime binds provider implementations to their declarations. A provider
// that is not declared in the registry is a wiring mistake, not a user error,
// so it fails loudly at construction.
func NewRuntime(implementations map[string]ToolProvider) (*Runtime, error) {
	runtime := &Runtime{providers: make(map[string]ToolProvider, len(implementations))}
	for name, implementation := range implementations {
		name = strings.TrimSpace(name)
		if _, ok := LookupProvider(name); !ok {
			return nil, fmt.Errorf("provider %q is not declared in the plugin provider registry", name)
		}
		if implementation == nil {
			return nil, fmt.Errorf("provider %q has a nil implementation", name)
		}
		runtime.providers[name] = implementation
	}
	return runtime, nil
}

// ActiveTools returns the native tools contributed by enabled plugins.
//
// A declared provider without an implementation in this build is skipped
// silently: manifests may reference providers that land in a later release.
// A tool whose provider needs a permission the manifest never declared is also
// skipped — install-time validation is the primary gate, and this is the
// second one that holds even for records written before that gate existed.
func (r *Runtime) ActiveTools(plugins []model.Plugin) ([]ActiveTool, error) {
	if r == nil {
		return nil, nil
	}
	var result []ActiveTool
	for _, item := range plugins {
		if !item.Enabled || len(item.Manifest) == 0 {
			continue
		}
		manifest := &Manifest{}
		if err := decodeManifest(item.Manifest, manifest); err != nil {
			return nil, err
		}
		if len(manifest.Contributes.Tools) == 0 {
			continue
		}
		declared, err := skills.NormalizePermissions(manifest.Permissions)
		if err != nil {
			return nil, err
		}
		granted := make(map[string]bool, len(declared))
		for _, permission := range declared {
			granted[permission] = true
		}
		owner := ownerName(item, manifest)
		for _, contribution := range manifest.Contributes.Tools {
			provider, ok := LookupProvider(contribution.Provider)
			if !ok {
				continue
			}
			implementation, ok := r.providers[strings.TrimSpace(contribution.Provider)]
			if !ok {
				continue
			}
			if !covers(granted, provider.Requires) {
				continue
			}
			// The manifest is the consent surface: a provider may implement
			// more tools than the bundle advertises, and only the advertised
			// names become visible to the model.
			advertised := make(map[string]bool, len(contribution.Names))
			for _, name := range contribution.Names {
				advertised[strings.TrimSpace(name)] = true
			}
			for _, spec := range implementation.Tools() {
				if !advertised[strings.TrimSpace(spec.Name)] {
					continue
				}
				result = append(result, ActiveTool{Owner: owner, Spec: spec, Provider: implementation})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Spec.Name < result[j].Spec.Name })
	return result, nil
}

// EnabledSet reports which plugin UUIDs are enabled. Skill and MCP records
// carry their owning plugin, and a record owned by a disabled plugin must not
// reach a turn even when the record itself is enabled.
func EnabledSet(plugins []model.Plugin) map[string]bool {
	enabled := make(map[string]bool, len(plugins))
	for _, item := range plugins {
		if item.Enabled {
			enabled[item.UUID] = true
		}
	}
	return enabled
}

// OwnerActive reports whether a record owned by pluginUUID may be used.
// Records without an owner are standalone and always pass.
func OwnerActive(enabled map[string]bool, pluginUUID string) bool {
	pluginUUID = strings.TrimSpace(pluginUUID)
	return pluginUUID == "" || enabled[pluginUUID]
}

func ownerName(plugin model.Plugin, manifest *Manifest) string {
	if id := strings.TrimSpace(plugin.PluginID); id != "" {
		return id
	}
	if id := strings.TrimSpace(manifest.ID); id != "" {
		return id
	}
	return plugin.Name
}

func covers(granted map[string]bool, required []string) bool {
	for _, permission := range required {
		if !granted[permission] {
			return false
		}
	}
	return true
}
