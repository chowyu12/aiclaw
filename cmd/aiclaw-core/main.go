package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chowyu12/aiclaw/internal/appservice"
)

// drainTimeout bounds how long shutdown waits for requests that are still
// running. A stuck turn must not hold the process open forever, but abandoning
// work the instant input ends would drop replies the host is waiting on.
const drainTimeout = 5 * time.Second

func main() {
	root := flag.String("data-dir", "", "data directory, defaults to ~/.aiclaw")
	flag.Parse()

	core := &Core{out: json.NewEncoder(os.Stdout)}
	host := appservice.NewHost(appservice.Options{
		Root:       *root,
		Emit:       core.emit,
		Dialogs:    core,
		PreviewURL: core.previewURL,
	})
	core.dataDir = host.Root()
	dispatcher, err := NewDispatcher(host.Service())
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		os.Exit(1)
	}
	core.dispatcher = dispatcher

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := host.Start(ctx); err != nil {
		// Report the failure on stderr and still hand the host a handshake so
		// it can open a window that explains itself instead of hanging.
		fmt.Fprintln(os.Stderr, "start:", err)
	}
	defer host.Stop()

	core.write(Handshake{Ready: true, Protocol: ProtocolVersion, Commands: dispatcher.Commands()})
	core.serve(ctx, os.Stdin)
}

// Core is the stdio transport: it reads requests, runs them, and writes
// events, replies and host calls back as JSON Lines.
type Core struct {
	dispatcher *Dispatcher

	// writeMu serialises writes; concurrent turns emit events from their own
	// goroutines and a torn line would desynchronise the host.
	writeMu sync.Mutex
	out     *json.Encoder

	// current is the request being served on this goroutine, so events can be
	// tagged with the call that produced them.
	current atomic.Value

	// pending routes host replies back to the call that is waiting.
	pendingMu sync.Mutex
	pending   map[string]chan HostReply
	nextCall  atomic.Uint64

	// inflight counts requests still being served, so shutdown can let them
	// finish writing their replies.
	inflight sync.WaitGroup

	// dataDir anchors attachment preview URLs; the host serves files from it.
	dataDir string
}

// previewURL points the interface at the host's attachment scheme instead of
// inlining the image.
//
// A base64 data URI would have to travel through this pipe, and an attachment
// is capped at 20MB — roughly 27MB encoded — which would stall every other
// message behind it. Only files inside the data directory can be addressed,
// which is also what the host enforces when it serves them.
func (c *Core) previewURL(file appservice.PreviewFile) (string, bool) {
	if c.dataDir == "" || strings.TrimSpace(file.Path) == "" {
		return "", false
	}
	relative, err := filepath.Rel(c.dataDir, filepath.Clean(file.Path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		// A file outside the data directory is not ours to serve.
		return "", false
	}
	return "aiclaw://preview/" + filepath.ToSlash(relative), true
}

// serve reads until the input ends.
//
// Reaching EOF means the host process is gone: returning here lets main exit
// and release the database. Without it a hard-killed host would leave this
// process holding SQLite forever, which is the orphan case the design calls
// out as mandatory to handle.
func (c *Core) serve(ctx context.Context, input io.Reader) {
	// Requests run on their own goroutines, so returning the moment input
	// ends would abandon work whose reply the host is still waiting for.
	defer c.drain()
	reader := bufio.NewReaderSize(input, 64<<10)
	decoder := json.NewDecoder(reader)
	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			if err != io.EOF {
				fmt.Fprintln(os.Stderr, "read:", err)
			}
			return
		}
		if request.Reply != nil {
			c.deliver(request.ID, *request.Reply)
			continue
		}
		c.run(ctx, request)
	}
}

// run serves one request on its own goroutine so a long turn does not block
// the next request.
func (c *Core) run(ctx context.Context, request Request) {
	c.inflight.Add(1)
	go func() {
		defer c.inflight.Done()
		c.current.Store(request.ID)
		result, err := c.dispatcher.Call(request.Command, request.Params)
		if err != nil {
			c.write(Response{ID: request.ID, OK: false, Error: err.Error()})
			return
		}
		c.write(Response{ID: request.ID, OK: true, Result: result})
	}()
}

// drain waits for in-flight requests, bounded so one stuck turn cannot hold
// shutdown open indefinitely.
func (c *Core) drain() {
	settled := make(chan struct{})
	go func() {
		c.inflight.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-time.After(drainTimeout):
		fmt.Fprintln(os.Stderr, "shutdown: gave up waiting for in-flight requests")
	}
}

// emit implements appservice.Emitter.
func (c *Core) emit(name string, data ...any) {
	id, _ := c.current.Load().(string)
	c.write(EventLine{ID: id, Name: name, Data: data})
}

func (c *Core) write(message any) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.out.Encode(message); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
	}
}

// PickFiles implements appservice.Dialogs by asking the host, which owns the
// window a native dialog needs.
func (c *Core) PickFiles(ctx context.Context, request appservice.FilePicker) ([]string, error) {
	var paths []string
	return paths, c.askHost(ctx, "dialog.pickFiles", request, &paths)
}

// PickDirectory implements appservice.Dialogs.
func (c *Core) PickDirectory(ctx context.Context, title string) (string, error) {
	var path string
	return path, c.askHost(ctx, "dialog.pickDirectory", map[string]string{"title": title}, &path)
}

// askHost issues a host call and waits for its reply.
func (c *Core) askHost(ctx context.Context, name string, params any, into any) error {
	id := fmt.Sprintf("host-%d", c.nextCall.Add(1))
	inbox := make(chan HostReply, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[string]chan HostReply)
	}
	c.pending[id] = inbox
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	c.write(HostCall{ID: id, Host: name, Params: params})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case reply := <-inbox:
		if !reply.OK {
			if reply.Error == "" {
				return fmt.Errorf("host refused %s", name)
			}
			return fmt.Errorf("%s: %s", name, reply.Error)
		}
		if len(reply.Result) == 0 {
			return nil
		}
		return json.Unmarshal(reply.Result, into)
	}
}

// deliver routes a host reply to whoever is waiting. A reply for an unknown id
// is dropped: it can only mean the waiter already gave up.
func (c *Core) deliver(id string, reply HostReply) {
	c.pendingMu.Lock()
	inbox := c.pending[id]
	c.pendingMu.Unlock()
	if inbox == nil {
		return
	}
	select {
	case inbox <- reply:
	default:
	}
}
