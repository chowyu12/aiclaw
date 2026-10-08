package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type skillRecorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (r *skillRecorder) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range records {
		r.records = append(r.records, record.Clone())
	}
	return nil
}
func (*skillRecorder) Shutdown(context.Context) error   { return nil }
func (*skillRecorder) ForceFlush(context.Context) error { return nil }

func TestSkillEventsObserveActualToolsAndResetEachTurn(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "code-mode"}[codeMode], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "SKILL.md")
			private := "private-skill-body-user@example.test"
			if err := os.WriteFile(path, []byte("---\nname: report\ndescription: private-description\n---\n"+private), 0o600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"path": path})
			read := sseToolCall("read", "read_file", string(args))
			if codeMode {
				script, _ := json.Marshal(map[string]string{"code": "await tools.read_file(" + string(args) + "); await tools.read_file(" + string(args) + ");"})
				read = sseToolCall("read", "exec", string(script))
			}
			model := &fakeModel{script: []string{
				sseToolCalls([3]string{"1", "load_skill", `{"name":"report"}`}, [3]string{"2", "load_skill", `{"name":"report"}`}, [3]string{"3", "load_skill", `{"name":"private-prompt-unknown"}`}),
				read, sseText("done"),
				sseToolCall("4", "load_skill", `{"name":"report"}`), sseText("done"),
			}}
			upstream := httptest.NewServer(http.HandlerFunc(model.handler))
			defer upstream.Close()
			recorder := &skillRecorder{}
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(recorder)))
			defer provider.Shutdown(context.Background())
			s, err := New(context.Background(), "session", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: upstream.URL, Model: "fake"}, Workdir: dir, SkillDirs: []string{dir}, CodeMode: codeMode}, StaticKey("private-api-key"), WithSkillLogger(provider.Logger("test")))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.RunTurn(context.Background(), "first", "use $report private-prompt", nil, nil, &recordingEmitter{approve: true})
			s.RunTurn(context.Background(), "second", "do the work", nil, nil, &recordingEmitter{approve: true})
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if len(recorder.records) != 3 {
				t.Fatalf("expected explicit + implicit, then implicit in next turn; got %d", len(recorder.records))
			}
			got := map[string]int{}
			for _, record := range recorder.records {
				attrs := map[string]string{}
				record.WalkAttributes(func(a attribute.KeyValue) bool { attrs[string(a.Key)] = a.Value.AsString(); return true })
				if len(attrs) != 6 || attrs["skill.name"] != "report" || attrs["conversation.id"] != "session" || attrs["model"] != "fake" {
					t.Fatalf("unexpected metadata: %v", attrs)
				}
				got[attrs["turn.id"]+":"+attrs["skill.invocation_type"]]++
				for _, value := range attrs {
					for _, secret := range []string{dir, private, "private-description", "private-prompt", "private-api-key"} {
						if strings.Contains(value, secret) {
							t.Fatal("private data in skill event")
						}
					}
				}
			}
			for _, key := range []string{"first:explicit", "first:implicit", "second:implicit"} {
				if got[key] != 1 {
					t.Fatalf("wrong detection or deduplication: %v", got)
				}
			}
		})
	}
}

func TestSkillMentionRequiresExactBoundaries(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: report\n---\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.loadSkills([]string{dir})
	provider := sdklog.NewLoggerProvider()
	defer provider.Shutdown(context.Background())
	s.skillLogger = provider.Logger("test")
	for _, input := range []struct {
		text     string
		explicit bool
	}{{"use $report", true}, {"($report)", true}, {"$reporting", false}, {"\\$report", false}, {"other$report", false}, {"x$report$report", false}, {"$reporting $report", true}} {
		u := s.newSkillUsage("turn")
		u.markExplicit(input.text)
		if u.explicit["report"] != input.explicit {
			t.Fatalf("wrong classification for %q", input.text)
		}
	}
}
