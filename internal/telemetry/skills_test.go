package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func exporterEnv(t *testing.T, endpoint string) {
	t.Helper()
	for _, key := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_COMPRESSION", "OTEL_EXPORTER_OTLP_LOGS_COMPRESSION"} {
		t.Setenv(key, "")
	}
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", endpoint+"/v1/logs")
}

func TestOTLPExportsOnlySkillMetadata(t *testing.T) {
	received := make(chan *collector.ExportLogsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("wrong OTLP request: %s %s", r.URL.Path, r.Header.Get("Content-Type"))
		}
		raw, _ := io.ReadAll(r.Body)
		request := &collector.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(raw, request); err != nil {
			t.Error(err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	exporterEnv(t, server.URL)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "prompt=private-prompt,path=/private/resource,email=user@example.test")
	provider, err := New(context.Background(), "test-version")
	if err != nil || provider == nil {
		t.Fatalf("init: %v", err)
	}
	EmitSkill(provider.Logger("aiclaw.skills"), "session", "turn", "model", "report", "explicit")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if len(got.ResourceLogs) != 1 || len(got.ResourceLogs[0].ScopeLogs) != 1 {
			t.Fatalf("wrong request: %v", got)
		}
		resource := got.ResourceLogs[0].Resource
		if len(resource.Attributes) != 2 {
			t.Fatalf("unexpected resource metadata: %v", resource)
		}
		logs := got.ResourceLogs[0].ScopeLogs[0].LogRecords
		if len(logs) != 1 || logs[0].EventName != SkillEvent || logs[0].Body.GetStringValue() != SkillEvent {
			t.Fatalf("wrong event: %v", logs)
		}
		attrs := map[string]string{}
		for _, a := range logs[0].Attributes {
			attrs[a.Key] = a.Value.GetStringValue()
		}
		want := map[string]string{"event.name": SkillEvent, "conversation.id": "session", "turn.id": "turn", "model": "model", "skill.name": "report", "skill.invocation_type": "explicit"}
		if !reflect.DeepEqual(attrs, want) {
			t.Fatalf("attributes: %v", attrs)
		}
		for _, private := range []string{"private-prompt", "/private/resource", "user@example.test"} {
			if strings.Contains(got.String(), private) {
				t.Fatalf("exported private data: %s", private)
			}
		}
	case <-ctx.Done():
		t.Fatal("no OTLP request")
	}
}

func TestDisabledExportMakesNoRequests(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	exporterEnv(t, server.URL)
	for _, setting := range []string{"", "none"} {
		t.Setenv("OTEL_LOGS_EXPORTER", setting)
		provider, err := New(context.Background(), "test")
		if err != nil || provider != nil {
			t.Fatalf("disabled setting initialized exporter: %v %v", provider, err)
		}
	}
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	if provider, err := New(context.Background(), "test"); err != nil || provider != nil {
		t.Fatal("SDK disabled was ignored")
	}
	if calls.Load() != 0 {
		t.Fatal("disabled telemetry made a request")
	}
}
