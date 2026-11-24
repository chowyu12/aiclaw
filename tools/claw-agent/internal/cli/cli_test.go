package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/providers"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

const textReply = `data: {"choices":[{"delta":{"content":"Hello "}}]}

data: {"choices":[{"delta":{"content":"CLI"},"finish_reason":"stop"}]}

data: [DONE]

`

const writeReply = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"fixture_write","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"cli-test.txt\",\"content\":\"approved\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`

func workspaceFixture(t *testing.T, handler http.HandlerFunc) []string {
	t.Helper()
	t.Setenv("AICLAW_LLM_KEY", "")
	t.Setenv("AICLAW_MODEL", "")
	t.Setenv("AICLAW_BASE_URL", "")
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}

	endpoint := httptest.NewServer(handler)
	t.Cleanup(endpoint.Close)
	path := filepath.Join(root, "model", "app.db")
	db, err := appdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store := providers.New(db)
	provider, err := store.Create(t.Context(), protocol.ProviderCreateParams{
		Name: "CLI fixture", Type: "openai-compatible", BaseURL: endpoint.URL + "/v1",
		APIKey: "fixture-private-key", Models: []string{"fallback", "fixture#vision@32768"},
	})
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config.json")
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"providerId":%d,"model":"fixture"}`, provider.ID)), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"--data-home", filepath.Join(root, "cli-home"), "--app-db", path, "--config", config, "--cwd", workspace}
}

func runCLI(t *testing.T, args []string, input string, interrupts <-chan os.Signal) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	err := Run(ctx, args, "test", strings.NewReader(input), &out, &stderr, interrupts)
	return out.String(), stderr.String(), err
}

func eventsFrom(t *testing.T, stdout string) []event {
	t.Helper()
	var events []event
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var item event
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("stdout contains non-JSON output: %s", line)
		}
		events = append(events, item)
	}
	return events
}

func TestExecJSONAndResumeUseDesktopModelAndPersistConversation(t *testing.T) {
	previousHistory := make(chan string, 4)
	flags := workspaceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-private-key" || !bytes.Contains(body, []byte(`"model":"fixture"`)) {
			t.Errorf("wrong provider route or model: %s", r.URL.Path)
		}
		previousHistory <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, textReply)
	})
	args := append([]string{"exec", "-", "--json"}, flags...)
	out, stderr, err := runCLI(t, args, "first input from stdin", nil)
	if err != nil {
		t.Fatalf("exec: %v\n%s\n%s", err, out, stderr)
	}
	events := eventsFrom(t, out)
	if len(events) != 6 || events[0].Type != "session.started" || events[1].Type != "turn.started" || events[2].Type != "message.delta" ||
		events[2].Delta+events[3].Delta != "Hello CLI" || events[4].Type != "message.completed" ||
		events[4].Text != "Hello CLI" || events[5].Type != "turn.completed" || strings.Contains(out, "fixture-private-key") || stderr != "" {
		t.Fatalf("unexpected exec output: %s\n%s", out, stderr)
	}
	if events[0].SessionID == "" || events[5].TurnID == "" {
		t.Fatalf("missing IDs: %+v", events)
	}
	if got := <-previousHistory; !strings.Contains(got, "first input from stdin") {
		t.Fatal("stdin prompt was not sent")
	}

	args = append([]string{"resume", "--last"}, flags...)
	out, stderr, err = runCLI(t, args, "follow-up\n/quit\n", nil)
	if err != nil || !strings.Contains(out, "You: first input from stdin") || !strings.Contains(out, "AIClaw: Hello CLI") || !strings.Contains(stderr, events[0].SessionID) {
		t.Fatalf("resume failed: %v\n%s\n%s", err, out, stderr)
	}
	if got := <-previousHistory; !strings.Contains(got, "first input from stdin") || !strings.Contains(got, "follow-up") {
		t.Fatal("follow-up lost prior history")
	}

	other := t.TempDir()
	out, stderr, err = runCLI(t, append(append([]string{"resume", "--last"}, flags...), "--cwd", other), "", nil)
	if err == nil || !strings.Contains(err.Error(), "no CLI session found") || out != "" {
		t.Fatalf("resumed across directories: %v %s %s", err, out, stderr)
	}
	_, _, err = runCLI(t, append(append([]string{"resume", events[0].SessionID}, flags...), "--cwd", other), "/exit\n", nil)
	if err == nil || !strings.Contains(err.Error(), "--cwd is only supported with --last") {
		t.Fatalf("ignored --cwd on resume by ID: %v", err)
	}
	profile := flags[:len(flags)-2]
	_, stderr, err = runCLI(t, append([]string{"resume", events[0].SessionID}, profile...), "/exit\n", nil)
	if err != nil || !strings.Contains(stderr, flags[len(flags)-1]) {
		t.Fatalf("resume by ID: %v", err)
	}

	out, stderr, err = runCLI(t, append([]string{"exec", "resume", events[0].SessionID, "--json", "-"}, profile...), "headless follow-up", nil)
	resumed := eventsFrom(t, out)
	if err != nil || stderr != "" || resumed[0].SessionID != events[0].SessionID || resumed[len(resumed)-1].Type != "turn.completed" {
		t.Fatalf("headless resume: %v\n%s\n%s", err, out, stderr)
	}
	if got := <-previousHistory; !strings.Contains(got, "first input from stdin") || !strings.Contains(got, "headless follow-up") {
		t.Fatal("headless follow-up lost history or stdin")
	}
	out, _, err = runCLI(t, append([]string{"exec", "resume", "--last", "--json", "continue by directory"}, flags...), "", nil)
	if err != nil || eventsFrom(t, out)[0].SessionID != events[0].SessionID {
		t.Fatalf("headless resume by directory: %v\n%s", err, out)
	}
	if got := <-previousHistory; !strings.Contains(got, "headless follow-up") || !strings.Contains(got, "continue by directory") {
		t.Fatal("headless follow-up did not persist")
	}
	for _, query := range []string{"FIRST INPUT", filepath.Base(flags[len(flags)-1]), events[0].SessionID} {
		out, _, err := runCLI(t, append([]string{"sessions", "search", query}, profile...), "", nil)
		if err != nil || !strings.HasPrefix(out, events[0].SessionID+"\t") {
			t.Fatalf("search %q: %v\n%s", query, err, out)
		}
	}
	out, _, err = runCLI(t, append([]string{"sessions", "search", "no matching title or path"}, profile...), "", nil)
	if err != nil || out != "" {
		t.Fatalf("unexpected search result: %v %q", err, out)
	}
	out, stderr, err = runCLI(t, append([]string{"sessions", "show", events[0].SessionID}, profile...), "", nil)
	if err != nil || stderr != "" || !strings.Contains(out, "You: headless follow-up") || !strings.Contains(out, "AIClaw: Hello CLI") {
		t.Fatalf("read-only transcript: %v\n%s\n%s", err, out, stderr)
	}
	db, err := store.Open(flags[1])
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := json.Marshal(map[string]string{"parentId": events[0].SessionID})
	err = db.Save(t.Context(), store.Session{
		ID: "child-review", Title: "Review worker", Workdir: flags[len(flags)-1],
		CreatedAt: time.Now(), UpdatedAt: time.Now(), Config: parent,
		Messages: json.RawMessage(`[{"role":"assistant","content":"Saved child review"}]`),
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = runCLI(t, append([]string{"sessions", "search", "review worker"}, profile...), "", nil)
	if err != nil || !strings.HasPrefix(out, "child-review\t") {
		t.Fatalf("child search: %v\n%s", err, out)
	}
	out, _, err = runCLI(t, append([]string{"sessions", "show", "child-review"}, profile...), "", nil)
	if err != nil || !strings.Contains(out, "Saved child review") {
		t.Fatalf("child transcript: %v\n%s", err, out)
	}
	if len(previousHistory) != 0 {
		t.Fatal("session search or history called the model")
	}
}

func TestCLIApprovalRequiresTerminalConfirmation(t *testing.T) {
	flags := workspaceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if bytes.Contains(body, []byte(`"content":"Try again"`)) {
			_, _ = io.WriteString(w, strings.Replace(writeReply, "fixture_write", "fixture_resume_write", 1))
			return
		}
		if bytes.Contains(body, []byte(`"role":"tool"`)) {
			_, _ = io.WriteString(w, textReply)
			return
		}
		_, _ = io.WriteString(w, writeReply)
	})
	path := filepath.Join(flags[len(flags)-1], "cli-test.txt")
	out, stderr, err := runCLI(t, append([]string{"exec", "Write the file", "--json"}, flags...), "yes\n", nil)
	if err == nil || !strings.Contains(err.Error(), "requires interactive approval") || strings.Contains(stderr, "Allow once") {
		t.Fatalf("headless approval: %v\n%s\n%s", err, out, stderr)
	}
	events := eventsFrom(t, out)
	if events[len(events)-1].Type != "error" {
		t.Fatalf("missing JSON error: %s", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("headless run changed the file: %v", err)
	}
	profile := flags[:len(flags)-2]
	out, stderr, err = runCLI(t, append([]string{"exec", "resume", events[0].SessionID, "--json", "Try again"}, profile...), "yes\n", nil)
	if err == nil || !strings.Contains(err.Error(), "requires interactive approval") || strings.Contains(stderr, "Allow once") {
		t.Fatalf("headless resume approval: %v\n%s\n%s", err, out, stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("headless resume changed the file: %v", err)
	}

	out, stderr, err = runCLI(t, flags, "Write the file\nyes\n/exit\n", nil)
	got, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(got) != "approved" || !strings.Contains(stderr, "Allow once? [y/N]") || !strings.Contains(out, "Hello CLI") {
		t.Fatalf("interactive approval: %v / %v\n%s\n%s", err, readErr, out, stderr)
	}
}

func TestCLIReportsProviderSpendLimitCode(t *testing.T) {
	for _, fixture := range []struct {
		code   string
		status int
	}{
		{"api_key_spend_limit_exceeded", http.StatusTooManyRequests},
		{"project_spend_limit_exceeded", http.StatusTooManyRequests},
		{"organization_spend_limit_exceeded", http.StatusOK},
	} {
		t.Run(fixture.code, func(t *testing.T) {
			calls := make(chan struct{}, 8)
			flags := workspaceFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls <- struct{}{}
				payload := fmt.Sprintf(`{"error":{"type":"insufficient_quota","code":%q}}`, fixture.code)
				w.Header().Set("Retry-After", "4")
				if fixture.status == http.StatusOK {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", payload)
				} else {
					w.WriteHeader(fixture.status)
					fmt.Fprint(w, payload)
				}
			})
			out, _, err := runCLI(t, append([]string{"exec", "--json", "Test limit"}, flags...), "", nil)
			events := eventsFrom(t, out)
			if err == nil || len(calls) != 1 || len(events) < 2 ||
				events[len(events)-2].Type != "turn.completed" || events[len(events)-2].ErrorCode != fixture.code ||
				events[len(events)-1].Type != "error" || events[len(events)-1].ErrorCode != fixture.code {
				t.Fatalf("unexpected spend limit result: %v, %d calls\n%s", err, len(calls), out)
			}
		})
	}
}

func TestStandaloneExecUsesEnvironmentWithoutAnAppDatabase(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer standalone-fixture" {
			t.Error("standalone credentials were not used")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, textReply)
	}))
	defer model.Close()
	t.Setenv("AICLAW_LLM_KEY", "standalone-fixture")
	t.Setenv("AICLAW_BASE_URL", model.URL+"/v1")
	t.Setenv("AICLAW_MODEL", "fixture")
	root := t.TempDir()
	out, stderr, err := runCLI(t, []string{"exec", "Hello", "--json", "--app-db=", "--data-home", filepath.Join(root, "cli"), "--cwd", root}, "", nil)
	if err != nil || strings.Contains(out, "standalone-fixture") || !strings.Contains(out, "Hello CLI") {
		t.Fatalf("standalone exec: %v\n%s\n%s", err, out, stderr)
	}
}

func TestProvidersDefaultModelAndSessionsStayInTheirCLIProfile(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer profile-fixture" || !bytes.Contains(body, []byte(`"model":"preferred"`)) {
			t.Error("CLI did not use the saved provider and model")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, textReply)
	}))
	defer endpoint.Close()
	t.Setenv("AICLAW_BASE_URL", "")
	t.Setenv("AICLAW_MODEL", "")
	t.Setenv("AICLAW_LLM_KEY", "profile-fixture")
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	run := func(home string, args ...string) string {
		t.Helper()
		out, stderr, err := runCLI(t, append(args, "--data-home", home, "--cwd", root), "", nil)
		if err != nil || strings.Contains(out, "profile-fixture") {
			t.Fatalf("CLI settings %v: %v\n%s\n%s", args, err, out, stderr)
		}
		return out
	}
	run(first, "providers", "add", "fixture", "--base-url", endpoint.URL+"/v1", "--models", "fallback,preferred")
	if got := run(first, "models", "list"); !strings.Contains(got, "1\tpreferred") {
		t.Fatal(got)
	}
	run(first, "models", "use", "--provider", "1", "--model", "preferred")
	if got := run(first, "config", "show"); !strings.Contains(got, `"model":"preferred"`) {
		t.Fatal(got)
	}
	t.Setenv("AICLAW_LLM_KEY", "")
	events := eventsFrom(t, run(first, "exec", "profile prompt", "--json"))
	id := events[0].SessionID
	if got := run(first, "sessions", "list"); !strings.Contains(got, id) {
		t.Fatal(got)
	}
	if providers, sessions := run(second, "providers", "list"), run(second, "sessions", "list"); providers != "" || sessions != "" {
		t.Fatalf("other CLI profile inherited providers or sessions: %q / %q", providers, sessions)
	}
	if got := run(second, "config", "show"); !strings.Contains(got, `"model":""`) {
		t.Fatal(got)
	}
}

func TestInterruptCancelsActiveTurn(t *testing.T) {
	interrupts := make(chan os.Signal, 1)
	flags := workspaceFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		interrupts <- os.Interrupt
		<-r.Context().Done()
	})
	out, stderr, err := runCLI(t, append([]string{"exec", "--json", "Interrupt a blocked model"}, flags...), "", interrupts)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("interrupt: %v\n%s\n%s", err, out, stderr)
	}
	events := eventsFrom(t, out)
	if events[len(events)-2].Type != "turn.completed" {
		t.Fatalf("interrupt lost completion: %s", out)
	}
}

func TestCLILockPreventsSecondKernelAndCanBeReacquired(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cli space")
	release, err := lockDataHome(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockDataHome(t.Context(), dir); err == nil {
		second()
		t.Error("second CLI obtained the session lock")
	}
	release()
	release, err = lockDataHome(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestTerminalTextDoesNotEmitEscapeSequences(t *testing.T) {
	if got := terminalText("ok\x1b[2J\r\nnew\tline"); got != "ok[2J\nnew\tline" {
		t.Fatalf("terminal text: %q", got)
	}
}
