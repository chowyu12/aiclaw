package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestNativeCatalogFieldsReachResolvedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		protocol model.APIProtocol
		body     string
		want     map[string]model.ModelCapabilities
	}{
		{model.APIAnthropic, `{"data":[{"id":"chat","max_input_tokens":200000,"capabilities":{"image_input":{"supported":true},"effort":{"supported":true,"high":{"supported":true},"max":{"supported":true},"xhigh":{"supported":false}}}},{"id":"legacy"}]}`,
			map[string]model.ModelCapabilities{"chat": {ContextWindow: 200000, Vision: "supported", Reasoning: "supported", ReasoningEfforts: "high,max"}}},
		{model.APIGemini, `{"models":[{"name":"models/chat","inputTokenLimit":1048576,"thinking":true},{"name":"models/legacy","thinking":false}]}`,
			map[string]model.ModelCapabilities{"chat": {ContextWindow: 1048576, Reasoning: "supported"}, "legacy": {Reasoning: "unsupported"}}},
	} {
		t.Run(string(tc.protocol), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := NewConfigured(Config{BaseURL: server.URL, APIKey: "fixture", Protocol: tc.protocol})
			if err != nil {
				t.Fatal(err)
			}
			names, capabilities, err := client.ListModelsAndMetadata(context.Background())
			if err != nil || !reflect.DeepEqual(names, []string{"chat", "legacy"}) || !reflect.DeepEqual(capabilities, tc.want) {
				t.Fatalf("names=%v capabilities=%+v err=%v", names, capabilities, err)
			}
		})
	}
}
