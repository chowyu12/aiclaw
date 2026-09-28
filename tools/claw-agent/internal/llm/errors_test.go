package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryableClassification(t *testing.T) {
	cases := []struct {
		name  string
		err   Error
		retry bool
	}{
		{"连接失败", Error{Message: "dial tcp: connection refused"}, true},
		{"流中断", Error{Message: "读取模型流失败"}, true},
		{"限流", Error{Status: 429, Message: "rate limit"}, true},
		{"网关故障", Error{Status: 502, Message: "bad gateway"}, true},
		{"鉴权失败", Error{Status: 401, Message: "invalid api key"}, false},
		{"没有权限", Error{Status: 403, Message: "forbidden"}, false},
		{"参数错误", Error{Status: 400, Message: "unknown parameter"}, false},
		{"模型不存在", Error{Status: 404, Message: "model not found"}, false},
	}
	for _, c := range cases {
		if got := c.err.Retryable(); got != c.retry {
			t.Errorf("%s：期望 retryable=%v，实际 %v", c.name, c.retry, got)
		}
	}
}

func TestContextWindowDetection(t *testing.T) {
	// 各家上游的说法都不一样，判漏了整轮就直接失败。
	exceeded := []Error{
		{Status: 400, Message: "This model's maximum context length is 8192 tokens, however you requested 9000"},
		{Status: 400, Message: "context_length_exceeded"},
		{Status: 400, Message: "prompt is too long: 250000 tokens > 200000 maximum"},
		{Status: 413, Message: "Input is too long for requested model"},
		{Status: 400, Message: "请求的上下文长度超过模型上限"},
	}
	for _, err := range exceeded {
		if !err.ContextWindowExceeded() {
			t.Errorf("应当识别为超窗：%q", err.Message)
		}
	}

	other := []Error{
		{Status: 400, Message: "unknown parameter: reasoning_effort"},
		{Status: 401, Message: "invalid api key"},
		// 5xx 带上下文字样也不算：那是上游自己的问题，压缩救不了，该重试。
		{Status: 500, Message: "internal error in context handler"},
	}
	for _, err := range other {
		if err.ContextWindowExceeded() {
			t.Errorf("不该识别为超窗：%q", err.Message)
		}
	}
}

// 额度用完常以 429 返回，但它不是限流：重试只会白等十几秒再收同一个错。
func TestQuotaExhaustedIsNotRetried(t *testing.T) {
	cases := []*Error{
		{Status: 429, Code: "insufficient_quota", Message: "You exceeded your current quota, please check your plan and billing details."},
		{Status: 400, Message: "Access denied, please make sure your account is in good standing. Arrearage"},
		{Status: 402, Message: "Insufficient Balance"},
		{Status: 0, Message: "模型流式返回错误：credit_balance_exhausted"},
	}
	for _, err := range cases {
		if !err.QuotaExhausted() || err.Retryable() {
			t.Errorf("额度用完不该重试：%+v", err)
		}
	}
	if limited := (&Error{Status: 429, Message: "Rate limit reached for requests"}); !limited.Retryable() || limited.QuotaExhausted() {
		t.Error("真正的限流仍要重试")
	}
}

func TestRejectsReasoningEffort(t *testing.T) {
	yes := []*Error{
		{Status: 400, Message: "Unsupported parameter: 'reasoning_effort' is not supported with this model."},
		{Status: 400, Message: "Unrecognized request argument supplied: reasoning_effort"},
		{Status: 422, Message: "reasoning is not supported for this model"},
	}
	for _, err := range yes {
		if !err.RejectsReasoningEffort() {
			t.Errorf("应认出是推理档位参数被拒：%q", err.Message)
		}
	}
	no := []*Error{
		{Status: 400, Message: "Invalid value for 'temperature'"},
		{Status: 500, Message: "reasoning_effort internal error"},
	}
	for _, err := range no {
		if err.RejectsReasoningEffort() {
			t.Errorf("不该当成推理档位被拒：%+v", err)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"30":                            30 * time.Second,
		"1.5":                           1500 * time.Millisecond,
		"-3":                            0,
		"garbage":                       0,
		"Mon, 28 Sep 2026 10:01:00 GMT": time.Minute,
		"Mon, 28 Sep 2026 09:59:00 GMT": 0,
	}
	for header, want := range cases {
		if got := parseRetryAfter(header, now); got != want {
			t.Errorf("Retry-After %q = %v，应为 %v", header, got, want)
		}
	}
}

// 真的 HTTP 响应：Retry-After 头与 error.code 要读进 Error。
func TestReadErrorBodyKeepsRetryAfterAndCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "sk-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *Error：%v", err)
	}
	if apiErr.RetryAfter != 7*time.Second || apiErr.Code != "rate_limit_exceeded" {
		t.Errorf("RetryAfter=%v Code=%q", apiErr.RetryAfter, apiErr.Code)
	}
}
