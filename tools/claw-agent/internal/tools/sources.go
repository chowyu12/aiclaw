package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/internal/tools/websearch"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type sourceKey struct{}

// SourceSink belongs to one outer tool call; Code Mode shares its context with nested tools.
type SourceSink struct {
	mu      sync.Mutex
	sources []protocol.SearchSource
}

func WithSources(ctx context.Context, sink *SourceSink) context.Context {
	return context.WithValue(ctx, sourceKey{}, sink)
}

func (s *SourceSink) Sources() []protocol.SearchSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.SearchSource(nil), s.sources...)
}

func RecordSearchSources(ctx context.Context, output string) {
	sink, ok := ctx.Value(sourceKey{}).(*SourceSink)
	if !ok {
		return
	}
	sources := ParseSearchSources(output)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, source := range sources {
		found := false
		for _, existing := range sink.sources {
			if existing.URL == source.URL {
				found = true
				break
			}
		}
		if !found {
			sink.sources = append(sink.sources, source)
		}
	}
}

// Accept the search service's response shape, never arbitrary prose or tool URLs.
func ParseSearchSources(output string) []protocol.SearchSource {
	var response websearch.SearchResponse
	if json.Unmarshal([]byte(output), &response) != nil || response.Provider == "" || response.Query == "" {
		return nil
	}
	var sources []protocol.SearchSource
	seen := map[string]bool{}
	for _, hit := range response.Results {
		raw := strings.TrimSpace(hit.URL)
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
			continue
		}
		parsed.Fragment = ""
		raw = parsed.String()
		if seen[raw] {
			continue
		}
		seen[raw] = true
		sources = append(sources, protocol.SearchSource{Title: strings.TrimSpace(hit.Title), URL: raw, Snippet: strings.TrimSpace(hit.Snippet)})
	}
	return sources
}
