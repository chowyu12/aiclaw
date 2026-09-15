package connector

import (
	"context"
	"sync"
)

// Dispatcher runs inbound messages off the connection goroutine.
//
// Two properties matter and neither is optional. Messages from one
// conversation run one at a time, because two turns on the same thread would
// interleave their replies and confuse the transcript. Total concurrency is
// bounded, because an external party controls how fast messages arrive and an
// unbounded goroutine per message is a way to be flooded off the machine.
//
// When the bound is reached the message is rejected rather than queued
// silently: the sender is told the agent is busy, which is true, instead of
// waiting on a reply that may never come.
type Dispatcher struct {
	slots chan struct{}

	mu    sync.Mutex
	busy  map[string]bool
	queue map[string][]func(context.Context)
	wg    sync.WaitGroup
}

// NewDispatcher bounds how many inbound turns run at once.
func NewDispatcher(limit int) *Dispatcher {
	if limit <= 0 {
		limit = 1
	}
	return &Dispatcher{
		slots: make(chan struct{}, limit),
		busy:  make(map[string]bool),
		queue: make(map[string][]func(context.Context)),
	}
}

// Submit schedules work for one conversation. It returns false when the
// dispatcher is at capacity, so the caller can tell the sender to retry.
//
// Work for a conversation that is already running is queued behind it rather
// than rejected: a follow-up message in the same chat is the normal case.
func (d *Dispatcher) Submit(ctx context.Context, key string, work func(context.Context)) bool {
	d.mu.Lock()
	if d.busy[key] {
		if len(d.queue[key]) >= cap(d.slots) {
			d.mu.Unlock()
			return false
		}
		d.queue[key] = append(d.queue[key], work)
		d.mu.Unlock()
		return true
	}
	select {
	case d.slots <- struct{}{}:
	default:
		d.mu.Unlock()
		return false
	}
	d.busy[key] = true
	d.mu.Unlock()

	d.wg.Add(1)
	go d.run(ctx, key, work)
	return true
}

func (d *Dispatcher) run(ctx context.Context, key string, work func(context.Context)) {
	defer d.wg.Done()
	defer func() { <-d.slots }()
	for {
		func() {
			// A panic in one message must not stop the conversation from
			// serving the next.
			defer func() { _ = recover() }()
			work(ctx)
		}()
		d.mu.Lock()
		pending := d.queue[key]
		if len(pending) == 0 || ctx.Err() != nil {
			delete(d.busy, key)
			delete(d.queue, key)
			d.mu.Unlock()
			return
		}
		work, d.queue[key] = pending[0], pending[1:]
		d.mu.Unlock()
	}
}

// Wait blocks until every scheduled message has finished, so a channel can
// shut down without cutting a reply in half.
func (d *Dispatcher) Wait() { d.wg.Wait() }
