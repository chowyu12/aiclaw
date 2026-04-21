package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

var DefaultBaseURLs = map[model.ProviderType]string{
	model.ProviderOpenAI:       "https://api.openai.com/v1",
	model.ProviderQwen:         "https://dashscope.aliyuncs.com/compatible-mode/v1",
	model.ProviderKimi:         "https://api.moonshot.cn/v1",
	model.ProviderOpenRouter:   "https://openrouter.ai/api/v1",
	model.ProviderOpenAICompat: "",
	model.ProviderClaude:       "https://api.anthropic.com",
	model.ProviderGemini:       "https://generativelanguage.googleapis.com",
}

// providerHTTPClient 复用连接池与 loggingTransport，避免 claude/gemini/FetchRemoteModels
// 以及每个 provider 实例都各自持有 *http.Client 引起的连接重复建立。
var providerHTTPClient = &http.Client{
	Transport: &loggingTransport{inner: http.DefaultTransport},
}

// SharedHTTPClient 返回 provider 层统一的 HTTP 客户端（测试或特殊场景可用）。
func SharedHTTPClient() *http.Client { return providerHTTPClient }

type adapter struct {
	client *openai.Client
}

func (a *adapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return a.client.CreateChatCompletion(ctx, req)
}

func (a *adapter) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (ChatStream, error) {
	return a.client.CreateChatCompletionStream(ctx, req)
}

var _ LLMProvider = (*adapter)(nil)

func resolveBaseURL(p *model.Provider) (string, error) {
	baseURL := p.BaseURL
	if baseURL == "" {
		var ok bool
		baseURL, ok = DefaultBaseURLs[p.Type]
		if !ok || baseURL == "" {
			return "", fmt.Errorf("unsupported provider type: %s", p.Type)
		}
	}
	return strings.TrimRight(baseURL, "/"), nil
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func FetchRemoteModels(ctx context.Context, p *model.Provider) ([]string, error) {
	baseURL, err := resolveBaseURL(p)
	if err != nil {
		return nil, err
	}

	switch p.Type {
	case model.ProviderClaude:
		return fetchClaudeModels(ctx, p.APIKey, baseURL)
	case model.ProviderGemini:
		return fetchGeminiModels(ctx, p.APIKey, baseURL)
	}

	modelsURL := baseURL + "/models"

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("models API returned %d: %s", resp.StatusCode, string(body))
	}

	var result openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

func NewFromProvider(p *model.Provider) (LLMProvider, error) {
	baseURL, err := resolveBaseURL(p)
	if err != nil {
		return nil, err
	}

	switch p.Type {
	case model.ProviderClaude:
		return newClaudeAdapter(p.APIKey, baseURL), nil
	case model.ProviderGemini:
		return newGeminiAdapter(p.APIKey, baseURL), nil
	}

	config := openai.DefaultConfig(p.APIKey)
	config.BaseURL = baseURL
	config.HTTPClient = providerHTTPClient

	client := openai.NewClientWithConfig(config)
	return &adapter{client: client}, nil
}

type loggingTransport struct {
	inner http.RoundTripper
}

var base64DataRe = regexp.MustCompile(`"data:[^"]{0,50};base64,[A-Za-z0-9+/=]{200,}"`)

func truncateBase64(body string) string {
	return base64DataRe.ReplaceAllStringFunc(body, func(m string) string {
		return m[:min(80, len(m))] + `...(base64 truncated)"`
	})
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	l := log.WithFields(log.Fields{"method": req.Method, "url": req.URL.String()})

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}

		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))

		logBody := string(bodyBytes)
		if len(logBody) > 50*1024 {
			logBody = truncateBase64(logBody)
		}
		l.WithField("body", logBody).Trace("[LLM-HTTP] >> request")
	}

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		l.WithError(err).Trace("[LLM-HTTP] << error")
		return nil, err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		l.WithField("status", resp.StatusCode).Trace("[LLM-HTTP] << streaming response started")
		return resp, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	l.WithFields(log.Fields{"status": resp.StatusCode, "body": string(respBody)}).Trace("[LLM-HTTP] << response")
	return resp, nil
}

// effortBudgetRatio 将 reasoning_effort 级别映射为 thinking budget 占 max_tokens 的比例。
func effortBudgetRatio(effort string) float64 {
	switch effort {
	case "low":
		return 0.25
	case "high":
		return 0.75
	default:
		return 0.5
	}
}
