package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type processParams struct {
	Action    string `json:"action"`
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
}

type session struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	Running   bool      `json:"running"`
	ExitCode  int       `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
	output    *bytes.Buffer
	cmd       *exec.Cmd
	mu        sync.Mutex
}

var (
	sessions = make(map[string]*session)
	mu       sync.RWMutex
)

const maxOutput = 64_000

func Handler(ctx context.Context, args string) (string, error) {
	var p processParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch p.Action {
	case "start":
		return startSession(p.Command)
	case "list":
		return listSessions()
	case "read":
		return readSession(p.SessionID)
	case "kill":
		return killSession(p.SessionID)
	default:
		return "", fmt.Errorf("unknown action %q, supported: start, list, read, kill", p.Action)
	}
}

func startSession(command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required for start")
	}

	shell := findShell()
	cmd := exec.Command(shell, "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start command: %w", err)
	}

	s := &session{
		ID:        uuid.New().String()[:8],
		Command:   command,
		PID:       cmd.Process.Pid,
		Running:   true,
		StartedAt: time.Now(),
		output:    &buf,
		cmd:       cmd,
	}

	mu.Lock()
	sessions[s.ID] = s
	mu.Unlock()

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.Running = false
		if cmd.ProcessState != nil {
			s.ExitCode = cmd.ProcessState.ExitCode()
		}
		s.mu.Unlock()
		if err != nil {
			log.WithFields(log.Fields{"session": s.ID, "error": err}).Debug("[process] session ended with error")
		}
	}()

	return fmt.Sprintf("Session started: id=%s pid=%d command=%q", s.ID, s.PID, command), nil
}

func listSessions() (string, error) {
	mu.RLock()
	defer mu.RUnlock()

	if len(sessions) == 0 {
		return "No active sessions.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Sessions (%d):\n", len(sessions)))
	for _, s := range sessions {
		s.mu.Lock()
		status := "running"
		if !s.Running {
			status = fmt.Sprintf("exited (code=%d)", s.ExitCode)
		}
		sb.WriteString(fmt.Sprintf("  %s  pid=%-6d  %s  %s  (%s ago)\n",
			s.ID, s.PID, status, s.Command,
			time.Since(s.StartedAt).Truncate(time.Second),
		))
		s.mu.Unlock()
	}
	return sb.String(), nil
}

func readSession(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("session_id is required for read")
	}

	mu.RLock()
	s, ok := sessions[id]
	mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("session %q not found", id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	out := s.output.String()
	if len(out) > maxOutput {
		out = out[len(out)-maxOutput:]
		out = "... (earlier output truncated)\n" + out
	}

	status := "running"
	if !s.Running {
		status = fmt.Sprintf("exited (code=%d)", s.ExitCode)
	}

	return fmt.Sprintf("[session %s | pid %d | %s]\n%s", s.ID, s.PID, status, out), nil
}

func killSession(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("session_id is required for kill")
	}

	mu.RLock()
	s, ok := sessions[id]
	mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("session %q not found", id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.Running {
		return fmt.Sprintf("Session %s already exited (code=%d)", s.ID, s.ExitCode), nil
	}

	if err := syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}

	return fmt.Sprintf("Session %s (pid=%d) killed", s.ID, s.PID), nil
}

func findShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	for _, s := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "/bin/sh"
}
