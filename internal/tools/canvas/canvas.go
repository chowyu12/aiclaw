package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type canvasParams struct {
	Action     string `json:"action"`
	HTML       string `json:"html"`
	Expression string `json:"expression"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func Handler(ctx context.Context, args string) (string, error) {
	var p canvasParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch p.Action {
	case "show":
		return show(ctx, p)
	case "evaluate":
		return evaluate(ctx, p)
	case "snapshot":
		return snapshot(ctx, p)
	default:
		return "", fmt.Errorf("unknown action %q, supported: show, evaluate, snapshot", p.Action)
	}
}

func show(ctx context.Context, p canvasParams) (string, error) {
	if p.HTML == "" {
		return "", fmt.Errorf("html is required for show")
	}

	tmpFile, err := saveHTML(ctx, p.HTML)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Canvas rendered: %s\nOpen in browser to preview.", tmpFile), nil
}

func evaluate(ctx context.Context, p canvasParams) (string, error) {
	if p.HTML == "" {
		return "", fmt.Errorf("html is required for evaluate")
	}
	if p.Expression == "" {
		return "", fmt.Errorf("expression is required for evaluate")
	}

	tmpFile, err := saveHTML(ctx, p.HTML)
	if err != nil {
		return "", err
	}

	allocCtx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	allocCtx, cancel = context.WithTimeout(allocCtx, 30*time.Second)
	defer cancel()

	var result string
	err = chromedp.Run(allocCtx,
		chromedp.Navigate("file://"+tmpFile),
		chromedp.WaitReady("body"),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Evaluate(p.Expression, &result),
	)
	if err != nil {
		return "", fmt.Errorf("evaluate: %w", err)
	}

	return result, nil
}

func snapshot(ctx context.Context, p canvasParams) (string, error) {
	if p.HTML == "" {
		return "", fmt.Errorf("html is required for snapshot")
	}

	tmpFile, err := saveHTML(ctx, p.HTML)
	if err != nil {
		return "", err
	}

	width := p.Width
	if width <= 0 {
		width = 1280
	}
	height := p.Height
	if height <= 0 {
		height = 720
	}

	allocCtx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	allocCtx, cancel = context.WithTimeout(allocCtx, 30*time.Second)
	defer cancel()

	var buf []byte
	err = chromedp.Run(allocCtx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate("file://"+tmpFile),
		chromedp.WaitReady("body"),
		chromedp.Sleep(1*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, _, err = page.PrintToPDF().WithPrintBackground(true).Do(ctx)
			if err != nil {
				buf, err = page.CaptureScreenshot().Do(ctx)
			}
			return err
		}),
	)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}

	tmpDir := workspace.AgentTmpFromCtx(ctx)
	snapshotPath := filepath.Join(tmpDir, fmt.Sprintf("canvas_%d.png", time.Now().UnixMilli()))
	if err := os.WriteFile(snapshotPath, buf, 0o644); err != nil {
		return "", fmt.Errorf("save snapshot: %w", err)
	}

	return result.NewFileResult(snapshotPath, "image/png", "Canvas snapshot"), nil
}

func saveHTML(ctx context.Context, html string) (string, error) {
	tmpDir := workspace.AgentTmpFromCtx(ctx)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("create tmp dir: %w", err)
	}

	filePath := filepath.Join(tmpDir, fmt.Sprintf("canvas_%d.html", time.Now().UnixMilli()))
	if err := os.WriteFile(filePath, []byte(html), 0o644); err != nil {
		return "", fmt.Errorf("save html: %w", err)
	}
	return filePath, nil
}
