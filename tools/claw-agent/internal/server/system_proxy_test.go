package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestParseSystemProxy(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		invalid     bool
	}{
		{"DIRECT", "", false},
		{"PROXY localhost:8080; DIRECT", "http://localhost:8080", false},
		{"SOCKS5 [::1]:1080", "socks5://[::1]:1080", false},
		{"HTTPS proxy.example:443", "https://proxy.example:443", false},
		{"SOCKS4 host:123", "", true},
		{"PROXY user:pass@host:123", "", true},
		{"", "", true},
	} {
		u, err := parseSystemProxy(tc.input)
		if (err != nil) != tc.invalid {
			t.Errorf("%q: %v", tc.input, err)
			continue
		}
		if err == nil {
			got := ""
			if u != nil {
				got = u.String()
			}
			if got != tc.want {
				t.Errorf("%q: got %q, want %q", tc.input, got, tc.want)
			}
		}
	}
}

func TestSystemProxyResolvesDestinationThroughHost(t *testing.T) {
	w := &receiptWriter{}
	s := &Server{out: w, outboundPending: map[int64]chan frame{}}
	req, err := http.NewRequest("GET", "https://name:secret@example.com/path?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		u, err := s.systemProxy(req)
		if err == nil && (u == nil || u.String() != "http://localhost:8080") {
			t.Errorf("unexpected proxy %v", u)
		}
		done <- err
	}()
	var request frame
	eventually(t, "proxy request", func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		if len(w.frames) == 0 {
			return false
		}
		request = w.frames[0]
		return true
	})
	if request.Method != "proxy/resolve" || strings.Contains(string(request.Params), "secret") {
		t.Fatalf("unsafe proxy request: %+v", request)
	}
	s.resolveOutbound(frame{ID: request.ID, Result: json.RawMessage(`"PROXY localhost:8080; DIRECT"`)})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
