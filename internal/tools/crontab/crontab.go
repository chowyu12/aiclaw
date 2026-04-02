package crontab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/robfig/cron/v3"
)

type cronArgs struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Expression  string `json:"expression"`
	Command     string `json:"command"`
	Pattern     string `json:"pattern"`
	LogOutput   bool   `json:"log_output"`
	SystemEvent string `json:"system_event"`
	Interval    string `json:"interval"`
	EventID     string `json:"event_id"`
}

type wakeEvent struct {
	ID          string    `json:"id"`
	Expression  string    `json:"expression"`
	SystemEvent string    `json:"system_event"`
	Interval    string    `json:"interval"`
	CreatedAt   time.Time `json:"created_at"`
	NextFire    string    `json:"next_fire,omitempty"`
}

var (
	events   []wakeEvent
	eventsMu sync.RWMutex
)

func Handler(ctx context.Context, args string) (string, error) {
	var p cronArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	ws := workspace.FromContext(ctx)

	switch p.Action {
	case "schedule":
		return schedule(ws, p)
	case "list":
		return listJobs()
	case "remove":
		return removeJob(p)
	case "add_event":
		return addEvent(ws, p)
	case "list_events":
		return listEvents(ws)
	case "remove_event":
		return removeEvent(ws, p)
	default:
		return "", fmt.Errorf("unknown action %q, supported: schedule, list, remove, add_event, list_events, remove_event", p.Action)
	}
}

func scriptDir(ws *workspace.Workspace) string {
	if ws != nil {
		return ws.CronScripts()
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiclaw", "cron", "scripts")
}

func logDir(ws *workspace.Workspace) string {
	if ws != nil {
		return ws.CronLogs()
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiclaw", "cron", "logs")
}

func eventsFile(ws *workspace.Workspace) string {
	if ws != nil {
		return filepath.Join(ws.CronDir(), "events.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aiclaw", "cron", "events.json")
}

func defaultPath() string {
	if runtime.GOOS == "darwin" {
		return "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

func schedule(ws *workspace.Workspace, p cronArgs) (string, error) {
	if p.Expression == "" {
		return "", fmt.Errorf("expression is required")
	}

	if p.Content != "" {
		return scheduleScript(ws, p)
	}
	if p.Command != "" {
		return addCronJob(ws, p)
	}
	return "", fmt.Errorf("either command or content (script) is required")
}

func scheduleScript(ws *workspace.Workspace, p cronArgs) (string, error) {
	if p.Name == "" {
		return "", fmt.Errorf("name is required when providing script content")
	}
	if strings.HasPrefix(p.Expression, "@every") {
		return "", fmt.Errorf("@every is not supported by system crontab; use a standard 5-field expression")
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(p.Expression); err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", p.Expression, err)
	}

	dir := scriptDir(ws)
	os.MkdirAll(dir, 0o755)

	name := sanitizeName(p.Name)
	if !strings.HasSuffix(name, ".sh") {
		name += ".sh"
	}

	content := p.Content
	if !strings.HasPrefix(strings.TrimSpace(content), "#!") {
		content = "#!/bin/bash\nset -eo pipefail\nexport PATH=\"" + defaultPath() + ":$PATH\"\n\n" + content
	}

	filePath := filepath.Join(dir, name)
	if err := os.WriteFile(filePath, []byte(content), 0o755); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	p.Command = filePath
	result, err := addCronJob(ws, p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Script saved: %s\n%s", filePath, result), nil
}

func addCronJob(ws *workspace.Workspace, p cronArgs) (string, error) {
	if strings.HasPrefix(p.Expression, "@every") {
		return "", fmt.Errorf("@every is not supported by system crontab; use a standard 5-field expression")
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(p.Expression); err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", p.Expression, err)
	}

	command := p.Command
	if p.LogOutput {
		os.MkdirAll(logDir(ws), 0o755)
		logName := sanitizeName(filepath.Base(command))
		logName = strings.TrimSuffix(logName, ".sh")
		logFile := filepath.Join(logDir(ws), logName+".log")
		command = fmt.Sprintf("%s >> %s 2>&1", command, logFile)
	}

	entry := fmt.Sprintf("%s %s", p.Expression, command)

	existing, _ := exec.Command("crontab", "-l").Output()
	existingStr := string(existing)
	for _, line := range strings.Split(existingStr, "\n") {
		if strings.TrimSpace(line) == entry {
			return fmt.Sprintf("Job already exists: %s", entry), nil
		}
	}

	newCrontab := ensureEnvHeader(existingStr)
	newCrontab = strings.TrimRight(newCrontab, "\n") + "\n" + entry + "\n"

	if err := installCrontab(newCrontab); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cron job added:\n  %s", entry), nil
}

func addEvent(ws *workspace.Workspace, p cronArgs) (string, error) {
	if p.SystemEvent == "" {
		return "", fmt.Errorf("system_event is required for add_event")
	}
	if p.Expression == "" && p.Interval == "" {
		return "", fmt.Errorf("expression or interval is required for add_event")
	}

	expr := p.Expression
	if expr == "" {
		expr = "@every " + p.Interval
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("invalid expression %q: %w", expr, err)
	}

	nextFire := sched.Next(time.Now()).Format(time.RFC3339)

	evt := wakeEvent{
		ID:          fmt.Sprintf("evt_%d", time.Now().UnixMilli()),
		Expression:  expr,
		SystemEvent: p.SystemEvent,
		Interval:    p.Interval,
		CreatedAt:   time.Now(),
		NextFire:    nextFire,
	}

	eventsMu.Lock()
	loadEventsLocked(ws)
	events = append(events, evt)
	saveEventsLocked(ws)
	eventsMu.Unlock()

	return fmt.Sprintf("Wake event created:\n  ID: %s\n  Schedule: %s\n  Next fire: %s\n  Event: %s",
		evt.ID, expr, nextFire, evt.SystemEvent), nil
}

func listEvents(ws *workspace.Workspace) (string, error) {
	eventsMu.RLock()
	loadEventsLocked(ws)
	list := make([]wakeEvent, len(events))
	copy(list, events)
	eventsMu.RUnlock()

	if len(list) == 0 {
		return "No wake events.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Wake events (%d):\n", len(list)))
	for _, e := range list {
		sb.WriteString(fmt.Sprintf("  %s  schedule=%s  next=%s\n    event: %s\n",
			e.ID, e.Expression, e.NextFire, e.SystemEvent))
	}
	return sb.String(), nil
}

func removeEvent(ws *workspace.Workspace, p cronArgs) (string, error) {
	if p.EventID == "" {
		return "", fmt.Errorf("event_id is required for remove_event")
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	loadEventsLocked(ws)

	found := false
	filtered := events[:0]
	for _, e := range events {
		if e.ID == p.EventID {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}

	if !found {
		return "", fmt.Errorf("event %q not found", p.EventID)
	}

	events = filtered
	saveEventsLocked(ws)
	return fmt.Sprintf("Event %s removed.", p.EventID), nil
}

func loadEventsLocked(ws *workspace.Workspace) {
	data, err := os.ReadFile(eventsFile(ws))
	if err != nil {
		return
	}
	json.Unmarshal(data, &events)
}

func saveEventsLocked(ws *workspace.Workspace) {
	data, _ := json.MarshalIndent(events, "", "  ")
	ef := eventsFile(ws)
	os.MkdirAll(filepath.Dir(ef), 0o755)
	os.WriteFile(ef, data, 0o644)
}

func ensureEnvHeader(existing string) string {
	hasShell := false
	hasPath := false
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "SHELL=") {
			hasShell = true
		}
		if strings.HasPrefix(trimmed, "PATH=") {
			hasPath = true
		}
	}

	var header strings.Builder
	if !hasShell {
		header.WriteString("SHELL=/bin/bash\n")
	}
	if !hasPath {
		header.WriteString("PATH=" + defaultPath() + "\n")
	}
	if header.Len() == 0 {
		return existing
	}
	return header.String() + existing
}

func installCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install crontab: %w\n%s", err, string(output))
	}
	return nil
}

func listJobs() (string, error) {
	output, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "no crontab") {
			return "No crontab entries found.", nil
		}
		return "", fmt.Errorf("list crontab: %w\n%s", err, string(output))
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "No crontab entries found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Current crontab (%d lines):\n", len(lines)))
	for i, line := range lines {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, line))
	}
	return sb.String(), nil
}

func removeJob(p cronArgs) (string, error) {
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required for remove")
	}

	existing, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read crontab: %w\n%s", err, string(existing))
	}

	var kept, removed []string
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.Contains(line, p.Pattern) {
			removed = append(removed, line)
		} else {
			kept = append(kept, line)
		}
	}

	if len(removed) == 0 {
		return fmt.Sprintf("No entries matching %q found.", p.Pattern), nil
	}

	newCrontab := strings.Join(kept, "\n")
	if !strings.HasSuffix(newCrontab, "\n") {
		newCrontab += "\n"
	}

	if err := installCrontab(newCrontab); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Removed %d entries:\n", len(removed)))
	for _, line := range removed {
		sb.WriteString(fmt.Sprintf("  - %s\n", line))
	}
	return sb.String(), nil
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "..", "_")
	return name
}
