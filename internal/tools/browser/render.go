package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	log "github.com/sirupsen/logrus"
)

// RenderPageText 在共享浏览器中临时打开一个 tab 渲染 rawURL，返回 body.innerText。
// 完成后立即关闭该 tab，不写入 tabs/tabOrder/activeTab，避免污染工具会话。
// 复用 defaultBrowser 的 allocator，避免每次调用都启动一个新的 Chrome 进程。
func RenderPageText(ctx context.Context, rawURL string, timeout time.Duration, maxLen int) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	if err := isURLSafe(rawURL); err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxLen <= 0 {
		maxLen = 10_000
	}

	bm := defaultBrowser

	bm.opMu.Lock()
	defer bm.opMu.Unlock()

	if err := bm.ensureStarted(ctx); err != nil {
		return "", fmt.Errorf("start browser: %w", err)
	}
	defer bm.resetIdleTimer()

	bm.mu.Lock()
	allocCtx := bm.allocCtx
	bm.mu.Unlock()
	if allocCtx == nil {
		return "", fmt.Errorf("browser allocator missing")
	}

	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(log.Errorf))
	defer tabCancel()

	deadline := time.Now().Add(timeout)
	if ctx != nil {
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
	}
	runCtx, runCancel := context.WithDeadline(tabCtx, deadline)
	defer runCancel()

	var text string
	js := fmt.Sprintf(`(document.body && document.body.innerText || '').substring(0,%d)`, maxLen)
	tasks := chromedp.Tasks{
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500 * time.Millisecond),
		chromedp.Evaluate(js, &text),
	}
	if err := chromedp.Run(runCtx, tasks); err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	return strings.TrimSpace(text), nil
}
