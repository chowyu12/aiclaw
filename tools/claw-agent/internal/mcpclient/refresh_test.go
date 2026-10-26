package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolNotificationsRefreshCoalescePaginateAndKeepOldOnFailure(t *testing.T) {
	var revision, lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q.ID == nil {
			w.WriteHeader(202)
			return
		}
		result := `{}`
		if q.Method == "tools/list" {
			lists.Add(1)
			switch revision.Load() {
			case 0:
				result = `{"tools":[{"name":"old","inputSchema":{"type":"object"}}]}`
			case 1:
				if q.Params.Cursor == "" {
					result = `{"tools":[{"name":"new","inputSchema":{"type":"object"}}],"nextCursor":"page2"}`
				} else {
					result = `{"tools":[{"name":"second","inputSchema":{"type":"object"}}]}`
				}
			case 2:
				http.Error(w, "unavailable", 503)
				return
			default:
				result = `{"tools":[{"name":"recovered","inputSchema":{"type":"object"}}]}`
			}
		}
		if q.Method == "tools/call" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[]}}\n\n", *q.ID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, *q.ID, result)
	}))
	defer server.Close()
	c, err := Start(context.Background(), Config{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.RefreshTools(context.Background()); err != nil || lists.Load() != 1 {
		t.Fatal("unnecessary refresh", err)
	}
	revision.Store(1)
	_, _ = c.CallTool(context.Background(), "old", json.RawMessage(`{}`))
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := c.RefreshTools(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if lists.Load() != 3 || len(c.Tools()) != 2 || c.Tools()[0].Name != "new" {
		t.Fatal("refresh not coalesced/paginated", lists.Load(), c.Tools())
	}
	revision.Store(2)
	c.changed.Add(1)
	if c.RefreshTools(context.Background()) == nil || c.Tools()[0].Name != "new" {
		t.Fatal("failed refresh replaced catalog")
	}
	count := lists.Load()
	_ = c.RefreshTools(context.Background())
	if lists.Load() != count {
		t.Fatal("missing retry cooldown")
	}
	revision.Store(3)
	c.refreshMu.Lock()
	c.retryAfter = time.Time{}
	c.refreshMu.Unlock()
	if err = c.RefreshTools(context.Background()); err != nil || c.Tools()[0].Name != "recovered" {
		t.Fatal("refresh did not recover", err)
	}
}

func TestStdIOAndHTTPNotificationsDoNotConsumeRPCResponses(t *testing.T) {
	var notices atomic.Int32
	notify := func() { notices.Add(1) }
	tpt := &stdioTransport{onToolsChanged: notify, pending: map[int64]chan rpcResponse{1: make(chan rpcResponse, 1)}, done: make(chan struct{})}
	answer := tpt.pending[1]
	tpt.readLoop(strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n"))
	if notices.Load() != 1 {
		t.Fatal("stdio notification lost")
	}
	select {
	case <-answer:
	default:
		t.Fatal("response lost")
	}
	if toolsChanged([]byte(`{"id":1,"method":"notifications/tools/list_changed"}`), notify) {
		t.Fatal("request treated as notification")
	}
}

func TestHTTPNotificationStreamStopsOnClose(t *testing.T) {
	var notices atomic.Int32
	ready := make(chan struct{})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "unexpected", 400)
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "test-session" {
			t.Error("missing session")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
		close(done)
	}))
	defer server.Close()
	tpt, err := startHTTP(Config{URL: server.URL, onToolsChanged: func() { notices.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	tpt.sessionID = "test-session"
	tpt.startNotifications()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("stream not opened")
	}
	deadline := time.Now().Add(time.Second)
	for notices.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	tpt.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream survived close")
	}
	if notices.Load() != 1 {
		t.Fatal("stream notification lost")
	}
}
