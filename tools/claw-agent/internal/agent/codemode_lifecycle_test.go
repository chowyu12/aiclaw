package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestCodeModeCatalogReinstallKeepsStoredValues(t *testing.T) {
	e := &recordingEmitter{approve: true}
	s := codeModeSession(t, e)
	if _, err := runScript(t, s, e, `store('rows',{count:7});`); err != nil {
		t.Fatal(err)
	}
	s.installCodeMode()
	out, err := runScript(t, s, e, `return load('rows').count;`)
	if err != nil || out != "7" {
		t.Fatalf("refresh lost state: %q %v", out, err)
	}
}

func TestCodeModeChildEventsPersistWithoutExtraModelMessages(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"title": "Check files", "code": `await tools.write_file({path:'nested.txt',content:'intermediate'}); await tools.read_file({path:'nested.txt'});return 'selected';`})
	model := &fakeModel{script: []string{sseToolCall("exec1", "exec", string(args)), sseText("finished")}}
	s := newTestSession(t, model, protocol.ApprovalOnWrite)
	s.config.CodeMode = true
	s.installCodeMode()
	e := &recordingEmitter{approve: true}
	s.RunTurn(context.Background(), "turn", "run", nil, nil, e)
	started, completed := 0, 0
	for _, event := range e.events {
		if !strings.Contains(event, `"parentId"`) {
			continue
		}
		if strings.HasPrefix(event, protocol.NotifyItemStarted+" ") {
			started++
		}
		if strings.HasPrefix(event, protocol.NotifyItemCompleted+" ") {
			completed++
		}
	}
	if started != 2 || completed != 2 {
		t.Fatalf("child events started=%d completed=%d", started, completed)
	}
	results := 0
	for _, message := range s.messages {
		if message.Role == llm.RoleTool {
			results++
			if message.Content != "selected" {
				t.Fatalf("intermediate result leaked to model history: %q", message.Content)
			}
		}
	}
	if results != 1 {
		t.Fatalf("extra model tool messages: %d", results)
	}
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	history, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	lastSeq := 0
	for _, item := range history {
		if item.Seq > 0 {
			if item.Seq != lastSeq+1 {
				t.Fatalf("non-monotonic restored sequence: %d after %d", item.Seq, lastSeq)
			}
			lastSeq = item.Seq
		}
		if item.ParentID != "" {
			children++
			if item.StartedAt == 0 || item.ToolFailed {
				t.Fatalf("bad restored child: %+v", item)
			}
		}
	}
	if children != 2 {
		t.Fatalf("restored %d children", children)
	}
}

func TestCodeModeWaitReusesCellAcrossObserverContexts(t *testing.T) {
	e := &recordingEmitter{approve: true}
	s := codeModeSession(t, e)
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := s.turnEnv("turn", e)
	env.RunContext = lifetime
	execTool, _ := s.registry.Get("exec")
	ctx, endObserver := context.WithCancel(context.Background())
	out, err := execTool.Handler(ctx, json.RawMessage(`{"title":"wait","code":"text('first');yield_control();await new Promise(resolve=>setTimeout(resolve,20));return 'second';"}`), env)
	endObserver()
	if err != nil || !strings.Contains(out, "cell_1") {
		t.Fatalf("exec=%q %v", out, err)
	}
	wait, _ := s.registry.Get("wait")
	out, err = wait.Handler(context.Background(), json.RawMessage(`{"cell_id":"cell_1","yield_time_ms":1000}`), env)
	if err != nil || out != "second" {
		t.Fatalf("wait=%q %v", out, err)
	}
}

func TestCodeModeMediaOutputRestoresTypedContent(t *testing.T) {
	e := &recordingEmitter{approve: true}
	s := codeModeSession(t, e)
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j6ioAAAAASUVORK5CYII="
	code := `image('data:image/png;base64,` + png + `');audio('data:audio/wav;base64,` + base64.StdEncoding.EncodeToString([]byte("fixture-audio")) + `');return 'emitted';`
	args, _ := json.Marshal(map[string]string{"title": "Emit media", "code": code})
	call := llm.ToolCall{ID: "media-call", Name: "exec", Arguments: string(args)}
	s.appendMessage(llm.Message{ID: "media-assistant", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}})
	env := s.turnEnv("media-turn", e)
	result := s.executeOne(context.Background(), "media-turn", call, env, e, &stepCounter{})
	s.appendMessage(result)
	if len(result.OutputContent) != 2 || len(env.TakeAttachments()) != 1 || len(result.Artifacts) != 1 {
		t.Fatalf("missing media metadata: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(s.config.Workdir, result.Artifacts[0])); err != nil {
		t.Fatal(err)
	}
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	history, err := StoredHistory(context.Background(), db, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range history {
		if item.ToolName == "exec" {
			if len(item.Content) != 2 {
				t.Fatalf("media content lost: %+v", item)
			}
			return
		}
	}
	t.Fatal("missing media tool history")
}
