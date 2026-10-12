package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

type checkpointEmitter struct {
	recordingEmitter
	checkpoint func(protocol.Item)
}

func (e *checkpointEmitter) Notify(method string, params any) {
	e.recordingEmitter.Notify(method, params)
	if notification, ok := params.(protocol.ItemNotification); ok && notification.Item.Kind == protocol.ItemNotice && strings.Contains(notification.Item.Text, "模型循环") {
		e.checkpoint(notification.Item)
	}
}

func TestIterationCheckpointsSaveAndContinue(t *testing.T) {
	if iterationCheckpointInterval != 200 {
		t.Fatal("checkpoint must be every 200 model iterations")
	}
	for _, calls := range []int{199, 400} {
		t.Run(fmt.Sprint(calls), func(t *testing.T) {
			script := make([]string, 0, calls+1)
			for i := 0; i < calls; i++ {
				if calls == 199 {
					// Multiple tools in one response still count as one model iteration.
					script = append(script, sseToolCalls([3]string{fmt.Sprint(i), "fixture", `{}`}, [3]string{fmt.Sprintf("second_%d", i), "fixture", `{}`}))
				} else {
					script = append(script, sseToolCall(fmt.Sprint(i), "fixture", `{}`))
				}
			}
			script = append(script, sseText("finished"))
			model := &fakeModel{script: script}
			s := newTestSession(t, model, protocol.ApprovalBypass)
			s.config.Model.ContextWindow = 1000000
			if err := s.registry.Register(tools.Tool{Name: "fixture", Effect: tools.EffectRead, Handler: func(context.Context, json.RawMessage, *tools.Env) (string, error) { return "ok", nil }}); err != nil {
				t.Fatal(err)
			}
			db := persistTestSession(t, s)
			var checkpoints []int
			emitter := &checkpointEmitter{checkpoint: func(item protocol.Item) {
				rows, err := db.Transcript(context.Background(), s.ID)
				if err != nil {
					t.Fatal(err)
				}
				results := 0
				for _, raw := range rows {
					var m llm.Message
					if err := json.Unmarshal(raw, &m); err != nil {
						t.Fatal(err)
					}
					if m.Role == llm.RoleTool {
						results++
					}
				}
				checkpoints = append(checkpoints, results)
				if !strings.Contains(item.Text, fmt.Sprint(results)) {
					t.Fatalf("notice does not match persisted progress: %s, %d", item.Text, results)
				}
			}}
			s.RunTurn(context.Background(), "turn", "work", nil, nil, emitter)
			if s.LastAnswer() != "finished" || model.calls != calls+1 {
				t.Fatalf("stopped early: answer=%s model calls=%d", s.LastAnswer(), model.calls)
			}
			if emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
				t.Fatalf("unexpected error: %v", emitter.events)
			}
			var want []int
			if calls == 400 {
				want = []int{200, 400}
			}
			if !reflect.DeepEqual(checkpoints, want) {
				t.Fatalf("checkpoints=%v want=%v", checkpoints, want)
			}
		})
	}
}

func TestCancellationAtIterationCheckpointStopsBeforeNextSample(t *testing.T) {
	script := make([]string, 0, 201)
	for i := 0; i < 200; i++ {
		script = append(script, sseToolCall(fmt.Sprint(i), "get_goal", `{}`))
	}
	script = append(script, sseText("should not run"))
	model := &fakeModel{script: script}
	s := newTestSession(t, model, protocol.ApprovalBypass)
	s.config.Model.ContextWindow = 1000000
	persistTestSession(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter := &checkpointEmitter{checkpoint: func(protocol.Item) { cancel() }}
	s.RunTurn(ctx, "turn", "work", nil, nil, emitter)
	if model.calls != 200 || s.LastAnswer() == "should not run" {
		t.Fatalf("cancelled checkpoint sampled again: calls=%d answer=%s", model.calls, s.LastAnswer())
	}
	if !emitter.find(protocol.NotifyTurnCompleted, `"error"`) {
		t.Fatal("cancellation not reported")
	}
}
