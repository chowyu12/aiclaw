package urlreader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"

	"github.com/chowyu12/aiclaw/internal/tools/browser"
	"github.com/chowyu12/aiclaw/internal/tools/result"
)

const (
	httpFetchTimeout = 15 * time.Second
	renderTimeout    = 30 * time.Second
	maxBodyBytes     = 10_000
)

// sharedHTTPClient 复用底层 TCP/TLS 连接池，避免每次请求新建 Transport。
var sharedHTTPClient = &http.Client{
	Timeout: httpFetchTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

func Handler(ctx context.Context, args string) (string, error) {
	targetURL := result.ExtractJSONField(args, "url")
	if targetURL == "" {
		return "", fmt.Errorf("url is required")
	}

	content, httpErr := fetchURL(ctx, targetURL)
	if httpErr == nil {
		if !looksLikeHTML(content) {
			return content, nil
		}
		text, err := renderPage(ctx, targetURL)
		if err == nil && text != "" {
			return text, nil
		}
		if err != nil {
			log.WithFields(log.Fields{"url": targetURL, "error": err}).Warn("[url_reader] browser render failed, using raw HTTP content")
		}
		return content, nil
	}

	log.WithFields(log.Fields{"url": targetURL, "http_error": httpErr}).Info("[url_reader] HTTP failed, trying browser render")
	text, err := renderPage(ctx, targetURL)
	if err != nil {
		return "", fmt.Errorf("http: %v; render: %w", httpErr, err)
	}
	return text, nil
}

func fetchURL(ctx context.Context, targetURL string) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	reader, err := charset.NewReader(resp.Body, contentType)
	if err != nil {
		reader = resp.Body
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// renderPage 走共享浏览器，避免每次请求都启动新 Chrome 实例。
func renderPage(ctx context.Context, targetURL string) (string, error) {
	text, err := browser.RenderPageText(ctx, targetURL, renderTimeout, maxBodyBytes)
	if err != nil {
		return "", err
	}
	if len(text) > maxBodyBytes {
		text = text[:maxBodyBytes] + "\n... (content truncated)"
	}
	return text, nil
}

func looksLikeHTML(content string) bool {
	if content == "" {
		return false
	}
	head := strings.ToLower(content[:min(len(content), 500)])
	return strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html")
}
