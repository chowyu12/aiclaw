package codemode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// OutputItem keeps media outside text previews and their truncation budgets.
type OutputItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Hint string `json:"hint,omitempty"`
}

type Hooks struct {
	Output func(OutputItem) error
	Yield  func()
	Store  func(string, json.RawMessage) error
	Load   func(string) (json.RawMessage, error)
}
type hooksKey struct{}

func WithHooks(ctx context.Context, h Hooks) context.Context {
	return context.WithValue(ctx, hooksKey{}, h)
}
func hooksFrom(ctx context.Context) Hooks { h, _ := ctx.Value(hooksKey{}).(Hooks); return h }

const maxMediaBytes = 8 * 1024 * 1024

func mediaItem(kind string, value any) (OutputItem, error) {
	item := OutputItem{Type: kind}
	switch v := value.(type) {
	case string:
		item.URL = v
	case map[string]any:
		key := "image_url"
		if kind == "audio" {
			key = "audio_url"
		}
		item.URL, _ = v[key].(string)
		if item.URL == "" {
			data, _ := v["data"].(string)
			mime, _ := v["mimeType"].(string)
			if data != "" && mime != "" {
				item.URL = "data:" + mime + ";base64," + data
			}
		}
		item.Hint, _ = v["output_hint"].(string)
	default:
		return item, errors.New("expected a data URL or media content block")
	}
	prefix, data, ok := strings.Cut(item.URL, ",")
	mediaPrefix := "data:image/"
	if kind == "audio" {
		mediaPrefix = "data:audio/"
	}
	if !ok || !strings.HasPrefix(prefix, mediaPrefix) || !strings.HasSuffix(prefix, ";base64") {
		return item, errors.New("media must be a base64 data URL of the matching kind")
	}
	if len(data) > base64.StdEncoding.EncodedLen(maxMediaBytes) {
		return item, errors.New("media exceeds 8 MiB")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 {
		return item, errors.New("invalid or empty base64 media")
	}
	return item, nil
}
