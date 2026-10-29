package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStdioCancellationIsOrderedAndConnectionSurvives(t *testing.T) {
	in, stdin := io.Pipe()
	stdout, out := io.Pipe()
	tr := &stdioTransport{stdin: stdin, pending: map[int64]chan rpcResponse{}, done: make(chan struct{})}
	t.Cleanup(func() { stdin.Close(); in.Close(); out.Close(); stdout.Close() })
	go tr.readLoop(stdout)
	frames := make(chan rpcRequest, 8)
	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			var req rpcRequest
			if json.Unmarshal(scanner.Bytes(), &req) == nil {
				frames <- req
			}
		}
	}()
	next := func() rpcRequest {
		t.Helper()
		select {
		case frame := <-frames:
			return frame
		case <-time.After(time.Second):
			t.Fatal("missing frame")
			return rpcRequest{}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := tr.Call(ctx, "tools/call", map[string]any{"name": "slow"}); done <- err }()
	request := next()
	if request.Method != "tools/call" {
		t.Fatal(request.Method)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local cancellation blocked")
	}
	notice := next()
	params, _ := json.Marshal(notice.Params)
	var canceled struct {
		RequestID int64 `json:"requestId"`
	}
	_ = json.Unmarshal(params, &canceled)
	if notice.Method != "notifications/cancelled" || notice.ID != nil || canceled.RequestID != *request.ID {
		t.Fatalf("bad cancellation: %s", params)
	}
	// A late reply to the canceled request must not break the shared transport.
	fmt.Fprintf(out, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{}}\n", *request.ID)
	go func() { _, err := tr.Call(context.Background(), "ping", nil); done <- err }()
	request = next()
	fmt.Fprintf(out, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{}}\n", *request.ID)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shared connection broken")
	}
}

func TestHTTPCancellationNotifiesServerAndConnectionSurvives(t *testing.T) {
	started := make(chan int64, 1)
	canceled := make(chan int64, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				RequestID int64 `json:"requestId"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "tools/call":
			started <- req.ID
			<-r.Context().Done()
		case "notifications/cancelled":
			canceled <- req.Params.RequestID
			w.WriteHeader(202)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID)
		}
	}))
	defer server.Close()
	tr, err := startHTTP(Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := tr.Call(ctx, "tools/call", nil); done <- err }()
	var id int64
	select {
	case id = <-started:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local call did not stop")
	}
	select {
	case got := <-canceled:
		if got != id {
			t.Fatal("wrong cancellation ID")
		}
	case <-time.After(time.Second):
		t.Fatal("no cancellation notification")
	}
	if _, err := tr.Call(context.Background(), "ping", nil); err != nil {
		t.Fatal(err)
	}
}
