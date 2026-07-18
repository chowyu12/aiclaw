package runtimeclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestRenderPrompt(t *testing.T) {
	prompt := renderPrompt(&model.RuntimeTask{
		SystemPrompt: "Be precise.",
		WorkingDir:   "/work/project",
		Messages: []model.RuntimeTaskMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "answer"},
			{Role: "user", Content: "latest"},
		},
	})
	for _, expected := range []string{"System instructions:\nBe precise.", "Working directory: /work/project", "User: first", "Assistant: answer", "User: latest"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}

func TestCommandForTaskPromptModes(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := &model.RuntimeTask{Command: command, Args: []string{"--print"}, Messages: []model.RuntimeTaskMessage{{Role: "user", Content: "hello"}}}
	stdinTask := *base
	stdinTask.PromptMode = model.RuntimePromptStdin
	stdinCmd, err := commandForTask(context.Background(), &stdinTask)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(stdinCmd.Args[1:], " "); got != "--print" {
		t.Fatalf("stdin args = %q", got)
	}
	stdin, err := io.ReadAll(stdinCmd.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stdin), "User: hello") {
		t.Fatalf("stdin prompt missing message: %q", stdin)
	}

	argumentTask := *base
	argumentTask.PromptMode = model.RuntimePromptArgument
	argumentCmd, err := commandForTask(context.Background(), &argumentTask)
	if err != nil {
		t.Fatal(err)
	}
	if argumentCmd.Stdin != nil {
		t.Fatal("argument mode must not write the prompt to stdin")
	}
	if got := argumentCmd.Args[len(argumentCmd.Args)-1]; !strings.Contains(got, "User: hello") {
		t.Fatalf("last argument missing prompt: %q", got)
	}
}

func TestNormalizeServerURL(t *testing.T) {
	if got, err := normalizeServerURL("https://example.com/"); err != nil || got != "https://example.com" {
		t.Fatalf("unexpected URL: %q, %v", got, err)
	}
	if _, err := normalizeServerURL("localhost:8080"); err == nil {
		t.Fatal("relative URL should be rejected")
	}
}

func TestDetectLocalAgents(t *testing.T) {
	detected := detectLocalAgents(func(command string) (string, error) {
		if command == "codex" || command == "hermes" {
			return "/bin/" + command, nil
		}
		return "", os.ErrNotExist
	})
	if got := strings.Join(detected, ","); got != "codex,hermes" {
		t.Fatalf("unexpected detected agents: %q", got)
	}
}

func TestProviderProtocolHelpers(t *testing.T) {
	if got := messageText([]byte(`{"content":[{"type":"thinking","text":"hidden"},{"type":"text","text":"answer"}]}`)); got != "answer" {
		t.Fatalf("message text = %q", got)
	}
	result, err := openClawResultText([]byte(`{"ok":true,"result":{"payloads":[{"text":"first"},{"text":"second"}]}}`))
	if err != nil || result != "first\nsecond" {
		t.Fatalf("openclaw result = %q, %v", result, err)
	}
	if _, err := openClawResultText([]byte(`not-json`)); err == nil {
		t.Fatal("invalid OpenClaw output must fail")
	}
	if got := normalizeACPUpdateType("agentMessageChunk"); got != "agent_message_chunk" {
		t.Fatalf("ACP update type = %q", got)
	}
}

func TestExecuteCodexUsesAppServerProtocol(t *testing.T) {
	script := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *'"method":"thread/start"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"thread":{"id":"thr-1"}}}\n' "$id" ;;
    *'"method":"turn/start"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"turn":{"id":"turn-1"}}}\n' "$id"
      printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"type":"agentMessage","text":"hello from codex"}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"turn":{"status":"completed"}}}'
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, token: "rt-test", http: server.Client()}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	content, errorText := c.executeCodex(ctx, ctx, &model.RuntimeTask{
		RunID: "run-1", AgentType: model.RuntimeAgentTypeCodex, Command: script,
		Messages: []model.RuntimeTaskMessage{{Role: "user", Content: "hello"}},
	})
	if errorText != "" || content != "hello from codex" {
		t.Fatalf("codex execution = content %q error %q", content, errorText)
	}
}

func TestExecuteHermesUsesACP(t *testing.T) {
	script := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1}}\n' "$id" ;;
    *'"method":"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"ses-1"}}\n' "$id" ;;
    *'"method":"session/prompt"'*)
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agentMessageChunk","content":{"text":"hello from hermes"}}}}'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, token: "rt-test", http: server.Client()}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	content, errorText := c.executeHermes(ctx, ctx, &model.RuntimeTask{
		RunID: "run-1", AgentType: model.RuntimeAgentTypeHermes, Command: script,
		Messages: []model.RuntimeTaskMessage{{Role: "user", Content: "hello"}},
	})
	if errorText != "" || content != "hello from hermes" {
		t.Fatalf("hermes execution = content %q error %q", content, errorText)
	}
}
