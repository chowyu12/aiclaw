package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const claudeAPIVersion = "2023-06-01"

type claudeAdapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func newClaudeAdapter(apiKey, baseURL string) *claudeAdapter {
	return &claudeAdapter{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Transport: &loggingTransport{inner: http.DefaultTransport}},
	}
}

// ── OpenAI → Claude request conversion ──────────────────────────

type claudeRequest struct {
	Model       string           `json:"model"`
	MaxTokens   int              `json:"max_tokens"`
	System      string           `json:"system,omitempty"`
	Messages    []claudeMessage  `json:"messages"`
	Tools       []claudeTool     `json:"tools,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Temperature *float32         `json:"temperature,omitempty"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []claudeContentBlock
}

type claudeContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // for tool_result: string or []block

	// image source
	Source *claudeImageSource `json:"source,omitempty"`
}

type claudeImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type claudeTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

func buildClaudeRequest(req openai.ChatCompletionRequest) claudeRequest {
	cr := claudeRequest{
		Model:     req.Model,
		MaxTokens: req.MaxCompletionTokens,
	}
	if cr.MaxTokens == 0 {
		cr.MaxTokens = 4096
	}
	if req.Temperature > 0 {
		cr.Temperature = &req.Temperature
	}

	cr.System, cr.Messages = convertToClaudeMessages(req.Messages)
	cr.Tools = convertToClaudeTools(req.Tools)
	return cr
}

func convertToClaudeMessages(msgs []openai.ChatCompletionMessage) (string, []claudeMessage) {
	var system string
	var result []claudeMessage

	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case openai.ChatMessageRoleSystem:
			if system != "" {
				system += "\n\n"
			}
			system += m.Content

		case openai.ChatMessageRoleUser:
			blocks := userMsgToClaudeBlocks(m)
			result = appendClaudeMsg(result, "user", blocks)

		case openai.ChatMessageRoleAssistant:
			var blocks []claudeContentBlock
			if m.Content != "" {
				blocks = append(blocks, claudeContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, claudeContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			if len(blocks) > 0 {
				result = append(result, claudeMessage{Role: "assistant", Content: blocks})
			}

		case openai.ChatMessageRoleTool:
			var toolResults []claudeContentBlock
			toolResults = append(toolResults, claudeContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
			for i+1 < len(msgs) && msgs[i+1].Role == openai.ChatMessageRoleTool {
				i++
				toolResults = append(toolResults, claudeContentBlock{
					Type:      "tool_result",
					ToolUseID: msgs[i].ToolCallID,
					Content:   msgs[i].Content,
				})
			}
			result = appendClaudeMsg(result, "user", toolResults)
		}
	}
	return system, result
}

func appendClaudeMsg(msgs []claudeMessage, role string, blocks []claudeContentBlock) []claudeMessage {
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == role {
		existing, ok := msgs[len(msgs)-1].Content.([]claudeContentBlock)
		if ok {
			msgs[len(msgs)-1].Content = append(existing, blocks...)
			return msgs
		}
	}
	return append(msgs, claudeMessage{Role: role, Content: blocks})
}

func userMsgToClaudeBlocks(m openai.ChatCompletionMessage) []claudeContentBlock {
	if len(m.MultiContent) == 0 {
		return []claudeContentBlock{{Type: "text", Text: m.Content}}
	}
	var blocks []claudeContentBlock
	for _, part := range m.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			blocks = append(blocks, claudeContentBlock{Type: "text", Text: part.Text})
		case openai.ChatMessagePartTypeImageURL:
			if part.ImageURL != nil {
				if b := parseDataURLToClaudeImage(part.ImageURL.URL); b != nil {
					blocks = append(blocks, *b)
				}
			}
		}
	}
	return blocks
}

func parseDataURLToClaudeImage(dataURL string) *claudeContentBlock {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil
	}
	rest := dataURL[5:]
	semiIdx := strings.Index(rest, ";base64,")
	if semiIdx < 0 {
		return nil
	}
	mediaType := rest[:semiIdx]
	data := rest[semiIdx+8:]
	return &claudeContentBlock{
		Type:   "image",
		Source: &claudeImageSource{Type: "base64", MediaType: mediaType, Data: data},
	}
}

func convertToClaudeTools(tools []openai.Tool) []claudeTool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]claudeTool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, claudeTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return result
}

// ── Claude response → OpenAI response conversion ───────────────

type claudeResponse struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Role       string               `json:"role"`
	Content    []claudeRespBlock    `json:"content"`
	Model      string               `json:"model"`
	StopReason string               `json:"stop_reason"`
	Usage      claudeUsage          `json:"usage"`
	Error      *claudeErrorDetail   `json:"error,omitempty"`
}

type claudeRespBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type claudeErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func claudeToOpenAIResponse(cr *claudeResponse) openai.ChatCompletionResponse {
	var textParts []string
	var toolCalls []openai.ToolCall

	for _, block := range cr.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			argsBytes, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   block.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsBytes),
				},
			})
		}
	}

	finishReason := openai.FinishReasonStop
	if cr.StopReason == "tool_use" {
		finishReason = openai.FinishReasonToolCalls
	} else if cr.StopReason == "max_tokens" {
		finishReason = openai.FinishReasonLength
	}

	return openai.ChatCompletionResponse{
		ID:    cr.ID,
		Model: cr.Model,
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   strings.Join(textParts, ""),
				ToolCalls: toolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: openai.Usage{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.InputTokens + cr.Usage.OutputTokens,
		},
	}
}

// ── Non-streaming implementation ────────────────────────────────

func (a *claudeAdapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	cr := buildClaudeRequest(req)
	cr.Stream = false

	body, _ := json.Marshal(cr)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	a.setHeaders(httpReq)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("claude request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return openai.ChatCompletionResponse{}, fmt.Errorf("claude API error %d: %s", resp.StatusCode, string(respBody))
	}

	var cr2 claudeResponse
	if err := json.Unmarshal(respBody, &cr2); err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("parse response: %w", err)
	}
	if cr2.Error != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("claude error: [%s] %s", cr2.Error.Type, cr2.Error.Message)
	}

	return claudeToOpenAIResponse(&cr2), nil
}

// ── Streaming implementation ────────────────────────────────────

func (a *claudeAdapter) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (ChatStream, error) {
	cr := buildClaudeRequest(req)
	cr.Stream = true

	body, _ := json.Marshal(cr)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	a.setHeaders(httpReq)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claude stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("claude API error %d: %s", resp.StatusCode, string(errBody))
	}

	return &claudeStream{
		body:    resp.Body,
		scanner: bufio.NewScanner(resp.Body),
	}, nil
}

func (a *claudeAdapter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", claudeAPIVersion)
}

var _ LLMProvider = (*claudeAdapter)(nil)

// ── Claude SSE stream → OpenAI ChatStream ───────────────────────

type claudeStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	done    bool
	model   string
	msgID   string

	// accumulated tool call state per content_block index
	toolCalls map[int]*openai.ToolCall
	usage     openai.Usage
}

type claudeSSEEvent struct {
	Type string
	Data json.RawMessage
}

func (s *claudeStream) Recv() (openai.ChatCompletionStreamResponse, error) {
	for {
		event, ok := s.nextSSEEvent()
		if !ok {
			return openai.ChatCompletionStreamResponse{}, io.EOF
		}

		switch event.Type {
		case "message_start":
			var data struct {
				Message struct {
					ID    string      `json:"id"`
					Model string      `json:"model"`
					Usage claudeUsage `json:"usage"`
				} `json:"message"`
			}
			_ = json.Unmarshal(event.Data, &data)
			s.msgID = data.Message.ID
			s.model = data.Message.Model
			s.usage.PromptTokens = data.Message.Usage.InputTokens

		case "content_block_start":
			var data struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			_ = json.Unmarshal(event.Data, &data)

			if data.ContentBlock.Type == "tool_use" {
				if s.toolCalls == nil {
					s.toolCalls = make(map[int]*openai.ToolCall)
				}
				idx := len(s.toolCalls)
				s.toolCalls[data.Index] = &openai.ToolCall{
					ID:   data.ContentBlock.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name: data.ContentBlock.Name,
					},
				}
				i := idx
				return openai.ChatCompletionStreamResponse{
					ID:    s.msgID,
					Model: s.model,
					Choices: []openai.ChatCompletionStreamChoice{{
						Delta: openai.ChatCompletionStreamChoiceDelta{
							ToolCalls: []openai.ToolCall{{
								Index: &i,
								ID:    data.ContentBlock.ID,
								Type:  openai.ToolTypeFunction,
								Function: openai.FunctionCall{
									Name: data.ContentBlock.Name,
								},
							}},
						},
					}},
				}, nil
			}

		case "content_block_delta":
			var data struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			_ = json.Unmarshal(event.Data, &data)

			if data.Delta.Type == "text_delta" && data.Delta.Text != "" {
				return openai.ChatCompletionStreamResponse{
					ID:    s.msgID,
					Model: s.model,
					Choices: []openai.ChatCompletionStreamChoice{{
						Delta: openai.ChatCompletionStreamChoiceDelta{
							Content: data.Delta.Text,
						},
					}},
				}, nil
			}

			if data.Delta.Type == "input_json_delta" && data.Delta.PartialJSON != "" {
				if tc, ok := s.toolCalls[data.Index]; ok {
					tc.Function.Arguments += data.Delta.PartialJSON
				}
				idx := len(s.toolCalls) - 1
				return openai.ChatCompletionStreamResponse{
					ID:    s.msgID,
					Model: s.model,
					Choices: []openai.ChatCompletionStreamChoice{{
						Delta: openai.ChatCompletionStreamChoiceDelta{
							ToolCalls: []openai.ToolCall{{
								Index: &idx,
								Function: openai.FunctionCall{
									Arguments: data.Delta.PartialJSON,
								},
							}},
						},
					}},
				}, nil
			}

		case "message_delta":
			var data struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(event.Data, &data)

			s.usage.CompletionTokens = data.Usage.OutputTokens
			s.usage.TotalTokens = s.usage.PromptTokens + s.usage.CompletionTokens

			finishReason := openai.FinishReasonStop
			if data.Delta.StopReason == "tool_use" {
				finishReason = openai.FinishReasonToolCalls
			} else if data.Delta.StopReason == "max_tokens" {
				finishReason = openai.FinishReasonLength
			}

			return openai.ChatCompletionStreamResponse{
				ID:    s.msgID,
				Model: s.model,
				Choices: []openai.ChatCompletionStreamChoice{{
					FinishReason: finishReason,
				}},
				Usage: &openai.Usage{
					PromptTokens:     s.usage.PromptTokens,
					CompletionTokens: s.usage.CompletionTokens,
					TotalTokens:      s.usage.TotalTokens,
				},
			}, nil

		case "message_stop":
			s.done = true
			return openai.ChatCompletionStreamResponse{}, io.EOF

		case "error":
			var data struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(event.Data, &data)
			return openai.ChatCompletionStreamResponse{}, fmt.Errorf("claude stream error: %s", data.Error.Message)
		}
	}
}

func (s *claudeStream) Close() error {
	return s.body.Close()
}

func (s *claudeStream) nextSSEEvent() (claudeSSEEvent, bool) {
	if s.done {
		return claudeSSEEvent{}, false
	}
	var eventType string
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			return claudeSSEEvent{Type: eventType, Data: json.RawMessage(data)}, true
		}
	}
	return claudeSSEEvent{}, false
}

// ── Claude remote models ────────────────────────────────────────

func fetchClaudeModels(ctx context.Context, apiKey, baseURL string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", claudeAPIVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch claude models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("claude models API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}
