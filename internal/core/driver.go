package core

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/model"
)

// SamplingMessage is the provider-neutral model context format reconstructed
// from Rollout items. Provider adapters translate it to their wire protocol.
type SamplingMessage struct {
	Role        string
	Content     string
	Attachments []*model.File
	ToolCallID  string
	Name        string
	ToolCalls   []ToolCall
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type SamplingRequest struct {
	Thread   model.Thread
	Messages []SamplingMessage
	Tools    []ToolDefinition
}

type SamplingResult struct {
	Text      string
	ToolCalls []ToolCall
	Tokens    int
}

// Sampler isolates the provider-specific request/streaming implementation.
// User-configured Provider records are resolved by its implementation, never
// by the Thread or the local App.
type Sampler interface {
	Sample(context.Context, SamplingRequest, func(string) error) (SamplingResult, error)
}

type ToolDefinition struct {
	Name        string
	Description string
	Schema      model.JSON
}

type ToolResult struct {
	CallID  string
	Name    string
	Content string
	Error   string
}

// ToolDispatcher is the Session-owned tool boundary. Its implementations wrap
// built-ins, persisted MCP servers, and web-search without exposing either to
// a UI transport.
type ToolDispatcher interface {
	Definitions(context.Context, model.Thread) ([]ToolDefinition, error)
	Execute(context.Context, model.Thread, ToolCall) (ToolResult, error)
}

// ContextProvider lets a tool/plugin dispatcher add trusted local context,
// such as enabled SKILL.md instructions, without coupling Session to storage.
type ContextProvider interface {
	ContextMessages(context.Context, model.Thread) ([]SamplingMessage, error)
}

// TurnContextPreparer attaches server-owned turn identity and performs any
// local pre-sampling work, such as capturing an explicit memory request.
type TurnContextPreparer interface {
	PrepareTurn(context.Context, model.Thread, Turn) (context.Context, error)
}
