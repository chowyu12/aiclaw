package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/skills"
)

func TestExplicitSkillEntersFirstModelRequestAndExpiresWithTurn(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCall("implicit", "load_skill", `{"name":"implicit"}`),
		sseText("done"),
		sseText("next turn"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	tokens := map[string]string{
		"selected": "SELECTED_BODY",
		"implicit": "IMPLICIT_BODY",
		"disabled": "DISABLED_BODY",
		"escaped":  "ESCAPED_BODY",
	}
	var dirs []string
	for name, token := range tokens {
		dir := t.TempDir()
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(fmt.Sprintf("---\nname: %s\ndescription: find %s\n---\n%s", name, name, token)), 0o600); err != nil {
			t.Fatal(err)
		}
		if name == "disabled" {
			if err := os.WriteFile(filepath.Join(dir, ".disabled"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		dirs = append(dirs, dir)
	}
	session.loadSkills(dirs)
	emitter := &recordingEmitter{}
	const text = `Run $selected and $selected, ignore $disabled and \$escaped.`
	session.RunTurn(t.Context(), "first", text, nil, nil, emitter)
	session.RunTurn(t.Context(), "second", "continue", nil, nil, emitter)

	if len(model.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(model.requests))
	}
	for i, request := range model.requests {
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		wire := string(encoded)
		wantExplicit := 0
		if i < 2 {
			wantExplicit = 1
		}
		if strings.Count(wire, tokens["selected"]) != wantExplicit ||
			strings.Contains(wire, tokens["implicit"]) != (i > 0) ||
			strings.Contains(wire, tokens["disabled"]) || strings.Contains(wire, tokens["escaped"]) {
			t.Fatalf("unexpected skill body placement in request %d", i+1)
		}
		system := request["messages"].([]any)[0].(map[string]any)["content"].(string)
		if !strings.Contains(system, "- selected: find selected") || strings.Contains(system, tokens["selected"]) {
			t.Fatalf("unexpected system catalog in request %d", i+1)
		}
	}
	if saved := session.snapshotMessages(); saved[1].Content != text {
		t.Fatal("selected skill instructions leaked into stored user text")
	}
}

func TestSkillCatalogHasTotalBudgetAndKeepsOmittedSkillsCallable(t *testing.T) {
	list := make([]skills.Skill, 300)
	for i := range list {
		list[i] = skills.Skill{Name: fmt.Sprintf("技能-%03d", i), Description: strings.Repeat("用途", 100)}
	}
	const window = 8000
	catalog := renderSkillCatalog(list, window)
	if approxTokens(catalog) > window/50 || !strings.Contains(catalog, "- 技能-000") ||
		!strings.Contains(catalog, "Some skills are omitted") || strings.Contains(catalog, "技能-299") {
		t.Fatalf("unexpected bounded catalog: %q", catalog)
	}
	if large := renderSkillCatalog(list, 1_000_000); approxTokens(large) > skillCatalogMaxTokens {
		t.Fatal("large model exceeded the catalog ceiling")
	}
	if compact := renderSkillCatalog(list[:3], 10_000); !strings.Contains(compact, "技能-002") || !strings.Contains(compact, "用途") {
		t.Fatal("shortening descriptions should keep small skill sets useful")
	}
	if strings.Contains(string(skillToolSchema()), `"enum"`) {
		t.Fatal("tool schema reintroduced the full unbounded skill list")
	}
}

func TestSelectedSkillExcerptStaysValidUTF8(t *testing.T) {
	session := &Session{skills: []skills.Skill{{Name: "large", Body: strings.Repeat("正文", selectedSkillMaxBytes)}}}
	prompts := &turnSkillPrompts{selected: make(map[string]bool)}
	ctx := context.WithValue(t.Context(), turnSkillPromptsKey{}, prompts)
	session.selectTurnSkills(ctx, "message", "$large")
	body, found := strings.CutPrefix(prompts.prompt.String(), "\n\nSelected skill \"large\" (skill directory: ):\n\n")
	if !found || !utf8.ValidString(body) || len(body) > selectedSkillMaxBytes || !strings.Contains(body, "excerpt truncated") {
		t.Fatalf("invalid skill excerpt with %d bytes", len(body))
	}
}

func TestExplicitSkillSurvivesCompactionRetry(t *testing.T) {
	model := &fakeModel{script: []string{
		"!400 This model's maximum context length is 8192 tokens",
		sseText("summary"),
		sseText("done"),
	}}
	session := newTestSession(t, model, protocol.ApprovalBypass)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: report\ndescription: reporting\n---\nREPORT_INSTRUCTIONS"), 0o600); err != nil {
		t.Fatal(err)
	}
	session.loadSkills([]string{dir})
	session.RunTurn(t.Context(), "first", "$report please", nil, nil, &recordingEmitter{})
	if len(model.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(model.requests))
	}
	for i, request := range model.requests {
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), "REPORT_INSTRUCTIONS") != 1 {
			t.Fatalf("request %d did not include a single copy of the selected skill", i+1)
		}
	}
	for _, message := range session.snapshotMessages() {
		if strings.Contains(message.Content, "REPORT_INSTRUCTIONS") {
			t.Fatal("compaction persisted the selected skill body in the conversation")
		}
	}
}
