package llm

import (
	"fmt"
	"strings"
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
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("模型返回错误（HTTP %d）：%s", e.Status, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable 报告重新发一遍同样的请求是否可能成功。
//
// 判据是「问题出在传输还是出在请求本身」：连接断了、限流了、上游 5xx，
// 过一会儿重试有意义；参数错、鉴权失败、超出上下文窗口，重试只是把同一个
// 错误再收一遍，还要多花一次额度。
func (e *Error) Retryable() bool {
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
