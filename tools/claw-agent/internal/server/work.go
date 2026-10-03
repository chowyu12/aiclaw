package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

func (s *Server) handleWork(ctx context.Context, f frame) {
	var p struct {
		SessionID string                `json:"sessionId"`
		ItemID    string                `json:"itemId"`
		RequestID string                `json:"requestId"`
		Action    string                `json:"action"`
		ChangeID  string                `json:"changeId"`
		Model     *protocol.ModelConfig `json:"model"`
		Goal      agent.Goal            `json:"goal"`
		Update    agent.GoalUpdate      `json:"update"`
	}
	if err := json.Unmarshal(f.Params, &p); err != nil || p.SessionID == "" {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	session := s.session(p.SessionID)
	if session == nil {
		s.writeError(f.ID, codeInvalidParams, "restore the session first")
		return
	}
	var result any
	var err error
	switch f.Method {
	case "session/work":
		g, e := session.Goal()
		err = e
		pending, e := s.db.PendingSubmissions(ctx, p.SessionID)
		if err == nil {
			err = e
		}
		var execution store.Execution
		_ = s.db.State(ctx, p.SessionID, "execution", &execution)
		if execution.State == "uncertain" && len(pending) == 0 {
			pending = append(pending, store.Submission{TurnID: execution.TurnID, State: "uncertain", CreatedAt: execution.StartedAt, Payload: json.RawMessage(`{"text":"Previous execution stopped; inspect its outcome before continuing."}`)})
		}
		source, item := session.ForkOrigin()
		result = map[string]any{"goal": g, "pending": pending, "forkSourceId": source, "forkItemId": item}
	case "goal/set":
		result, err = session.SetGoal(p.Goal)
	case "goal/update":
		if p.Update.Status == "active" {
			pending, e := s.db.PendingSubmissions(ctx, p.SessionID)
			var execution store.Execution
			_ = s.db.State(ctx, p.SessionID, "execution", &execution)
			if e != nil {
				err = e
				break
			}
			if len(pending) > 0 || execution.State == "uncertain" {
				err = i18n.E("请先检查并处理恢复提示，再继续目标")
				break
			}
		}
		result, err = session.UpdateGoal(p.Update, true)
		if err == nil && p.Update.Status != "" && p.Update.Status != "active" {
			session.Interrupt()
			s.collab.InterruptTree(session.ID)
		}
	case "session/fork":
		id := fmt.Sprintf("s_%d", time.Now().UnixNano())
		var child *agent.Session
		child, err = session.ForkAt(ctx, id, p.ItemID, p.Model, s.sessionOptions(id)...)
		if err == nil {
			s.guard(child)
			err = child.Checkpoint()
			if err == nil {
				s.sessMu.Lock()
				s.sessions[id] = child
				s.sessMu.Unlock()
				result = map[string]any{"sessionId": id}
			} else {
				child.Close()
			}
		}
	case "session/recover":
		s.submissionMu.Lock()
		defer s.submissionMu.Unlock()
		if session.Busy() {
			err = i18n.E("请等待当前轮次结束")
			break
		}
		if p.Action == "dismiss" && p.RequestID == "" {
			var execution store.Execution
			err = s.db.State(ctx, p.SessionID, "execution", &execution)
			if err == nil && execution.State != "uncertain" {
				err = i18n.E("这次执行已无需恢复处理")
			}
			if err == nil {
				execution.State = "reviewed"
				err = s.db.PutState(ctx, p.SessionID, "execution", execution)
			}
			result = map[string]any{"dismissed": err == nil}
			break
		}
		var recovered protocol.TurnStartParams
		record, e := s.db.Submission(ctx, p.SessionID, p.RequestID)
		err = e
		if err != nil {
			break
		}
		if p.Action == "dismiss" {
			err = s.db.ResolveSubmission(ctx, p.SessionID, p.RequestID)
			if err == nil {
				session.RemovePending(p.RequestID)
				pending, _ := s.db.PendingSubmissions(ctx, p.SessionID)
				if len(pending) == 0 {
					var execution store.Execution
					if s.db.State(ctx, p.SessionID, "execution", &execution) == nil && execution.State == "uncertain" {
						execution.State = "reviewed"
						err = s.db.PutState(ctx, p.SessionID, "execution", execution)
					}
				}
			}
			result = map[string]any{"dismissed": err == nil}
			break
		}
		if p.Action != "resume" || record.State != "queued" {
			err = i18n.E("只能恢复尚未处理的排队输入；结果不确定时请先检查再发送新指令")
			break
		}
		err = json.Unmarshal(record.Payload, &recovered)
		if err != nil {
			break
		}
		turn := fmt.Sprintf("t_%d", time.Now().UnixNano())
		// Claim before scheduling, so a second click cannot start it twice.
		err = s.db.SetSubmission(ctx, p.SessionID, p.RequestID, turn, "running")
		if err != nil {
			break
		}
		result = protocol.TurnStartResult{TurnID: turn}
		session.RemovePending(p.RequestID)
		go session.RunRequest(ctx, turn, recovered, &emitter{server: s})
	case "changes/list":
		result, err = s.db.Changes(ctx, p.SessionID)
	case "changes/undo":
		err = session.UndoChange(ctx, p.ChangeID)
		result = map[string]any{"undone": err == nil}
	}
	if err != nil {
		s.writeError(f.ID, codeInvalidParams, err.Error())
		return
	}
	s.writeResult(f.ID, result)
}
