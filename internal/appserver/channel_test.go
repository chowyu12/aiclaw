package appserver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/core"
	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

// policySampler records the tool policy each turn ran under, by asking the
// dispatcher what it would advertise.
type policySampler struct {
	contexts []context.Context
	replies  []string
}

func (s *policySampler) Sample(ctx context.Context, request SamplingRequestAlias, emit func(string) error) (core.SamplingResult, error) {
	s.contexts = append(s.contexts, ctx)
	for _, message := range request.Messages {
		if message.Role == "system" {
			s.replies = append(s.replies, message.Content)
		}
	}
	if err := emit("ack"); err != nil {
		return core.SamplingResult{}, err
	}
	return core.SamplingResult{Text: "ack"}, nil
}

// SamplingRequestAlias keeps the sampler signature readable.
type SamplingRequestAlias = core.SamplingRequest

func newGatewayFixture(t *testing.T) (context.Context, *gormstore.GormStore, *ChannelGateway, *policySampler) {
	t.Helper()
	ctx := context.Background()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "channel.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sampler := &policySampler{}
	service := New(store, sampler, core.NewLocalToolDispatcher(store))
	return ctx, store, NewChannelGateway(service, store), sampler
}

func inbound() pluginpkg.Inbound {
	return pluginpkg.Inbound{
		ChannelID: "wecom", ExternalKey: "group-1", DisplayName: "Ops group",
		SenderID: "user-7", Text: "please summarize today's alerts",
	}
}

// Receiving a message must never be enough to start a turn: an unknown
// conversation is recorded for the user to authorize, and served nothing.
func TestFirstContactIsRecordedAndRefused(t *testing.T) {
	ctx, store, gateway, sampler := newGatewayFixture(t)

	err := gateway.Submit(ctx, "plugin-1", inbound(), nil)
	if !errors.Is(err, pluginpkg.ErrBindingNotAllowed) {
		t.Fatalf("first contact was served: %v", err)
	}
	if len(sampler.contexts) != 0 {
		t.Fatal("an unauthorized message reached the model")
	}
	bindings, err := store.ListChannelBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("bindings = %+v", bindings)
	}
	if bindings[0].Allowed || bindings[0].ThreadUUID != "" {
		t.Fatalf("first contact created an authorized binding: %+v", bindings[0])
	}
	if bindings[0].DisplayName != "Ops group" {
		t.Fatalf("display name was lost: %+v", bindings[0])
	}

	// Repeating must not accumulate rows, and must stay refused.
	if err := gateway.Submit(ctx, "plugin-1", inbound(), nil); !errors.Is(err, pluginpkg.ErrBindingNotAllowed) {
		t.Fatalf("a repeated message was served: %v", err)
	}
	bindings, _ = store.ListChannelBindings(ctx)
	if len(bindings) != 1 {
		t.Fatalf("repeated contact duplicated the binding: %+v", bindings)
	}
}

func TestAuthorizedBindingRunsATurnUnderAnUntrustedPolicy(t *testing.T) {
	ctx, store, gateway, sampler := newGatewayFixture(t)
	if err := gateway.Submit(ctx, "plugin-1", inbound(), nil); !errors.Is(err, pluginpkg.ErrBindingNotAllowed) {
		t.Fatal(err)
	}
	binding, err := store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	if err != nil {
		t.Fatal(err)
	}
	binding.Allowed, binding.ProviderID, binding.ModelName = true, 1, "test-model"
	binding.AllowedTools = model.JSON(`["write"]`)
	if err := store.SaveChannelBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}

	var events []protocol.Event
	if err := gateway.Submit(ctx, "plugin-1", inbound(), func(event protocol.Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sampler.contexts) == 0 {
		t.Fatal("an authorized message did not reach the model")
	}
	policy := core.ToolPolicyFromContext(sampler.contexts[len(sampler.contexts)-1])
	if !policy.Untrusted {
		t.Fatal("an inbound turn ran as a trusted local turn")
	}
	if policy.Source != "channel:wecom/group-1" {
		t.Fatalf("policy source = %q", policy.Source)
	}
	if !policy.Allowed["write"] || policy.Allowed["exec"] {
		t.Fatalf("granted tools = %+v", policy.Allowed)
	}
	if policy.Permits("exec") {
		t.Fatal("an ungranted acting tool was permitted")
	}

	// The thread must be recorded on the binding so the conversation resumes
	// into the same thread rather than starting over.
	stored, _ := store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	if stored.ThreadUUID == "" {
		t.Fatal("no thread was bound to the conversation")
	}
	completed := false
	for _, event := range events {
		completed = completed || event.Kind == protocol.EventTurnCompleted
	}
	if !completed {
		t.Fatalf("the channel never saw the turn complete: %+v", events)
	}

	// A second message continues the same thread.
	if err := gateway.Submit(ctx, "plugin-1", inbound(), nil); err != nil {
		t.Fatal(err)
	}
	again, _ := store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	if again.ThreadUUID != stored.ThreadUUID {
		t.Fatalf("a follow-up message started a new thread: %q -> %q", stored.ThreadUUID, again.ThreadUUID)
	}
}

// Revoking has to take effect on the next message, not on the next restart.
func TestRevokedBindingStopsBeingServed(t *testing.T) {
	ctx, store, gateway, _ := newGatewayFixture(t)
	_ = gateway.Submit(ctx, "plugin-1", inbound(), nil)
	binding, _ := store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	binding.Allowed, binding.ProviderID, binding.ModelName = true, 1, "test-model"
	if err := store.SaveChannelBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Submit(ctx, "plugin-1", inbound(), nil); err != nil {
		t.Fatal(err)
	}

	binding, _ = store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	binding.Allowed = false
	if err := store.SaveChannelBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Submit(ctx, "plugin-1", inbound(), nil); !errors.Is(err, pluginpkg.ErrBindingNotAllowed) {
		t.Fatalf("a revoked conversation was still served: %v", err)
	}
}

func TestIncompleteInboundIsRejected(t *testing.T) {
	ctx, _, gateway, _ := newGatewayFixture(t)
	for _, message := range []pluginpkg.Inbound{
		{ExternalKey: "group-1"},
		{ChannelID: "wecom"},
	} {
		if err := gateway.Submit(ctx, "plugin-1", message, nil); err == nil {
			t.Fatalf("an inbound message without identity was accepted: %+v", message)
		}
	}
	if err := gateway.Submit(ctx, "", inbound(), nil); err == nil {
		t.Fatal("an inbound message without a plugin was accepted")
	}
}

// An authorized binding whose model was never chosen must fail loudly rather
// than start a turn against provider zero.
func TestAuthorizedBindingWithoutAModelIsRejected(t *testing.T) {
	ctx, store, gateway, _ := newGatewayFixture(t)
	_ = gateway.Submit(ctx, "plugin-1", inbound(), nil)
	binding, _ := store.GetChannelBinding(ctx, "plugin-1", "wecom", "group-1")
	binding.Allowed = true
	if err := store.SaveChannelBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	err := gateway.Submit(ctx, "plugin-1", inbound(), nil)
	if err == nil || !strings.Contains(err.Error(), "no model configured") {
		t.Fatalf("a binding without a model started a turn: %v", err)
	}
}
