package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestComputerRejectsIncompleteHostSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result protocol.ComputerResult
	}{
		{"approval-shaped or empty", protocol.ComputerResult{}},
		{"screenshot missing", protocol.ComputerResult{Text: "done"}},
		{"invalid image", protocol.ComputerResult{Text: "done", ImageBase64: "not base64"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_screenshot", `{"app":"com.example.fixture"}`}), sseText("cannot observe")}}
			session := computerSession(t, model, protocol.ApprovalBypass)
			emitter := &recordingEmitter{computerOK: true, computerResult: tc.result}
			session.RunTurn(context.Background(), "t1", "observe", nil, nil, emitter)
			for _, message := range session.messages {
				if message.Role == llm.RoleTool {
					if !strings.HasPrefix(message.Content, "错误：") {
						t.Fatalf("false success: %s", message.Content)
					}
					return
				}
			}
			t.Fatal("missing tool result")
		})
	}
}

func TestComputerInputObservationReachesModel(t *testing.T) {
	image := []byte("post-action screenshot")
	model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_type", `{"app":"com.example.fixture","observation_id":"o1","text":"北京天气"}`}), sseText("verified")}}
	session := computerSession(t, model, protocol.ApprovalBypass)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "effect unverifiable; new window state", ImageBase64: base64.StdEncoding.EncodeToString(image)}}
	session.RunTurn(context.Background(), "t1", "type", nil, nil, emitter)
	if len(emitter.computer) != 1 || emitter.computer[0].Action != "type_text" {
		t.Fatalf("wrong driver operation: %+v", emitter.computer)
	}
	for _, message := range session.messages {
		if message.Role == llm.RoleUser && len(message.Images) == 1 && string(message.Images[0]) == string(image) {
			return
		}
	}
	t.Fatal("post-action screenshot lost")
}

func TestComputerDiscoveryAndTreeOnlyObservationAreReadOnly(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "computer_list_apps", `{}`}, [3]string{"c3", "computer_screenshot", `{"app":"com.example.fixture","include_screenshot":false}`}), sseText("observed"),
	}}
	session := computerSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "tree or app inventory"}}
	session.RunTurn(context.Background(), "t1", "inspect", nil, nil, emitter)
	if len(emitter.approvals) != 0 || len(emitter.computer) != 2 {
		t.Fatalf("unexpected approval/dispatch: %+v %+v", emitter.approvals, emitter.computer)
	}
}

func TestComputerSchemaRejectsDesktopOverridesBeforeHostCall(t *testing.T) {
	var specs []computerToolSpec
	if err := json.Unmarshal(computerToolsJSON, &specs); err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if spec.Name != "computer_click" {
			continue
		}
		for _, args := range []string{`{}`, `{"app":""}`, `{"app":123}`, `{"app":"com.example.fixture"}`, `{"app":"com.example.fixture","observation_id":null}`, `{"app":"com.example.fixture","observation_id":"o1","scope":"desktop"}`, `{"app":"com.example.fixture","observation_id":"o1","session":"other"}`} {
			if validateComputerArgs(json.RawMessage(args), spec.Schema) == nil {
				t.Fatalf("admitted %s", args)
			}
		}
	}
}

func TestComputerSchemasUseNativeAppContract(t *testing.T) {
	specs, err := computerToolSpecs(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	expected := 0
	if runtime.GOOS == "darwin" {
		expected = 22
	}
	if len(specs) != expected {
		t.Fatalf("unexpected native tool count, got %d", len(specs))
	}
	for _, spec := range specs {
		if spec.Action == "get_window_state" || spec.Action == "list_windows" {
			t.Fatal("old driver tool exposed")
		}
		if spec.Name == "computer_click" {
			if err := validateComputerArgs(json.RawMessage(`{"app":"com.example.fixture","observation_id":"o1","element_index":"1"}`), spec.Schema); err != nil {
				t.Fatal(err)
			}
			if err := validateComputerArgs(json.RawMessage(`{"app":"com.example.fixture","observation_id":"o1","element_token":"s00000001:1"}`), spec.Schema); err == nil {
				t.Fatal("old driver token admitted")
			}
		}
	}
}

func TestComputerQueryAndDeliveryOptionsAreMacOnly(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		specs, err := computerToolSpecs(platform)
		if err != nil {
			t.Fatal(err)
		}
		query, native := false, false
		for _, spec := range specs {
			if spec.Action == "query" {
				query = spec.ReadOnly
			}
			if spec.Action == "click" {
				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				if err := json.Unmarshal(spec.Schema, &schema); err != nil {
					t.Fatal(err)
				}
				_, native = schema.Properties["duration"]
			}
		}
		if query != (platform == "darwin") || native != (platform == "darwin") {
			t.Fatalf("wrong capabilities on %s: query=%v method=%v", platform, query, native)
		}
	}
}

func TestComputerWorkflowInsideOneExec(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		approve, hostOK, verificationFailed bool
		wantCalls, wantApprovals            int
	}{
		{"sequential workflow", true, true, false, 5, 2},
		{"denied input stops", false, true, false, 1, 1},
		{"host error stops", true, false, false, 1, 0},
		{"unknown verification stops", true, true, true, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := `const app="com.example.fixture";
    const first=await tools.computer_query({app,text:"Input"});
    if (!first.includes("observation_id: o1")) throw Error("missing observation");
    await tools.computer_set_value({app,element_index:"1",observation_id:"o1",value:"hello",verify_after:false});
    await tools.computer_query({app,text:"Submit"});
    await tools.computer_click({app,element_index:"1",observation_id:"o1",verify_after:false});
    text(await tools.computer_screenshot({app}));`
			raw, err := json.Marshal(map[string]string{"title": "Fill and submit fixture", "code": code})
			if err != nil {
				t.Fatal(err)
			}
			model := &fakeModel{script: []string{sseToolCalls([3]string{"workflow", "exec", string(raw)}), sseText("finished")}}
			session := computerSession(t, model, protocol.ApprovalOnWrite, true)
			if !slices.Contains(session.FoldedTools(), "computer_query") || slices.Contains(session.Tools(), "computer_query") {
				t.Fatal("desktop tools must be folded into exec")
			}
			emitter := &recordingEmitter{approve: tc.approve, computerOK: tc.hostOK, computerResult: protocol.ComputerResult{Text: "observation_id: o1", ImageBase64: base64.StdEncoding.EncodeToString([]byte("fixture")), VerificationFailed: tc.verificationFailed}}
			session.RunTurn(context.Background(), "t1", "Fill and submit", nil, nil, emitter)
			if model.calls != 2 || len(emitter.computer) != tc.wantCalls || len(emitter.approvals) != tc.wantApprovals {
				t.Fatalf("model calls=%d dispatches=%d approvals=%d", model.calls, len(emitter.computer), len(emitter.approvals))
			}
		})
	}
}

type cancellingComputerWorkflowEmitter struct {
	*recordingEmitter
	cancel context.CancelFunc
}

func (e *cancellingComputerWorkflowEmitter) RequestComputer(ctx context.Context, params protocol.ComputerRequestParams) (protocol.ComputerResult, error) {
	result, err := e.recordingEmitter.RequestComputer(ctx, params)
	if params.Action == "set_value" {
		e.cancel()
		return protocol.ComputerResult{}, ctx.Err()
	}
	return result, err
}

func TestComputerWorkflowCancellationDoesNotDispatchNextAction(t *testing.T) {
	code := `await tools.computer_query({app:"com.example.fixture",text:"Input"});
 await tools.computer_set_value({app:"com.example.fixture",element_index:"1",observation_id:"o1",value:"hello",verify_after:false});
 await tools.computer_click({app:"com.example.fixture",element_index:"1",observation_id:"o1"});`
	raw, err := json.Marshal(map[string]string{"title": "Fill fixture", "code": code})
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{script: []string{sseToolCalls([3]string{"workflow", "exec", string(raw)}), sseText("stopped")}}
	session := computerSession(t, model, protocol.ApprovalOnWrite, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter := &cancellingComputerWorkflowEmitter{recordingEmitter: &recordingEmitter{approve: true, computerOK: true, computerResult: protocol.ComputerResult{Text: "observation_id: o1"}}, cancel: cancel}
	session.RunTurn(ctx, "t1", "Fill fixture", nil, nil, emitter)
	if len(emitter.computer) != 2 || emitter.computer[1].Action != "set_value" {
		t.Fatalf("cancelled script continued: %+v", emitter.computer)
	}
}
