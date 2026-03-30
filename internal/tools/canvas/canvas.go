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

	toolresult "github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

const (
	defaultWidth   = 1280
	defaultHeight  = 720
	browserTimeout = 30 * time.Second
	// waitAfterLoad is the settle time given to JS/CSS animations before capture.
	waitAfterLoad = 500 * time.Millisecond
	// waitAfterLoadSnapshot is slightly longer to let heavier charts/canvas finish rendering.
	waitAfterLoadSnapshot = 1 * time.Second
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

// show renders the HTML and captures a screenshot so the result can be
// displayed visually both in the chat UI and passed to vision models.
func show(ctx context.Context, p canvasParams) (string, error) {
	if p.HTML == "" {
		return "", fmt.Errorf("html is required for show")
	}
	// Delegate to snapshot with default dimensions so the LLM and user both
	// receive a real PNG instead of raw HTML source text.
	return snapshot(ctx, p)
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

	browserCtx, done := newBrowserCtx(ctx, browserTimeout)
	defer done()

	var evalOut string
	err = chromedp.Run(browserCtx,
		chromedp.Navigate("file://"+tmpFile),
		chromedp.WaitReady("body"),
		chromedp.Sleep(waitAfterLoad),
		chromedp.Evaluate(p.Expression, &evalOut),
	)
	if err != nil {
		return "", fmt.Errorf("evaluate: %w", err)
	}

	return evalOut, nil
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
		width = defaultWidth
	}
	height := p.Height
	if height <= 0 {
		height = defaultHeight
	}

	browserCtx, done := newBrowserCtx(ctx, browserTimeout)
	defer done()

	var (
		buf   []byte
		isPDF bool
	)
	err = chromedp.Run(browserCtx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate("file://"+tmpFile),
		chromedp.WaitReady("body"),
		chromedp.Sleep(waitAfterLoadSnapshot),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var scrErr error
			// Prefer PNG screenshot: it is a supported vision-model format and
			// avoids the image-format mismatch that occurs when PDF bytes are
			// saved with a .png extension.
			buf, scrErr = page.CaptureScreenshot().Do(ctx)
			if scrErr != nil {
				// Fall back to PDF only when screenshot is unavailable.
				var pdfErr error
				buf, _, pdfErr = page.PrintToPDF().WithPrintBackground(true).Do(ctx)
				if pdfErr != nil {
					return fmt.Errorf("screenshot: %w; pdf: %w", scrErr, pdfErr)
				}
				isPDF = true
			}
			return nil
		}),
	)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}

	outPath, mimeType, desc, err := writeSnapshotFile(ctx, buf, isPDF)
	if err != nil {
		return "", err
	}

	return toolresult.NewFileResult(outPath, mimeType, desc), nil
}

// newBrowserCtx creates a new chromedp browser context that is cancelled when
// the returned done func is called or when the parent ctx is done, whichever
// comes first.
func newBrowserCtx(parent context.Context, timeout time.Duration) (context.Context, func()) {
	// Respect parent cancellation so requests can be aborted.
	allocCtx, allocCancel := chromedp.NewContext(parent)
	timeoutCtx, timeoutCancel := context.WithTimeout(allocCtx, timeout)
	return timeoutCtx, func() {
		timeoutCancel()
		allocCancel()
	}
}

// writeSnapshotFile writes buf to the agent's temp directory with the correct
// extension and returns the path, MIME type, and description.
func writeSnapshotFile(ctx context.Context, buf []byte, isPDF bool) (path, mimeType, desc string, err error) {
	tmpDir := workspace.AgentTmpFromCtx(ctx)
	if err = os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("create tmp dir: %w", err)
	}

	ts := time.Now().UnixMilli()
	if isPDF {
		path = filepath.Join(tmpDir, fmt.Sprintf("canvas_%d.pdf", ts))
		mimeType = "application/pdf"
		desc = "Canvas snapshot (PDF)"
	} else {
		path = filepath.Join(tmpDir, fmt.Sprintf("canvas_%d.png", ts))
		mimeType = "image/png"
		desc = "Canvas snapshot"
	}

	if err = os.WriteFile(path, buf, 0o644); err != nil {
		return "", "", "", fmt.Errorf("save snapshot: %w", err)
	}
	return path, mimeType, desc, nil
}

// saveHTML writes html to a temp file and returns its absolute path.
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
