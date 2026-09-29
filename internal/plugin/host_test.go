package plugin

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

const wecomManifest = `{"schema_version":1,"id":"aiclaw.wecom","name":"WeCom",
	"permissions":["network.access","channel.receive","channel.send","secrets.read","filesystem.write"],
	"config":{"bot_secret":{"type":"string","required":true,"secret":true}},
	"contributes":{"channels":[{"id":"wecom","provider":"builtin:wecom","display_name":"企业微信"}]}}`

// fakeChannel reports what the host did to it.
type fakeChannel struct {
	mu       sync.Mutex
	starts   int
	secrets  []string
	failWith error
	// blockUntilCancelled keeps Run alive so the host sees a healthy channel.
	blockUntilCancelled bool
	started             chan struct{}
}

func (c *fakeChannel) ID() string { return "wecom" }

func (c *fakeChannel) Run(ctx context.Context, deps ChannelDeps) error {
	c.mu.Lock()
	c.starts++
	c.secrets = append(c.secrets, deps.Config.String("bot_secret"))
	started := c.started
	failure := c.failWith
	block := c.blockUntilCancelled
	c.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if failure != nil {
		return failure
	}
	if !block {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (c *fakeChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts
}

type recordingGateway struct{}

func (recordingGateway) Submit(context.Context, string, Inbound, func(protocol.Event) error) error {
	return nil
}

func newHostFixture(t *testing.T, channel *fakeChannel) (*Host, *memStore, model.Plugin) {
	t.Helper()
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "WeCom", Manifest: model.JSON(wecomManifest), Enabled: true}
	store.plugins[plugin.UUID] = &plugin
	// 一个配齐了的连接：没有连接（或没配齐）的渠道插件不会启动。
	store.addConnection(plugin.UUID, "c1", "机器人 A")
	store.config[configKey(plugin.UUID, "c1", "bot_secret")] = &model.PluginConfig{
		PluginUUID: plugin.UUID, ConnectionID: "c1", Key: "bot_secret", Value: "initial", Secret: true,
	}
	host, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:wecom": func() Channel { return channel }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	return host, store, plugin
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}

func TestNewHostRejectsUndeclaredProviders(t *testing.T) {
	store := newMemStore()
	if _, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:nope": func() Channel { return &fakeChannel{} }}); err == nil {
		t.Fatal("a factory for an undeclared provider was bound")
	}
	if _, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:wecom": nil}); err == nil {
		t.Fatal("a nil factory was bound")
	}
}

// A channel holds a connection, so enabling and disabling its plugin has to
// start and stop it now rather than at the next turn.
func TestHostStartsAndStopsWithPluginEnablement(t *testing.T) {
	channel := &fakeChannel{blockUntilCancelled: true, started: make(chan struct{}, 1)}
	host, store, plugin := newHostFixture(t, channel)
	ctx := context.Background()

	if err := host.Sync(ctx, []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return channel.count() == 1 }, "the channel never started")
	waitFor(t, func() bool {
		status := host.Status()
		return len(status) == 1 && status[0].State == ChannelRunning
	}, "the channel never reported running")

	// Syncing again must not start a second copy.
	if err := host.Sync(ctx, []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	if channel.count() != 1 {
		t.Fatalf("channel starts = %d after a repeated sync", channel.count())
	}

	disabled := plugin
	disabled.Enabled = false
	store.plugins[plugin.UUID] = &disabled
	if err := host.Sync(ctx, []model.Plugin{disabled}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(host.Status()) == 0 }, "the channel was not stopped")
}

// A channel that cannot run must not be retried forever against, say, a
// revoked credential.
func TestHostRetriesWithBackoffThenGivesUp(t *testing.T) {
	channel := &fakeChannel{failWith: fmt.Errorf("authentication rejected")}
	host, _, plugin := newHostFixture(t, channel)

	if err := host.Sync(context.Background(), []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		status := host.Status()
		return len(status) == 1 && status[0].State == ChannelRetrying && status[0].Attempts > 0
	}, "a failing channel never entered retry")

	status := host.Status()[0]
	if status.LastError != "authentication rejected" {
		t.Fatalf("last error = %q", status.LastError)
	}
	// The channel restarted at least once, and the failure is visible rather
	// than swallowed.
	waitFor(t, func() bool { return channel.count() >= 2 }, "a failing channel was never retried")
}

// A panic in connector code must not take the application down with it.
func TestHostTurnsAPanicIntoARestart(t *testing.T) {
	channel := &panickingChannel{}
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "WeCom", Manifest: model.JSON(wecomManifest), Enabled: true}
	store.plugins[plugin.UUID] = &plugin
	store.addConnection(plugin.UUID, "c1", "机器人 A")
	store.config[configKey(plugin.UUID, "c1", "bot_secret")] = &model.PluginConfig{
		PluginUUID: plugin.UUID, ConnectionID: "c1", Key: "bot_secret", Value: "s", Secret: true,
	}
	host, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:wecom": func() Channel { return channel }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	if err := host.Sync(context.Background(), []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		status := host.Status()
		return len(status) == 1 && status[0].State == ChannelRetrying &&
			status[0].LastError != "" && status[0].Attempts > 0
	}, "a panicking channel did not become a restartable failure")
}

type panickingChannel struct{}

func (panickingChannel) ID() string { return "wecom" }

func (panickingChannel) Run(context.Context, ChannelDeps) error {
	panic("connector exploded")
}

// Configuration is re-read per attempt so rotating a credential takes effect
// on the next reconnect.
func TestChannelReceivesConfigurationOnEveryAttempt(t *testing.T) {
	channel := &fakeChannel{failWith: fmt.Errorf("retry me")}
	host, store, plugin := newHostFixture(t, channel)
	service := NewConfigService(store)
	ctx := context.Background()
	if err := service.Set(ctx, plugin, "c1", "bot_secret", "first"); err != nil {
		t.Fatal(err)
	}
	if err := host.Sync(ctx, []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return channel.count() >= 1 }, "the channel never started")

	if err := service.Set(ctx, plugin, "c1", "bot_secret", "rotated"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, secret := range channel.secrets {
			if secret == "rotated" {
				return true
			}
		}
		return false
	}, "a rotated credential never reached the channel")
}

// A bundle that never declared channel.receive must not be able to run a
// connector, whatever its manifest contributes.
func TestChannelWithoutReceivePermissionNeverStarts(t *testing.T) {
	channel := &fakeChannel{blockUntilCancelled: true}
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "WeCom", Enabled: true, Manifest: model.JSON(
		`{"schema_version":1,"id":"aiclaw.wecom","name":"WeCom","permissions":["network.access"],
			"contributes":{"channels":[{"id":"wecom","provider":"builtin:wecom"}]}}`)}
	host, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:wecom": func() Channel { return channel }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	if err := host.Sync(context.Background(), []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if channel.count() != 0 || len(host.Status()) != 0 {
		t.Fatalf("a connector ran without channel.receive: starts=%d status=%+v", channel.count(), host.Status())
	}
}

// A declared provider this build does not implement is simply not run.
func TestUnimplementedChannelProviderIsSkipped(t *testing.T) {
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "WeCom", Manifest: model.JSON(wecomManifest), Enabled: true}
	host, err := NewHost(recordingGateway{}, NewConfigService(store), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	if err := host.Sync(context.Background(), []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	if len(host.Status()) != 0 {
		t.Fatalf("status = %+v", host.Status())
	}
}

// connectionChannel 记下每次启动时拿到的连接与凭据。
type connectionChannel struct {
	mu     sync.Mutex
	starts map[string]int
	seen   map[string]string
}

func (c *connectionChannel) ID() string { return "wecom" }

func (c *connectionChannel) Run(ctx context.Context, deps ChannelDeps) error {
	c.mu.Lock()
	c.starts[deps.ConnectionID]++
	c.seen[deps.ConnectionID] = deps.Config.String("bot_secret")
	c.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (c *connectionChannel) count(connectionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts[connectionID]
}

// 每个连接（一个企微机器人、一个微信号）各跑一个实例、各读自己的凭据；只重启一个时
// 别的不受影响；没配齐的连接不启动。
func TestEachConnectionRunsSeparately(t *testing.T) {
	channel := &connectionChannel{starts: map[string]int{}, seen: map[string]string{}}
	store := newMemStore()
	plugin := model.Plugin{UUID: "p1", Name: "WeCom", Manifest: model.JSON(wecomManifest), Enabled: true}
	store.plugins[plugin.UUID] = &plugin
	for _, connection := range []struct{ id, secret string }{{"a", "secret-a"}, {"b", "secret-b"}, {"c", ""}} {
		store.addConnection(plugin.UUID, connection.id, "机器人 "+connection.id)
		if connection.secret != "" {
			store.config[configKey(plugin.UUID, connection.id, "bot_secret")] = &model.PluginConfig{
				PluginUUID: plugin.UUID, ConnectionID: connection.id, Key: "bot_secret", Value: connection.secret, Secret: true,
			}
		}
	}
	host, err := NewHost(recordingGateway{}, NewConfigService(store),
		map[string]ChannelFactory{"builtin:wecom": func() Channel { return channel }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Stop)
	ctx := context.Background()
	if err := host.Sync(ctx, []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return channel.count("a") == 1 && channel.count("b") == 1 }, "both ready connections should start")
	channel.mu.Lock()
	if channel.seen["a"] != "secret-a" || channel.seen["b"] != "secret-b" {
		t.Errorf("each connection must read its own credentials: %v", channel.seen)
	}
	channel.mu.Unlock()
	if channel.count("c") != 0 || len(host.Status()) != 2 {
		t.Errorf("an unconfigured connection must not start: starts=%d status=%d", channel.count("c"), len(host.Status()))
	}

	host.RestartConnection("a")
	if err := host.Sync(ctx, []model.Plugin{plugin}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return channel.count("a") == 2 }, "the restarted connection should start again")
	if channel.count("b") != 1 {
		t.Errorf("restarting one connection must leave the others alone: b started %d times", channel.count("b"))
	}
	for _, status := range host.Status() {
		if status.ConnectionID != "a" && status.ConnectionID != "b" {
			t.Errorf("status must carry its connection: %+v", status)
		}
	}
}
