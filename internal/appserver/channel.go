package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/core"
	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
)

// bindingStore is the persistence a channel gateway needs.
type bindingStore interface {
	ListChannelBindings(ctx context.Context) ([]model.ChannelBinding, error)
	GetChannelBinding(ctx context.Context, pluginUUID, channelID, externalKey string) (*model.ChannelBinding, error)
	SaveChannelBinding(ctx context.Context, item *model.ChannelBinding) error
}

// ChannelGateway runs turns on behalf of external conversations.
//
// It is the only place an inbound message becomes a turn, and it enforces the
// two rules that make that safe: an unknown conversation is recorded for the
// user to authorize rather than served, and an authorized one runs under a
// restricted tool policy because its input is not the user speaking.
type ChannelGateway struct {
	service *Service
	store   bindingStore
}

func NewChannelGateway(service *Service, store bindingStore) *ChannelGateway {
	return &ChannelGateway{service: service, store: store}
}

// Submit implements plugin.Gateway.
func (g *ChannelGateway) Submit(ctx context.Context, pluginUUID string, message pluginpkg.Inbound, observe func(protocol.Event) error) error {
	channelID := strings.TrimSpace(message.ChannelID)
	externalKey := strings.TrimSpace(message.ExternalKey)
	if pluginUUID == "" || channelID == "" || externalKey == "" {
		return fmt.Errorf("inbound message is missing its plugin, channel or conversation identity")
	}
	binding, err := g.store.GetChannelBinding(ctx, pluginUUID, channelID, externalKey)
	if err != nil {
		return err
	}
	if binding == nil {
		// First contact from an unknown conversation: record the request so
		// the user can authorize it, and serve nothing.
		pending := &model.ChannelBinding{
			PluginUUID: pluginUUID, ChannelID: channelID, ExternalKey: externalKey,
			DisplayName: strings.TrimSpace(message.DisplayName), Allowed: false,
			LastMessage: time.Now(),
		}
		if err := g.store.SaveChannelBinding(ctx, pending); err != nil {
			return err
		}
		return pluginpkg.ErrBindingNotAllowed
	}
	if !binding.Allowed {
		binding.LastMessage = time.Now()
		if err := g.store.SaveChannelBinding(ctx, binding); err != nil {
			return err
		}
		return pluginpkg.ErrBindingNotAllowed
	}
	if binding.ProviderID == 0 || strings.TrimSpace(binding.ModelName) == "" {
		return fmt.Errorf("conversation %q has no model configured", bindingLabel(binding))
	}

	if strings.TrimSpace(binding.ThreadUUID) == "" {
		threadID, err := g.createThread(ctx, binding, message, observe)
		if err != nil {
			return err
		}
		binding.ThreadUUID = threadID
	}
	binding.LastMessage = time.Now()
	if err := g.store.SaveChannelBinding(ctx, binding); err != nil {
		return err
	}

	ctx = core.WithToolPolicy(ctx, toolPolicy(binding, channelID))
	return g.service.Handle(ctx, protocol.Command{
		Kind: protocol.CommandStartTurn, ThreadID: binding.ThreadUUID,
		Input: message.Text, Attachments: message.Attachments,
	}, observe)
}

func (g *ChannelGateway) createThread(ctx context.Context, binding *model.ChannelBinding, message pluginpkg.Inbound, observe func(protocol.Event) error) (string, error) {
	var created string
	sink := func(event protocol.Event) error {
		if event.Kind == protocol.EventThreadCreated {
			created = event.ThreadID
		}
		if observe == nil {
			return nil
		}
		return observe(event)
	}
	title := strings.TrimSpace(bindingLabel(binding))
	err := g.service.Handle(ctx, protocol.Command{
		Kind: protocol.CommandCreateThread, UserID: "local",
		ProviderID: binding.ProviderID, ModelName: binding.ModelName,
		Input: title,
	}, sink)
	if err != nil {
		return "", err
	}
	if created == "" {
		return "", fmt.Errorf("thread creation for %q reported no identifier", title)
	}
	_ = message
	return created, nil
}

// toolPolicy builds the restriction an inbound turn runs under.
func toolPolicy(binding *model.ChannelBinding, channelID string) core.ToolPolicy {
	allowed := make(map[string]bool)
	if len(binding.AllowedTools) > 0 {
		var names []string
		if err := json.Unmarshal(binding.AllowedTools, &names); err == nil {
			for _, name := range names {
				if name = strings.TrimSpace(name); name != "" {
					allowed[name] = true
				}
			}
		}
	}
	return core.ToolPolicy{
		Untrusted: true,
		Source:    "channel:" + channelID + "/" + binding.ExternalKey,
		Allowed:   allowed,
	}
}

func bindingLabel(binding *model.ChannelBinding) string {
	if name := strings.TrimSpace(binding.DisplayName); name != "" {
		return name
	}
	return binding.ChannelID + ":" + binding.ExternalKey
}
