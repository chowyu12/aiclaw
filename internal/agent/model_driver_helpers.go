package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/pkg/modelcaps"
)

const maxStreamMidwayRetries = 2

func (st *harnessTurnState) shouldRetryStreamMidway(err error) bool {
	if st == nil || !isStreamMidwayError(err) {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.streamMidwayRetries >= maxStreamMidwayRetries {
		return false
	}
	st.streamMidwayRetries++
	return true
}

func streamMidwayBackoff(retry int) time.Duration {
	if retry <= 0 {
		retry = 1
	}
	return time.Duration(retry) * 300 * time.Millisecond
}

func isStreamMidwayError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many empty messages") ||
		strings.Contains(msg, "stream broken") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "stream interrupted") ||
		strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "client connection lost") ||
		strings.Contains(msg, "server disconnected") ||
		strings.Contains(msg, "response body closed")
}

func (st *harnessTurnState) currentModel(defaultModel string) string {
	if st == nil {
		return strings.TrimSpace(defaultModel)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if modelName := strings.TrimSpace(st.activeModel); modelName != "" {
		return modelName
	}
	return strings.TrimSpace(defaultModel)
}

func (st *harnessTurnState) fallbackAlreadyActivated() bool {
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.fallbackActivated
}

func (st *harnessTurnState) markFallbackActivated() {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.fallbackActivated = true
	st.mu.Unlock()
}

func (st *harnessTurnState) activateFallbackModel(modelName string) {
	if st == nil {
		return
	}
	st.mu.Lock()
	st.fallbackActivated = true
	st.activeModel = strings.TrimSpace(modelName)
	st.mu.Unlock()
}

func fallbackModelForLLMError(ag *model.Agent, currentModel string, err error, fallbackUsed bool, req openai.ChatCompletionRequest) string {
	if ag == nil || fallbackUsed {
		return ""
	}
	if !isFallbackEligibleLLMError(err) {
		return ""
	}
	current := strings.TrimSpace(currentModel)
	if current == "" {
		current = strings.TrimSpace(ag.ModelName)
	}
	fallback := strings.TrimSpace(ag.FallbackModelName)
	if fallback == "" || fallback == current {
		return ""
	}
	caps := modelcaps.GetModelCaps(fallback)
	if len(req.Tools) > 0 && !caps.FunctionCalling {
		return ""
	}
	if req.Stream && caps.NoStreaming {
		return ""
	}
	return fallback
}

func isFallbackEligibleLLMError(err error) bool {
	if err == nil {
		return false
	}
	if provider.IsTransientError(err) || isStreamMidwayError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "deadline") ||
		strings.Contains(msg, "broken pipe")
}

func requestForFallbackModel(req openai.ChatCompletionRequest, ag *model.Agent, fallback string, pt model.ProviderType, l *log.Entry) openai.ChatCompletionRequest {
	out := req
	out.Model = fallback
	out.MaxTokens = 0
	out.MaxCompletionTokens = 0
	out.Temperature = 0
	out.TopP = 0
	out.ReasoningEffort = ""
	out.ChatTemplateKwargs = nil
	out.ExtraBody = nil
	if ag == nil {
		return out
	}
	fallbackAgent := *ag
	fallbackAgent.ModelName = fallback
	applyModelCaps(&out, &fallbackAgent, pt, l)
	return out
}

func stepMetaForModel(ec *execContext, modelName string) *model.StepMetadata {
	meta := ec.stepMeta()
	meta.Model = modelName
	return meta
}
