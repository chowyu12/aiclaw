package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type deferredTestTransport struct{ calls atomic.Int32 }

func (d *deferredTestTransport) Call(context.Context, string, any) (json.RawMessage, error) {
	d.calls.Add(1)
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}
func (*deferredTestTransport) Notify(string, any)  {}
func (*deferredTestTransport) Close()              {}
func (*deferredTestTransport) Diagnostics() string { return "" }

func TestDeferredStartsOnceAndReleasesOnce(t *testing.T) {
	def := ToolDef{Name: "test", InputSchema: json.RawMessage(`{"type":"object"}`)}
	transport := &deferredTestTransport{}
	active := &Client{tools: []ToolDef{def}, version: 1, transport: transport}
	var starts, releases atomic.Int32
	lazy := NewDeferred([]ToolDef{def}, func(context.Context) (*Client, func(), error) {
		starts.Add(1)
		return active, func() { releases.Add(1) }, nil
	})
	if err := lazy.RefreshTools(context.Background()); err != nil || starts.Load() != 0 {
		t.Fatal("mount started process", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := lazy.CallToolExpected(context.Background(), def, json.RawMessage(`{}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if starts.Load() != 1 || transport.calls.Load() != 8 {
		t.Fatal(starts.Load(), transport.calls.Load())
	}
	lazy.Close()
	lazy.Close()
	if releases.Load() != 1 {
		t.Fatal("lease release", releases.Load())
	}
	if _, err := lazy.CallToolExpected(context.Background(), def, nil); err == nil {
		t.Fatal("closed client executed")
	}
}

func TestDeferredRejectsChangedDeclarationEvenAfterCatalogRefresh(t *testing.T) {
	readOnly := true
	old := ToolDef{Name: "test", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: &ToolAnnotations{ReadOnlyHint: &readOnly}}
	current := old
	current.Annotations = nil
	transport := &deferredTestTransport{}
	active := &Client{tools: []ToolDef{current}, version: 1, transport: transport}
	lazy := NewDeferred([]ToolDef{old}, func(context.Context) (*Client, func(), error) { return active, func() {}, nil })
	defer lazy.Close()
	for i := 0; i < 2; i++ {
		if _, err := lazy.CallToolExpected(context.Background(), old, nil); err == nil {
			t.Fatal("old approval admitted new side effects")
		}
	}
	if transport.calls.Load() != 0 {
		t.Fatal("changed tool executed")
	}
	if lazy.Tools()[0].ReadOnly() {
		t.Fatal("new declaration not published")
	}
	if _, err := lazy.CallToolExpected(context.Background(), current, nil); err != nil {
		t.Fatal(err)
	}
}
func TestDeferredFailedStartCanRetryAndCancellationPropagates(t *testing.T) {
	def := ToolDef{Name: "test"}
	var starts int
	lazy := NewDeferred([]ToolDef{def}, func(ctx context.Context) (*Client, func(), error) {
		starts++
		if starts == 1 {
			return nil, nil, errors.New("unavailable")
		}
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	defer lazy.Close()
	if _, err := lazy.CallToolExpected(context.Background(), def, nil); err == nil {
		t.Fatal("startup failure swallowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lazy.CallToolExpected(ctx, def, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestActiveClientAlsoRejectsOutdatedApprovedDeclaration(t *testing.T) {
	approved := ToolDef{Name: "test", Description: "old"}
	current := approved
	current.Description = "changed"
	transport := &deferredTestTransport{}
	client := &Client{tools: []ToolDef{current}, version: 1, transport: transport}
	if _, err := client.CallToolExpected(context.Background(), approved, nil); err == nil || transport.calls.Load() != 0 {
		t.Fatal("active client admitted outdated handler", err)
	}
	if _, err := client.CallToolExpected(context.Background(), current, nil); err != nil || transport.calls.Load() != 1 {
		t.Fatal(err)
	}
}
