package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
)

// NewDeferred advertises a previously verified catalog without starting a server.
// A first call revalidates the exact declaration before any operation is sent.
func NewDeferred(defs []ToolDef, start func(context.Context) (*Client, func(), error)) *Client {
	life, cancel := context.WithCancel(context.Background())
	c := &Client{tools: append([]ToolDef(nil), defs...), version: 1}
	c.transport = &deferredTransport{owner: c, start: start, life: life, cancel: cancel}
	return c
}
func (c *Client) Deferred() bool { _, ok := c.transport.(*deferredTransport); return ok }

type deferredTransport struct {
	mu      sync.Mutex
	owner   *Client
	start   func(context.Context) (*Client, func(), error)
	active  *Client
	release func()
	life    context.Context
	cancel  context.CancelFunc
}

func (d *deferredTransport) catalog(active *Client) {
	defs, _ := active.Catalog()
	d.owner.toolsMu.Lock()
	defer d.owner.toolsMu.Unlock()
	if !reflect.DeepEqual(d.owner.tools, defs) {
		d.owner.tools = defs
		d.owner.version++
	}
}
func (d *deferredTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(d.life, cancel)
	defer stop()
	if d.life.Err() != nil {
		return nil, errors.New("MCP session was closed")
	}
	// The handler supplies the declaration admitted by its registry. A later
	// catalog refresh must not silently upgrade an already approved call.
	old, _ := d.owner.Catalog()
	if expected, ok := ctx.Value(expectedToolKey{}).(ToolDef); ok {
		old = []ToolDef{expected}
	}
	d.mu.Lock()
	if d.active == nil {
		active, release, err := d.start(callCtx)
		if err != nil {
			d.mu.Unlock()
			return nil, err
		}
		d.active, d.release = active, release
	}
	active := d.active
	d.mu.Unlock()
	if err := active.RefreshTools(callCtx); err != nil {
		return nil, err
	}
	d.catalog(active)
	if method == "tools/call" {
		name, _ := params.(map[string]any)["name"].(string)
		var expected, current *ToolDef
		for i := range old {
			if old[i].Name == name {
				expected = &old[i]
				break
			}
		}
		defs, _ := active.Catalog()
		for i := range defs {
			if defs[i].Name == name {
				current = &defs[i]
				break
			}
		}
		if expected == nil || current == nil || !reflect.DeepEqual(expected, current) {
			return nil, errors.New("MCP tool definition changed; inspect the refreshed catalog and retry explicitly")
		}
	}
	return active.transport.Call(callCtx, method, params)
}
func (d *deferredTransport) refresh(ctx context.Context) error {
	d.mu.Lock()
	active := d.active
	d.mu.Unlock()
	if active == nil {
		return nil
	}
	if err := active.RefreshTools(ctx); err != nil {
		return err
	}
	d.catalog(active)
	return nil
}
func (d *deferredTransport) Notify(method string, params any) {
	d.mu.Lock()
	active := d.active
	d.mu.Unlock()
	if active != nil {
		active.transport.Notify(method, params)
	}
}
func (d *deferredTransport) Diagnostics() string { return "Deferred MCP server" }
func (d *deferredTransport) Close() {
	d.cancel()
	d.mu.Lock()
	release := d.release
	d.release = nil
	d.mu.Unlock()
	if release != nil {
		release()
	}
}

// CallToolExpected binds execution to the declaration used for approval.
func (c *Client) CallToolExpected(ctx context.Context, expected ToolDef, args json.RawMessage) (string, error) {
	if !c.Deferred() {
		if err := c.RefreshTools(ctx); err != nil {
			return "", err
		}
		defs, _ := c.Catalog()
		matched := false
		for _, def := range defs {
			if reflect.DeepEqual(def, expected) {
				matched = true
				break
			}
		}
		if !matched {
			return "", errors.New("MCP tool definition changed; inspect the refreshed catalog and retry explicitly")
		}
	}
	return c.CallTool(context.WithValue(ctx, expectedToolKey{}, expected), expected.Name, args)
}

type expectedToolKey struct{}
