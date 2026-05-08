package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"
)

// ProviderError 是 provider 层的统一错误类型，封装上游 LLM API 返回的 HTTP 错误，
// 便于上层判断是否为可重试的瞬态错误（429/500/502/503/504 等）。
//
// 各 adapter（claude、gemini、openai 兼容）在收到非 200 响应时应统一构造 *ProviderError 返回，
// retryingAdapter 据此自动重试，且重试不会消耗 agent 的迭代次数。
type ProviderError struct {
	HTTPStatusCode int
	Message        string
	Body           string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("provider error %d: %s", e.HTTPStatusCode, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("provider error %d: %s", e.HTTPStatusCode, e.Body)
	}
	return fmt.Sprintf("provider error %d", e.HTTPStatusCode)
}

// ── 重试包装器 ─────────────────────────────────────────────

// RetryConfig 控制 LLMProvider 调用层的重试行为。
//   - MaxRetries: 最大重试次数（不含首次），<=0 表示禁用
//   - BaseBackoff: 第一次重试前等待时长，后续按指数 + 抖动递增
//   - MaxBackoff: 单次重试间隔上限
type RetryConfig struct {
	MaxRetries  int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// DefaultRetryConfig 适用于绝大多数 LLM 服务商：3 次重试 + 指数退避。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:  3,
		BaseBackoff: 500 * time.Millisecond,
		MaxBackoff:  8 * time.Second,
	}
}

type retryingAdapter struct {
	inner LLMProvider
	cfg   RetryConfig
}

// WithRetry 用透明重试包装 LLMProvider；对 429/500/502/503/504 等瞬态错误自动重试，
// 调用方（agent run loop）感知不到重试发生，迭代次数不会被消耗。
//
// 流式请求的重试只发生在「建立流之前」（即 CreateChatCompletionStream 返回 error 时）；
// 一旦上游开始下发数据再发生错误，由调用方（流消费循环）按业务语义处理，避免重放副作用。
func WithRetry(inner LLMProvider, cfg RetryConfig) LLMProvider {
	if inner == nil {
		return nil
	}
	if cfg.MaxRetries <= 0 {
		return inner
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 8 * time.Second
	}
	return &retryingAdapter{inner: inner, cfg: cfg}
}

func (a *retryingAdapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= a.cfg.MaxRetries; attempt++ {
		resp, err := a.inner.CreateChatCompletion(ctx, req)
		if err == nil {
			if attempt > 0 {
				log.WithField("attempt", attempt).Info("[Provider] retry succeeded")
			}
			return resp, nil
		}
		lastErr = err
		if !IsTransientError(err) || attempt == a.cfg.MaxRetries {
			return openai.ChatCompletionResponse{}, err
		}
		if waitErr := sleepWithBackoff(ctx, a.cfg, attempt, err); waitErr != nil {
			return openai.ChatCompletionResponse{}, waitErr
		}
	}
	return openai.ChatCompletionResponse{}, lastErr
}

func (a *retryingAdapter) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (ChatStream, error) {
	var lastErr error
	for attempt := 0; attempt <= a.cfg.MaxRetries; attempt++ {
		stream, err := a.inner.CreateChatCompletionStream(ctx, req)
		if err == nil {
			if attempt > 0 {
				log.WithField("attempt", attempt).Info("[Provider] stream retry succeeded")
			}
			return stream, nil
		}
		lastErr = err
		if !IsTransientError(err) || attempt == a.cfg.MaxRetries {
			return nil, err
		}
		if waitErr := sleepWithBackoff(ctx, a.cfg, attempt, err); waitErr != nil {
			return nil, waitErr
		}
	}
	return nil, lastErr
}

var _ LLMProvider = (*retryingAdapter)(nil)

// sleepWithBackoff 在重试前等待退避时长；context 取消时立即返回。
func sleepWithBackoff(ctx context.Context, cfg RetryConfig, attempt int, cause error) error {
	d := backoffDuration(cfg, attempt)
	log.WithFields(log.Fields{
		"attempt": attempt + 1, "max": cfg.MaxRetries, "backoff": d, "cause": cause.Error(),
	}).Warn("[Provider] transient error, will retry")
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// backoffDuration 指数退避 + 抖动，避免雪崩。
func backoffDuration(cfg RetryConfig, attempt int) time.Duration {
	d := cfg.BaseBackoff << attempt
	if d <= 0 || d > cfg.MaxBackoff {
		d = cfg.MaxBackoff
	}
	jitter := time.Duration(rand.Int64N(int64(d) / 2))
	return d/2 + jitter
}

// ── 瞬态错误判定 ────────────────────────────────────────────

// IsTransientError 判断 LLM 错误是否可重试。覆盖：
//   - *ProviderError（claude/gemini 等自定义 adapter 返回）
//   - *openai.APIError / *openai.RequestError（OpenAI 兼容 SDK 返回）
//   - 网络层错误字符串：connection reset、unexpected EOF、too many empty messages
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	var provErr *ProviderError
	if errors.As(err, &provErr) {
		return isTransientStatus(provErr.HTTPStatusCode)
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return isTransientStatus(apiErr.HTTPStatusCode)
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return isTransientStatus(reqErr.HTTPStatusCode)
	}
	msg := err.Error()
	return strings.Contains(msg, "too many empty messages") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected EOF")
}

func isTransientStatus(code int) bool {
	switch code {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	}
	return false
}
