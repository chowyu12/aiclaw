package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestDocumentWritesProduceArtifacts(t *testing.T) {
	for _, test := range []struct{ name, path, args string }{
		{"write_file", "报告.md", `{"path":"报告.md","content":"# Report"}`},
		{"write_docx", "报告.docx", `{"path":"报告.docx","content":"# Report"}`},
		{"write_xlsx", "报告.xlsx", `{"path":"报告.xlsx","sheets":[{"name":"Report","rows":[["Value"]]}]}`},
		{"write_pptx", "报告.pptx", `{"path":"报告.pptx","slides":[{"title":"Report"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, approved := range []bool{true, false} {
				env, _ := newEnv(t, protocol.ApprovalAlways, approved)
				registry := officeRegistry(t)
				tool, _ := registry.Get(test.name)
				sink := &ArtifactSink{}
				_, err := tool.Handler(WithArtifacts(context.Background(), sink), json.RawMessage(test.args), env)
				if !approved {
					if err == nil || len(sink.Paths()) != 0 {
						t.Fatalf("refused write produced artifacts: %v, %v", sink.Paths(), err)
					}
					continue
				}
				if err != nil || len(sink.Paths()) != 1 || sink.Paths()[0] != test.path {
					t.Fatalf("artifacts = %v, error = %v", sink.Paths(), err)
				}
				if _, err := os.Stat(filepath.Join(env.Workspace, test.path)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
