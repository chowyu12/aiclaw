package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/chowyu12/aiclaw/internal/model"
)

// httpToolClient HTTP handler 工具共享客户端；请求级超时通过 context 控制，
// 避免每次 NewHTTPHandler 触发都新建 *http.Client 而丢失底层连接池。
var httpToolClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

func NewHTTPHandler(cfg model.HTTPHandlerConfig, timeoutSec int) func(context.Context, string) (string, error) {
	return func(ctx context.Context, input string) (string, error) {
		return httpToolHandler(ctx, cfg, timeoutSec, input)
	}
}

func httpToolHandler(ctx context.Context, cfg model.HTTPHandlerConfig, timeoutSec int, input string) (string, error) {
	urlStr := cfg.URL
	method := cfg.Method
	if method == "" {
		method = http.MethodGet
	}

	var params map[string]any
	if input != "" {
		json.Unmarshal([]byte(input), &params)
	}
	for key, val := range params {
		urlStr = strings.ReplaceAll(urlStr, "{"+key+"}", fmt.Sprint(val))
	}

	var body io.Reader
	if cfg.Body != "" {
		bodyStr := cfg.Body
		for key, val := range params {
			bodyStr = strings.ReplaceAll(bodyStr, "{"+key+"}", fmt.Sprint(val))
		}
		body = strings.NewReader(bodyStr)
	}

	timeout := time.Duration(timeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := httpToolClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	reader, err := charset.NewReader(resp.Body, contentType)
	if err != nil {
		reader = resp.Body
	}

	respBody, err := io.ReadAll(io.LimitReader(reader, 10_000))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return string(respBody), nil
}
