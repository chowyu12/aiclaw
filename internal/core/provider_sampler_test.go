package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

type staticProviderResolver struct {
	provider *model.Provider
}

func TestProviderSamplerSendsOpenAICompatibleMultimodalJSON(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "pixel.png")
	if err := os.WriteFile(imagePath, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	var received []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 1 {
			t.Fatalf("messages = %d", len(request.Messages))
		}
		if err := json.Unmarshal(request.Messages[0].Content, &received); err != nil {
			t.Fatalf("multimodal content is not an array: %v (%s)", err, request.Messages[0].Content)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	sampler := ProviderSampler{Resolver: staticProviderResolver{provider: &model.Provider{
		ID: 1, Name: "test", Type: model.ProviderOpenAICompat, BaseURL: server.URL, APIKey: "test",
	}}}
	_, err := sampler.Sample(context.Background(), SamplingRequest{
		Thread: model.Thread{ProviderID: 1, ModelName: "vision-model"},
		Messages: []SamplingMessage{{Role: "user", Content: "inspect", Attachments: []*model.File{
			{Filename: "notes.txt", ContentType: "text/plain", FileType: model.FileTypeText, TextContent: "local evidence"},
			{Filename: "pixel.png", ContentType: "image/png", FileType: model.FileTypeImage, StoragePath: imagePath},
		}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 2 || received[0].Type != "text" || !strings.Contains(received[0].Text, "local evidence") {
		t.Fatalf("text content missing: %#v", received)
	}
	if received[1].Type != "image_url" || received[1].ImageURL == nil || !strings.HasPrefix(received[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image content missing: %#v", received)
	}
}

func (r staticProviderResolver) GetProvider(context.Context, int64) (*model.Provider, error) {
	return r.provider, nil
}

func TestProviderSamplerEmitsIncrementalDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.Stream {
			t.Fatal("provider request did not enable streaming")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, data := range []string{
			`{"id":"chunk","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"你"}}]}`,
			`{"id":"chunk","object":"chat.completion.chunk","created":1,"model":"test","choices":[{"index":0,"delta":{"content":"好"},"finish_reason":"stop"}]}`,
			`{"id":"chunk","object":"chat.completion.chunk","created":1,"model":"test","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	sampler := ProviderSampler{Resolver: staticProviderResolver{provider: &model.Provider{
		ID: 1, Name: "test", Type: model.ProviderOpenAICompat, BaseURL: server.URL, APIKey: "test",
	}}}
	var deltas []string
	result, err := sampler.Sample(context.Background(), SamplingRequest{
		Thread:   model.Thread{ProviderID: 1, ModelName: "test"},
		Messages: []SamplingMessage{{Role: "user", Content: "hello"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 || deltas[0] != "你" || deltas[1] != "好" {
		t.Fatalf("deltas = %#v, want incremental chunks", deltas)
	}
	if result.Text != "你好" || result.Tokens != 5 {
		t.Fatalf("result = %+v", result)
	}
}
