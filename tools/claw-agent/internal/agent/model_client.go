package agent

import (
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Each operation resolves its own connection snapshot. An already running HTTP
// request retains the previous snapshot; service edits affect the next operation.
func (s *Session) samplingClient() (protocol.ModelConfig, *llm.Client, error) {
	s.mu.Lock()
	original := s.config.Model
	s.mu.Unlock()
	resolved := original
	key, err := s.keyFor(&resolved)
	if err != nil {
		return resolved, nil, &llm.Error{Status: 400, Code: "invalid_configuration", Message: err.Error()}
	}
	client, err := llm.NewConfigured(llm.Config{ProviderID: resolved.ProviderID, BaseURL: resolved.BaseURL, APIKey: key, Protocol: resolved.APIProtocol, Capabilities: resolved.Capabilities})
	if err != nil {
		return resolved, nil, &llm.Error{Status: 400, Code: "invalid_configuration", Message: err.Error()}
	}
	s.mu.Lock()
	if s.config.Model == original {
		if resolved != original {
			s.lastInputTokens = 0
			s.estimateCompaction = true
		}
		s.config.Model = resolved
		s.llm = client
	}
	s.mu.Unlock()
	return resolved, client, nil
}
