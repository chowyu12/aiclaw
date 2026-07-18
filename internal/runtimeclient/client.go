package runtimeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

const (
	defaultPollInterval      = 2 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
)

type client struct {
	baseURL string
	token   string
	version string
	http    *http.Client
}

type apiResponse[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// Run handles `aiclaw runtime connect`. It blocks until SIGINT/SIGTERM.
func Run(args []string, version string) error {
	if len(args) == 0 || args[0] != "connect" {
		return errors.New("usage: aiclaw runtime connect --server <url> --token <rt-token>")
	}
	fs := flag.NewFlagSet("runtime connect", flag.ContinueOnError)
	serverURL := fs.String("server", "", "AiClaw server URL")
	token := fs.String("token", "", "Runtime token")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := newClient(*serverURL, *token, version)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.heartbeat(ctx); err != nil {
		return fmt.Errorf("connect runtime: %w", err)
	}
	fmt.Printf("Runtime connected to %s\n", c.baseURL)

	go c.heartbeatLoop(ctx)
	return c.claimLoop(ctx)
}

// StartEmbedded starts a local runtime worker inside the AiClaw server
// process. It is intentionally not a separate daemon or shell command.
func StartEmbedded(ctx context.Context, serverURL, token, version string) error {
	c, err := newClient(serverURL, token, version)
	if err != nil {
		return err
	}
	go c.runEmbedded(ctx)
	return nil
}

func newClient(serverURL, token, version string) (*client, error) {
	baseURL, err := normalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(token), "rt-") {
		return nil, errors.New("a valid runtime token is required")
	}
	return &client{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		version: version,
		http:    &http.Client{Timeout: 35 * time.Second},
	}, nil
}

func (c *client) runEmbedded(ctx context.Context) {
	for {
		if err := c.heartbeat(ctx); err == nil {
			break
		} else if ctx.Err() != nil {
			return
		} else {
			fmt.Fprintf(os.Stderr, "embedded runtime heartbeat failed: %v\n", err)
		}
		if !waitContext(ctx, defaultPollInterval) {
			return
		}
	}
	go c.heartbeatLoop(ctx)
	_ = c.claimLoop(ctx)
}

func (c *client) claimLoop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		task, err := c.claim(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "claim task failed: %v\n", err)
			if !waitContext(ctx, defaultPollInterval) {
				return nil
			}
			continue
		}
		if task == nil {
			if !waitContext(ctx, defaultPollInterval) {
				return nil
			}
			continue
		}
		fmt.Printf("Running %s with %s\n", task.RunID, task.Command)
		c.execute(ctx, task)
	}
}

func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", errors.New("server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("server URL must be an absolute http(s) URL")
	}
	return raw, nil
}

func (c *client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.heartbeat(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "runtime heartbeat failed: %v\n", err)
			}
		}
	}
}

func (c *client) heartbeat(ctx context.Context) error {
	return c.post(ctx, "/api/v1/runtime-daemon/heartbeat", map[string]any{
		"version": c.version,
		"agents":  DetectLocalAgents(),
	}, nil)
}

// DetectLocalAgents returns supported agent CLIs available on this machine.
func DetectLocalAgents() []string {
	env := localCLIEnvironment()
	return detectLocalAgents(func(command string) (string, error) {
		return resolveRuntimeCommand(command, env)
	})
}

func detectLocalAgents(lookPath func(string) (string, error)) []string {
	known := []struct {
		agentType string
		command   string
	}{
		{model.RuntimeAgentTypeCodex, "codex"},
		{model.RuntimeAgentTypeCursor, "cursor-agent"},
		{model.RuntimeAgentTypeClaudeCode, "claude"},
		{model.RuntimeAgentTypeCodeBuddy, "codebuddy"},
		{model.RuntimeAgentTypeOpenClaw, "openclaw"},
		{model.RuntimeAgentTypeHermes, "hermes"},
	}
	detected := make([]string, 0, len(known))
	for _, candidate := range known {
		if _, err := lookPath(candidate.command); err == nil {
			detected = append(detected, candidate.agentType)
		}
	}
	return detected
}

func (c *client) claim(ctx context.Context) (*model.RuntimeTask, error) {
	var task model.RuntimeTask
	status, err := c.postStatus(ctx, "/api/v1/runtime-daemon/tasks/claim", struct{}{}, &task)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &task, nil
}

func (c *client) execute(parent context.Context, task *model.RuntimeTask) {
	ctx := parent
	cancel := func() {}
	if task.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(task.TimeoutSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	defer cancel()
	if agentType, ok := model.NormalizeRuntimeAgentType(task.AgentType); ok && agentType != model.RuntimeAgentTypeCustom {
		content, errorText := c.executeKnownProvider(parent, ctx, task)
		c.complete(parent, task.RunID, content, errorText)
		return
	}

	cmd, err := commandForTask(ctx, task)
	if err != nil {
		c.complete(parent, task.RunID, "", err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.complete(parent, task.RunID, "", err.Error())
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		c.complete(parent, task.RunID, "", err.Error())
		return
	}

	var output strings.Builder
	buf := make([]byte, 4096)
	var streamErr error
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			delta := string(buf[:n])
			output.WriteString(delta)
			if err := c.post(parent, "/api/v1/runtime-daemon/tasks/"+url.PathEscape(task.RunID)+"/events", model.RuntimeRunEvent{Delta: delta}, nil); err != nil {
				streamErr = err
				cancel()
				break
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			streamErr = readErr
			cancel()
			break
		}
	}
	waitErr := cmd.Wait()
	if streamErr != nil {
		c.complete(parent, task.RunID, output.String(), "stream output: "+streamErr.Error())
		return
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			waitErr = fmt.Errorf("%w: %s", waitErr, detail)
		}
		c.complete(parent, task.RunID, output.String(), waitErr.Error())
		return
	}
	c.complete(parent, task.RunID, output.String(), "")
}

func commandForTask(ctx context.Context, task *model.RuntimeTask) (*exec.Cmd, error) {
	if task == nil || strings.TrimSpace(task.Command) == "" {
		return nil, errors.New("runtime task has no command")
	}
	prompt := renderPrompt(task)
	args := append([]string(nil), task.Args...)
	promptMode, ok := model.NormalizeRuntimePromptMode(task.PromptMode)
	if !ok {
		return nil, fmt.Errorf("unsupported runtime prompt mode %q", task.PromptMode)
	}
	if promptMode == model.RuntimePromptArgument {
		args = append(args, prompt)
	}
	cmd, err := runtimeCommand(ctx, task.Command, args...)
	if err != nil {
		return nil, err
	}
	cmd.Dir = task.WorkingDir
	if promptMode == model.RuntimePromptStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}
	return cmd, nil
}

func renderPrompt(task *model.RuntimeTask) string {
	var b strings.Builder
	if strings.TrimSpace(task.SystemPrompt) != "" {
		b.WriteString("System instructions:\n")
		b.WriteString(strings.TrimSpace(task.SystemPrompt))
		b.WriteString("\n\n")
	}
	if task.WorkingDir != "" {
		b.WriteString("Working directory: ")
		b.WriteString(task.WorkingDir)
		b.WriteString("\n\n")
	}
	b.WriteString("Conversation:\n")
	for _, message := range task.Messages {
		role := "User"
		if message.Role == "assistant" {
			role = "Assistant"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(message.Content)
		b.WriteString("\n\n")
	}
	b.WriteString("Respond to the latest user message.")
	return b.String()
}

func (c *client) complete(ctx context.Context, runID, content, errorText string) {
	completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	err := c.post(completeCtx, "/api/v1/runtime-daemon/tasks/"+url.PathEscape(runID)+"/complete", model.RuntimeRunComplete{
		Content: content,
		Error:   errorText,
	}, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "complete task %s failed: %v\n", runID, err)
		return
	}
	if errorText != "" {
		fmt.Fprintf(os.Stderr, "Task %s failed: %s\n", runID, errorText)
	} else {
		fmt.Printf("Task %s completed\n", runID)
	}
}

func (c *client) post(ctx context.Context, path string, requestBody, responseData any) error {
	_, err := c.postStatus(ctx, path, requestBody, responseData)
	return err
}

func (c *client) postStatus(ctx context.Context, path string, requestBody, responseData any) (int, error) {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	var envelope apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return resp.StatusCode, fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 {
		return resp.StatusCode, errors.New(envelope.Message)
	}
	if responseData != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, responseData); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
