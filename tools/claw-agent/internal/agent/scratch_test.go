package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestSessionScratchFilesStayInExecutionHistoryOnly(t *testing.T) {
	ctx := context.Background()
	model := &fakeModel{}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	db := persistTestSession(t, session)
	if err := session.prepareScratchLocked(); err != nil {
		t.Fatal(err)
	}
	scratch := session.scratchDir
	script := filepath.Join(scratch, "helper.py")
	existing := filepath.Join(session.Workspace(), "existing.txt")
	if err := os.WriteFile(existing, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := func(fields map[string]string) string {
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	model.script = []string{
		sseToolCall("helper", "write_file", arguments(map[string]string{"path": script, "content": "print(1)"})),
		sseToolCall("update-helper", "edit_file", arguments(map[string]string{"path": script, "old_text": "print(1)", "new_text": "print(2)"})),
		sseToolCall("report", "write_file", `{"path":"report.md","content":"Result"}`),
		sseToolCall("update-project", "edit_file", `{"path":"existing.txt","old_text":"before","new_text":"after"}`),
		sseText("done"),
		sseText("still here"),
	}
	emitter := &recordingEmitter{}
	session.RunTurn(ctx, "first", "verify and deliver a report", nil, nil, emitter)
	if len(emitter.approvals) != 0 {
		t.Fatalf("unexpected approvals: %+v", emitter.approvals)
	}
	data, err := os.ReadFile(script)
	if err != nil || string(data) != "print(2)" {
		t.Fatalf("temporary script = %q, %v", data, err)
	}
	changes, err := db.Changes(ctx, session.ID)
	if err != nil || len(changes) != 2 {
		t.Fatalf("recorded changes = %+v, %v", changes, err)
	}
	for _, change := range changes {
		switch filepath.Base(change.Path) {
		case "report.md":
			if change.Existed || change.After != "Result" {
				t.Fatalf("unexpected deliverable change: %+v", change)
			}
		case "existing.txt":
			if !change.Existed || change.Before != "before" || change.After != "after" {
				t.Fatalf("unexpected project change: %+v", change)
			}
		default:
			t.Fatalf("unexpected recorded file: %s", change.Path)
		}
	}
	helperRecorded := false
	reportRecorded := false
	for _, event := range emitter.events {
		payload, ok := strings.CutPrefix(event, protocol.NotifyItemCompleted+" ")
		if !ok {
			continue
		}
		var notification protocol.ItemNotification
		if err := json.Unmarshal([]byte(payload), &notification); err != nil {
			t.Fatal(err)
		}
		item := notification.Item
		if item.ToolName == "write_file" && strings.Contains(item.ToolResult, script) {
			helperRecorded = !item.ToolFailed && len(item.Artifacts) == 0
		}
		if item.ToolName == "write_file" && !item.ToolFailed && len(item.Artifacts) == 1 && item.Artifacts[0] == "report.md" {
			reportRecorded = true
		}
	}
	if !helperRecorded {
		t.Fatal("temporary path must stay in the execution result without becoming an artifact")
	}
	if !reportRecorded {
		t.Fatal("formal report must still be listed as an artifact")
	}
	if !strings.Contains(session.messages[0].Content, "Temporary directory for this session:") {
		t.Fatal("agent was not told where to write scratch files")
	}
	session.RunTurn(ctx, "second", "continue", nil, nil, emitter)
	if session.scratchDir != scratch {
		t.Fatal("another turn moved the session scratch directory")
	}
	other := newTestSession(t, &fakeModel{}, protocol.ApprovalOnWrite)
	if err := other.prepareScratchLocked(); err != nil {
		t.Fatal(err)
	}
	if _, inside, err := other.turnEnv("other", emitter).ResolveWrite(script); err != nil || inside {
		t.Fatalf("one session must not treat another session's scratch files as its own: inside=%v err=%v", inside, err)
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		link := filepath.Join(scratch, "outside")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		env := session.turnEnv("second", emitter)
		escaped := filepath.Join(link, "external.txt")
		if _, inside, err := env.ResolveWrite(escaped); err != nil || inside || env.IsTemporaryPath(escaped) {
			t.Fatalf("a symlink out of scratch must keep external write rules: inside=%v err=%v", inside, err)
		}
	}
	session.Close()
	if _, err := os.Stat(script); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch file survived session close: %v", err)
	}
	if _, err := os.Stat(existing); err != nil {
		t.Fatalf("project file removed at close: %v", err)
	}
}
