package llm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

func encodeGemini(c *Client, req Request) (string, any, error) {
	contents := []map[string]any{}
	system := []any{}
	names := map[string]string{}
	appendParts := func(role string, parts []any) {
		if len(parts) == 0 {
			return
		}
		if len(contents) > 0 && contents[len(contents)-1]["role"] == role {
			last := contents[len(contents)-1]
			last["parts"] = append(last["parts"].([]any), parts...)
		} else {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	for _, m := range req.Messages {
		for _, call := range m.ToolCalls {
			names[call.ID] = call.Name
		}
		if m.Role == RoleSystem {
			system = append(system, map[string]any{"text": m.Content})
			continue
		}
		role := "user"
		parts := []any{}
		if m.Role == RoleTool {
			if names[m.ToolCallID] == "" {
				return "", nil, &Error{Status: 400, Message: "Missing tool call for Gemini function response"}
			}
			result := map[string]any{"name": names[m.ToolCallID], "response": map[string]string{"result": m.Content}}
			if !strings.HasPrefix(m.ToolCallID, "gemini_call_") {
				result["id"] = m.ToolCallID
			}
			parts = append(parts, map[string]any{"functionResponse": result})
		} else if m.Role == RoleAssistant {
			role = "model"
			if c.replayable(m.ProviderState, req.Model) {
				for _, raw := range m.ProviderState.Output {
					parts = append(parts, raw)
				}
			} else {
				if m.Content != "" {
					parts = append(parts, map[string]any{"text": m.Content})
				}
				for _, call := range m.ToolCalls {
					if !json.Valid([]byte(call.Arguments)) {
						return "", nil, &Error{Status: 400, Message: "Invalid tool arguments in history"}
					}
					parts = append(parts, map[string]any{"functionCall": map[string]any{"id": call.ID, "name": call.Name, "args": json.RawMessage(call.Arguments)}})
				}
			}
		} else {
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, image := range m.Images {
				parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(image)}})
			}
		}
		appendParts(role, parts)
	}
	wire := map[string]any{"contents": contents}
	if len(system) > 0 {
		wire["systemInstruction"] = map[string]any{"parts": system}
	}
	if len(req.Tools) > 0 {
		decl := []any{}
		for _, tool := range req.Tools {
			schema := tool.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			decl = append(decl, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": schema})
		}
		wire["tools"] = []any{map[string]any{"functionDeclarations": decl}}
	}
	generation := map[string]any{}
	if req.MaxTokens > 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		generation["temperature"] = req.Temperature
	}
	if req.ReasoningEffort == "none" {
		generation["thinkingConfig"] = map[string]any{"thinkingBudget": 0}
	} else if req.ReasoningEffort != "" {
		generation["thinkingConfig"] = map[string]string{"thinkingLevel": strings.ToUpper(req.ReasoningEffort)}
	}
	// The review deadline remains authoritative. Do not guess a model-specific
	// zero-thinking control when the caller only requests a quick response.
	if len(generation) > 0 {
		wire["generationConfig"] = generation
	}
	name := strings.TrimPrefix(req.Model, "models/")
	if name == "" || strings.ContainsAny(name, "/?#") {
		return "", nil, &Error{Status: 400, Message: "Invalid Gemini model ID"}
	}
	return "/models/" + url.PathEscape(name) + ":streamGenerateContent?alt=sse", wire, nil
}

func consumeGemini(body io.Reader, onDelta func(Delta)) (response Response, err error) {
	var text strings.Builder
	defer finishSample(&response, &text, &err)
	output := []json.RawMessage{}
	prefix := "gemini_call_" + rand.Text() + "_"
	err = readEvents(body, func(raw json.RawMessage) (bool, error) {
		var chunk struct {
			Candidates []struct {
				Index   int `json:"index"`
				Content struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage struct {
				Input    int `json:"promptTokenCount"`
				Output   int `json:"candidatesTokenCount"`
				Thoughts int `json:"thoughtsTokenCount"`
				Total    int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &chunk) != nil {
			return false, incompleteNative("invalid Gemini event")
		}
		if chunk.Error != nil {
			return false, &Error{Status: chunk.Error.Code, Message: chunk.Error.Message, Code: chunk.Error.Status}
		}
		if chunk.PromptFeedback.BlockReason != "" {
			return false, incompleteNative(chunk.PromptFeedback.BlockReason)
		}
		u := chunk.Usage
		if u.Total > 0 {
			response.Usage = Usage{InputTokens: u.Input, OutputTokens: u.Output + u.Thoughts, TotalTokens: u.Total}
		}
		for _, candidate := range chunk.Candidates {
			if candidate.Index != 0 {
				continue
			}
			for _, rawPart := range candidate.Content.Parts {
				var part struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
					Call    *struct {
						ID   string          `json:"id"`
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
				}
				if json.Unmarshal(rawPart, &part) != nil {
					return false, incompleteNative("invalid Gemini part")
				}
				output = append(output, rawPart) // Includes signature-only parts, in exact order.
				if part.Thought {
					if onDelta != nil {
						onDelta(Delta{Reasoning: part.Text})
					}
				} else if part.Text != "" {
					text.WriteString(part.Text)
					if onDelta != nil {
						onDelta(Delta{Content: part.Text})
					}
				}
				if part.Call != nil {
					call := part.Call
					if call.Name == "" {
						return false, incompleteNative("missing function name")
					}
					if len(call.Args) == 0 {
						call.Args = json.RawMessage(`{}`)
					}
					if !json.Valid(call.Args) {
						return false, incompleteNative("invalid function arguments")
					}
					id := call.ID
					if id == "" {
						id = fmt.Sprintf("%s%d", prefix, len(response.ToolCalls))
					}
					response.ToolCalls = append(response.ToolCalls, ToolCall{ID: id, Name: call.Name, Arguments: string(call.Args)})
				}
			}
			if candidate.FinishReason != "" {
				if candidate.FinishReason != "STOP" {
					return false, incompleteNative(candidate.FinishReason)
				}
				response.FinishReason = "stop"
				if len(response.ToolCalls) > 0 {
					response.FinishReason = "tool_calls"
				}
				response.ProviderState = &ProviderState{Output: output}
				return true, nil
			}
		}
		return false, nil
	})
	return response, err
}
