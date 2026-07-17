package runtimeclient

import (
	"context"
	"io"
	"strings"
	"testing"

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
	base := &model.RuntimeTask{Command: "agent", Args: []string{"--print"}, Messages: []model.RuntimeTaskMessage{{Role: "user", Content: "hello"}}}
	stdinTask := *base
	stdinTask.PromptMode = model.RuntimePromptStdin
	stdinCmd, err := commandForTask(context.Background(), &stdinTask)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(stdinCmd.Args, " "); got != "agent --print" {
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
