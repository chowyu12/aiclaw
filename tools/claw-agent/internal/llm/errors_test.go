package llm

import "testing"

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
