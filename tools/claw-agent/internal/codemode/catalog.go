package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
)

type catalogKey struct{}

func (r *Runtime) snapshot(ctx context.Context) context.Context {
	if ctx.Value(catalogKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, catalogKey{}, r.Tools())
}
func (r *Runtime) toolsFor(ctx context.Context) []Tool {
	if list, ok := ctx.Value(catalogKey{}).([]Tool); ok {
		return list
	}
	return r.Tools()
}

// UpdateTools replaces only the catalog. Session data and concurrency gates survive refresh.
func (r *Runtime) UpdateTools(list []Tool) {
	copy := append([]Tool(nil), list...)
	r.catalogMu.Lock()
	r.tools = copy
	r.catalogMu.Unlock()
}

func (r *Runtime) Tools() []Tool {
	r.catalogMu.RLock()
	defer r.catalogMu.RUnlock()
	return append([]Tool(nil), r.tools...)
}

// DiscoveryTools exposes complete declarations on demand, including tools omitted from the prompt.
func (r *Runtime) DiscoveryTools() []Tool {
	return []Tool{
		{Name: "tool_search", Ident: "tool_search", ParallelSafe: true,
			Description: "Find tools by name or description and read their complete parameter declarations before calling them.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["query"]}`),
			Call: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var args struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", err
				}
				if strings.TrimSpace(args.Query) == "" {
					return "", errors.New("query must not be empty")
				}
				if args.Limit == 0 {
					args.Limit = 8
				}
				if args.Limit < 1 || args.Limit > 20 {
					return "", errors.New("limit must be between 1 and 20")
				}
				return r.discover(ctx, args.Query, "", args.Limit)
			}},
		{Name: "tool_describe", Ident: "tool_describe", ParallelSafe: true,
			Description: "Get the full description, JSON input schema and TypeScript declaration for one exact callable name.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
			Call: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var args struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", err
				}
				if args.Name == "" {
					return "", errors.New("name must not be empty")
				}
				return r.discover(ctx, "", strings.TrimPrefix(args.Name, "tools."), 1)
			}},
	}
}

func (r *Runtime) discover(ctx context.Context, query, exact string, limit int) (string, error) {
	type result struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Declaration string          `json:"declaration"`
		InputSchema json.RawMessage `json:"inputSchema"`
		score       int
	}
	var matches []result
	terms := strings.FieldsFunc(strings.ToLower(query), func(c rune) bool { return unicode.IsSpace(c) || c == '_' || c == '-' })
	for _, tool := range r.toolsFor(ctx) {
		if exact != "" && tool.Ident != exact {
			continue
		}
		score := 0
		name, description := strings.ToLower(tool.Ident), strings.ToLower(tool.Description)
		for _, term := range terms {
			if strings.Contains(name, term) {
				score += 4
			}
			if strings.Contains(description, term) {
				score++
			}
		}
		if name == strings.ToLower(query) {
			score += 20
		}
		if exact == "" && score == 0 {
			continue
		}
		schema := tool.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{}`)
		}
		matches = append(matches, result{tool.Ident, tool.Description, "tools." + tool.Ident + "(" + RenderType(schema) + "): Promise<unknown>", schema, score})
	}
	if exact != "" && len(matches) == 0 {
		return "", errors.New("unknown tool: " + exact)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].Name < matches[j].Name
		}
		return matches[i].score > matches[j].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	if matches == nil {
		matches = []result{}
	}
	encoded, err := json.Marshal(map[string]any{"tools": matches})
	return string(encoded), err
}
