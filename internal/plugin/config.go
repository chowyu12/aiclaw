package plugin

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/internal/model"
)

// ConfigStore persists plugin configuration. Values live in the same local
// SQLite database as the rest of the application's credentials.
//
// 渠道插件（微信、企业微信）的配置是按连接存的：每个连接（一个微信号、一个企微机器人）
// 一套凭据，connectionID 就是它的 id。不带渠道的插件只有插件级配置，connectionID 为空。
type ConfigStore interface {
	ListPluginConfig(ctx context.Context, pluginUUID, connectionID string) ([]model.PluginConfig, error)
	SetPluginConfig(ctx context.Context, item *model.PluginConfig) error
	DeletePluginConfig(ctx context.Context, pluginUUID, connectionID, key string) error
	ListChannelConnections(ctx context.Context, pluginUUID string) ([]model.ChannelConnection, error)
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
func (s *ConfigService) Load(ctx context.Context, pluginUUID, connectionID string) (Values, error) {
	items, err := s.store.ListPluginConfig(ctx, pluginUUID, connectionID)
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
func (s *ConfigService) Fields(ctx context.Context, plugin model.Plugin, connectionID string) ([]Field, error) {
	declared, err := declaredConfig(plugin)
	if err != nil {
		return nil, err
	}
	items, err := s.store.ListPluginConfig(ctx, plugin.UUID, connectionID)
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
func (s *ConfigService) Set(ctx context.Context, plugin model.Plugin, connectionID, key, value string) error {
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
		return s.Delete(ctx, plugin, connectionID, key)
	}
	return s.store.SetPluginConfig(ctx, &model.PluginConfig{
		PluginUUID: plugin.UUID, ConnectionID: connectionID, Key: key, Value: value, Secret: definition.Secret,
	})
}

// Delete removes one value. A required plugin-level key cannot be cleared
// while the plugin is enabled: that would leave it running without what it
// declared it needs. A connection's key can: that connection just stops
// starting until it is filled in again, the others keep running.
func (s *ConfigService) Delete(ctx context.Context, plugin model.Plugin, connectionID, key string) error {
	key = strings.TrimSpace(key)
	declared, err := declaredConfig(plugin)
	if err != nil {
		return err
	}
	if definition, ok := declared[key]; ok && definition.Required && plugin.Enabled && connectionID == "" {
		return fmt.Errorf("%q is required by %s; disable the plugin before clearing it", key, plugin.Name)
	}
	return s.store.DeletePluginConfig(ctx, plugin.UUID, connectionID, key)
}

// MissingRequired reports the declared keys that are required and unset. A
// plugin can be installed without them, but not enabled.
func (s *ConfigService) MissingRequired(ctx context.Context, plugin model.Plugin, connectionID string) ([]string, error) {
	declared, err := declaredConfig(plugin)
	if err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		return nil, nil
	}
	values, err := s.Load(ctx, plugin.UUID, connectionID)
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
//
// 渠道插件看的是连接：至少有一个连接的配置是齐的才能启用；没配齐的连接不启动，
// 其它连接照跑。
func (s *ConfigService) ValidateEnable(ctx context.Context, plugin model.Plugin) error {
	if HasChannels(plugin) {
		ready, err := s.ReadyConnections(ctx, plugin)
		if err != nil {
			return err
		}
		if len(ready) == 0 {
			return i18n.E("「{name}」还没有可用的连接：先添加一个并填好配置", "name", plugin.Name)
		}
		return nil
	}
	missing, err := s.MissingRequired(ctx, plugin, "")
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("plugin %q needs configuration before it can be enabled: %s", plugin.Name, strings.Join(missing, ", "))
	}
	return nil
}

// ReadyConnections 是配置齐了、可以启动的那些连接。
func (s *ConfigService) ReadyConnections(ctx context.Context, plugin model.Plugin) ([]model.ChannelConnection, error) {
	connections, err := s.store.ListChannelConnections(ctx, plugin.UUID)
	if err != nil {
		return nil, err
	}
	ready := make([]model.ChannelConnection, 0, len(connections))
	for _, connection := range connections {
		missing, err := s.MissingRequired(ctx, plugin, connection.UUID)
		if err != nil {
			return nil, err
		}
		if len(missing) == 0 {
			ready = append(ready, connection)
		}
	}
	return ready, nil
}

// HasChannels 报告插件是否贡献了渠道（它的配置按连接存）。
func HasChannels(plugin model.Plugin) bool {
	if len(plugin.Manifest) == 0 {
		return false
	}
	manifest := &Manifest{}
	if err := decodeManifest(plugin.Manifest, manifest); err != nil {
		return false
	}
	return len(manifest.Contributes.Channels) > 0
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
