package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestReasoningConfigureAcknowledgesOnlyValidatedUpdates(t *testing.T) {
	s, root := collabServer(t, &scriptedModel{root: func(int, wireRequest) string { return sseText("done") }, child: func(wireRequest) string { return sseText("child") }})
	writer := &receiptWriter{}
	s.out = writer
	effort := "high"
	request := func(id string, params protocol.SessionConfigureParams) bool {
		raw, _ := json.Marshal(params)
		key, _ := json.Marshal(id)
		s.dispatch(context.Background(), frame{ID: key, Method: protocol.MethodSessionConfigure, Params: raw})
		writer.mu.Lock()
		defer writer.mu.Unlock()
		for _, f := range writer.frames {
			if string(f.ID) == string(key) {
				return f.Error == nil
			}
		}
		t.Fatal("missing acknowledgement")
		return false
	}
	if !request("valid", protocol.SessionConfigureParams{SessionID: root.ID, ReasoningEffort: &effort}) || root.Model().ReasoningEffort != "high" {
		t.Fatal("update not applied")
	}
	effort = "invalid"
	if request("invalid", protocol.SessionConfigureParams{SessionID: root.ID, ReasoningEffort: &effort}) || root.Model().ReasoningEffort != "high" {
		t.Fatal("invalid update applied")
	}
	effort = "low"
	if request("mixed", protocol.SessionConfigureParams{SessionID: root.ID, ReasoningEffort: &effort, Model: protocol.ModelConfig{Model: "different"}}) {
		t.Fatal("mixed update accepted")
	}
	effort = ""
	if !request("default", protocol.SessionConfigureParams{SessionID: root.ID, ReasoningEffort: &effort}) || root.Model().ReasoningEffort != "" {
		t.Fatal("default effort not applied")
	}
}
