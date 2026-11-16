package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/codemode"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
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

func TestCodeModeLocalAudioIsMaterializedAndRestoresAfterSourceRemoval(t *testing.T) {
	e := &recordingEmitter{approve: true}
	s := codeModeSession(t, e)
	source := filepath.Join(s.config.Workdir, "source.wav")
	if err := os.WriteFile(source, []byte("fixture-audio"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"code": `audio({path:'source.wav',mimeType:'audio/wav'});`})
	call := llm.ToolCall{ID: "local-audio", Name: "exec", Arguments: string(args)}
	s.appendMessage(llm.Message{ID: "assistant-audio", Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}})
	result := s.executeOne(context.Background(), "turn", call, s.turnEnv("turn", e), e, &stepCounter{})
	s.appendMessage(result)
	if len(result.OutputContent) != 1 || len(result.Artifacts) != 1 || result.OutputContent[0].Type != "audio" {
		t.Fatalf("%+v", result)
	}
	os.Remove(source)
	saved, err := os.ReadFile(filepath.Join(s.config.Workdir, result.Artifacts[0]))
	if err != nil || string(saved) != "fixture-audio" {
		t.Fatalf("saved %q %v", saved, err)
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
			if len(item.Content) != 1 || !strings.HasPrefix(item.Content[0].URL, "data:audio/wav;base64,") {
				t.Fatalf("%+v", item)
			}
			return
		}
	}
	t.Fatal("missing persisted local audio")
}

func TestCodeModeAudioRejectsProtectedMissingLargeAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	env := &tools.Env{Workspace: root, Home: root, ProtectedPaths: []string{filepath.Join(root, "private.wav")}}
	for _, path := range []string{"private.wav", "missing.wav", "."} {
		if _, err := readCodeAudio(context.Background(), env, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	file, err := os.Create(filepath.Join(root, "large.wav"))
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(8*1024*1024 + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := readCodeAudio(context.Background(), env, "large.wav"); err == nil {
		t.Fatal("oversized audio accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "private.wav"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "private.wav"), filepath.Join(root, "alias.wav")); err == nil {
		if _, err := readCodeAudio(context.Background(), env, "alias.wav"); err == nil {
			t.Fatal("protected symlink read")
		}
	}
}

func TestCodeModeFileAudioSharesEncodedObservationBudget(t *testing.T) {
	e := &recordingEmitter{approve: true}
	s := codeModeSession(t, e)
	source := filepath.Join(s.config.Workdir, "large-valid.wav")
	if err := os.WriteFile(source, make([]byte, 6*1024*1024), 0600); err != nil {
		t.Fatal(err)
	}
	item := codemode.OutputItem{Type: "audio", Path: source, MIME: "audio/wav"}
	_, err := s.codeObservation(context.Background(), s.turnEnv("turn", e), codemode.Observation{Media: []codemode.OutputItem{item, item}})
	if err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("encoded budget not enforced: %v", err)
	}
}

func TestCancelledBeforeAdmissionCannotReopenAutomaticWakes(t *testing.T) {
	s := codeModeSession(t, &recordingEmitter{approve: true})
	s.Interrupt()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.RunTurn(ctx, "cancelled-before-start", "input", nil, nil, &recordingEmitter{approve: true})
	if _, allowed := s.ReserveWake(); allowed {
		t.Fatal("cancelled admission reopened automatic wakes")
	}
}
