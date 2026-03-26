package urlreader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"

	"github.com/chowyu12/aiclaw/internal/tools/result"
)

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
		text, err := webpageToText(targetURL, 30*time.Second)
		if err == nil {
			return text, nil
		}
		log.WithFields(log.Fields{"url": targetURL, "error": err}).Warn("[url_reader] chromedp render failed, using raw HTTP content")
		return content, nil
	}

	log.WithFields(log.Fields{"url": targetURL, "http_error": httpErr}).Info("[url_reader] HTTP failed, trying chromedp")
	text, err := webpageToText(targetURL, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("http: %v; chromedp: %w", httpErr, err)
	}
	return text, nil
}

func fetchURL(ctx context.Context, targetURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
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

	body, err := io.ReadAll(io.LimitReader(reader, 10_000))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func webpageToText(targetURL string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	var textContent string
	tasks := chromedp.Tasks{
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2 * time.Second),
		chromedp.Evaluate(`document.body.innerText`, &textContent),
	}

	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", fmt.Errorf("chromedp render: %w", err)
	}

	const maxLen = 10_000
	if len(textContent) > maxLen {
		textContent = textContent[:maxLen] + "\n... (content truncated)"
	}

	return textContent, nil
}

func looksLikeHTML(content string) bool {
	head := strings.ToLower(content[:min(len(content), 500)])
	return strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html")
}
