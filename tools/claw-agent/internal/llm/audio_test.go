package llm

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/internal/model"
	"strings"
	"testing"
)

func TestAudioNativeWireAndUnsupportedProtocols(t *testing.T) {
	for _, api := range []model.APIProtocol{model.APIChatCompletions, model.APIGemini} {
		c, err := NewConfigured(Config{APIKey: "fixture", BaseURL: "https://example.com/v1", Protocol: api, Capabilities: model.ModelCapabilities{AudioInput: "supported"}})
		if err != nil {
			t.Fatal(err)
		}
		req := Request{Model: "fixture", Messages: []Message{{Role: RoleUser, Content: "listen", Audio: []Audio{{Path: "private-path.wav", Format: "wav", DurationMS: 1000, Data: []byte{1, 2, 3}}}, Sampling: nil}}}
		_, wire, err := c.adapter.encode(c, req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(wire)
		body := string(raw)
		if !strings.Contains(body, "AQID") || strings.Contains(body, "private-path") || strings.Contains(body, "durationMs") {
			t.Fatalf("bad wire %s", body)
		}
		key := "input_audio"
		if api == model.APIGemini {
			key = "inlineData"
		}
		if !strings.Contains(body, key) {
			t.Fatal(body)
		}
	}
	c, _ := NewConfigured(Config{APIKey: "fixture", BaseURL: "https://example.com/v1", Protocol: model.APIResponses, Capabilities: model.ModelCapabilities{AudioInput: "supported"}})
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: RoleUser, Audio: []Audio{{Format: "wav", Data: []byte{1}}}}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "native audio") {
		t.Fatalf("unsupported route %v", err)
	}
}
