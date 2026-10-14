package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

func readOutputTest(t *testing.T, s *Session, id string, offset, limit int) map[string]any {
	t.Helper()
	tool, ok := s.registry.Get("read_tool_output")
	if !ok {
		t.Fatal("missing reader")
	}
	args, _ := json.Marshal(map[string]any{"output_id": id, "offset": offset, "limit": limit})
	out, err := tool.Handler(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	var page map[string]any
	if err = json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	return page
}
func TestRetainedOutputRestoresWithinOwningSession(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	db := persistTestSession(t, s)
	original := strings.Repeat("中文字段\n", 9000) + "TAIL_PERSONAL_DATA"
	id, err := s.saveToolOutput(context.Background(), original, true)
	if err != nil {
		t.Fatal(err)
	}
	restored := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	restored.SetPersistence(db)
	var collected strings.Builder
	offset := 0
	for {
		page := readOutputTest(t, restored, id, offset, 4096)
		text := page["text"].(string)
		if !utf8.ValidString(text) || page["complete"] != true {
			t.Fatal("bad page")
		}
		collected.WriteString(text)
		if page["hasMore"] == false {
			break
		}
		next := int(page["nextOffset"].(float64))
		if next <= offset {
			t.Fatal("no progress")
		}
		offset = next
	}
	if collected.String() != original {
		t.Fatal("lost full output tail or page boundary")
	}
	other := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	other.ID = "another-session"
	other.SetPersistence(db)
	reader, _ := other.registry.Get("read_tool_output")
	args, _ := json.Marshal(map[string]any{"output_id": id})
	if _, err = reader.Handler(context.Background(), args, nil); err == nil {
		t.Fatal("cross-session read")
	}
	reader, _ = s.registry.Get("read_tool_output")
	for _, args := range []string{`{"output_id":"../bad"}`, `{"output_id":"` + id + `","offset":1}`, `{"output_id":"` + id + `","offset":-1}`, `{"output_id":"` + id + `","limit":4097}`} {
		if _, err = reader.Handler(context.Background(), json.RawMessage(args), nil); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}
func TestRetainedOutputCapIsExplicit(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	id, err := s.saveToolOutput(context.Background(), strings.Repeat("a", tools.MaxRetainedOutput+1), true)
	if err != nil {
		t.Fatal(err)
	}
	page := readOutputTest(t, s, id, tools.MaxRetainedOutput-10, 4096)
	if page["complete"] != false || page["hasMore"] != false || page["totalBytes"] != float64(tools.MaxRetainedOutput) {
		t.Fatal(page)
	}
}
func TestLargeToolResultIncludesRecoverableReference(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	original := "HEAD" + strings.Repeat("middle ", 10000) + "TAIL"
	preview := s.historyOutput(context.Background(), original)
	if !strings.Contains(preview, "已截断") || !strings.Contains(preview, "read_tool_output(output_id=") || !strings.Contains(preview, "TAIL") {
		t.Fatal("missing reference or tail")
	}
}
func TestCodeModeCanPageItsFullPrintedOutput(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	s.installCodeMode()
	tool, _ := s.registry.Get("exec")
	env := s.turnEnv("turn", &recordingEmitter{})
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"code":"return 'x'.repeat(70000) + 'COMPLETE_TAIL'"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	marker := "output_id="
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatal("no retained reference")
	}
	id := out[start+len(marker) : start+len(marker)+32]
	args, _ := json.Marshal(map[string]string{"code": `return await tools.read_tool_output({output_id:"` + id + `",offset:69995});`})
	page, err := tool.Handler(context.Background(), args, env)
	if err != nil || !strings.Contains(page, "COMPLETE_TAIL") {
		t.Fatalf("%s %v", page, err)
	}
	if _, err = s.ContextStatus(); err != nil {
		t.Fatal(err)
	}
}

func TestCodeModeRetainsOversizedResultEvenWhenScriptSlicesIt(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	persistTestSession(t, s)
	register(t, s, tools.Tool{Name: "large_response", Effect: tools.EffectRead, Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) {
		return strings.Repeat("x", 50000) + "HIDDEN_TAIL", nil
	}})
	s.installCodeMode()
	tool, _ := s.registry.Get("exec")
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"code":"const r=await tools.large_response({}); return r.slice(0,10);"}`), s.turnEnv("t", &recordingEmitter{}))
	if err != nil {
		t.Fatal(err)
	}
	marker := "output_id="
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatal("sliced response lost recovery metadata")
	}
	id := out[start+len(marker) : start+len(marker)+32]
	args, _ := json.Marshal(map[string]string{"code": `return await tools.read_tool_output({output_id:"` + id + `",offset:49995});`})
	page, err := tool.Handler(context.Background(), args, s.turnEnv("t", &recordingEmitter{}))
	if err != nil || !strings.Contains(page, "HIDDEN_TAIL") {
		t.Fatalf("%s %v", page, err)
	}
}
