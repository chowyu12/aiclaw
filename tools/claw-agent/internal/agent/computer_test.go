package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
			model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_screenshot", `{"pid":123,"window_id":456}`}), sseText("cannot observe")}}
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
	model := &fakeModel{script: []string{sseToolCalls([3]string{"c1", "computer_type", `{"pid":123,"window_id":456,"text":"北京天气"}`}), sseText("verified")}}
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
		sseToolCalls([3]string{"c1", "computer_list_apps", `{}`}, [3]string{"c2", "computer_list_windows", `{"app":"Fixture"}`}, [3]string{"c3", "computer_screenshot", `{"pid":123,"window_id":456,"include_screenshot":false}`}), sseText("observed"),
	}}
	session := computerSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{computerOK: true, computerResult: protocol.ComputerResult{Text: "tree or app inventory"}}
	session.RunTurn(context.Background(), "t1", "inspect", nil, nil, emitter)
	if len(emitter.approvals) != 0 || len(emitter.computer) != 3 {
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
		for _, args := range []string{`{"pid":0,"window_id":456}`, `{"pid":123,"window_id":-1}`, `{"pid":123}`, `{"pid":123,"window_id":456,"scope":"desktop"}`, `{"pid":123,"window_id":456,"session":"other"}`} {
			if validateComputerArgs(json.RawMessage(args), spec.Schema) == nil {
				t.Fatalf("admitted %s", args)
			}
		}
	}
}

func TestComputerSchemasFollowNativePlatformCapabilities(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			specs, err := computerToolSpecs(platform)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range specs {
				if spec.Name != "computer_click" {
					continue
				}
				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				if err := json.Unmarshal(spec.Schema, &schema); err != nil {
					t.Fatal(err)
				}
				_, hasAction := schema.Properties["action"]
				if hasAction != (platform != "linux") {
					t.Fatalf("%s click.action advertised=%t", platform, hasAction)
				}
				args := json.RawMessage(`{"pid":123,"window_id":456,"action":"show_menu"}`)
				err = validateComputerArgs(args, spec.Schema)
				if (err != nil) != (platform == "linux") {
					t.Fatalf("%s accepted unsupported action: %v", platform, err)
				}
				if err := validateComputerArgs(json.RawMessage(`{"pid":123,"window_id":456,"element_token":"s00000001:1"}`), spec.Schema); err != nil {
					t.Fatalf("common click rejected on %s: %v", platform, err)
				}
			}
		})
	}
}
