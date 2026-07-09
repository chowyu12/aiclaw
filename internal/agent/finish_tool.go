package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/chowyu12/aiclaw/internal/model"
)

const finishToolName = "finish"

type finishResultSink struct {
	mu     sync.Mutex
	called bool
	answer string
}

func (s *finishResultSink) set(answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = true
	s.answer = answer
}

func (s *finishResultSink) result() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called, s.answer
}

type finishResultSinkContextKey struct{}

func withFinishResultSink(ctx context.Context, sink *finishResultSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, finishResultSinkContextKey{}, sink)
}

func publishFinishResult(ctx context.Context, answer string) {
	sink, _ := ctx.Value(finishResultSinkContextKey{}).(*finishResultSink)
	if sink != nil {
		sink.set(answer)
	}
}

func finishHandler(ctx context.Context, args string) (string, error) {
	answer := extractFinishAnswer(args)
	publishFinishResult(ctx, answer)
	return "final answer received", nil
}

func extractFinishAnswer(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Answer      string `json:"answer"`
		FinalAnswer string `json:"final_answer"`
		Content     string `json:"content"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return trimmed
	}
	for _, v := range []string{payload.Answer, payload.FinalAnswer, payload.Content} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func finishToolDef() model.Tool {
	return model.Tool{
		Name:        finishToolName,
		Description: "Deliver the final answer and end the current turn. Call only when the user's request is fully completed.",
		HandlerType: model.HandlerBuiltin,
		Enabled:     true,
		FunctionDef: mustModelJSON(map[string]any{
			"name":        finishToolName,
			"description": "Deliver the complete user-facing final answer and end the turn. Do not call this for progress updates.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"answer": map[string]any{
						"type":        "string",
						"description": "The complete final answer for the user.",
					},
				},
				"required": []string{"answer"},
			},
		}),
	}
}

func mustModelJSON(v any) model.JSON {
	data, _ := json.Marshal(v)
	return model.JSON(data)
}
