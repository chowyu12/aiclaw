package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestComputerRejectsIncompleteHostSuccess(t *testing.T) {
	for _, result := range []protocol.ComputerResult{{}, {Text: "done"}, {Text: "done", ImageBase64: "not base64"}} {
		model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_get_window_state", `{"pid":123,"window_id":456}`}), sseText("cannot observe")}}
		session := computerSession(t, model, protocol.ApprovalBypass)
		emitter := &recordingEmitter{computerOK: true, computerResult: result}
		session.RunTurn(context.Background(), "t1", "observe", nil, nil, emitter)
		found := false
		for _, message := range session.messages {
			if message.Role == llm.RoleTool {
				found = true
				if !strings.HasPrefix(message.Content, "错误：") {
					t.Fatalf("false success: %s", message.Content)
				}
			}
		}
		if !found {
			t.Fatal("missing tool result")
		}
	}
}
func TestCuaContractDoesNotExposeForegroundScriptsOrModelSessions(t *testing.T) {
	specs, err := computerToolSpecs("darwin")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 19 {
		t.Fatalf("tools=%d", len(specs))
	}
	for _, spec := range specs {
		if spec.Action == "run_script" || spec.Action == "bring_to_front" || spec.Action == "get_desktop_state" {
			t.Fatalf("unsafe action: %s", spec.Action)
		}
		var schema struct{ Properties map[string]json.RawMessage }
		if err := json.Unmarshal(spec.Schema, &schema); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"session", "delivery_mode", "foreground_fallback", "scope", "target", "prompt", "screenshot_out_file"} {
			if _, ok := schema.Properties[key]; ok {
				t.Fatalf("model override: %s.%s", spec.Action, key)
			}
		}
		if spec.Action == "click" {
			for _, raw := range []string{`{}`, `{"pid":123}`, `{"pid":123,"window_id":-1}`, `{"pid":123,"window_id":456,"delivery_mode":"foreground"}`, `{"pid":123,"window_id":456,"session":"other"}`} {
				if validateComputerArgs(json.RawMessage(raw), spec.Schema) == nil {
					t.Fatalf("admitted %s", raw)
				}
			}
			if err := validateComputerArgs(json.RawMessage(`{"pid":123,"window_id":456,"element_token":"snapshot:1"}`), spec.Schema); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestComputerDiscoveryAndTreeOnlyObservationAreReadOnly(t *testing.T) {
	model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_list_windows", `{}`}, [3]string{"c2", "computer_get_window_state", `{"pid":123,"window_id":456,"include_screenshot":false}`}), sseText("observed")}}
	session := computerSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "window inventory"}}
	session.RunTurn(context.Background(), "t1", "inspect", nil, nil, emitter)
	if len(emitter.approvals) != 0 || len(emitter.computer) != 2 {
		t.Fatalf("approvals=%d dispatch=%d", len(emitter.approvals), len(emitter.computer))
	}
}
func TestCuaBatchIsOneApprovedDispatchInsideCodeMode(t *testing.T) {
	for _, approve := range []bool{true, false} {
		code := `text(await tools.computer_run_steps({pid:123,window_id:456,steps:[{tool:"set_value",args:{role:"textfield",name:"Input",value:"中文"}},{tool:"click",args:{role:"button",name:"Save"},expect:[{text:"Saved"}]}],observe:{include_screenshot:true}}));`
		raw, _ := json.Marshal(map[string]string{"title": "Fill fixture", "code": code})
		model := &fakeModel{script: []string{sseToolCalls([3]string{"batch", "exec", string(raw)}), sseText("finished")}}
		session := computerSession(t, model, protocol.ApprovalOnWrite, true)
		if !slices.Contains(session.FoldedTools(), "computer_run_steps") || slices.Contains(session.Tools(), "computer_run_steps") {
			t.Fatal("batch must be folded into exec")
		}
		emitter := &recordingEmitter{approve: approve, computerOK: true, computerResult: protocol.ComputerResult{Text: "2/2 steps ok", ImageBase64: base64.StdEncoding.EncodeToString([]byte("fixture"))}}
		session.RunTurn(context.Background(), "t1", "Fill fixture", nil, nil, emitter)
		expected := 0
		if approve {
			expected = 1
		}
		if len(emitter.computer) != expected || len(emitter.approvals) != 1 {
			t.Fatalf("dispatch=%d approvals=%d", len(emitter.computer), len(emitter.approvals))
		}
	}
}

func TestComputerBackgroundOnly(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"执行过程中保持后台操作，不要切换我的前台窗口或移动鼠标", true},
		{"只在后台完成，失败就停止", true},
		{"background-only, do not switch to the foreground", true},
		{"后台优先，必要时可以前台操作", false},
	} {
		if got := computerBackgroundOnly(tc.input); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.input, got, tc.want)
		}
	}
}

func TestComputerBackgroundPolicyPropagatesThroughCodeMode(t *testing.T) {
	code := `text(await tools.computer_list_windows({}));`
	raw, _ := json.Marshal(map[string]string{"title": "List", "code": code})
	model := &fakeModel{script: []string{sseToolCalls([3]string{"call", "exec", string(raw)}), sseText("done")}}
	session := computerSession(t, model, protocol.ApprovalBypass, true)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "windows"}}
	session.RunTurn(context.Background(), "turn", "请严格后台，不要切换我的前台窗口", nil, nil, emitter)
	if len(emitter.computer) != 1 || !emitter.computer[0].BackgroundOnly {
		t.Fatalf("background restriction not propagated: %+v", emitter.computer)
	}
}

func TestComputerTurnDoesNotRememberWithoutRequest(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_list_windows", `{}`}),
		sseToolCalls([3]string{"c2", "remember", `{"text":"test observation","scope":"workspace"}`}),
		sseText("done"),
	}}
	session := computerSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "windows"}}
	session.RunTurn(context.Background(), "turn", "请操作桌面", nil, nil, emitter)
	found := false
	for _, m := range session.messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "unless the user explicitly requests") {
			found = true
		}
	}
	if !found {
		t.Fatal("unsolicited memory write was not refused")
	}
}
