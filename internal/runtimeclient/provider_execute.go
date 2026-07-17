package runtimeclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

// executeKnownProvider mirrors the protocol split used by Multica: Codex uses
// its app-server JSON-RPC protocol, Hermes uses ACP, Claude/CodeBuddy emit
// stream-json, Cursor emits JSONL, and OpenClaw returns a JSON result.
func (c *client) executeKnownProvider(parent, ctx context.Context, task *model.RuntimeTask) (string, string) {
	typeName, ok := model.NormalizeRuntimeAgentType(task.AgentType)
	if !ok || typeName == model.RuntimeAgentTypeCustom {
		return "", "unsupported runtime agent type"
	}
	switch typeName {
	case model.RuntimeAgentTypeCodex:
		return c.executeCodex(parent, ctx, task)
	case model.RuntimeAgentTypeHermes:
		return c.executeHermes(parent, ctx, task)
	case model.RuntimeAgentTypeClaudeCode, model.RuntimeAgentTypeCodeBuddy:
		return c.executeClaudeStream(parent, ctx, task, typeName)
	case model.RuntimeAgentTypeCursor:
		return c.executeCursorStream(parent, ctx, task)
	case model.RuntimeAgentTypeOpenClaw:
		return c.executeOpenClaw(parent, ctx, task)
	default:
		return "", "unsupported runtime agent type"
	}
}

func (c *client) providerDelta(ctx context.Context, task *model.RuntimeTask, delta string) error {
	if delta == "" {
		return nil
	}
	return c.post(ctx, "/api/v1/runtime-daemon/tasks/"+task.RunID+"/events", model.RuntimeRunEvent{Delta: delta}, nil)
}

func providerCommand(ctx context.Context, task *model.RuntimeTask, args []string) (*exec.Cmd, io.ReadCloser, io.WriteCloser, *bytes.Buffer, error) {
	if strings.TrimSpace(task.Command) == "" {
		return nil, nil, nil, nil, errors.New("runtime task has no command")
	}
	cmd := exec.CommandContext(ctx, task.Command, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = task.WorkingDir
	if task.AgentType == model.RuntimeAgentTypeHermes {
		cmd.Env = append(os.Environ(), "HERMES_YOLO_MODE=1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}
	return cmd, stdout, stdin, &stderr, nil
}

func providerPrompt(task *model.RuntimeTask, includeSystem bool) string {
	var b strings.Builder
	if includeSystem && strings.TrimSpace(task.SystemPrompt) != "" {
		b.WriteString("System instructions:\n")
		b.WriteString(strings.TrimSpace(task.SystemPrompt))
		b.WriteString("\n\n")
	}
	b.WriteString("Conversation:\n")
	for _, message := range task.Messages {
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		if message.Role == "assistant" {
			b.WriteString("Assistant: ")
		} else {
			b.WriteString("User: ")
		}
		b.WriteString(message.Content)
		b.WriteString("\n\n")
	}
	b.WriteString("Respond to the latest user message.")
	return b.String()
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

type rpcPeer struct {
	stdin          io.WriteCloser
	writeMu        sync.Mutex
	mu             sync.Mutex
	nextID         int
	pending        map[int]chan rpcReply
	done           chan struct{}
	doneOnce       sync.Once
	onNotification func(string, json.RawMessage)
	onRequest      func(json.RawMessage, string, json.RawMessage)
}

func newRPCPeer(stdin io.WriteCloser) *rpcPeer {
	return &rpcPeer{stdin: stdin, pending: make(map[int]chan rpcReply), done: make(chan struct{})}
}

func (p *rpcPeer) serve(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		p.handleLine(scanner.Text())
	}
	p.closePending(errors.New("agent protocol stream closed"))
}

func (p *rpcPeer) closePending(err error) {
	p.doneOnce.Do(func() {
		p.mu.Lock()
		for id, ch := range p.pending {
			ch <- rpcReply{err: err}
			delete(p.pending, id)
		}
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *rpcPeer) handleLine(line string) {
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &raw) != nil {
		return
	}
	if idRaw, hasID := raw["id"]; hasID {
		if _, ok := raw["result"]; ok {
			p.deliver(idRaw, rpcReply{result: raw["result"]})
			return
		}
		if errRaw, ok := raw["error"]; ok {
			var value struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(errRaw, &value)
			p.deliver(idRaw, rpcReply{err: errors.New(value.Message)})
			return
		}
		if methodRaw, ok := raw["method"]; ok && p.onRequest != nil {
			var method string
			_ = json.Unmarshal(methodRaw, &method)
			p.onRequest(idRaw, method, raw["params"])
			return
		}
	}
	if methodRaw, ok := raw["method"]; ok && p.onNotification != nil {
		var method string
		_ = json.Unmarshal(methodRaw, &method)
		p.onNotification(method, raw["params"])
	}
}

func (p *rpcPeer) deliver(idRaw json.RawMessage, value rpcReply) {
	var id int
	if json.Unmarshal(idRaw, &id) != nil {
		return
	}
	p.mu.Lock()
	ch := p.pending[id]
	delete(p.pending, id)
	p.mu.Unlock()
	if ch != nil {
		ch <- value
	}
}

func (p *rpcPeer) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

func (p *rpcPeer) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	ch := make(chan rpcReply, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	if err := p.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (p *rpcPeer) notify(method string, params any) error {
	return p.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (p *rpcPeer) response(id json.RawMessage, result any) error {
	return p.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *client) executeCodex(parent, ctx context.Context, task *model.RuntimeTask) (content, errorText string) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, stdout, stdin, stderr, err := providerCommand(runCtx, task, []string{"app-server", "--listen", "stdio://"})
	if err != nil {
		return "", err.Error()
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	peer := newRPCPeer(stdin)
	go peer.serve(stdout)
	var output strings.Builder
	terminal := make(chan error, 1)
	finish := func(err error) {
		select {
		case terminal <- err:
		default:
		}
	}
	peer.onRequest = func(id json.RawMessage, method string, params json.RawMessage) {
		switch method {
		case "item/commandExecution/requestApproval", "execCommandApproval", "item/fileChange/requestApproval", "applyPatchApproval":
			_ = peer.response(id, map[string]any{"decision": "accept"})
		case "item/permissions/requestApproval":
			var p struct {
				Permissions map[string]any `json:"permissions"`
			}
			_ = json.Unmarshal(params, &p)
			_ = peer.response(id, map[string]any{"permissions": p.Permissions, "scope": "turn"})
		case "mcpServer/elicitation/request":
			_ = peer.response(id, map[string]any{"action": "accept", "content": nil})
		default:
			finish(fmt.Errorf("unsupported codex request: %s", method))
		}
	}
	peer.onNotification = func(method string, params json.RawMessage) {
		var data map[string]any
		_ = json.Unmarshal(params, &data)
		switch method {
		case "item/completed":
			if item, _ := data["item"].(map[string]any); item != nil && item["type"] == "agentMessage" {
				if text, _ := item["text"].(string); text != "" {
					output.WriteString(text)
					_ = c.providerDelta(parent, task, text)
				}
			}
		case "turn/completed":
			if turn, _ := data["turn"].(map[string]any); turn != nil && turn["status"] == "failed" {
				if errData, _ := turn["error"].(map[string]any); errData != nil {
					if msg, _ := errData["message"].(string); msg != "" {
						finish(errors.New(msg))
						return
					}
				}
				finish(errors.New("codex turn failed"))
				return
			}
			finish(nil)
		case "error":
			if errData, _ := data["error"].(map[string]any); errData != nil {
				if msg, _ := errData["message"].(string); msg != "" {
					finish(errors.New(msg))
				}
			}
		}
	}
	if _, err = peer.request(runCtx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "aiclaw", "version": "1"}, "capabilities": map[string]any{}}); err != nil {
		return "", withStderr("codex initialize", err, stderr)
	}
	_ = peer.notify("initialized", map[string]any{})
	cwd := task.WorkingDir
	if cwd == "" {
		cwd = "."
	}
	thread, err := peer.request(runCtx, "thread/start", map[string]any{"model": nilIfBlank(task.ModelName), "cwd": cwd, "developerInstructions": nilIfBlank(task.SystemPrompt)})
	if err != nil {
		return "", withStderr("codex thread/start", err, stderr)
	}
	threadID := readString(thread, "thread", "id")
	if threadID == "" {
		threadID = readString(thread, "id")
	}
	if threadID == "" {
		return "", "codex thread/start returned no thread ID"
	}
	_, err = peer.request(runCtx, "turn/start", map[string]any{"threadId": threadID, "input": []map[string]any{{"type": "text", "text": providerPrompt(task, false)}}})
	if err != nil {
		return "", withStderr("codex turn/start", err, stderr)
	}
	select {
	case err = <-terminal:
	case <-runCtx.Done():
		err = runCtx.Err()
	case <-peer.done:
		err = errors.New("codex ended before completing the turn")
	}
	if err != nil {
		return "", withStderr("codex", err, stderr)
	}
	return output.String(), ""
}

func (c *client) executeHermes(parent, ctx context.Context, task *model.RuntimeTask) (content, errorText string) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, stdout, stdin, stderr, err := providerCommand(runCtx, task, []string{"acp"})
	if err != nil {
		return "", err.Error()
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	peer := newRPCPeer(stdin)
	go peer.serve(stdout)
	var output strings.Builder
	peer.onRequest = func(id json.RawMessage, method string, params json.RawMessage) {
		if method != "session/request_permission" {
			return
		}
		var p struct {
			Options []struct {
				OptionID string `json:"optionId"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		_ = json.Unmarshal(params, &p)
		for _, option := range p.Options {
			if option.OptionID != "" && (option.Kind == "allow_once" || option.Kind == "allow_always") {
				_ = peer.response(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option.OptionID}})
				return
			}
		}
		_ = peer.response(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
	}
	peer.onNotification = func(method string, params json.RawMessage) {
		if method != "session/update" && method != "session/notification" {
			return
		}
		var p struct {
			Update json.RawMessage `json:"update"`
		}
		_ = json.Unmarshal(params, &p)
		if normalizeACPUpdateType(readString(p.Update, "sessionUpdate")) != "agent_message_chunk" && normalizeACPUpdateType(readString(p.Update, "type")) != "agent_message_chunk" {
			return
		}
		text := readString(p.Update, "content", "text")
		if text != "" {
			output.WriteString(text)
			_ = c.providerDelta(parent, task, text)
		}
	}
	if _, err = peer.request(runCtx, "initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "aiclaw", "version": "1"}, "clientCapabilities": map[string]any{}}); err != nil {
		return "", withStderr("hermes initialize", err, stderr)
	}
	cwd := task.WorkingDir
	if cwd == "" {
		cwd = "."
	}
	session, err := peer.request(runCtx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}, "model": nilIfBlank(task.ModelName)})
	if err != nil {
		return "", withStderr("hermes session/new", err, stderr)
	}
	sessionID := readString(session, "sessionId")
	if sessionID == "" {
		return "", "hermes session/new returned no session ID"
	}
	if task.ModelName != "" {
		if _, err = peer.request(runCtx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": task.ModelName}); err != nil {
			return "", withStderr("hermes set model", err, stderr)
		}
	}
	_, err = peer.request(runCtx, "session/prompt", map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": providerPrompt(task, true)}}})
	if err != nil {
		return "", withStderr("hermes session/prompt", err, stderr)
	}
	return output.String(), ""
}

func (c *client) executeClaudeStream(parent, ctx context.Context, task *model.RuntimeTask, kind string) (content, errorText string) {
	args := []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "--disallowedTools", "AskUserQuestion"}
	if task.ModelName != "" {
		args = append(args, "--model", task.ModelName)
	}
	if task.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", task.SystemPrompt)
	}
	cmd, stdout, stdin, stderr, err := providerCommand(ctx, task, args)
	if err != nil {
		return "", err.Error()
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	var writeMu sync.Mutex
	write := func(value any) error {
		data, _ := json.Marshal(value)
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err := stdin.Write(append(data, '\n'))
		return err
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": providerPrompt(task, false)}}}})
	}()
	var output, final strings.Builder
	resultSeen := false
	var protocolErr string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var event struct {
			Type      string          `json:"type"`
			Message   json.RawMessage `json:"message"`
			Result    string          `json:"result"`
			IsError   bool            `json:"is_error"`
			Error     string          `json:"error"`
			RequestID string          `json:"request_id"`
			Request   json.RawMessage `json:"request"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Type {
		case "assistant":
			text := messageText(event.Message)
			if text != "" {
				output.WriteString(text)
				_ = c.providerDelta(parent, task, text)
			}
		case "result":
			resultSeen = true
			final.WriteString(event.Result)
			if event.IsError {
				protocolErr = event.Result
			}
			_ = stdin.Close()
		case "control_request":
			_ = write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": event.RequestID, "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}}})
		case "error":
			protocolErr = event.Error
		}
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	writeErr := <-writeDone
	if ctx.Err() != nil {
		return "", ctx.Err().Error()
	}
	if writeErr != nil {
		return "", withStderr(kind+" input", writeErr, stderr)
	}
	if protocolErr != "" {
		return "", protocolErr
	}
	if waitErr != nil {
		return "", withStderr(kind, waitErr, stderr)
	}
	if !resultSeen {
		return "", kind + " stream ended without a result"
	}
	if output.Len() == 0 && final.Len() > 0 {
		_ = c.providerDelta(parent, task, final.String())
		return final.String(), ""
	}
	return output.String(), ""
}

func (c *client) executeCursorStream(parent, ctx context.Context, task *model.RuntimeTask) (content, errorText string) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := []string{"-p", providerPrompt(task, true), "--output-format", "stream-json", "--yolo"}
	if task.WorkingDir != "" {
		args = append(args, "--workspace", task.WorkingDir)
	}
	if task.ModelName != "" {
		args = append(args, "--model", task.ModelName)
	}
	cmd := exec.CommandContext(runCtx, task.Command, args...)
	cmd.Dir = task.WorkingDir
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err.Error()
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return "", err.Error()
	}
	var output, final strings.Builder
	resultSeen := false
	var protocolErr string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(scanner.Text(), "stdout:"), "stderr:"))
		var event struct {
			Type, Subtype, Result, Error, Detail string
			IsError                              bool `json:"is_error"`
			Message, Part                        json.RawMessage
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "assistant":
			text := messageText(event.Message)
			if text != "" {
				output.WriteString(text)
				_ = c.providerDelta(parent, task, text)
			}
		case "text":
			text := readString(event.Part, "text")
			if text != "" {
				output.WriteString(text)
				_ = c.providerDelta(parent, task, text)
			}
		case "result":
			resultSeen = true
			final.WriteString(event.Result)
			if event.IsError || event.Subtype == "error" {
				protocolErr = firstNonEmpty(event.Error, event.Detail, event.Result)
			}
			cancel() // Cursor can leave a worker alive after the terminal result.
		case "error":
			protocolErr = firstNonEmpty(event.Error, event.Detail, event.Result)
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err().Error()
	}
	if protocolErr != "" {
		return "", protocolErr
	}
	if waitErr != nil && !resultSeen {
		return "", withStderr("cursor-agent", waitErr, &stderr)
	}
	if !resultSeen {
		return "", "cursor-agent stream ended without a result"
	}
	if output.Len() == 0 && final.Len() > 0 {
		_ = c.providerDelta(parent, task, final.String())
		return final.String(), ""
	}
	return output.String(), ""
}

func (c *client) executeOpenClaw(parent, ctx context.Context, task *model.RuntimeTask) (content, errorText string) {
	args := []string{"agent", "--local", "--json", "--session-id", "aiclaw-" + task.RunID}
	if task.TimeoutSeconds > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%d", task.TimeoutSeconds))
	}
	if task.ModelName != "" {
		args = append(args, "--agent", task.ModelName)
	}
	args = append(args, "--message", providerPrompt(task, true))
	cmd := exec.CommandContext(ctx, task.Command, args...)
	cmd.Dir = task.WorkingDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", withStderr("openclaw", err, &stderr)
	}
	if ctx.Err() != nil {
		return "", ctx.Err().Error()
	}
	text, err := openClawResultText(stdout.Bytes())
	if err != nil {
		return "", err.Error()
	}
	if text == "" {
		return "", "openclaw returned no parseable output"
	}
	_ = c.providerDelta(parent, task, text)
	return text, ""
}

func messageText(raw json.RawMessage) string {
	var message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range message.Content {
		if block.Type == "text" || block.Type == "output_text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func readString(raw json.RawMessage, path ...string) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[key]
	}
	text, _ := value.(string)
	return text
}
func nilIfBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeACPUpdateType(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), "_", ""), "-", ""))
	if value == "agentmessagechunk" {
		return "agent_message_chunk"
	}
	return ""
}
func withStderr(prefix string, err error, stderr *bytes.Buffer) string {
	if stderr != nil && strings.TrimSpace(stderr.String()) != "" {
		return fmt.Sprintf("%s: %v: %s", prefix, err, strings.TrimSpace(stderr.String()))
	}
	return fmt.Sprintf("%s: %v", prefix, err)
}

func openClawResultText(data []byte) (string, error) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return "", errors.New("openclaw returned invalid JSON")
	}
	var texts []string
	var visit func(any)
	visit = func(v any) {
		switch item := v.(type) {
		case map[string]any:
			if text, ok := item["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
			for _, child := range item {
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return strings.Join(texts, "\n"), nil
}
