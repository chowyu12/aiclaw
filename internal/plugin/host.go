package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
)

const (
	// restartBaseDelay and restartMaxDelay bound the backoff between restarts
	// of a channel that keeps failing.
	restartBaseDelay = time.Second
	restartMaxDelay  = time.Minute
	// maxRestartAttempts stops an endlessly failing channel instead of
	// reconnecting forever against, say, a revoked credential.
	maxRestartAttempts = 8
	// healthyRun is how long an attempt must have stayed connected for the
	// failure that ends it to count as a fresh incident rather than the next
	// step of an ongoing one. Without this a channel that reconnects fine
	// after every network blip still burns through its attempts over days
	// and ends up permanently failed.
	healthyRun = 30 * time.Second
)

// Host supervises the long-running channels that enabled plugins contribute.
//
// The tool axis is rebuilt every turn, so it needs no lifecycle. A channel
// holds a connection, so it does: Host starts one goroutine per channel,
// restarts it with backoff when it fails, and stops it when its plugin is
// disabled or the application exits.
type Host struct {
	factories map[string]ChannelFactory
	gateway   Gateway
	config    *ConfigService
	log       func(format string, args ...any)

	mu      sync.Mutex
	running map[string]*channelRun
	wg      sync.WaitGroup
}

type channelRun struct {
	cancel context.CancelFunc
	status *statusHolder
}

type HostOption func(*Host)

// WithHostLogger records channel lifecycle messages. Configuration values are
// never passed to it.
func WithHostLogger(log func(format string, args ...any)) HostOption {
	return func(h *Host) { h.log = log }
}

// NewHost binds channel implementations to their declarations. A factory for
// an undeclared provider is a wiring mistake and fails here.
func NewHost(gateway Gateway, config *ConfigService, factories map[string]ChannelFactory, options ...HostOption) (*Host, error) {
	host := &Host{
		factories: make(map[string]ChannelFactory, len(factories)),
		gateway:   gateway, config: config,
		log:     func(string, ...any) {},
		running: make(map[string]*channelRun),
	}
	for name, factory := range factories {
		name = strings.TrimSpace(name)
		if _, ok := LookupProvider(name); !ok {
			return nil, fmt.Errorf("provider %q is not declared in the plugin provider registry", name)
		}
		if factory == nil {
			return nil, fmt.Errorf("provider %q has a nil channel factory", name)
		}
		host.factories[name] = factory
	}
	for _, option := range options {
		option(host)
	}
	return host, nil
}

// Sync starts the channels of enabled plugins and stops everything else. It is
// called at startup and whenever a plugin is toggled, and is safe to call
// repeatedly: a channel already running is left alone.
func (h *Host) Sync(ctx context.Context, plugins []model.Plugin) error {
	wanted, err := h.desired(plugins)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, run := range h.running {
		if _, keep := wanted[key]; !keep {
			run.cancel()
			delete(h.running, key)
		}
	}
	keys := make([]string, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, alive := h.running[key]; alive {
			continue
		}
		h.start(ctx, key, wanted[key])
	}
	return nil
}

// desiredChannel is one channel that should be running.
type desiredChannel struct {
	plugin       model.Plugin
	contribution ChannelContribution
	factory      ChannelFactory
}

func (h *Host) desired(plugins []model.Plugin) (map[string]desiredChannel, error) {
	wanted := make(map[string]desiredChannel)
	for _, plugin := range plugins {
		if !plugin.Enabled || len(plugin.Manifest) == 0 {
			continue
		}
		manifest := &Manifest{}
		if err := decodeManifest(plugin.Manifest, manifest); err != nil {
			return nil, err
		}
		if len(manifest.Contributes.Channels) == 0 {
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
		for _, contribution := range manifest.Contributes.Channels {
			provider, ok := LookupProvider(contribution.Provider)
			if !ok {
				continue
			}
			factory, ok := h.factories[strings.TrimSpace(contribution.Provider)]
			if !ok {
				// Declared but not implemented in this build.
				continue
			}
			// Receiving is the permission that lets outside messages start a
			// turn; without it the connector must not run at all.
			if !granted[skills.PermissionChannelReceive] || !covers(granted, provider.Requires) {
				continue
			}
			wanted[channelKey(plugin.UUID, contribution.ID)] = desiredChannel{
				plugin: plugin, contribution: contribution, factory: factory,
			}
		}
	}
	return wanted, nil
}

// start launches one supervised channel. The caller holds h.mu.
func (h *Host) start(ctx context.Context, key string, wanted desiredChannel) {
	runCtx, cancel := context.WithCancel(ctx)
	holder := &statusHolder{status: ChannelStatus{
		PluginUUID: wanted.plugin.UUID, PluginName: wanted.plugin.Name,
		ChannelID: wanted.contribution.ID, DisplayName: wanted.contribution.DisplayName,
		State: ChannelStarting,
	}}
	h.running[key] = &channelRun{cancel: cancel, status: holder}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer cancel()
		h.supervise(runCtx, wanted, holder)
	}()
}

func (h *Host) supervise(ctx context.Context, wanted desiredChannel, holder *statusHolder) {
	delay := restartBaseDelay
	for attempt := 0; attempt <= maxRestartAttempts; attempt++ {
		if ctx.Err() != nil {
			holder.set(func(s *ChannelStatus) { s.State = ChannelStopped })
			return
		}
		// Configuration is re-read on every attempt so rotating a credential
		// takes effect on the next reconnect instead of needing a restart.
		values, err := h.config.Load(ctx, wanted.plugin.UUID)
		started := time.Now()
		if err == nil {
			holder.set(func(s *ChannelStatus) {
				s.State, s.StartedAt, s.LastError = ChannelRunning, time.Now(), ""
				s.Attempts = attempt
			})
			if attempt > 0 {
				h.log("channel %s/%s reconnecting (attempt %d)", wanted.plugin.Name, wanted.contribution.ID, attempt)
			}
			err = h.run(ctx, wanted, values)
		}
		ran := time.Since(started)
		if err != nil && ran >= healthyRun && attempt > 0 {
			// It was up for a while: this is a new incident, not the previous
			// one continuing. Start the backoff over so a long-lived channel
			// never exhausts its attempts through unrelated blips.
			h.log("channel %s/%s ran %s before failing; resetting backoff", wanted.plugin.Name, wanted.contribution.ID, ran.Round(time.Second))
			attempt, delay = 0, restartBaseDelay
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			holder.set(func(s *ChannelStatus) { s.State = ChannelStopped })
			return
		}
		if err == nil {
			// A channel that returns without error has finished on purpose.
			holder.set(func(s *ChannelStatus) { s.State = ChannelStopped })
			return
		}
		message := err.Error()
		h.log("channel %s/%s failed after %s: %v", wanted.plugin.Name, wanted.contribution.ID, ran.Round(time.Second), err)
		if attempt == maxRestartAttempts {
			holder.set(func(s *ChannelStatus) {
				s.State, s.LastError, s.Attempts = ChannelFailed, message, attempt
			})
			return
		}
		holder.set(func(s *ChannelStatus) {
			s.State, s.LastError, s.Attempts = ChannelRetrying, message, attempt+1
		})
		select {
		case <-ctx.Done():
			holder.set(func(s *ChannelStatus) { s.State = ChannelStopped })
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > restartMaxDelay {
			delay = restartMaxDelay
		}
	}
}

// run isolates one channel attempt, turning a panic in third-party-shaped code
// into a restartable error instead of taking the application down.
func (h *Host) run(ctx context.Context, wanted desiredChannel, values Values) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("channel panicked: %v", recovered)
		}
	}()
	channel := wanted.factory()
	return channel.Run(ctx, ChannelDeps{
		PluginUUID: wanted.plugin.UUID,
		Config:     values,
		Gateway:    h.gateway,
		Log:        h.log,
	})
}

// Status reports every supervised channel, newest state first by name.
func (h *Host) Status() []ChannelStatus {
	h.mu.Lock()
	runs := make([]*channelRun, 0, len(h.running))
	for _, run := range h.running {
		runs = append(runs, run)
	}
	h.mu.Unlock()
	result := make([]ChannelStatus, 0, len(runs))
	for _, run := range runs {
		result = append(result, run.status.get())
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].PluginName != result[j].PluginName {
			return result[i].PluginName < result[j].PluginName
		}
		return result[i].ChannelID < result[j].ChannelID
	})
	return result
}

// Stop cancels every channel and waits for them to finish.
func (h *Host) Stop() {
	h.mu.Lock()
	for key, run := range h.running {
		run.cancel()
		delete(h.running, key)
	}
	h.mu.Unlock()
	h.wg.Wait()
}
