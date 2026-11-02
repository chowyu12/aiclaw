package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Network diagnostics use only the configured provider, never a guessed endpoint or generation call.
func (s *Server) handleNetworkDiagnostics(ctx context.Context, f frame) {
	var p struct {
		ProviderID int64  `json:"providerId"`
		Model      string `json:"model"`
	}
	if json.Unmarshal(f.Params, &p) != nil || p.ProviderID <= 0 || s.providers == nil {
		s.writeResult(f.ID, map[string]string{"report": "FAIL configuration: Select a configured model provider first; no requests sent."})
		return
	}
	m := protocol.ModelConfig{ProviderID: p.ProviderID, Model: p.Model}
	key, err := s.providers.Resolve(ctx, &m)
	if err != nil {
		s.writeResult(f.ID, map[string]string{"report": "FAIL configuration: Cannot resolve the configured provider and API key; no requests sent."})
		return
	}
	s.writeResult(f.ID, map[string]string{"report": probeNetwork(ctx, m, key, http.DefaultClient)})
}

func probeNetwork(ctx context.Context, model protocol.ModelConfig, key string, client *http.Client) string {
	u, err := url.Parse(strings.TrimSpace(model.BaseURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(key) == "" || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/chat/completions") || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/models") {
		return "FAIL configuration: Invalid API base URL or missing key; no requests sent. Use an HTTP(S) API base without URL credentials, query, fragment or method suffix."
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows := []string{"PASS configuration: Configured API base and key resolved.", "Scope: GET /models only; no generation request. Redirects are not followed."}
	var mu sync.Mutex
	phases := map[string]string{}
	set := func(name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			phases[name] = "FAIL"
		} else if phases[name] != "FAIL" {
			phases[name] = "PASS"
		}
	}
	trace := &httptrace.ClientTrace{
		DNSDone:          func(i httptrace.DNSDoneInfo) { set("DNS", i.Err) },
		ConnectDone:      func(_, _ string, err error) { set("TCP / proxy connection", err) },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) { set("TLS", err) },
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u.String(), nil)
	if err != nil {
		return "FAIL configuration: Cannot construct a request; no requests sent."
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	started := time.Now()
	resp, requestErr := c.Do(req)
	mu.Lock()
	for _, phase := range []string{"DNS", "TCP / proxy connection", "TLS"} {
		state := phases[phase]
		if state == "" {
			state = "NOT OBSERVED (cached connection, IP address, proxy or plain HTTP)"
		}
		rows = append(rows, state+" "+phase)
	}
	mu.Unlock()
	if requestErr != nil {
		detail := "Connection failed; check the failed phase, endpoint and proxy configuration."
		if ctx.Err() != nil {
			detail = "Request cancelled or timed out (10-second maximum)."
		}
		rows = append(rows, "FAIL HTTP: "+detail)
		return strings.Join(rows, "\n")
	}
	defer resp.Body.Close()
	rows = append(rows, fmt.Sprintf("HTTP %d (%d ms)", resp.StatusCode, time.Since(started).Milliseconds()))
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		rows = append(rows, "FAIL authentication: Provider rejected credentials or access.")
	case resp.StatusCode == 404 || resp.StatusCode == 405:
		rows = append(rows, "UNKNOWN catalog: /models is unavailable; check the API base. This does not prove generation is unavailable.")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		rows = append(rows, "FAIL redirect: Not followed, so credentials cannot be forwarded to a different endpoint.")
	case resp.StatusCode == 429:
		rows = append(rows, "FAIL rate limit: Provider is reachable but limited this request.")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		rows = append(rows, "FAIL provider: Non-success HTTP response; response body omitted.")
	default:
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
		var catalog struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if e != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &catalog) != nil || catalog.Data == nil {
			rows = append(rows, "UNKNOWN catalog: Response is incomplete, too large or not a model list; body omitted.")
			break
		}
		rows = append(rows, "PASS HTTP: Model catalog received.")
		if model.Model != "" {
			found := false
			for _, item := range catalog.Data {
				found = found || item.ID == model.Model
			}
			if found {
				rows = append(rows, "PASS model: Configured model appears in this catalog.")
			} else {
				rows = append(rows, "UNKNOWN model: Configured model is absent from the catalog; generation was not tested.")
			}
		}
	}
	return strings.Join(rows, "\n")
}
