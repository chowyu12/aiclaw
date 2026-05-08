package provider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/chowyu12/go-openai"
)

type fakeProvider struct {
	calls   atomic.Int32
	errs    []error
	respErr error
}

func (f *fakeProvider) CreateChatCompletion(_ context.Context, _ openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	idx := int(f.calls.Add(1)) - 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return openai.ChatCompletionResponse{}, f.errs[idx]
	}
	if f.respErr != nil {
		return openai.ChatCompletionResponse{}, f.respErr
	}
	return openai.ChatCompletionResponse{ID: "ok"}, nil
}

func (f *fakeProvider) CreateChatCompletionStream(_ context.Context, _ openai.ChatCompletionRequest) (ChatStream, error) {
	idx := int(f.calls.Add(1)) - 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return nil, f.errs[idx]
	}
	return nil, errors.New("stream not used in this test")
}

func TestIsTransientError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"provider_429", &ProviderError{HTTPStatusCode: 429}, true},
		{"provider_500", &ProviderError{HTTPStatusCode: 500}, true},
		{"provider_502", &ProviderError{HTTPStatusCode: 502}, true},
		{"provider_503", &ProviderError{HTTPStatusCode: 503}, true},
		{"provider_504", &ProviderError{HTTPStatusCode: 504}, true},
		{"provider_400", &ProviderError{HTTPStatusCode: 400}, false},
		{"provider_401", &ProviderError{HTTPStatusCode: 401}, false},
		{"openai_429", &openai.APIError{HTTPStatusCode: 429}, true},
		{"openai_400", &openai.APIError{HTTPStatusCode: 400}, false},
		{"connection_reset", errors.New("read tcp: connection reset by peer"), true},
		{"unexpected_eof", errors.New("unexpected EOF"), true},
		{"too_many_empty", errors.New("too many empty messages in a row"), true},
		{"random", errors.New("invalid api key"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTransientError(c.err); got != c.want {
				t.Errorf("IsTransientError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestWithRetry_RetriesTransient(t *testing.T) {
	fp := &fakeProvider{
		errs: []error{
			&ProviderError{HTTPStatusCode: 503, Message: "boom"},
			&ProviderError{HTTPStatusCode: 502, Message: "again"},
			nil,
		},
	}
	cfg := RetryConfig{MaxRetries: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	p := WithRetry(fp, cfg)

	resp, err := p.CreateChatCompletion(t.Context(), openai.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if resp.ID != "ok" {
		t.Errorf("expected resp ID=ok, got %q", resp.ID)
	}
	if got := fp.calls.Load(); got != 3 {
		t.Errorf("expected 3 calls (2 retries + success), got %d", got)
	}
}

func TestWithRetry_StopsOnNonTransient(t *testing.T) {
	fp := &fakeProvider{
		errs: []error{&ProviderError{HTTPStatusCode: 401, Message: "bad key"}},
	}
	cfg := RetryConfig{MaxRetries: 3, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	p := WithRetry(fp, cfg)

	_, err := p.CreateChatCompletion(t.Context(), openai.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error for non-transient")
	}
	if got := fp.calls.Load(); got != 1 {
		t.Errorf("expected 1 call (no retry), got %d", got)
	}
}

func TestWithRetry_GivesUpAfterMax(t *testing.T) {
	fp := &fakeProvider{
		errs: []error{
			&ProviderError{HTTPStatusCode: 503},
			&ProviderError{HTTPStatusCode: 503},
			&ProviderError{HTTPStatusCode: 503},
			&ProviderError{HTTPStatusCode: 503},
		},
	}
	cfg := RetryConfig{MaxRetries: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	p := WithRetry(fp, cfg)

	_, err := p.CreateChatCompletion(t.Context(), openai.ChatCompletionRequest{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := fp.calls.Load(); got != 3 {
		t.Errorf("expected 3 calls (1 + 2 retries), got %d", got)
	}
}

func TestWithRetry_DisabledWhenMaxRetriesZero(t *testing.T) {
	fp := &fakeProvider{}
	p := WithRetry(fp, RetryConfig{MaxRetries: 0})
	if p != fp {
		t.Error("WithRetry should return inner unchanged when MaxRetries<=0")
	}
}

func TestProviderError_Error(t *testing.T) {
	e := &ProviderError{HTTPStatusCode: 502, Message: "bad gateway"}
	if got := e.Error(); got != "provider error 502: bad gateway" {
		t.Errorf("unexpected error string: %q", got)
	}
}
