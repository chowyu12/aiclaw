package llm

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

func encodeAnthropic(c *Client, req Request) (string, any, error) {
	var system []any
	messages := []map[string]any{}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			system = append(system, map[string]any{"type": "text", "text": m.Content})
			continue
		}
		role := "user"
		parts := []any{}
		if m.Role == RoleAssistant {
			role = "assistant"
			if c.replayable(m.ProviderState, req.Model) {
				for _, raw := range m.ProviderState.Output {
					parts = append(parts, raw)
				}
			} else {
				if m.Content != "" {
					parts = append(parts, map[string]any{"type": "text", "text": m.Content})
				}
				for _, call := range m.ToolCalls {
					if !json.Valid([]byte(call.Arguments)) {
						return "", nil, &Error{Status: 400, Message: "Invalid tool arguments in history"}
					}
					parts = append(parts, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": json.RawMessage(call.Arguments)})
				}
			}
		} else if m.Role == RoleTool {
			parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content})
		} else {
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": m.Content})
			}
			for _, image := range m.Images {
				parts = append(parts, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(image)}})
			}
		}
		mergeContent(&messages, role, parts)
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	wire := map[string]any{"model": req.Model, "messages": messages, "stream": true, "max_tokens": maxTokens}
	if len(system) > 0 {
		wire["system"] = system
	}
	if len(req.Tools) > 0 {
		tools := []any{}
		for _, tool := range req.Tools {
			schema := tool.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
		wire["tools"] = tools
	}
	if req.ReasoningEffort == "none" {
		wire["thinking"] = map[string]string{"type": "disabled"}
	} else if req.ReasoningEffort != "" {
		wire["thinking"] = map[string]string{"type": "adaptive"}
		wire["output_config"] = map[string]string{"effort": req.ReasoningEffort}
	}
	if req.Temperature != nil {
		wire["temperature"] = req.Temperature
	}
	return "/messages", wire, nil
}

func consumeAnthropic(body io.Reader, onDelta func(Delta)) (response Response, err error) {
	var text strings.Builder
	defer finishSample(&response, &text, &err)
	type block struct {
		fields map[string]any
		args   strings.Builder
		closed bool
	}
	blocks := map[int]*block{}
	order := []int{}
	started := false
	reason := ""
	err = readEvents(body, func(raw json.RawMessage) (bool, error) {
		var e struct {
			Type         string         `json:"type"`
			Index        int            `json:"index"`
			ContentBlock map[string]any `json:"content_block"`
			Message      struct {
				Usage struct {
					Input       int `json:"input_tokens"`
					Output      int `json:"output_tokens"`
					CacheRead   int `json:"cache_read_input_tokens"`
					CacheCreate int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
			Error *wireError `json:"error"`
		}
		if json.Unmarshal(raw, &e) != nil {
			return false, incompleteNative("invalid Messages event")
		}
		if e.Error != nil {
			return false, &Error{Message: e.Error.Message, Code: e.Error.Type}
		}
		switch e.Type {
		case "message_start":
			started = true
			u := e.Message.Usage
			response.Usage.InputTokens = u.Input + u.CacheRead + u.CacheCreate
			response.Usage.OutputTokens = u.Output
		case "content_block_start":
			if blocks[e.Index] != nil || e.ContentBlock == nil {
				return false, incompleteNative("duplicate or missing content block")
			}
			blocks[e.Index] = &block{fields: e.ContentBlock}
			order = append(order, e.Index)
			if e.ContentBlock["type"] == "text" {
				initial := stringField(e.ContentBlock, "text")
				text.WriteString(initial)
				if initial != "" && onDelta != nil {
					onDelta(Delta{Content: initial})
				}
			}
		case "content_block_delta":
			b := blocks[e.Index]
			if b == nil || b.closed {
				return false, incompleteNative("content delta outside block")
			}
			switch e.Delta.Type {
			case "text_delta":
				b.fields["text"] = stringField(b.fields, "text") + e.Delta.Text
				text.WriteString(e.Delta.Text)
				if onDelta != nil {
					onDelta(Delta{Content: e.Delta.Text})
				}
			case "thinking_delta":
				b.fields["thinking"] = stringField(b.fields, "thinking") + e.Delta.Thinking
				if onDelta != nil {
					onDelta(Delta{Reasoning: e.Delta.Thinking})
				}
			case "signature_delta":
				b.fields["signature"] = stringField(b.fields, "signature") + e.Delta.Signature
			case "input_json_delta":
				b.args.WriteString(e.Delta.PartialJSON)
			}
		case "content_block_stop":
			b := blocks[e.Index]
			if b == nil || b.closed {
				return false, incompleteNative("invalid block stop")
			}
			b.closed = true
			if b.args.Len() > 0 {
				if !json.Valid([]byte(b.args.String())) {
					return false, incompleteNative("invalid tool JSON")
				}
				b.fields["input"] = json.RawMessage(b.args.String())
			}
		case "message_delta":
			reason = e.Delta.StopReason
			response.Usage.OutputTokens = e.Usage.Output
		case "message_stop":
			if !started || (reason != "end_turn" && reason != "tool_use" && reason != "stop_sequence" && reason != "refusal") {
				return false, incompleteNative(reason)
			}
			output := []json.RawMessage{}
			for _, index := range order {
				b := blocks[index]
				if !b.closed {
					return false, incompleteNative("unclosed content block")
				}
				switch b.fields["type"] {
				case "tool_use":
					id, name := stringField(b.fields, "id"), stringField(b.fields, "name")
					args, e := json.Marshal(b.fields["input"])
					if id == "" || name == "" || e != nil || string(args) == "null" {
						return false, incompleteNative("invalid tool use")
					}
					response.ToolCalls = append(response.ToolCalls, ToolCall{ID: id, Name: name, Arguments: string(args)})
				case "text", "thinking", "redacted_thinking":
				default:
					return false, incompleteNative("unsupported content block")
				}
				raw, e := json.Marshal(b.fields)
				if e != nil {
					return false, e
				}
				output = append(output, raw)
			}
			response.FinishReason = "stop"
			if len(response.ToolCalls) > 0 {
				response.FinishReason = "tool_calls"
			}
			response.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
			response.ProviderState = &ProviderState{Output: output}
			return true, nil
		}
		return false, nil
	})
	return response, err
}

func stringField(m map[string]any, key string) string { value, _ := m[key].(string); return value }
