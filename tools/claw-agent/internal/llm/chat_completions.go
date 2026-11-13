package llm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"io"
	"net/url"
	"strings"
)

// ---------- 线格式 ----------

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type wireMessage struct {
	ReasoningContent *string        `json:"reasoning_content,omitempty"`
	Role             string         `json:"role"`
	Content          any            `json:"content"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

// contentPart 是多模态消息里的一段。带图时 content 从字符串变成这个的数组。
type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}
type contentPart struct {
	InputAudio *inputAudio `json:"input_audio,omitempty"`
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	ImageURL   *imageURL   `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type wireRequest struct {
	Model           string        `json:"model"`
	Messages        []wireMessage `json:"messages"`
	Tools           []wireTool    `json:"tools,omitempty"`
	Stream          bool          `json:"stream"`
	StreamOptions   *streamOpts   `json:"stream_options,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	EnableThinking  *bool         `json:"enable_thinking,omitempty"`
	Thinking        *thinkingMode `json:"thinking,omitempty"`
}

type thinkingMode struct {
	Type string `json:"type"`
}

// Use provider controls only for known compatible endpoints/models. Unknown gateways
// keep their existing wire format and remain bounded by the caller's deadline.
func (c *Client) disableThinking(wire *wireRequest) {
	model := strings.ToLower(wire.Model)
	if strings.Contains(model, "thinking") || model == "deepseek-r1" || model == "deepseek-reasoner" {
		return
	}
	if c.dialect == dialectDashScope && (strings.HasPrefix(model, "qwen3") || strings.HasPrefix(model, "deepseek-v3.1") || strings.HasPrefix(model, "deepseek-v3.2") || strings.HasPrefix(model, "deepseek-v4")) {
		disabled := false
		wire.EnableThinking = &disabled
		wire.ReasoningEffort = ""
	} else if parsed, err := url.Parse(c.baseURL); err == nil && strings.EqualFold(parsed.Hostname(), "api.deepseek.com") && (model == "deepseek-chat" || strings.HasPrefix(model, "deepseek-v4") || strings.HasPrefix(model, "deepseek-v3.2")) {
		wire.Thinking = &thinkingMode{Type: "disabled"}
		wire.ReasoningEffort = ""
	}
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content          string         `json:"content"`
			ReasoningContent *string        `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error    *wireError `json:"error"`
	Type     string     `json:"type"`
	Code     any        `json:"code"`
	Message  string     `json:"message"`
	Response *struct {
		Error *wireError `json:"error"`
	} `json:"response"`
}

type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func buildWireRequest(req Request) wireRequest {
	messages := make([]wireMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		wire := wireMessage{Role: string(message.Role), ToolCallID: message.ToolCallID}
		switch {
		case len(message.Images) > 0 || len(message.Audio) > 0:
			// 有图就把 content 换成分段数组。图用 data URL 内联——
			// 上游拿不到我们本机的文件，也不该为了一张截图去架一个图床。
			parts := make([]contentPart, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, contentPart{Type: "text", Text: message.Content})
			}
			for _, image := range message.Images {
				parts = append(parts, contentPart{
					Type: "image_url",
					ImageURL: &imageURL{
						URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(image),
					},
				})
			}
			for _, audio := range message.Audio {
				parts = append(parts, contentPart{Type: "input_audio", InputAudio: &inputAudio{Data: base64.StdEncoding.EncodeToString(audio.Data), Format: audio.Format}})
			}
			wire.Content = parts
		case message.Content == "" && len(message.ToolCalls) > 0:
			// 带工具调用的 assistant 消息 content 必须是 null 而不是空串：
			// 部分上游（Claude 兼容层尤其）对空串会报参数错误。
			wire.Content = nil
		default:
			wire.Content = message.Content
		}
		for index, call := range message.ToolCalls {
			wireCall := wireToolCall{Index: index, ID: call.ID, Type: "function"}
			wireCall.Function.Name = call.Name
			wireCall.Function.Arguments = call.Arguments
			wire.ToolCalls = append(wire.ToolCalls, wireCall)
		}
		messages = append(messages, wire)
	}

	tools := make([]wireTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		var wire wireTool
		wire.Type = "function"
		wire.Function.Name = tool.Name
		wire.Function.Description = tool.Description
		wire.Function.Parameters = tool.Parameters
		if len(wire.Function.Parameters) == 0 {
			wire.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, wire)
	}

	return wireRequest{
		Model:           req.Model,
		Messages:        messages,
		Tools:           tools,
		Stream:          true,
		StreamOptions:   &streamOpts{IncludeUsage: true},
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		ReasoningEffort: req.ReasoningEffort,
	}
}

// toolCallAccumulator 按 index 拼接工具参数。
//
// Chat Completions 的流式里，一次工具调用会拆成多个 chunk：id 和 name 通常
// 只在第一片出现，arguments 逐片追加；多个并行调用靠 index 区分。
// 这里必须按 index 累积——按 id 累积会在 id 还没出现的分片上丢数据。
type toolCallAccumulator struct {
	order []int
	calls map[int]*ToolCall
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: map[int]*ToolCall{}}
}

func (a *toolCallAccumulator) add(fragment wireToolCall) {
	call, exists := a.calls[fragment.Index]
	if !exists {
		call = &ToolCall{}
		a.calls[fragment.Index] = call
		a.order = append(a.order, fragment.Index)
	}
	if fragment.ID != "" {
		call.ID = fragment.ID
	}
	if fragment.Function.Name != "" {
		call.Name = fragment.Function.Name
	}
	call.Arguments += fragment.Function.Arguments
}

func (a *toolCallAccumulator) result() []ToolCall {
	calls := make([]ToolCall, 0, len(a.order))
	for _, index := range a.order {
		call := a.calls[index]
		if call.Name == "" {
			continue
		}
		if call.ID == "" {
			// 少数上游不给 id。自己补一个，否则后面的 tool 结果消息关联不上。
			call.ID = fmt.Sprintf("call_%d", index)
		}
		if strings.TrimSpace(call.Arguments) == "" {
			call.Arguments = "{}"
		}
		calls = append(calls, *call)
	}
	return calls
}

func consumeStream(body io.Reader, onDelta func(Delta)) (response Response, err error) {
	scanner := bufio.NewScanner(body)
	// 单个 SSE 事件可能很大（一次给出长参数的工具调用），放宽行上限。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	accumulator := newToolCallAccumulator()
	var content strings.Builder
	var reasoning strings.Builder
	reasoningSeen := false
	complete := false
	defer func() {
		response.Content = content.String()
		if err == nil {
			response.ToolCalls = accumulator.result()
			if reasoningSeen {
				raw, _ := json.Marshal(map[string]string{"reasoning_content": reasoning.String()})
				response.ProviderState = &ProviderState{Output: []json.RawMessage{raw}}
			}
		}
	}()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			complete = true
			break
		}

		var chunk wireChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 单个坏分片不中断整条流：上游偶尔会插入心跳或非标准行。
			continue
		}
		failure := chunk.Error
		if failure == nil && chunk.Type == "response.failed" && chunk.Response != nil {
			failure = chunk.Response.Error
		}
		if failure == nil && chunk.Type == "error" {
			failure = &wireError{Message: chunk.Message, Code: chunk.Code}
		}
		if failure != nil {
			code := strings.TrimSpace(fmt.Sprint(firstNonNil(failure.Code)) + " " + failure.Type)
			message := failure.Message
			if message == "" {
				message = code
			}
			return response, &Error{Code: code, Message: message}
		}
		if chunk.Usage != nil {
			response.Usage = Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				TotalTokens:  chunk.Usage.TotalTokens,
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if onDelta != nil {
					onDelta(Delta{Content: choice.Delta.Content})
				}
			}
			if choice.Delta.ReasoningContent != nil {
				reasoningSeen = true
				reasoning.WriteString(*choice.Delta.ReasoningContent)
				if onDelta != nil {
					onDelta(Delta{Reasoning: *choice.Delta.ReasoningContent})
				}
			}
			for _, fragment := range choice.Delta.ToolCalls {
				accumulator.add(fragment)
			}
			if choice.FinishReason != "" {
				response.FinishReason = choice.FinishReason
				switch choice.FinishReason {
				case "stop", "tool_calls", "function_call":
					complete = true
				default:
					return response, &Error{Code: "response_incomplete", Message: i18n.D("模型返回不完整（finish_reason={reason}），未执行工具调用。", "reason", choice.FinishReason)}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return response, &Error{Message: i18n.D("读取模型流失败：{error}", "error", err), Err: err}
	}

	if !complete {
		return response, &Error{Message: i18n.D("模型流在完成前结束，已保留部分回答，未执行工具调用。"), Err: io.ErrUnexpectedEOF}
	}
	return response, nil
}
