package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogRefreshPreservesSnapshotsAndStore(t *testing.T) {
	r := New(nil, DefaultLimits())
	if _, err := r.Run(context.Background(), `store('rows',{count:7});`); err != nil {
		t.Fatal(err)
	}
	r.UpdateTools(append([]Tool{{Name: "hidden_query", Ident: "hidden_query", Description: "Search invoices", Schema: json.RawMessage(`{"type":"object","properties":{"invoiceId":{"type":["string","null"]}},"required":["invoiceId"]}`)}}, r.DiscoveryTools()...))
	out, err := r.Run(context.Background(), `const rows=load('rows');rows.count=99;const d=await tools.tool_describe({name:'hidden_query'}); return [load('rows').count,d.tools[0].inputSchema.required[0],d.tools[0].declaration.includes('null')].join(',');`)
	if err != nil || out != "7,invoiceId,true" {
		t.Fatalf("%s %v", out, err)
	}
	out, err = r.Run(context.Background(), `return (await tools.tool_search({query:'invoices'})).tools[0].name;`)
	if err != nil || out != "hidden_query" {
		t.Fatalf("discovery=%q %v", out, err)
	}
	// A running cell retains its admitted catalog even when a later sampling boundary updates it.
	ctx := r.snapshot(context.Background())
	r.UpdateTools(r.DiscoveryTools())
	out, err = r.Run(ctx, `return (await tools.tool_describe({name:'hidden_query'})).tools[0].name;`)
	if err != nil || out != "hidden_query" {
		t.Fatalf("admitted catalog changed: %q %v", out, err)
	}
	_, err = r.Run(context.Background(), `await tools.tool_describe({name:'hidden_query'});`)
	if err == nil {
		t.Fatal("removed tool remained discoverable for new cells")
	}
}

func TestStoreLimitsAndExitCannotBeForged(t *testing.T) {
	r := New(nil, DefaultLimits())
	for _, source := range []string{`store('x','a'.repeat(1024*1024));`, `store('x',()=>42);`, `const c={};c.self=c;store('x',c);`, `throw new Error('codemode: exit');`} {
		if _, err := r.Run(context.Background(), source); err == nil {
			t.Fatalf("unexpected success: %s", source)
		}
	}
	out, err := r.Run(context.Background(), `text('before');exit();text('after');`)
	if err != nil || out != "before" {
		t.Fatalf("real exit failed: %q %v", out, err)
	}
	for i := 0; i < maxStoreKeys; i++ {
		if err := r.put(strings.Repeat("x", i+1), json.RawMessage(`0`)); err != nil && i < 256 {
			t.Fatal(err)
		}
	}
	if err := r.put("new", json.RawMessage(`0`)); err == nil {
		t.Fatal("store key quota missing")
	}
}

func TestYieldWaitReturnsOnlyNewOutputAndNeverReplays(t *testing.T) {
	r := New(nil, DefaultLimits())
	var calls atomic.Int32
	release := make(chan struct{})
	r.UpdateTools([]Tool{{Name: "read", Ident: "read", ParallelSafe: true, Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
		calls.Add(1)
		select {
		case <-release:
			return "result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}})
	cells := NewCells(r)
	defer cells.Close()
	first, err := cells.Start(context.Background(), context.Background(), `text('first');yield_control();return await tools.read({});`, 1000, 1024)
	if err != nil || !first.Running || first.Text != "first" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	empty, err := cells.Wait(context.Background(), first.ID, 1, 1024, false)
	if err != nil || !empty.Running || empty.Text != "" {
		t.Fatalf("repeated output=%+v %v", empty, err)
	}
	close(release)
	last, err := cells.Wait(context.Background(), first.ID, 1000, 1024, false)
	if err != nil || last.Error != nil || last.Running || last.Text != "result" || calls.Load() != 1 {
		t.Fatalf("last=%+v calls=%d %v", last, calls.Load(), err)
	}
	if _, err := cells.Wait(context.Background(), first.ID, 0, 1024, false); err == nil {
		t.Fatal("collected cell reopened")
	}
}

func TestTerminateAndTurnCancellationStopLaterSideEffects(t *testing.T) {
	for _, terminate := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var later atomic.Int32
		entered := make(chan struct{})
		r := New([]Tool{{Name: "blocked", Ident: "blocked", Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
			close(entered)
			<-ctx.Done()
			return "", ctx.Err()
		}}, {Name: "later", Ident: "later", Call: func(context.Context, json.RawMessage) (string, error) { later.Add(1); return "", nil }}}, DefaultLimits())
		cells := NewCells(r)
		first, err := cells.Start(context.Background(), ctx, `try{await tools.blocked({});}catch(e){} await tools.later({});`, 0, 1024)
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		if !terminate {
			cancel()
		}
		last, err := cells.Wait(context.Background(), first.ID, 1000, 1024, terminate)
		cancel()
		cells.Close()
		if err != nil || last.Running || last.Error == nil || later.Load() != 0 {
			t.Fatalf("terminate=%v result=%+v later=%d err=%v", terminate, last, later.Load(), err)
		}
	}
}

func TestMediaRemainsSeparateFromTruncatedText(t *testing.T) {
	r := New(nil, DefaultLimits())
	cells := NewCells(r)
	defer cells.Close()
	o, err := cells.Start(context.Background(), context.Background(), `text('x'.repeat(4096));image({type:'image',mimeType:'image/png',data:'aGVsbG8='});audio('data:audio/wav;base64,aGVsbG8=');`, 1000, 100)
	if err != nil || o.Error != nil || len(o.Media) != 2 || !strings.Contains(o.Text, "Partial output") {
		t.Fatalf("%+v %v", o, err)
	}
	_, err = r.Run(WithHooks(context.Background(), Hooks{Output: func(OutputItem) error { return nil }}), `image('https://example.com/picture.png');`)
	if err == nil {
		t.Fatal("remote URL accepted as inline media")
	}
}

func TestCodeModeWorker(t *testing.T) {
	if os.Args[len(os.Args)-1] != "code-mode-worker-test" {
		return
	}
	if err := ServeWorker(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func workerCommand() []string {
	return []string{os.Args[0], "-test.run=^TestCodeModeWorker$", "--", "code-mode-worker-test"}
}

func TestProcessWorkerRetainsStateDelegatesAndRecovers(t *testing.T) {
	r := New([]Tool{fakeTool("echo", func(args json.RawMessage) (string, error) { return string(args), nil })}, DefaultLimits())
	r.WorkerCommand = workerCommand()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := r.Run(ctx, `store('x',{value:42});return (await tools.echo({value:load('x').value})).value;`)
	if err != nil || out != "42" {
		t.Fatalf("process=%q %v", out, err)
	}
	// Exhausting the disposable worker must leave both the parent and its store alive.
	_, err = r.Run(ctx, `const chunks=[];while(true){chunks.push('x'.repeat(1024*1024));}`)
	if err == nil {
		t.Fatal("resource exhaustion was accepted")
	}
	out, err = r.Run(ctx, `return load('x').value;`)
	if err != nil || out != "42" {
		t.Fatalf("worker recovery lost state: %q %v", out, err)
	}
}

func TestProcessCancellationReachesHostTools(t *testing.T) {
	entered := make(chan struct{})
	r := New([]Tool{{Name: "blocked", Ident: "blocked", Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}}}, DefaultLimits())
	r.WorkerCommand = workerCommand()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Run(ctx, `await tools.blocked({});`); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not call host")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker cancellation hung")
	}
}
