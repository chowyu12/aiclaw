package codemode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

type Observation struct {
	ID      string
	Running bool
	Text    string
	Media   []OutputItem
	Error   error
}

// Cells owns execution lifetimes; observing a result never reruns its script.
type Cells struct {
	runtime *Runtime
	mu      sync.Mutex
	cells   map[string]*cell
	next    atomic.Uint64
	closed  bool
}
type cell struct {
	id         string
	cancel     context.CancelFunc
	done       chan struct{}
	yield      chan struct{}
	observe    sync.Mutex
	mu         sync.Mutex
	output     tools.OutputBuffer
	media      []OutputItem
	mediaBytes int
	hadOutput  bool
	err        error
}

func NewCells(r *Runtime) *Cells { return &Cells{runtime: r, cells: map[string]*cell{}} }

func (m *Cells) Start(ctx, lifetime context.Context, source string, yieldMS, maxBytes int) (Observation, error) {
	if len(source) > maxScriptBytes {
		return Observation{}, errors.New("script exceeds 1 MiB")
	}
	if err := validateObservation(yieldMS, maxBytes); err != nil {
		return Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if err := lifetime.Err(); err != nil {
		return Observation{}, err
	}
	ctx = m.runtime.snapshot(ctx)
	m.mu.Lock()
	if m.closed || len(m.cells) >= 8 {
		m.mu.Unlock()
		return Observation{}, errors.New("code mode is closed or has 8 uncollected cells; wait or terminate existing cells")
	}
	id := fmt.Sprintf("cell_%d", m.next.Add(1))
	// Preserve call metadata, but not the short-lived observer deadline. The turn owns cancellation.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	stop := context.AfterFunc(lifetime, cancel)
	c := &cell{id: id, cancel: cancel, done: make(chan struct{}), yield: make(chan struct{}, 1), output: tools.OutputBuffer{Complete: true}}
	m.cells[id] = c
	m.mu.Unlock()
	go func() {
		defer close(c.done)
		defer cancel()
		defer stop()
		out, err := m.runtime.Run(WithHooks(runCtx, Hooks{
			Output: c.append,
			Yield: func() {
				select {
				case c.yield <- struct{}{}:
				default:
				}
			},
		}), source)
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.hadOutput && out != "" {
			_, _ = c.output.Write([]byte(out))
		}
		c.err = err
	}()
	return m.observe(ctx, c, yieldMS, maxBytes)
}

func validateObservation(ms, bytes int) error {
	if ms < 0 || ms > 60000 {
		return errors.New("yield_time_ms must be between 0 and 60000")
	}
	if bytes < 1 || bytes > tools.MaxOutputPreview {
		return errors.New("max_output_bytes must be between 1 and 24576")
	}
	return nil
}

func (m *Cells) Wait(ctx context.Context, id string, yieldMS, maxBytes int, terminate bool) (Observation, error) {
	if err := validateObservation(yieldMS, maxBytes); err != nil {
		return Observation{}, err
	}
	m.mu.Lock()
	c := m.cells[id]
	m.mu.Unlock()
	if c == nil {
		return Observation{}, errors.New("cell_id is unknown, already collected, or belongs to another session")
	}
	if terminate {
		c.cancel()
		select {
		case <-c.done:
		case <-ctx.Done():
			return Observation{}, ctx.Err()
		}
	}
	return m.observe(ctx, c, yieldMS, maxBytes)
}

func (c *cell) append(item OutputItem) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hadOutput = true
	if item.Type == "text" {
		_, _ = c.output.Write([]byte(item.Text))
		return nil
	}
	if len(c.media) >= 16 || c.mediaBytes+len(item.URL) > 16*1024*1024 {
		return errors.New("cell media output exceeds 16 items or 16 MiB")
	}
	c.media = append(c.media, item)
	c.mediaBytes += len(item.URL)
	return nil
}

func (m *Cells) observe(ctx context.Context, c *cell, ms, maxBytes int) (Observation, error) {
	c.observe.Lock()
	defer c.observe.Unlock()
	// A concurrent waiter may already have collected and closed this cell.
	m.mu.Lock()
	exists := m.cells[c.id] == c
	m.mu.Unlock()
	if !exists {
		return Observation{}, errors.New("cell has already been collected")
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-c.done:
	case <-c.yield:
	case <-timer.C:
	case <-ctx.Done():
		c.cancel()
		return Observation{}, ctx.Err()
	}
	finished := false
	select {
	case <-c.done:
		finished = true
	default:
	}
	c.mu.Lock()
	text, complete := c.output.String(), c.output.Complete
	media := c.media
	c.output = tools.OutputBuffer{Complete: true}
	c.media = nil
	c.mediaBytes = 0
	err := c.err
	if !finished {
		err = nil
	}
	c.mu.Unlock()
	if finished {
		m.mu.Lock()
		delete(m.cells, c.id)
		m.mu.Unlock()
	}
	text = tools.OutputPreview(ctx, text, maxBytes, complete, m.runtime.SaveOutput)
	return Observation{ID: c.id, Running: !finished, Text: strings.TrimRight(text, "\n"), Media: media, Error: err}, nil
}

func (m *Cells) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.cells {
		c.cancel()
	}
}

// FinishTurn joins canceled workers before turn/completed and frees uncollected cells.
func (m *Cells) FinishTurn() int {
	m.mu.Lock()
	list := make([]*cell, 0, len(m.cells))
	running := 0
	for _, c := range m.cells {
		select {
		case <-c.done:
		default:
			running++
		}
		c.cancel()
		list = append(list, c)
	}
	m.mu.Unlock()
	for _, c := range list {
		<-c.done
	}
	m.mu.Lock()
	for _, c := range list {
		delete(m.cells, c.id)
	}
	m.mu.Unlock()
	return running
}
func (m *Cells) Close() {
	m.mu.Lock()
	m.closed = true
	list := make([]*cell, 0, len(m.cells))
	for _, c := range m.cells {
		c.cancel()
		list = append(list, c)
	}
	m.mu.Unlock()
	for _, c := range list {
		<-c.done
	}
	m.mu.Lock()
	m.cells = map[string]*cell{}
	m.mu.Unlock()
}
