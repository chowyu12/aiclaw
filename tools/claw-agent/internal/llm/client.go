// Package llm supplies one model-call contract with explicit protocol adapters.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/internal/model"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL      string
	apiKey       string
	http         *http.Client
	protocol     model.APIProtocol
	providerID   int64
	adapter      adapter
	capabilities model.ModelCapabilities
	// dialect 决定画图、听写、朗读及厂商专有的推理控制参数。
	dialect dialect
}

func New(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	return NewConfigured(Config{BaseURL: baseURL, APIKey: apiKey, Timeout: timeout})
}

// Config is a resolved, immutable connection snapshot. Credentials are never persisted here.
type Config struct {
	Capabilities model.ModelCapabilities
	ProviderID   int64
	BaseURL      string
	APIKey       string
	Protocol     model.APIProtocol
	Timeout      time.Duration
}

func NewConfigured(config Config) (*Client, error) {
	baseURL, apiKey, timeout := config.BaseURL, config.APIKey, config.Timeout
	protocol := config.Protocol.Normalized()
	driver, ok := adapters[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported model API protocol: %s", protocol)
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, i18n.E("模型端点未配置")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, i18n.E("模型端点必须是 http(s) 地址：{url}", "url", baseURL)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid model API base URL: use an HTTP(S) base without credentials, query or fragment")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, i18n.E("模型 Key 未配置")
	}
	if timeout <= 0 {
		// 单次调用可能很长（长上下文 + 高推理档位），上限放宽；
		// 真正的中断由 context 负责，不靠这个超时。
		timeout = 10 * time.Minute
	}
	return &Client{
		baseURL:      baseURL,
		apiKey:       apiKey,
		http:         &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		dialect:      detectDialect(baseURL),
		protocol:     protocol,
		providerID:   config.ProviderID,
		adapter:      driver,
		capabilities: config.Capabilities,
	}, nil
}

// Stream never infers a protocol or changes user parameters in response to an error.
// The caller owns retries; adapters only encode/decode one provider request.
func (c *Client) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Response, error) {
	if req.ReasoningEffort != "" && req.ReasoningEffort != "low" && req.ReasoningEffort != "medium" && req.ReasoningEffort != "high" && req.ReasoningEffort != "none" {
		return Response{}, &Error{Status: 400, Code: "invalid_request", Message: "Unsupported reasoning effort: " + req.ReasoningEffort}
	}
	if req.ReasoningPreference {
		if c.capabilities.Reasoning != "supported" || (c.capabilities.ReasoningEfforts != "" && !strings.Contains(","+c.capabilities.ReasoningEfforts+",", ","+req.ReasoningEffort+",")) {
			req.ReasoningEffort = ""
		}
	}
	req.Messages = normaliseHistory(req.Messages)
	if len(req.Tools) > 0 && c.capabilities.Tools == "unsupported" {
		return Response{}, &Error{Status: 400, Message: "This model does not support tools; choose a tool-capable model"}
	}
	for _, m := range req.Messages {
		if len(m.Images) > 0 && c.capabilities.Vision == "unsupported" {
			return Response{}, &Error{Status: 400, Message: "This model does not support image input"}
		}
	}
	if req.ReasoningEffort != "" && c.capabilities.Reasoning == "unsupported" {
		return Response{}, &Error{Status: 400, Message: "This model does not support reasoning settings; choose Model default"}
	}
	if req.ReasoningEffort != "" && c.capabilities.ReasoningEfforts != "" && !strings.Contains(","+c.capabilities.ReasoningEfforts+",", ","+req.ReasoningEffort+",") {
		return Response{}, &Error{Status: 400, Message: "Unsupported reasoning effort for this model: " + req.ReasoningEffort}
	}

	path, wire, err := c.adapter.encode(c, req)
	if err != nil {
		return Response{}, err
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return Response{}, fmt.Errorf("%s: %w", i18n.D("编码请求失败"), err)
	}
	httpReq, err := c.request(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Response{}, &Error{Message: i18n.D("调用模型失败：{error}", "error", err), Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Response{}, readErrorBody(resp)
	}
	result, err := c.adapter.decode(resp.Body, onDelta)
	if err == nil {
		seen := map[string]bool{}
		for _, call := range result.ToolCalls {
			if call.ID == "" || call.Name == "" || seen[call.ID] {
				result.ToolCalls = nil
				result.ProviderState = nil
				return result, incompleteNative("invalid or duplicate tool call")
			}
			seen[call.ID] = true
		}
	}
	if err == nil && result.ProviderState != nil {
		result.ProviderState.Protocol = c.protocol
		result.ProviderState.ProviderID = c.providerID
		result.ProviderState.BaseURL = c.baseURL
		result.ProviderState.Model = req.Model
	}
	return result, err
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.adapter.authorize(req, c.apiKey)
	return req, nil
}

func readErrorBody(resp *http.Response) *Error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			// code / type 是机器可读的原因（insufficient_quota 之类），比 message 稳。
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
		Code    any    `json:"code"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		code := strings.TrimSpace(fmt.Sprint(firstNonNil(envelope.Error.Code, envelope.Code)) + " " + envelope.Error.Type)
		if envelope.Error.Message != "" {
			return &Error{Status: resp.StatusCode, Message: envelope.Error.Message, Code: code, RetryAfter: retryAfter}
		}
		if envelope.Message != "" {
			return &Error{Status: resp.StatusCode, Message: envelope.Message, Code: code, RetryAfter: retryAfter}
		}
	}
	snippet := strings.TrimSpace(string(raw))
	if len(snippet) > 400 {
		snippet = snippet[:400] + "…"
	}
	return &Error{Status: resp.StatusCode, Message: snippet, RetryAfter: retryAfter}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return ""
}

// parseRetryAfter 读 Retry-After：秒数或 HTTP 日期（RFC 9110 两种都允许）。
// 没给、给错、已经过期都返回 0，调用方退回自己的退避。
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(header, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if at, err := http.ParseTime(header); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}
