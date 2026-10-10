package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func TestToolArtifactsSurviveSaveLoad(t *testing.T) {
	model := &fakeModel{script: []string{sseToolCall("image", "fixture_image", `{}`), sseText("saved")}}
	s := newTestSession(t, model, protocol.ApprovalOnWrite)
	path := "generated/cat.png"
	if err := s.registry.Register(tools.Tool{Name: "fixture_image", Effect: tools.EffectRead, Handler: func(ctx context.Context, _ json.RawMessage, _ *tools.Env) (string, error) {
		tools.Produce(ctx, path)
		return "saved", nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s.RunTurn(ctx, "turn", "draw a cat", nil, nil, &recordingEmitter{approve: true})
	db := newTestStore(t)
	if err := s.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loaded.Close)
	for _, item := range loaded.History() {
		if item.Kind == protocol.ItemToolCall && item.ToolName == "fixture_image" {
			if !reflect.DeepEqual(item.Artifacts, []string{path}) {
				t.Fatalf("restored artifacts: %v", item.Artifacts)
			}
			return
		}
	}
	t.Fatal("missing restored image tool")
}

func TestDocumentArtifactsSurviveCodeModeAndSaveLoad(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		name, args := "write_file", `{"path":"report.md","content":"# Report"}`
		if codeMode {
			name, args = "exec", `{"code":"text(await tools.write_file({path: 'report.md', content: '# Report'}))"}`
		}
		model := &fakeModel{script: []string{sseToolCall("document", name, args), sseText("saved")}}
		s := newTestSession(t, model, protocol.ApprovalOnWrite)
		if codeMode {
			s.config.CodeMode = true
			s.installCodeMode()
		}
		ctx := context.Background()
		s.RunTurn(ctx, "turn", "write a report", nil, nil, &recordingEmitter{approve: true})
		db := newTestStore(t)
		if err := s.Save(ctx, db); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(loaded.Close)
		found := false
		for _, item := range loaded.History() {
			if item.Kind == protocol.ItemToolCall && item.ToolName == name {
				found = true
				if item.ToolFailed || !reflect.DeepEqual(item.Artifacts, []string{"report.md"}) {
					t.Fatalf("code mode %v: restored artifacts %v, failed %v, result %s", codeMode, item.Artifacts, item.ToolFailed, item.ToolResult)
				}
			}
		}
		if !found {
			t.Fatalf("code mode %v: missing document tool", codeMode)
		}
	}
}

func TestHistoryRestoresLegacyCodeModeImage(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	path := "generated/image-20261009-112157.png"
	// Actual legacy exec output: text(JSON.stringify(generate_image result)).
	output, _ := json.Marshal("已生成并保存到 " + path + "（1235 KB）。画面在下一条消息里。")
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "image", Name: "exec", Arguments: `{"code":"text(await tools.generate_image({prompt: 'cat'}))"}`}}})
	s.appendMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "image", Content: string(output)})
	ctx := context.Background()
	db := newTestStore(t)
	if err := s.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loaded.Close)
	for _, item := range loaded.History() {
		if item.Kind == protocol.ItemToolCall && item.ToolName == "exec" {
			if !reflect.DeepEqual(item.Artifacts, []string{path}) {
				t.Fatalf("legacy Code Mode image not restored: %v", item.Artifacts)
			}
			return
		}
	}
	t.Fatal("missing Code Mode step")
}

func TestLegacyGeneratedImageArtifacts(t *testing.T) {
	path := "generated/image-20261009-112157.png"
	for _, output := range []string{
		"已生成并保存到 " + path + "（123 KB）。画面在下一条消息里。",
		"Generated and saved to " + path + " (123 KB). The image is in the next message.",
	} {
		if got := historyArtifacts("generate_image", output, nil); !reflect.DeepEqual(got, []string{path}) {
			t.Fatalf("legacy artifact: %v", got)
		}
		if got := historyArtifacts("read_file", output, nil); got != nil {
			t.Fatalf("inferred image from unrelated tool: %v", got)
		}
	}
	if got := historyArtifacts("generate_image", "saved /private/file.png", nil); got != nil {
		t.Fatalf("inferred nonstandard image path: %v", got)
	}
	second := "generated/image-20261009-112158.png"
	firstResult := "Generated and saved to " + path + " (123 KB). The image is in the next message."
	encoded, _ := json.Marshal(firstResult)
	output := string(encoded) + "\n" + firstResult + "\nGenerated and saved to " + second + " (42 KB). The image is in the next message."
	if got := historyArtifacts("exec", output, nil); !reflect.DeepEqual(got, []string{path, second}) {
		t.Fatalf("legacy Code Mode multiple images: %v", got)
	}
	if got := historyArtifacts("exec", "Error: "+firstResult, nil); got != nil {
		t.Fatalf("inferred image from failed output: %v", got)
	}
}
