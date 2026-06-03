package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/internal/skills"
)

type skillToolArgs struct {
	Action      string `json:"action"`
	FileName    string `json:"file_name,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type skillToolResult struct {
	Success bool   `json:"success"`
	Action  string `json:"action,omitempty"`
	Message string `json:"message,omitempty"`
	Items   any    `json:"items,omitempty"`
	Path    string `json:"path,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (e *Executor) skillHandler(_ context.Context, args string) (string, error) {
	var p skillToolArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if e.ws == nil {
		return skillErr(p.Action, "workspace not configured"), nil
	}

	switch p.Action {
	case "list_pending":
		return e.skillListPending(p)
	case "read_pending":
		return e.skillReadPending(p)
	case "promote":
		return e.skillPromote(p)
	case "discard":
		return e.skillDiscard(p)
	case "list_active":
		return e.skillListActive(p)
	default:
		return skillErr(p.Action, fmt.Sprintf("unknown action %q (use list_pending, read_pending, promote, discard, list_active)", p.Action)), nil
	}
}

func (e *Executor) skillListPending(p skillToolArgs) (string, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 10
	}
	items, err := ListPendingSkills(e.ws.Root(), limit)
	if err != nil {
		return skillErr("list_pending", err.Error()), nil
	}
	r := skillToolResult{
		Success: true,
		Action:  "list_pending",
		Items:   items,
		Message: fmt.Sprintf("Found %d pending skill candidates. Use read_pending(file_name=...) to inspect full content, then promote(...) to activate one.", len(items)),
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

func (e *Executor) skillReadPending(p skillToolArgs) (string, error) {
	if strings.TrimSpace(p.FileName) == "" {
		return skillErr("read_pending", "file_name is required"), nil
	}
	content, err := ReadPendingSkill(e.ws.Root(), p.FileName)
	if err != nil {
		return skillErr("read_pending", err.Error()), nil
	}
	r := skillToolResult{
		Success: true,
		Action:  "read_pending",
		Items:   content,
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

func (e *Executor) skillPromote(p skillToolArgs) (string, error) {
	if strings.TrimSpace(p.FileName) == "" || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Description) == "" {
		return skillErr("promote", "file_name, name, and description are required"), nil
	}
	dir, err := PromotePendingSkill(e.ws.Root(), e.ws.Skills(), p.FileName, p.Name, p.Description)
	if err != nil {
		return skillErr("promote", err.Error()), nil
	}
	// 转正后让 skill 缓存失效（确保下一次执行能加载到新 skill）
	e.sc.mu.Lock()
	e.sc.data = nil
	e.sc.mu.Unlock()

	r := skillToolResult{
		Success: true,
		Action:  "promote",
		Path:    dir,
		Message: fmt.Sprintf("Promoted skill «%s». It will be loaded automatically in the next session.", p.Name),
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

func (e *Executor) skillDiscard(p skillToolArgs) (string, error) {
	if strings.TrimSpace(p.FileName) == "" {
		return skillErr("discard", "file_name is required"), nil
	}
	if err := DiscardPendingSkill(e.ws.Root(), p.FileName); err != nil {
		return skillErr("discard", err.Error()), nil
	}
	r := skillToolResult{
		Success: true,
		Action:  "discard",
		Message: "Candidate deleted.",
	}
	out, _ := json.Marshal(r)
	return string(out), nil
}

func (e *Executor) skillListActive(p skillToolArgs) (string, error) {
	infos, err := skills.ScanAll(e.ws.Skills())
	if err != nil {
		return skillErr("list_active", err.Error()), nil
	}
	type item struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		DirName     string `json:"dir_name"`
	}
	out := make([]item, 0, len(infos))
	for _, info := range infos {
		out = append(out, item{
			Name:        info.Name,
			Description: info.Description,
			DirName:     info.DirName,
		})
	}
	r := skillToolResult{
		Success: true,
		Action:  "list_active",
		Items:   out,
	}
	data, _ := json.Marshal(r)
	return string(data), nil
}

func skillErr(action, msg string) string {
	r := skillToolResult{Success: false, Action: action, Error: msg}
	out, _ := json.Marshal(r)
	return string(out)
}
