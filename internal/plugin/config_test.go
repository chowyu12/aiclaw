package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

// computerManifest 是内置 computer-use 插件的 manifest 样本，配置校验用它当「没有配置项」的例子。
const computerManifest = `{"schema_version":1,"id":"aiclaw.computer-use","name":"Computer Use",
	"permissions":["computer.control","filesystem.write"],
	"contributes":{"tools":[{"provider":"builtin:computer_use","names":["computer"]}]}}`

const connectorManifest = `{"schema_version":1,"id":"acme.connector","name":"Connector",
	"permissions":["network.access","channel.receive","channel.send","secrets.read","filesystem.write"],
	"config":{
		"bot_id":{"type":"string","required":true,"description":"机器人 ID"},
		"bot_secret":{"type":"string","required":true,"secret":true},
		"greeting":{"type":"string"}
	},
	"contributes":{"channels":[{"id":"acme","provider":"builtin:wecom"}]}}`

func newConfigFixture(t *testing.T) (*ConfigService, *memStore, model.Plugin) {
	t.Helper()
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "Connector", Manifest: model.JSON(connectorManifest)}
	store.plugins[plugin.UUID] = &plugin
	// 渠道插件的配置按连接存：夹具里放一个连接，下面的读写都落在它上面。
	store.addConnection(plugin.UUID, "c1", "机器人 A")
	return NewConfigService(store), store, plugin
}

// A stored secret is never handed back. The UI can show that one exists and
// offer to replace it, but cannot display or re-transmit the value.
func TestFieldsNeverReturnSecretValues(t *testing.T) {
	service, _, plugin := newConfigFixture(t)
	ctx := context.Background()
	if err := service.Set(ctx, plugin, "c1", "bot_secret", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(ctx, plugin, "c1", "greeting", "hello"); err != nil {
		t.Fatal(err)
	}

	fields, err := service.Fields(ctx, plugin, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %+v", fields)
	}
	for _, field := range fields {
		switch field.Key {
		case "bot_secret":
			if !field.Secret || !field.IsSet {
				t.Fatalf("secret field = %+v", field)
			}
			if field.Value != "" {
				t.Fatalf("a stored secret was returned to the caller: %+v", field)
			}
		case "greeting":
			if field.Secret || field.Value != "hello" {
				t.Fatalf("plain field = %+v", field)
			}
		case "bot_id":
			if !field.Required || field.IsSet {
				t.Fatalf("unset required field = %+v", field)
			}
		}
	}

	// The value must still be readable by the plugin implementation itself.
	values, err := service.Load(ctx, plugin.UUID, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if values.String("bot_secret") != "s3cr3t" {
		t.Fatal("the plugin cannot read its own secret")
	}
}

// Whether a value is secret comes from the manifest, so a caller cannot store
// a credential as a plain field and have it echoed back.
func TestSecretFlagComesFromTheManifest(t *testing.T) {
	service, store, plugin := newConfigFixture(t)
	if err := service.Set(context.Background(), plugin, "c1", "bot_secret", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	stored := store.config[configKey(plugin.UUID, "c1", "bot_secret")]
	if stored == nil || !stored.Secret {
		t.Fatalf("stored record = %+v", stored)
	}
}

func TestSetRejectsUndeclaredKeys(t *testing.T) {
	service, store, plugin := newConfigFixture(t)
	err := service.Set(context.Background(), plugin, "c1", "webhook_url", "https://example.invalid")
	if err == nil || !strings.Contains(err.Error(), "webhook_url") {
		t.Fatalf("an undeclared key was stored: %v", err)
	}
	if len(store.config) != 0 {
		t.Fatalf("config = %+v", store.config)
	}
}

func TestEnableRequiresDeclaredRequiredConfig(t *testing.T) {
	service, _, plugin := newConfigFixture(t)
	ctx := context.Background()

	// 渠道插件看连接：一个配齐了的连接都没有，就不能启用。
	err := service.ValidateEnable(ctx, plugin)
	if err == nil || !strings.Contains(err.Error(), "连接") {
		t.Fatalf("a plugin with no ready connection was enabled: %v", err)
	}
	if err := service.Set(ctx, plugin, "c1", "bot_id", "bot-1"); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateEnable(ctx, plugin); err == nil {
		t.Fatal("a partially configured plugin was enabled")
	}
	if err := service.Set(ctx, plugin, "c1", "bot_secret", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateEnable(ctx, plugin); err != nil {
		t.Fatalf("a fully configured plugin was refused: %v", err)
	}

	// An optional key stays optional.
	missing, err := service.MissingRequired(ctx, plugin, "c1")
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %+v err = %v", missing, err)
	}
}

// Clearing a required plugin-level key while the plugin runs would leave it
// enabled without what it declared it needs. A connection's key is different:
// clearing it only stops that connection; the others keep running.
func TestRequiredKeyCannotBeClearedWhileEnabled(t *testing.T) {
	store := newMemStore()
	service := NewConfigService(store)
	ctx := context.Background()
	plugin := model.Plugin{UUID: "p3", Name: "Research", Manifest: model.JSON(`{"schema_version":1,"id":"acme.research","name":"Research",
		"config":{"token":{"type":"string","required":true,"secret":true}}}`)}
	if err := service.Set(ctx, plugin, "", "token", "t"); err != nil {
		t.Fatal(err)
	}
	enabled := plugin
	enabled.Enabled = true
	if err := service.Delete(ctx, enabled, "", "token"); err == nil {
		t.Fatal("a required plugin-level key was cleared while the plugin was enabled")
	}
	if store.config[configKey(plugin.UUID, "", "token")] == nil {
		t.Fatal("the refused delete still removed the value")
	}
	if err := service.Delete(ctx, plugin, "", "token"); err != nil {
		t.Fatal(err)
	}

	connector, connectorStore, channelPlugin := newConfigFixture(t)
	if err := connector.Set(ctx, channelPlugin, "c1", "bot_id", "bot-1"); err != nil {
		t.Fatal(err)
	}
	running := channelPlugin
	running.Enabled = true
	if err := connector.Delete(ctx, running, "c1", "bot_id"); err != nil {
		t.Fatalf("clearing one connection's key should only stop that connection: %v", err)
	}
	if connectorStore.config[configKey(channelPlugin.UUID, "c1", "bot_id")] != nil {
		t.Fatal("the connection's value survived the delete")
	}
}

// Writing an empty value is how a form clears a field.
func TestEmptyValueClearsAKey(t *testing.T) {
	service, store, plugin := newConfigFixture(t)
	ctx := context.Background()
	if err := service.Set(ctx, plugin, "c1", "greeting", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := service.Set(ctx, plugin, "c1", "greeting", "   "); err != nil {
		t.Fatal(err)
	}
	if store.config[configKey(plugin.UUID, "c1", "greeting")] != nil {
		t.Fatalf("config = %+v", store.config)
	}
}

// A plugin that declares no configuration must not be blocked from enabling.
func TestPluginWithoutDeclaredConfigEnablesFreely(t *testing.T) {
	store := newMemStore()
	service := NewConfigService(store)
	plugin := model.Plugin{UUID: "p2", Name: "Computer Use", Manifest: model.JSON(computerManifest)}
	if err := service.ValidateEnable(context.Background(), plugin); err != nil {
		t.Fatal(err)
	}
	fields, err := service.Fields(context.Background(), plugin, "")
	if err != nil || len(fields) != 0 {
		t.Fatalf("fields = %+v err = %v", fields, err)
	}
}

// Secrets must not outlive the bundle they belong to.
func TestUninstallClearsStoredConfig(t *testing.T) {
	store := newMemStore()
	installer := NewInstaller(store, t.TempDir())
	ctx := context.Background()
	plugin, _, err := installer.Install(ctx, researchBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	store.config[configKey(plugin.UUID, "c1", "token")] = &model.PluginConfig{
		PluginUUID: plugin.UUID, Key: "token", Value: "s3cr3t", Secret: true,
	}
	if err := installer.Uninstall(ctx, *plugin); err != nil {
		t.Fatal(err)
	}
	if len(store.config) != 0 {
		t.Fatalf("a secret survived uninstall: %+v", store.config)
	}
}
