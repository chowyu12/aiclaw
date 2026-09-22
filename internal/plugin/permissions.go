package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
)

// BuiltinPrefix marks a contribution implemented inside the application
// binary. Only bundled plugins may use it.
const BuiltinPrefix = "builtin:"

// Provider declares a native tool or channel implementation that a bundled
// plugin may reference. The permissions it needs live next to the declaration
// so install-time validation and runtime activation read one source.
//
// A declaration is not an implementation: whether this build actually carries
// the code is answered by Runtime, which is given the implementations at
// construction. Manifests may reference a provider whose implementation lands
// in a later release (see the delivery order in docs/design/plugin-system.md).
type Provider struct {
	Requires []string
}

// providers is the registry of declared bundled providers.
var providers = map[string]Provider{
	"builtin:computer_use": {Requires: []string{skills.PermissionComputerControl, skills.PermissionFilesystemWrite}},
	"builtin:wechat": {Requires: []string{
		skills.PermissionNetworkAccess, skills.PermissionChannelReceive,
		skills.PermissionChannelSend, skills.PermissionSecretsRead,
		skills.PermissionFilesystemWrite,
	}},
	"builtin:wecom": {Requires: []string{
		skills.PermissionNetworkAccess, skills.PermissionChannelReceive,
		skills.PermissionChannelSend, skills.PermissionSecretsRead,
		skills.PermissionFilesystemWrite,
	}},
}

// LookupProvider reports the registered provider for a contribution.
func LookupProvider(name string) (Provider, bool) {
	provider, ok := providers[strings.TrimSpace(name)]
	return provider, ok
}

// derivePermissions returns the permission set a bundle actually needs.
//
// A manifest that opts into the contribution contract (schema_version >= 1)
// must declare every permission its contributions need: silently widening an
// author's declaration would make the install-time consent screen a lie. A
// legacy manifest predates the contract and cannot have declared anything, so
// its permissions are derived instead of demanded — it still ends up with the
// same set, just without the authoring guarantee.
func derivePermissions(manifest *Manifest, skillInfos []skills.SkillInfo, servers map[string]MCPContribution) ([]string, error) {
	declared, err := skills.NormalizePermissions(manifest.Permissions)
	if err != nil {
		return nil, err
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, permission := range declared {
		declaredSet[permission] = true
	}

	required := make(map[string]bool)
	for _, info := range skillInfos {
		permissions, err := skills.StoredPermissions(info.Permissions)
		if err != nil {
			return nil, err
		}
		for _, permission := range permissions {
			required[permission] = true
		}
	}
	for _, server := range servers {
		if strings.TrimSpace(server.URL) != "" {
			required[skills.PermissionNetworkAccess] = true
			continue
		}
		required[skills.PermissionProcessExecute] = true
	}
	for _, contribution := range manifest.Contributes.Tools {
		provider, ok := LookupProvider(contribution.Provider)
		if !ok {
			return nil, fmt.Errorf("tool contribution uses unknown provider %q", contribution.Provider)
		}
		for _, permission := range provider.Requires {
			required[permission] = true
		}
	}
	for _, contribution := range manifest.Contributes.Channels {
		provider, ok := LookupProvider(contribution.Provider)
		if !ok {
			return nil, fmt.Errorf("channel %q uses unknown provider %q", contribution.ID, contribution.Provider)
		}
		for _, permission := range provider.Requires {
			required[permission] = true
		}
	}

	// A contributed skill or server may need less than the bundle declares
	// (an author can declare a permission a future version will use), but
	// under the contract it can never need more than was declared.
	if manifest.SchemaVersion >= 1 {
		missing := make([]string, 0, len(required))
		for permission := range required {
			if !declaredSet[permission] {
				missing = append(missing, permission)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf("plugin %q contributes capabilities requiring undeclared permissions: %s", manifest.Name, strings.Join(missing, ", "))
		}
	}

	for permission := range required {
		declaredSet[permission] = true
	}
	result := make([]string, 0, len(declaredSet))
	for permission := range declaredSet {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result, nil
}

// InstalledPermissions derives the permission set of an already installed
// bundle from its stored records. It is what the settings UI displays, and it
// deliberately re-derives instead of trusting a cached column so a record
// edited out of band cannot under-report risk.
func InstalledPermissions(plugin model.Plugin, skillItems []model.Skill, servers []model.MCPServer) ([]string, error) {
	set := make(map[string]bool)
	manifest := &Manifest{}
	if len(plugin.Manifest) > 0 {
		if err := decodeManifest(plugin.Manifest, manifest); err != nil {
			return nil, err
		}
		declared, err := skills.NormalizePermissions(manifest.Permissions)
		if err != nil {
			return nil, err
		}
		for _, permission := range declared {
			set[permission] = true
		}
		for _, contribution := range manifest.Contributes.Tools {
			if provider, ok := LookupProvider(contribution.Provider); ok {
				for _, permission := range provider.Requires {
					set[permission] = true
				}
			}
		}
		for _, contribution := range manifest.Contributes.Channels {
			if provider, ok := LookupProvider(contribution.Provider); ok {
				for _, permission := range provider.Requires {
					set[permission] = true
				}
			}
		}
	}
	for _, item := range skillItems {
		if item.PluginUUID != plugin.UUID {
			continue
		}
		permissions, err := skills.StoredPermissions(item.Permissions)
		if err != nil {
			return nil, err
		}
		for _, permission := range permissions {
			set[permission] = true
		}
	}
	for _, server := range servers {
		if server.PluginUUID != plugin.UUID {
			continue
		}
		if server.Transport == model.MCPTransportStdio {
			set[skills.PermissionProcessExecute] = true
			continue
		}
		set[skills.PermissionNetworkAccess] = true
	}
	result := make([]string, 0, len(set))
	for permission := range set {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result, nil
}

// NativeContributions reports the native tools and channels an installed
// record's manifest advertises. Skills and MCP servers are counted from their
// own records instead, since those are persisted per contribution.
func NativeContributions(plugin model.Plugin) (tools int, channels int, err error) {
	if len(plugin.Manifest) == 0 {
		return 0, 0, nil
	}
	manifest := &Manifest{}
	if err := decodeManifest(plugin.Manifest, manifest); err != nil {
		return 0, 0, err
	}
	for _, contribution := range manifest.Contributes.Tools {
		tools += len(contribution.Names)
	}
	return tools, len(manifest.Contributes.Channels), nil
}

func covers(granted map[string]bool, required []string) bool {
	for _, permission := range required {
		if !granted[permission] {
			return false
		}
	}
	return true
}
