package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
	"github.com/chowyu12/aiclaw/pkg/sse"
)

func TestWriteStreamChunkUsesDedicatedHarnessEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, ok := sse.NewWriter(recorder)
	if !ok {
		t.Fatal("expected SSE writer")
	}

	event := harnesspkg.Event{Version: harnesspkg.ProtocolVersion, Type: harnesspkg.EventModelDelta, Delta: "verified"}
	if err := writeStreamChunk(writer, model.StreamChunk{HarnessEvent: event}); err != nil {
		t.Fatalf("write harness event: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: harness\n") {
		t.Fatalf("expected harness event, got %q", body)
	}
	if strings.Contains(body, "harness_event") {
		t.Fatalf("harness event must not be wrapped in the legacy message payload: %q", body)
	}
}

func TestWriteStreamChunkKeepsLegacyMessageEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, ok := sse.NewWriter(recorder)
	if !ok {
		t.Fatal("expected SSE writer")
	}

	if err := writeStreamChunk(writer, model.StreamChunk{Delta: "hello"}); err != nil {
		t.Fatalf("write legacy chunk: %v", err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: message\n") || !strings.Contains(body, `"delta":"hello"`) {
		t.Fatalf("expected legacy message event, got %q", body)
	}
}
