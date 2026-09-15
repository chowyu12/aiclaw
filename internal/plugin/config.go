package plugin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// ConfigStore persists plugin configuration. Values live in the same local
// SQLite database as the rest of the application's credentials.
type ConfigStore interface {
	ListPluginConfig(ctx context.Context, pluginUUID string) ([]model.PluginConfig, error)
	SetPluginConfig(ctx context.Context, item *model.PluginConfig) error
	DeletePluginConfig(ctx context.Context, pluginUUID, key string) error
}

// ConfigReader is what a plugin implementation sees. It deliberately exposes
// values one key at a time rather than handing over the whole map, so a
// provider reads the secrets it needs and nothing else.
type ConfigReader interface {
	Value(key string) (string, bool)
	String(key string) string
}

// Values is the loaded configuration of one plugin.
type Values map[string]string

func (v Values) Value(key string) (string, bool) {
	value, ok := v[strings.TrimSpace(key)]
	return value, ok
}

func (v Values) String(key string) string {
	value, _ := v.Value(key)
	return value
}

// Field describes one configuration key for the settings UI.
//
// Value is populated only for a non-secret field. A secret is reported through
// IsSet alone: a stored secret is never handed back, so a UI can show that one
// exists and let the user replace it, but cannot display or re-transmit it.
type Field struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Description string `json:"description,omitzero"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"`
	IsSet       bool   `json:"is_set"`
	Value       string `json:"value,omitzero"`
}

// ConfigService reads and writes plugin configuration under the rules the
// manifest declares.
type ConfigService struct {
	store ConfigStore
}

func NewConfigService(store ConfigStore) *ConfigService {
	return &ConfigService{store: store}
}

// Load returns every stored value of a plugin, secrets included. It is the
// path a plugin implementation reads through; nothing here reaches the UI.
func (s *ConfigService) Load(ctx context.Context, pluginUUID string) (Values, error) {
	items, err := s.store.ListPluginConfig(ctx, pluginUUID)
	if err != nil {
		return nil, err
	}
	values := make(Values, len(items))
	for _, item := range items {
		values[item.Key] = item.Value
	}
	return values, nil
}

// Fields merges a plugin's declared configuration with what is stored, for
// display. Secret values are reported as set, never returned.
func (s *ConfigService) Fields(ctx context.Context, plugin model.Plugin) ([]Field, error) {
	declared, err := declaredConfig(plugin)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListPluginConfig(ctx, plugin.UUID)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]model.PluginConfig, len(items))
	for _, item := range items {
		stored[item.Key] = item
	}
	fields := make([]Field, 0, len(declared))
	for key, definition := range declared {
		field := Field{
			Key: key, Type: definition.Type, Description: definition.Description,
			Required: definition.Required, Secret: definition.Secret,
		}
		if item, ok := stored[key]; ok {
			field.IsSet = item.Value != ""
			if !definition.Secret {
				field.Value = item.Value
			}
		}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return fields, nil
}

// Set stores one value.
//
// Only keys the manifest declares are accepted: an undeclared key would never
// be read by the plugin, and storing arbitrary user input under a plugin's
// name invites confusion about what is actually configured. Whether a value is
// secret comes from the manifest, never from the caller.
func (s *ConfigService) Set(ctx context.Context, plugin model.Plugin, key, value string) error {
	key = strings.TrimSpace(key)
	declared, err := declaredConfig(plugin)
	if err != nil {
		return err
	}
	definition, ok := declared[key]
	if !ok {
		return fmt.Errorf("plugin %q does not declare a configuration key %q", plugin.Name, key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return s.Delete(ctx, plugin, key)
	}
	return s.store.SetPluginConfig(ctx, &model.PluginConfig{
		PluginUUID: plugin.UUID, Key: key, Value: value, Secret: definition.Secret,
	})
}

// Delete removes one value. A required key cannot be cleared while the plugin
// is enabled: that would leave it running without what it declared it needs.
func (s *ConfigService) Delete(ctx context.Context, plugin model.Plugin, key string) error {
	key = strings.TrimSpace(key)
	declared, err := declaredConfig(plugin)
	if err != nil {
		return err
	}
	if definition, ok := declared[key]; ok && definition.Required && plugin.Enabled {
		return fmt.Errorf("%q is required by %s; disable the plugin before clearing it", key, plugin.Name)
	}
	return s.store.DeletePluginConfig(ctx, plugin.UUID, key)
}

// MissingRequired reports the declared keys that are required and unset. A
// plugin can be installed without them, but not enabled.
func (s *ConfigService) MissingRequired(ctx context.Context, plugin model.Plugin) ([]string, error) {
	declared, err := declaredConfig(plugin)
	if err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		return nil, nil
	}
	values, err := s.Load(ctx, plugin.UUID)
	if err != nil {
		return nil, err
	}
	var missing []string
	for key, definition := range declared {
		if definition.Required && strings.TrimSpace(values[key]) == "" {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// ValidateEnable refuses to enable a plugin whose required configuration is
// absent, so a connector cannot be switched on into a state where it can only
// fail.
func (s *ConfigService) ValidateEnable(ctx context.Context, plugin model.Plugin) error {
	missing, err := s.MissingRequired(ctx, plugin)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("plugin %q needs configuration before it can be enabled: %s", plugin.Name, strings.Join(missing, ", "))
	}
	return nil
}

func declaredConfig(plugin model.Plugin) (map[string]model.SkillConfigField, error) {
	if len(plugin.Manifest) == 0 {
		return nil, nil
	}
	manifest := &Manifest{}
	if err := decodeManifest(plugin.Manifest, manifest); err != nil {
		return nil, err
	}
	return manifest.Config, nil
}
