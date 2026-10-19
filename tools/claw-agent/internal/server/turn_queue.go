package server

import (
	"context"
	"encoding/json"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

type queuedTurn struct {
	ctx   context.Context
	turn  string
	input protocol.TurnStartParams
}

// All queue mutations and submission claims share submissionMu. A request is
// acknowledged only after its payload has been saved by AcceptSubmission.
func (s *Server) scheduleTurn(ctx context.Context, session *agent.Session, turn string, input protocol.TurnStartParams, front bool) (bool, error) {
	if s.activeTurns == nil {
		s.activeTurns = make(map[string]string)
		s.turnCancels = make(map[string]context.CancelFunc)
		s.queuedTurns = make(map[string][]queuedTurn)
	}
	if s.activeTurns[session.ID] != "" || session.Busy() {
		entry := queuedTurn{ctx, turn, input}
		if front {
			s.queuedTurns[session.ID] = append([]queuedTurn{entry}, s.queuedTurns[session.ID]...)
		} else {
			s.queuedTurns[session.ID] = append(s.queuedTurns[session.ID], entry)
		}
		return true, nil
	}
	if err := s.db.SetSubmission(ctx, session.ID, input.RequestID, turn, "running"); err != nil {
		return false, err
	}
	s.launchTurn(ctx, session, turn, input)
	return false, nil
}

func (s *Server) launchTurn(ctx context.Context, session *agent.Session, turn string, input protocol.TurnStartParams) {
	ctx, cancel := context.WithCancel(ctx)
	s.activeTurns[session.ID] = turn
	s.turnCancels[session.ID] = cancel
	go session.RunRequest(ctx, turn, input, &emitter{server: s})
}

func (s *Server) advanceQueue(completed protocol.TurnNotification) {
	s.submissionMu.Lock()
	defer s.submissionMu.Unlock()
	id := completed.SessionID
	if active := s.activeTurns[id]; active != "" && active != completed.TurnID {
		return
	}
	delete(s.activeTurns, id)
	if cancel := s.turnCancels[id]; cancel != nil {
		cancel()
	}
	delete(s.turnCancels, id)
	queue := s.queuedTurns[id]
	delete(s.queuedTurns, id)
	// A stop/error never silently starts more work. Queued payloads remain in
	// SQLite for explicit resume/cancel, including after the application restarts.
	if completed.Error == "" && len(queue) > 0 {
		entry := queue[0]
		if session := s.session(id); session != nil && entry.ctx.Err() == nil {
			if err := s.db.SetSubmission(entry.ctx, id, entry.input.RequestID, entry.turn, "running"); err == nil {
				s.queuedTurns[id] = queue[1:]
				s.launchTurn(entry.ctx, session, entry.turn, entry.input)
			} else {
				s.options.Logf("claim queued input: %v", err)
			}
		}
	}
	s.notifyQueue(id)
}

func (s *Server) removeQueued(id, requestID string) {
	queue := s.queuedTurns[id]
	for i, entry := range queue {
		if entry.input.RequestID == requestID {
			s.queuedTurns[id] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (s *Server) notifyQueue(id string) {
	pending, err := s.db.PendingSubmissions(context.Background(), id)
	if err != nil {
		s.options.Logf("read queued input: %v", err)
		return
	}
	raw, _ := json.Marshal(map[string]any{"sessionId": id, "pending": queueSummaries(pending), "queuedRequestIds": s.queuedRequestIDs(id)})
	s.write(frame{JSONRPC: "2.0", Method: "session/queueUpdated", Params: raw})
}

// The full attachments stay in SQLite; queue UI snapshots must not resend all
// queued base64 images and exceed the host transport's frame limit.
func queueSummaries(pending []store.Submission) []store.Submission {
	for i := range pending {
		var input struct {
			Mode string `json:"mode"`
			Text string `json:"text"`
		}
		if json.Unmarshal(pending[i].Payload, &input) == nil && input.Mode == "queue" {
			pending[i].Payload, _ = json.Marshal(input)
		}
	}
	return pending
}

func (s *Server) queuedRequestIDs(id string) []string {
	ids := make([]string, 0, len(s.queuedTurns[id]))
	for _, entry := range s.queuedTurns[id] {
		ids = append(ids, entry.input.RequestID)
	}
	return ids
}
