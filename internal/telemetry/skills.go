// Package telemetry exports only allowlisted skill invocation metadata.
package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	collector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const SkillEvent = "aiclaw.skill_invocation"

// New uses the standard OTLP HTTP exporter environment configuration. Export is
// opt-in; never install a bridge that could send ordinary application logs.
func New(ctx context.Context, version string) (*sdklog.LoggerProvider, error) {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return nil, nil
	}
	switch strings.TrimSpace(os.Getenv("OTEL_LOGS_EXPORTER")) {
	case "", "none":
		return nil, nil
	case "otlp":
	default:
		return nil, fmt.Errorf("OTEL_LOGS_EXPORTER must be none or otlp")
	}
	protocol := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL")
	if protocol == "" {
		protocol = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if protocol != "" && protocol != "http/protobuf" {
		return nil, fmt.Errorf("skill logs require OTLP http/protobuf")
	}
	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithCompression(otlploghttp.NoCompression),
		otlploghttp.WithHTTPClient(&http.Client{Timeout: 10 * time.Second, Transport: skillTransport{version: version}}),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize skill log exporter")
	}
	// The SDK still merges environment attributes; skillTransport replaces the
	// resource at the wire boundary before any data reaches the collector.
	res := resource.NewWithAttributes("", attribute.String("service.name", "aiclaw-kernel"), attribute.String("service.version", version))
	return sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter))), nil
}

type skillTransport struct{ version string }

func (t skillTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("read skill telemetry request")
	}
	var payload collector.ExportLogsServiceRequest
	if err := proto.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode skill telemetry request")
	}
	for _, logs := range payload.ResourceLogs {
		logs.Resource = &otlpresource.Resource{Attributes: []*common.KeyValue{
			{Key: "service.name", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: "aiclaw-kernel"}}},
			{Key: "service.version", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: t.version}}},
		}}
		logs.SchemaUrl = ""
	}
	raw, err = proto.Marshal(&payload)
	if err != nil {
		return nil, fmt.Errorf("encode skill telemetry request")
	}
	clean := request.Clone(request.Context())
	clean.Body = io.NopCloser(bytes.NewReader(raw))
	clean.ContentLength = int64(len(raw))
	clean.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	return http.DefaultTransport.RoundTrip(clean)
}

// EmitSkill accepts no prompt, body, arguments, output, resource path or identity.
func EmitSkill(logger log.Logger, sessionID, turnID, model, name, invocation string) {
	if logger == nil || (invocation != "explicit" && invocation != "implicit") {
		return
	}
	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetEventName(SkillEvent)
	record.SetSeverity(log.SeverityInfo)
	record.SetBody(attribute.StringValue(SkillEvent))
	record.AddAttributes(
		attribute.String("event.name", SkillEvent),
		attribute.String("conversation.id", sessionID),
		attribute.String("turn.id", turnID),
		attribute.String("model", model),
		attribute.String("skill.name", name),
		attribute.String("skill.invocation_type", invocation),
	)
	logger.Emit(context.Background(), record)
}
