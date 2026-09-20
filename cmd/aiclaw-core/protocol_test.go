package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/appservice"
)

// pipe collects what the core writes, decoding one JSON Lines message at a
// time the way the host will.
type pipe struct {
	mu    sync.Mutex
	lines []map[string]any
}

func (p *pipe) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			return 0, fmt.Errorf("core wrote a line that is not JSON: %q", line)
		}
		p.lines = append(p.lines, decoded)
	}
	return len(data), nil
}

func (p *pipe) all() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.lines...)
}

func (p *pipe) find(match func(map[string]any) bool) map[string]any {
	for _, line := range p.all() {
		if match(line) {
			return line
		}
	}
	return nil
}

func newTestCore(t *testing.T) (*Core, *pipe) {
	t.Helper()
	out := &pipe{}
	core := &Core{out: json.NewEncoder(out)}
	host := appservice.NewHost(appservice.Options{
		Root: t.TempDir(), Emit: core.emit, Dialogs: core,
	})
	dispatcher, err := NewDispatcher(host.Service())
	if err != nil {
		t.Fatal(err)
	}
	core.dispatcher = dispatcher
	return core, out
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

// Every reply carries the id of its request. Background turns run
// concurrently, so the host cannot match replies by arrival order.
func TestReplyCarriesTheRequestID(t *testing.T) {
	core, out := newTestCore(t)
	input := strings.NewReader(`{"id":"7","command":"Status"}` + "\n")
	core.serve(context.Background(), input)

	reply := out.find(func(line map[string]any) bool { return line["id"] == "7" })
	if reply == nil {
		t.Fatalf("no reply for the request: %+v", out.all())
	}
	if reply["ok"] != true {
		t.Fatalf("reply = %+v", reply)
	}
}

// A refused command must come back as a failed reply, not silence: the host
// has a promise waiting on it.
func TestRefusedCommandStillReplies(t *testing.T) {
	core, out := newTestCore(t)
	core.serve(context.Background(), strings.NewReader(
		`{"id":"1","command":"Stop"}`+"\n"+`{"id":"2","command":"Nope"}`+"\n"))

	for _, id := range []string{"1", "2"} {
		reply := out.find(func(line map[string]any) bool { return line["id"] == id })
		if reply == nil {
			t.Fatalf("no reply for %q: %+v", id, out.all())
		}
		if reply["ok"] != false || reply["error"] == "" {
			t.Fatalf("reply for %q = %+v", id, reply)
		}
	}
}

// Reaching the end of input means the host process is gone. Returning is what
// lets the core exit and release the database; without it a hard-killed host
// leaves an orphan holding SQLite.
func TestEndOfInputEndsTheLoop(t *testing.T) {
	core, _ := newTestCore(t)
	done := make(chan struct{})
	go func() {
		core.serve(context.Background(), strings.NewReader(""))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the core kept running after its input ended")
	}
}

// A malformed line must not wedge the loop open either.
func TestMalformedInputEndsTheLoop(t *testing.T) {
	core, _ := newTestCore(t)
	done := make(chan struct{})
	go func() {
		core.serve(context.Background(), strings.NewReader("this is not json\n"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the core kept running after unreadable input")
	}
}

// Events are tagged with the request that produced them, so the host can route
// a delta to the right conversation.
func TestEmittedEventsAreTaggedAndWellFormed(t *testing.T) {
	core, out := newTestCore(t)
	core.current.Store("turn-9")
	core.emit("chat:delta", map[string]any{"content": "hello"})

	event := out.find(func(line map[string]any) bool { return line["event"] == "chat:delta" })
	if event == nil {
		t.Fatalf("no event was written: %+v", out.all())
	}
	if event["id"] != "turn-9" {
		t.Fatalf("event id = %v", event["id"])
	}
}

// A native dialog needs a window, which the core does not have. It asks the
// host and waits for the answer to come back over the same pipe.
func TestDialogsAreForwardedToTheHost(t *testing.T) {
	core, out := newTestCore(t)

	result := make(chan []string, 1)
	go func() {
		paths, err := core.PickFiles(context.Background(), appservice.FilePicker{Title: "choose"})
		if err != nil {
			result <- nil
			return
		}
		result <- paths
	}()

	var call map[string]any
	waitFor(t, func() bool {
		call = out.find(func(line map[string]any) bool { return line["host"] == "dialog.pickFiles" })
		return call != nil
	}, "the core never asked the host for a dialog")

	id, _ := call["id"].(string)
	if id == "" {
		t.Fatalf("host call has no id: %+v", call)
	}
	core.deliver(id, HostReply{OK: true, Result: json.RawMessage(`["/tmp/a.png"]`)})

	select {
	case paths := <-result:
		if len(paths) != 1 || paths[0] != "/tmp/a.png" {
			t.Fatalf("paths = %+v", paths)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the dialog reply never reached the caller")
	}
}

// A host that cannot show the dialog must produce an error, not a hang.
func TestRefusedDialogReturnsAnError(t *testing.T) {
	core, out := newTestCore(t)
	result := make(chan error, 1)
	go func() {
		_, err := core.PickDirectory(context.Background(), "choose")
		result <- err
	}()

	var call map[string]any
	waitFor(t, func() bool {
		call = out.find(func(line map[string]any) bool { return line["host"] == "dialog.pickDirectory" })
		return call != nil
	}, "the core never asked the host")
	core.deliver(call["id"].(string), HostReply{OK: false, Error: "no window"})

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "no window") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a refused dialog hung instead of returning")
	}
}

// Cancelling must release a caller waiting on the host rather than leaking the
// goroutine for the rest of the process's life.
func TestCancelledDialogReleasesTheCaller(t *testing.T) {
	core, _ := newTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := core.PickDirectory(ctx, "choose")
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("a cancelled dialog reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling did not release the caller")
	}
}

// A reply for a call nobody is waiting on is dropped rather than blocking the
// reader loop, which would stall every later message.
func TestUnmatchedHostReplyIsDropped(t *testing.T) {
	core, _ := newTestCore(t)
	done := make(chan struct{})
	go func() {
		core.deliver("host-does-not-exist", HostReply{OK: true})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("an unmatched host reply blocked the reader")
	}
}

var _ io.Writer = (*pipe)(nil)
