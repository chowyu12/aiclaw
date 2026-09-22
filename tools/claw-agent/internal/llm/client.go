// Package llm 是 OpenAI Chat Completions 兼容的客户端，带流式与工具调用。
//
// 选 Chat Completions 而不是 Responses API：airouter 的全部渠道
// （OpenAI / Claude / DeepSeek / Kimi / Qwen）都原生支持它，工具调用语义
// 成熟稳定；Responses 是 OpenAI 特有的，airouter 对非 OpenAI 上游会在内部
// 转成 chat，等于多一层转换且能力取决于上游。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是模型发起的一次工具调用。
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message 是一条对话消息。
//
// ToolCallID 只在 RoleTool 上有值：Chat Completions 要求工具结果消息
// 通过它关联回是哪一次调用，缺了上游会报 400。
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	// Images 是随这条消息一起发给模型的图片（PNG 字节）。
	//
	// 只在 RoleUser 上有意义：Chat Completions 的 tool 结果消息必须是纯字符串，
	// 没有地方放图。所以截屏这类工具的图是作为**紧随其后的一条 user 消息**
	// 送进去的，不是塞在工具结果里。
	Images [][]byte
}

// Tool 是提供给模型的工具定义。Parameters 是 JSON Schema。
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Request struct {
	Model           string
	Messages        []Message
	Tools           []Tool
	Temperature     *float64
	MaxTokens       int
	ReasoningEffort string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Response 是一轮模型调用的结果。
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// Delta 是流式过程中推给上层的增量。
type Delta struct {
	// Content 是正文增量。
	Content string
	// Reasoning 是推理摘要增量（部分模型会给）。
	Reasoning string
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("模型端点未配置")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("模型端点必须是 http(s) 地址：%s", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("模型 Key 未配置")
	}
	if timeout <= 0 {
		// 单次调用可能很长（长上下文 + 高推理档位），上限放宽；
		// 真正的中断由 context 负责，不靠这个超时。
		timeout = 10 * time.Minute
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: timeout}}, nil
}

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
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// contentPart 是多模态消息里的一段。带图时 content 从字符串变成这个的数组。
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
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
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content          string         `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			ToolCalls        []wireToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func buildWireRequest(req Request) wireRequest {
	messages := make([]wireMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		wire := wireMessage{Role: string(message.Role), ToolCallID: message.ToolCallID}
		switch {
		case len(message.Images) > 0:
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

// Stream 发起一次流式调用，增量经 onDelta 回调推出，返回聚合后的结果。
func (c *Client) Stream(
	ctx context.Context,
	req Request,
	onDelta func(Delta),
) (Response, error) {
	payload, err := json.Marshal(buildWireRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("编码请求失败：%w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload),
	)
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Response{}, &Error{Message: "调用模型失败：" + err.Error(), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Response{}, readErrorBody(resp)
	}
	return consumeStream(resp.Body, onDelta)
}

func readErrorBody(resp *http.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		if envelope.Error.Message != "" {
			return &Error{Status: resp.StatusCode, Message: envelope.Error.Message}
		}
		if envelope.Message != "" {
			return &Error{Status: resp.StatusCode, Message: envelope.Message}
		}
	}
	snippet := strings.TrimSpace(string(raw))
	if len(snippet) > 400 {
		snippet = snippet[:400] + "…"
	}
	return &Error{Status: resp.StatusCode, Message: snippet}
}

// toolCallAccumulator 负责把分片的 tool_calls 拼回完整调用。
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

func consumeStream(body io.Reader, onDelta func(Delta)) (Response, error) {
	scanner := bufio.NewScanner(body)
	// 单个 SSE 事件可能很大（一次给出长参数的工具调用），放宽行上限。
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var response Response
	accumulator := newToolCallAccumulator()
	var content strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk wireChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// 单个坏分片不中断整条流：上游偶尔会插入心跳或非标准行。
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			// 流里带出来的错误没有 HTTP 状态码可依。当成传输层问题（可重试）：
			// 上游把错误塞进流里通常是后端临时故障，而请求本身的问题
			// （参数、鉴权、超窗）在建流之前就以 4xx 返回了。
			return response, &Error{Message: "模型流式返回错误：" + chunk.Error.Message}
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
			if choice.Delta.ReasoningContent != "" && onDelta != nil {
				onDelta(Delta{Reasoning: choice.Delta.ReasoningContent})
			}
			for _, fragment := range choice.Delta.ToolCalls {
				accumulator.add(fragment)
			}
			if choice.FinishReason != "" {
				response.FinishReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return response, &Error{Message: "读取模型流失败：" + err.Error(), Err: err}
	}

	response.Content = content.String()
	response.ToolCalls = accumulator.result()
	return response, nil
}
