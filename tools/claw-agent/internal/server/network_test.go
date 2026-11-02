package server

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkDiagnosticsStayOnConfiguredCatalogAndOmitSecrets(t *testing.T) {
	const secret = "synthetic-diagnostic-key"
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{200, `{"data":[{"id":"test"}]}`, "PASS model"},
		{200, `{"data":[]}`, "UNKNOWN model"},
		{200, `not JSON synthetic-diagnostic-key`, "UNKNOWN catalog"},
		{401, secret, "FAIL authentication"},
		{403, secret, "FAIL authentication"},
		{404, secret, "UNKNOWN catalog"},
		{429, secret, "FAIL rate limit"},
		{503, secret, "FAIL provider"},
	} {
		t.Run(tc.want+tc.body[:1], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("wrong diagnostic request")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			report := probeNetwork(context.Background(), protocol.ModelConfig{BaseURL: srv.URL + "/v1/", Model: "test"}, secret, srv.Client())
			if !strings.Contains(report, tc.want) || strings.Contains(report, secret) || strings.Contains(report, srv.URL) {
				t.Fatal(report)
			}
		})
	}
}
func TestNetworkDiagnosticsRejectUnsafeConfigurationAndRedirects(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Redirect(w, r, "/redirected", 302) }))
	defer srv.Close()
	for _, base := range []string{"", "://bad", srv.URL + "?token=secret", srv.URL + "#fragment", "http://user:secret@example.com", srv.URL + "/chat/completions", srv.URL + "/models"} {
		report := probeNetwork(context.Background(), protocol.ModelConfig{BaseURL: base}, "key", srv.Client())
		if !strings.Contains(report, "FAIL configuration") || calls.Load() != 0 {
			t.Fatal(report)
		}
	}
	report := probeNetwork(context.Background(), protocol.ModelConfig{BaseURL: srv.URL}, "key", srv.Client())
	if !strings.Contains(report, "FAIL redirect") || calls.Load() != 1 {
		t.Fatal(report, calls.Load())
	}
}
func TestNetworkDiagnosticsTLSAndCancellation(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) }))
	defer srv.Close()
	report := probeNetwork(context.Background(), protocol.ModelConfig{BaseURL: srv.URL}, "key", srv.Client())
	if !strings.Contains(report, "PASS TLS") {
		t.Fatal(report)
	}
	report = probeNetwork(context.Background(), protocol.ModelConfig{BaseURL: srv.URL}, "key", http.DefaultClient)
	if !strings.Contains(report, "FAIL TLS") {
		t.Fatal(report)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	report = probeNetwork(ctx, protocol.ModelConfig{BaseURL: slow.URL}, "key", slow.Client())
	if !strings.Contains(report, "cancelled or timed out") {
		t.Fatal(report)
	}
}

func TestNetworkDiagnosticRPCUsesSavedProviderWithoutChangingIt(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Error("diagnostic did not use saved configuration")
		}
		w.Write([]byte(`{"data":[{"id":"fake"}]}`))
	}))
	defer upstream.Close()
	s, binding := policyServer(t, upstream.URL+"/v1")
	writer := &receiptWriter{}
	s.out = writer
	params, _ := json.Marshal(map[string]any{"providerId": binding.ProviderID, "model": "fake"})
	s.dispatch(context.Background(), frame{ID: json.RawMessage(`1`), Method: "diagnostics/network", Params: params})
	body, _ := json.Marshal(writer.frames)
	if calls.Load() != 1 || !strings.Contains(string(body), "PASS model") || strings.Contains(string(body), "sk-test") {
		t.Fatal(string(body))
	}
	params = json.RawMessage(`{"providerId":-1}`)
	s.dispatch(context.Background(), frame{ID: json.RawMessage(`2`), Method: "diagnostics/network", Params: params})
	if calls.Load() != 1 {
		t.Fatal("invalid provider caused a probe")
	}
	list, err := s.providers.List(context.Background())
	if err != nil || len(list) != 1 || len(list[0].Models) != 1 || list[0].Models[0] != "fake" {
		t.Fatal("diagnostic mutated provider", err)
	}
}
