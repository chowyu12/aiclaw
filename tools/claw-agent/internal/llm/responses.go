package llm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

func (c *Client) responsesRequest(req Request) map[string]any {
	input := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleAssistant && c.replayable(m.ProviderState, req.Model) {
			for _, item := range m.ProviderState.Output {
				input = append(input, item)
			}
			continue
		}
		if m.Role == RoleTool {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
			continue
		}
		if m.Content != "" || len(m.Images) > 0 || len(m.ToolCalls) == 0 {
			var content any = m.Content
			if len(m.Images) > 0 {
				parts := []map[string]any{{"type": "input_text", "text": m.Content}}
				for _, image := range m.Images {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)})
				}
				content = parts
			}
			input = append(input, map[string]any{"role": m.Role, "content": content})
		}
		for _, call := range m.ToolCalls {
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments})
		}
	}
	tools := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		// Existing tool schemas have optional fields. Responses defaults to strict
		// schemas, so explicitly retain Chat Completions' non-strict contract.
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": parameters, "strict": false})
	}
	wire := map[string]any{"model": req.Model, "input": input, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}}
	if len(tools) > 0 {
		wire["tools"] = tools
	}
	if req.ReasoningEffort != "" {
		wire["reasoning"] = map[string]string{"effort": req.ReasoningEffort}
	}
	if req.MaxTokens > 0 {
		wire["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		wire["temperature"] = req.Temperature
	}
	return wire
}

type responseItem struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type responseEnvelope struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Error  *wireError        `json:"error"`
	Usage  struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
		Total  int `json:"total_tokens"`
	} `json:"usage"`
}

func consumeResponses(body io.Reader, onDelta func(Delta)) (response Response, err error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var content strings.Builder
	defer func() {
		if err != nil {
			response.Content = content.String()
			response.ToolCalls = nil
			response.ProviderState = nil
		}
	}()
	incomplete := func() error {
		return &Error{Code: "response_incomplete", Message: i18n.D("Responses 返回不完整，已保留部分回答，未执行工具调用。")}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break // A Responses stream requires response.completed, not just [DONE].
		}
		var event struct {
			Type     string            `json:"type"`
			Delta    string            `json:"delta"`
			Message  string            `json:"message"`
			Code     any               `json:"code"`
			Response *responseEnvelope `json:"response"`
			Error    *wireError        `json:"error"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		failure := event.Error
		if event.Response != nil {
			u := event.Response.Usage
			response.Usage = Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total}
		}
		if event.Response != nil && event.Response.Error != nil {
			failure = event.Response.Error
		}
		if event.Type == "error" && failure == nil {
			failure = &wireError{Message: event.Message, Code: event.Code}
		}
		if failure != nil {
			return response, &Error{Message: failure.Message, Code: strings.TrimSpace(fmt.Sprint(firstNonNil(failure.Code)) + " " + failure.Type)}
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			content.WriteString(event.Delta)
			if onDelta != nil {
				onDelta(Delta{Content: event.Delta})
			}
		case "response.reasoning_summary_text.delta":
			if onDelta != nil {
				onDelta(Delta{Reasoning: event.Delta})
			}
		case "response.failed", "response.incomplete":
			return response, incomplete()
		case "response.completed":
			completed := event.Response
			if completed == nil || completed.Status != "completed" || len(completed.Output) == 0 {
				return response, incomplete()
			}
			var text strings.Builder
			for _, raw := range completed.Output {
				var item responseItem
				if json.Unmarshal(raw, &item) != nil || (item.Status != "" && item.Status != "completed") {
					return response, incomplete()
				}
				switch item.Type {
				case "message":
					for _, part := range item.Content {
						if part.Type == "output_text" {
							text.WriteString(part.Text)
						} else if part.Type == "refusal" {
							text.WriteString(part.Refusal)
						}
					}
				case "function_call":
					if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
						return response, incomplete()
					}
					response.ToolCalls = append(response.ToolCalls, ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments})
				case "reasoning": // Preserve opaque encrypted reasoning in original order.
				default:
					return response, incomplete()
				}
			}
			response.Content = text.String()
			if content.Len() == 0 && response.Content != "" && onDelta != nil {
				onDelta(Delta{Content: response.Content})
			}
			response.FinishReason = "stop"
			if len(response.ToolCalls) > 0 {
				response.FinishReason = "tool_calls"
			}
			response.ProviderState = &ProviderState{Output: completed.Output}
			return response, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return response, &Error{Message: i18n.D("读取模型流失败：{error}", "error", err), Err: err}
	}
	return response, &Error{Message: i18n.D("模型流在完成前结束，已保留部分回答，未执行工具调用。"), Err: io.ErrUnexpectedEOF}
}
