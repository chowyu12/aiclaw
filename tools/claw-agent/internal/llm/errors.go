package llm

import (
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// Error 是一次模型调用的失败，带上重试与压缩需要的分类信息。
//
// 分类必须在这一层做：上层只拿到一个字符串的话，就只能靠 strings.Contains
// 猜「这个错该不该重试」，而那种猜测会在换一个上游渠道之后静默失效。
type Error struct {
	// Status 是 HTTP 状态码。0 表示压根没拿到响应——连接失败、DNS、
	// 或者流读到一半断了。
	Status int
	// Message 是上游给的错误描述，已从各种信封里取出来。
	Message string
	// Err 是底层错误（网络错误、读流错误），可能为 nil。
	Err error
	// Code 是上游给的机器可读原因（error.code / error.type，如 insufficient_quota），可能为空。
	Code string
	// RetryAfter 是上游在 Retry-After 里说的「多久之后再试」。0 表示没说。
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return i18n.D("模型返回错误（HTTP {status}）：{message}", "status", e.Status, "message", e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable 报告重新发一遍同样的请求是否可能成功。
//
// 判据是「问题出在传输还是出在请求本身」：连接断了、限流了、上游 5xx，
// 过一会儿重试有意义；参数错、鉴权失败、超出上下文窗口，重试只是把同一个
// 错误再收一遍，还要多花一次额度。
func (e *Error) Retryable() bool {
	// 额度用完、欠费：也常以 429 返回，但重试只会白等十几秒再收一遍同样的错。
	if e.QuotaExhausted() {
		return false
	}
	switch {
	case e.Status == 0:
		// 没走到响应：连接失败或流中断，重连有意义。
		return true
	case e.Status == 408 || e.Status == 409 || e.Status == 429:
		return true
	case e.Status >= 500:
		return true
	default:
		return false
	}
}

// contextWindowMarkers 是各家上游表达「历史太长」时用过的说法。
//
// 没有统一错误码：OpenAI 给 context_length_exceeded，Claude 兼容层说
// prompt is too long，Qwen/Kimi 的中文渠道直接返回中文。命中任意一条就按
// 超窗处理——判错了最多是白压缩一次，判漏了整轮直接失败。
var contextWindowMarkers = []string{
	"context_length_exceeded",
	"context length",
	"contextlength",
	"maximum context",
	"context window",
	"prompt is too long",
	"too many tokens",
	"reduce the length",
	"input is too long",
	"上下文长度",
	"上下文过长",
	"输入过长",
}

// quotaMarkers 是「额度用完 / 欠费」的说法。与限流（稍后再试就好）不同，这一类要用户去充值。
var quotaMarkers = []string{
	"insufficient_quota",
	"exceeded your current quota",
	"credit_balance_exhausted",
	"spend_limit_exceeded",
	"billing_hard_limit",
	"insufficient balance",
	"insufficient_balance",
	"arrearage",
	"余额不足",
	"欠费",
	"额度已用完",
}

// QuotaExhausted 报告这次失败是不是额度用完或欠费（重试没用）。
func (e *Error) QuotaExhausted() bool {
	lowered := strings.ToLower(e.Code + " " + e.Message)
	for _, marker := range quotaMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// RejectsReasoningEffort 报告上游是不是因为不认 reasoning_effort 参数而拒绝了请求。
//
// 非推理模型（gpt-4o 这一类）与 Azure 会直接 400：Unsupported parameter:
// 'reasoning_effort'。桌面端默认给每个模型都带推理档位，不认的上游就一轮都跑不起来。
func (e *Error) RejectsReasoningEffort() bool {
	if e.Status != 400 && e.Status != 422 {
		return false
	}
	lowered := strings.ToLower(e.Message)
	if strings.Contains(lowered, "reasoning_effort") || strings.Contains(lowered, "reasoning effort") {
		return true
	}
	if !strings.Contains(lowered, "reasoning") {
		return false
	}
	for _, marker := range []string{"not support", "unsupported", "unrecognized", "unknown", "invalid", "不支持"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// ContextWindowExceeded 报告这次失败是不是「历史塞不下了」。
//
// 这一类不能重试，得先压缩历史再试；分不出来的话会在窗口撑满之后
// 把同一个请求原样重发四次，然后整轮失败。
func (e *Error) ContextWindowExceeded() bool {
	if e.Status != 400 && e.Status != 413 && e.Status != 422 {
		return false
	}
	lowered := strings.ToLower(e.Message)
	for _, marker := range contextWindowMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
