package agent

import (
	"context"
	"sync"

	"github.com/chowyu12/aiclaw/internal/model"
)

// streamDispatcher serializes all legacy stream writes and turns a client-side
// write failure into cancellation for the active execution.
type streamDispatcher struct {
	mu      sync.Mutex
	handler func(model.StreamChunk) error
	cancel  context.CancelCauseFunc
	err     error
}

func newStreamDispatcher(ctx context.Context, handler func(model.StreamChunk) error) (*streamDispatcher, context.Context) {
	streamCtx, cancel := context.WithCancelCause(ctx)
	return &streamDispatcher{handler: handler, cancel: cancel}, streamCtx
}

func (d *streamDispatcher) Emit(chunk model.StreamChunk) error {
	if d == nil || d.handler == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	if err := d.handler(chunk); err != nil {
		d.err = err
		d.cancel(err)
		return err
	}
	return nil
}

func (d *streamDispatcher) Err() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}
