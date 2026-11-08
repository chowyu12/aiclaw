package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Chromium resolves system settings and PAC per destination. Do not fall back
// to a direct connection when resolution fails: that could bypass a proxy.
func (s *Server) systemProxy(req *http.Request) (*url.URL, error) {
	destination := *req.URL
	destination.User = nil
	destination.RawQuery = ""
	destination.Fragment = ""
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	raw, err := s.requestHost(ctx, "proxy/resolve", map[string]string{"url": destination.String()})
	if err != nil {
		return nil, fmt.Errorf("system proxy resolution failed: %w", err)
	}
	var result string
	if json.Unmarshal(raw, &result) != nil {
		return nil, fmt.Errorf("invalid system proxy response")
	}
	return parseSystemProxy(result)
}

func parseSystemProxy(result string) (*url.URL, error) {
	// Chromium returns alternatives in order. net/http accepts one proxy; use
	// the first rather than silently switching to a later DIRECT alternative.
	first := strings.TrimSpace(strings.SplitN(result, ";", 2)[0])
	parts := strings.Fields(first)
	if len(parts) == 1 && strings.EqualFold(parts[0], "DIRECT") {
		return nil, nil
	}
	if len(parts) != 2 {
		return nil, fmt.Errorf("unsupported system proxy configuration")
	}
	scheme := ""
	switch strings.ToUpper(parts[0]) {
	case "PROXY", "HTTP":
		scheme = "http"
	case "HTTPS":
		scheme = "https"
	case "SOCKS5":
		scheme = "socks5"
	default:
		return nil, fmt.Errorf("unsupported system proxy type")
	}
	u, err := url.Parse(scheme + "://" + parts[1])
	if err != nil || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid system proxy address")
	}
	return u, nil
}
