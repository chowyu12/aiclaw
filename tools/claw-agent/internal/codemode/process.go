package codemode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxFrameBytes = 24 * 1024 * 1024

type wireTool struct {
	Name, Ident, Description string
	Schema                   json.RawMessage
	ParallelSafe             bool
}
type frame struct {
	Kind   string
	ID     int64
	Name   string          `json:",omitempty"`
	Code   string          `json:",omitempty"`
	Text   string          `json:",omitempty"`
	Error  string          `json:",omitempty"`
	Data   json.RawMessage `json:",omitempty"`
	Item   OutputItem      `json:",omitempty"`
	Tools  []wireTool      `json:",omitempty"`
	Limits Limits          `json:",omitempty"`
}

type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *frameWriter) write(v frame) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > maxFrameBytes {
		return errors.New("code mode IPC frame exceeds 24 MiB")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	data = append(data, '\n')
	_, err = w.w.Write(data)
	return err
}
func frameScanner(reader io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), maxFrameBytes)
	return s
}

// SelfWorkerCommand uses the same shipped binary; no separately downloaded runtime is needed.
func SelfWorkerCommand() []string {
	path, err := os.Executable()
	if err != nil || strings.HasSuffix(path, ".test") {
		return nil
	}
	return []string{path, "code-mode-worker"}
}

func (r *Runtime) runProcess(parent context.Context, source string) (string, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, r.WorkerCommand[0], r.WorkerCommand[1:]...)
	// Tool credentials and inherited application configuration stay in the parent.
	cmd.Env = []string{"GOMEMLIMIT=128MiB", "AICLAW_LOCALE=" + os.Getenv("AICLAW_LOCALE")}
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return "", err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = input.Close()
		return "", err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		_ = input.Close()
		_ = output.Close()
		return "", err
	}
	var workers sync.WaitGroup
	defer func() { cancel(); _ = input.Close(); _ = cmd.Wait(); workers.Wait() }()
	writer := &frameWriter{w: input}
	list := r.toolsFor(ctx)
	byName := map[string]Tool{}
	init := frame{Kind: "start", Code: source, Limits: r.limits}
	for _, t := range list {
		byName[t.Ident] = t
		init.Tools = append(init.Tools, wireTool{t.Name, t.Ident, t.Description, t.Schema, t.ParallelSafe})
	}
	if err := writer.write(init); err != nil {
		return "", err
	}
	hooks := hooksFrom(ctx)
	var calls atomic.Int64
	scanner := frameScanner(output)
	for scanner.Scan() {
		var request frame
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return "", fmt.Errorf("invalid code mode worker response: %w", err)
		}
		switch request.Kind {
		case "call":
			if calls.Add(1) > int64(r.limits.MaxCalls) {
				return "", errors.New("worker exceeded tool call limit")
			}
			tool, ok := byName[request.Name]
			if !ok {
				return "", errors.New("worker requested an unknown tool")
			}
			if len(request.Data) > maxScriptBytes {
				return "", errors.New("tool arguments exceed 1 MiB")
			}
			workers.Add(1)
			go func(request frame, tool Tool) {
				defer workers.Done()
				if tool.ParallelSafe {
					r.gate.RLock()
				} else {
					r.gate.Lock()
				}
				text, err := "", ctx.Err()
				if err == nil {
					text, err = tool.Call(ctx, request.Data)
				}
				if tool.ParallelSafe {
					r.gate.RUnlock()
				} else {
					r.gate.Unlock()
				}
				response := frame{Kind: "reply", ID: request.ID, Text: text}
				if err != nil {
					response.Error = err.Error()
				}
				if len(text) > 8*1024*1024 {
					response.Text = ""
					response.Error = "tool result exceeds code mode's 8 MiB transport limit; paginate or narrow the query"
				}
				if err := writer.write(response); err != nil {
					cancel()
				}
			}(request, tool)
		case "store", "load", "save":
			response := frame{Kind: "reply", ID: request.ID}
			var err error
			switch request.Kind {
			case "store":
				err = r.put(request.Name, request.Data)
			case "load":
				response.Data, err = r.get(request.Name)
			case "save":
				if r.SaveOutput == nil {
					err = errors.New("output storage unavailable")
				} else {
					response.Text, err = r.SaveOutput(ctx, request.Text, true)
				}
			}
			if err != nil {
				response.Error = err.Error()
			}
			if err := writer.write(response); err != nil {
				return "", err
			}
		case "output":
			if hooks.Output != nil {
				if err := hooks.Output(request.Item); err != nil {
					return "", err
				}
			}
		case "yield":
			if hooks.Yield != nil {
				hooks.Yield()
			}
		case "done":
			if request.Error != "" {
				return request.Text, errors.New(request.Error)
			}
			return request.Text, nil
		default:
			return "", errors.New("unknown code mode worker message")
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("code mode worker stopped unexpectedly (possibly a resource limit); inspect state before repeating side effects: %v", scanner.Err())
}

type workerPeer struct {
	writer  *frameWriter
	mu      sync.Mutex
	pending map[int64]chan frame
	next    atomic.Int64
	done    chan struct{}
}

func (p *workerPeer) request(ctx context.Context, v frame) (frame, error) {
	v.ID = p.next.Add(1)
	reply := make(chan frame, 1)
	p.mu.Lock()
	p.pending[v.ID] = reply
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, v.ID); p.mu.Unlock() }()
	if err := p.writer.write(v); err != nil {
		return frame{}, err
	}
	select {
	case response := <-reply:
		if response.Error != "" {
			return response, errors.New(response.Error)
		}
		return response, nil
	case <-ctx.Done():
		return frame{}, ctx.Err()
	case <-p.done:
		return frame{}, errors.New("code mode parent disconnected")
	}
}

// ServeWorker owns only the JS VM. Every host operation is delegated back to the agent.
func ServeWorker(input io.Reader, output io.Writer) error {
	scanner := frameScanner(input)
	if !scanner.Scan() {
		return errors.New("missing code mode worker initialization")
	}
	var init frame
	if err := json.Unmarshal(scanner.Bytes(), &init); err != nil {
		return err
	}
	if init.Kind != "start" || len(init.Code) > maxScriptBytes {
		return errors.New("invalid worker initialization")
	}
	debug.SetMemoryLimit(128 * 1024 * 1024)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The watchdog is confined to this disposable process. OOM/exit cannot take down the agent.
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				if mem.HeapAlloc > 128*1024*1024 {
					os.Exit(70)
				}
			}
		}
	}()
	peer := &workerPeer{writer: &frameWriter{w: output}, pending: map[int64]chan frame{}, done: make(chan struct{})}
	go func() {
		defer close(peer.done)
		defer cancel()
		for scanner.Scan() {
			var v frame
			if json.Unmarshal(scanner.Bytes(), &v) != nil || v.Kind != "reply" {
				return
			}
			peer.mu.Lock()
			ch := peer.pending[v.ID]
			peer.mu.Unlock()
			if ch != nil {
				ch <- v
			}
		}
	}()
	var list []Tool
	for _, meta := range init.Tools {
		meta := meta
		list = append(list, Tool{Name: meta.Name, Ident: meta.Ident, Description: meta.Description, Schema: meta.Schema, ParallelSafe: meta.ParallelSafe,
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				v, err := peer.request(ctx, frame{Kind: "call", Name: meta.Ident, Data: args})
				return v.Text, err
			}})
	}
	rt := New(list, init.Limits)
	rt.SaveOutput = func(ctx context.Context, text string, _ bool) (string, error) {
		v, err := peer.request(ctx, frame{Kind: "save", Text: text})
		return v.Text, err
	}
	ctx = WithHooks(ctx, Hooks{
		Output: func(item OutputItem) error { return peer.writer.write(frame{Kind: "output", Item: item}) },
		Yield:  func() { _ = peer.writer.write(frame{Kind: "yield"}) },
		Store: func(key string, value json.RawMessage) error {
			_, err := peer.request(ctx, frame{Kind: "store", Name: key, Data: value})
			return err
		},
		Load: func(key string) (json.RawMessage, error) {
			v, err := peer.request(ctx, frame{Kind: "load", Name: key})
			return v.Data, err
		},
	})
	text, err := rt.Run(ctx, init.Code)
	response := frame{Kind: "done", Text: text}
	if err != nil {
		response.Error = err.Error()
	}
	return peer.writer.write(response)
}
