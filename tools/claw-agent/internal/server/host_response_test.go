package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Real request/response round trip: a host RPC error must not become the
// approval-shaped {approved:false} success that hid failed screenshots.
func TestHostErrorsPropagateForEveryCapability(t *testing.T) {
	for _, method := range []string{protocol.RequestComputer, protocol.RequestBrowser, protocol.RequestApproval} {
		t.Run(method, func(t *testing.T) {
			writer := &receiptWriter{}
			s := &Server{out: writer, outboundPending: map[int64]chan frame{}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.requestHost(ctx, method, map[string]any{}); done <- err }()
			var request frame
			eventually(t, "host request", func() bool {
				writer.mu.Lock()
				defer writer.mu.Unlock()
				if len(writer.frames) == 0 {
					return false
				}
				request = writer.frames[0]
				return true
			})
			s.resolveOutbound(frame{ID: request.ID, Error: &rpcError{Code: -32000, Message: "Screen Recording permission missing"}})
			if err := <-done; err == nil || !strings.Contains(err.Error(), "Screen Recording permission missing") {
				t.Fatalf("host error was lost: %v", err)
			}
			if len(s.outboundPending) != 0 {
				t.Fatal("completed host request leaked")
			}
		})
	}
}

func TestHostApprovalDenialRemainsAValidResponse(t *testing.T) {
	writer := &receiptWriter{}
	s := &Server{out: writer, outboundPending: map[int64]chan frame{}}
	done := make(chan json.RawMessage, 1)
	go func() {
		result, _ := s.requestHost(context.Background(), protocol.RequestApproval, nil)
		done <- result
	}()
	var request frame
	eventually(t, "approval request", func() bool {
		writer.mu.Lock()
		defer writer.mu.Unlock()
		if len(writer.frames) == 0 {
			return false
		}
		request = writer.frames[0]
		return true
	})
	s.resolveOutbound(frame{ID: request.ID, Result: json.RawMessage(`{"approved":false}`)})
	if got := string(<-done); got != `{"approved":false}` {
		t.Fatalf("denial changed: %s", got)
	}
}

func TestHostRequestCancellationRemovesPendingResponse(t *testing.T) {
	s := &Server{out: &receiptWriter{}, outboundPending: map[int64]chan frame{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.requestHost(ctx, protocol.RequestComputer, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if len(s.outboundPending) != 0 {
		t.Fatal("cancelled host request leaked")
	}
	// A late reply must be harmless, rather than blocking the receive loop.
	s.resolveOutbound(frame{ID: json.RawMessage(`1`), Result: json.RawMessage(`{}`)})
}
