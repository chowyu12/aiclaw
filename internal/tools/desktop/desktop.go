package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/google/uuid"
	"github.com/kbinani/screenshot"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const annotatedWidth = 1280 // 截图缩放到的目标宽度

type desktopParams struct {
	Action  string `json:"action"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Text    string `json:"text"`
	Key     string `json:"key"`
	Button  string `json:"button"`
	Clicks  int    `json:"clicks"`
	ScrollX int    `json:"scroll_x"`
	ScrollY int    `json:"scroll_y"`
	Window  string `json:"window"`
	App     string `json:"app"`
	Display int    `json:"display"`
	Region  *rect  `json:"region"`
}

type rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func Handler(ctx context.Context, args string) (string, error) {
	var p desktopParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch p.Action {
	case "screenshot":
		return takeScreenshot(p)
	case "click":
		return doAndVerify(p, actionClick)
	case "type":
		return doAndVerify(p, actionType)
	case "press":
		return doAndVerify(p, actionPress)
	case "scroll":
		return doAndVerify(p, actionScroll)
	case "mouse_move":
		return actionMouseMove(p)
	case "list_windows":
		return actionListWindows()
	case "focus_window":
		return doAndVerify(p, actionFocusWindow)
	case "find_element":
		return actionFindElement(p)
	default:
		return "", fmt.Errorf("unknown action: %s (available: screenshot, click, type, press, scroll, mouse_move, list_windows, focus_window, find_element)", p.Action)
	}
}

// ─── Screenshot ───

func takeScreenshot(p desktopParams) (string, error) {
	filePath := tempFilePath(".png")

	captured := false
	// Platform-native first (screencapture on macOS uses ScreenCaptureKit on 15+).
	if err := platformScreenshotFallback(filePath, p.Region); err == nil {
		captured = true
	}
	// Fall back to kbinani/screenshot (CGDisplayCreateImage — fast, cross-platform).
	if !captured {
		if err := captureDisplay(filePath, p.Display, p.Region); err != nil {
			return "", fmt.Errorf("screenshot failed: %v", err)
		}
	}

	annotateAndScale(filePath)

	sw, sh := getScreenSize()
	nDisp := screenshot.NumActiveDisplays()
	desc := fmt.Sprintf(
		"Desktop screenshot (display %d of %d, screen %dx%d). "+
			"The image has coordinate rulers on edges. "+
			"Use the ruler coordinates directly for x,y in click/scroll actions — they auto-map to real screen positions.",
		p.Display, nDisp, sw, sh,
	)
	return result.NewFileResult(filePath, "image/png", desc), nil
}

func captureDisplay(path string, displayIdx int, region *rect) error {
	n := screenshot.NumActiveDisplays()
	if n == 0 {
		return fmt.Errorf("no active displays found")
	}
	if displayIdx < 0 || displayIdx >= n {
		displayIdx = 0
	}

	bounds := screenshot.GetDisplayBounds(displayIdx)
	var img *image.RGBA
	var err error

	if region != nil {
		img, err = screenshot.Capture(
			bounds.Min.X+region.X, bounds.Min.Y+region.Y,
			region.Width, region.Height,
		)
	} else {
		img, err = screenshot.Capture(bounds.Min.X, bounds.Min.Y, bounds.Dx(), bounds.Dy())
	}
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()
	return png.Encode(f, img)
}

// doAndVerify 执行操作后截图返回，让 AI 验证操作结果。
func doAndVerify(p desktopParams, action func(desktopParams) (string, error)) (string, error) {
	msg, err := action(p)
	if err != nil {
		return "", err
	}

	time.Sleep(500 * time.Millisecond)

	filePath := tempFilePath(".png")
	if captureErr := captureDisplay(filePath, p.Display, nil); captureErr != nil {
		if fbErr := platformScreenshotFallback(filePath, nil); fbErr != nil {
			return msg + "\n(auto-verify screenshot failed)", nil
		}
	}
	annotateAndScale(filePath)
	return result.NewFileResult(filePath, "image/png", msg+". Screenshot for verification (with coordinate rulers)."), nil
}

// ─── Actions ───

func actionClick(p desktopParams) (string, error) {
	btn := p.Button
	if btn == "" {
		btn = "left"
	}
	clicks := p.Clicks
	if clicks <= 0 {
		clicks = 1
	}
	realX, realY := imageToScreen(p.X, p.Y, p.Display)
	if err := platformClick(realX, realY, btn, clicks); err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}
	return fmt.Sprintf("Clicked %s button %d time(s) at image(%d,%d) → screen(%d,%d)", btn, clicks, p.X, p.Y, realX, realY), nil
}

func actionType(p desktopParams) (string, error) {
	if p.Text == "" {
		return "", fmt.Errorf("text is required for type action")
	}
	if err := platformTypeText(p.Text); err != nil {
		return "", fmt.Errorf("type failed: %w", err)
	}
	return fmt.Sprintf("Typed %d characters", len([]rune(p.Text))), nil
}

func actionPress(p desktopParams) (string, error) {
	if p.Key == "" {
		return "", fmt.Errorf("key is required for press action (e.g. enter, tab, ctrl+c, cmd+v)")
	}
	if err := platformKeyPress(p.Key); err != nil {
		return "", fmt.Errorf("press failed: %w", err)
	}
	return fmt.Sprintf("Pressed key: %s", p.Key), nil
}

func actionScroll(p desktopParams) (string, error) {
	dx := p.ScrollX
	dy := p.ScrollY
	if dx == 0 && dy == 0 {
		dy = -3
	}
	realX, realY := imageToScreen(p.X, p.Y, p.Display)
	if err := platformScroll(realX, realY, dx, dy); err != nil {
		return "", fmt.Errorf("scroll failed: %w", err)
	}
	return fmt.Sprintf("Scrolled (%d, %d) at image(%d,%d) → screen(%d,%d)", dx, dy, p.X, p.Y, realX, realY), nil
}

func actionMouseMove(p desktopParams) (string, error) {
	realX, realY := imageToScreen(p.X, p.Y, p.Display)
	if err := platformMouseMove(realX, realY); err != nil {
		return "", fmt.Errorf("mouse_move failed: %w", err)
	}
	return fmt.Sprintf("Moved mouse to image(%d,%d) → screen(%d,%d)", p.X, p.Y, realX, realY), nil
}

func actionListWindows() (string, error) {
	windows, err := platformListWindows()
	if err != nil {
		return "", fmt.Errorf("list_windows failed: %w", err)
	}
	if len(windows) == 0 {
		return "No windows found", nil
	}
	data, _ := json.MarshalIndent(windows, "", "  ")
	return string(data), nil
}

func actionFocusWindow(p desktopParams) (string, error) {
	if p.Window == "" {
		return "", fmt.Errorf("window is required (app name or title keyword)")
	}
	if err := platformFocusWindow(p.Window); err != nil {
		return "", fmt.Errorf("focus_window failed: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	return fmt.Sprintf("Focused window: %s", p.Window), nil
}

func actionFindElement(p desktopParams) (string, error) {
	if p.App == "" {
		return "", fmt.Errorf("app is required for find_element")
	}
	elements, err := platformFindElements(p.App, p.Text)
	if err != nil {
		return "", fmt.Errorf("find_element failed: %w", err)
	}
	if len(elements) == 0 {
		return "No matching elements found. Try screenshot and use visual coordinates.", nil
	}

	sw, sh := getScreenSize()
	scale := getScaleFactor()
	sw /= scale
	sh /= scale

	for i := range elements {
		rx, ry := screenToRuler(elements[i].ScreenX, elements[i].ScreenY, sw, sh)
		elements[i].RulerX = rx
		elements[i].RulerY = ry
	}

	data, _ := json.MarshalIndent(elements, "", "  ")
	prefix := fmt.Sprintf("Found %d element(s)", len(elements))
	if p.Text != "" {
		prefix += fmt.Sprintf(" matching '%s'", p.Text)
	} else {
		prefix += " (interactive elements)"
	}
	return prefix + ". Use ruler_x/ruler_y as x/y for click actions:\n" + string(data), nil
}

// ─── Coordinate mapping ───

// imageToScreen 将标尺坐标映射到实际屏幕坐标。
// AI 从截图标尺上读到的数字就是内容区域坐标（0 ~ contentW），
// 直接按比例映射到屏幕像素即可。
func imageToScreen(rulerX, rulerY, displayIdx int) (int, int) {
	if rulerX < 0 {
		rulerX = 0
	}
	if rulerY < 0 {
		rulerY = 0
	}

	sw, sh := getScreenSize()
	scale := getScaleFactor()
	sw /= scale
	sh /= scale

	contentW := annotatedWidth - rulerLeft
	if contentW <= 0 {
		return rulerX, rulerY
	}
	contentH := sh * contentW / sw

	screenX := rulerX * sw / contentW
	screenY := rulerY * sh / contentH

	return screenX, screenY
}

// screenToRuler 将屏幕坐标转换为标尺坐标（imageToScreen 的逆操作）。
func screenToRuler(screenX, screenY, screenW, screenH int) (int, int) {
	contentW := annotatedWidth - rulerLeft
	contentH := screenH * contentW / screenW
	rulerX := screenX * contentW / screenW
	rulerY := screenY * contentH / screenH
	return rulerX, rulerY
}

// ─── Image annotation ───

const (
	rulerLeft = 32
	rulerTop  = 18
	gridStep  = 100
)

var (
	rulerColor = color.RGBA{R: 255, G: 60, B: 60, A: 220}
	gridColor  = color.RGBA{R: 180, G: 180, B: 180, A: 80}
)

// annotateAndScale 读取原始截图，缩放到 annotatedWidth，添加坐标标尺，覆盖写回。
func annotateAndScale(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		return
	}

	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return
	}

	contentW := annotatedWidth - rulerLeft
	contentH := srcH * contentW / srcW
	totalW := annotatedWidth
	totalH := contentH + rulerTop

	dst := image.NewRGBA(image.Rect(0, 0, totalW, totalH))

	// 白色背景 (标尺区)
	draw.Draw(dst, dst.Bounds(), &image.Uniform{color.RGBA{R: 255, G: 255, B: 255, A: 255}}, image.Point{}, draw.Src)

	// 缩放原图到内容区域（最近邻插值）
	for y := range contentH {
		srcY := y * srcH / contentH
		for x := range contentW {
			srcX := x * srcW / contentW
			dst.Set(x+rulerLeft, y+rulerTop, src.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY))
		}
	}

	// 网格线
	for gx := gridStep; gx < contentW; gx += gridStep {
		imgX := gx + rulerLeft
		for y := rulerTop; y < totalH; y++ {
			blendPixel(dst, imgX, y, gridColor)
		}
	}
	for gy := gridStep; gy < contentH; gy += gridStep {
		imgY := gy + rulerTop
		for x := rulerLeft; x < totalW; x++ {
			blendPixel(dst, x, imgY, gridColor)
		}
	}

	face := basicfont.Face7x13

	// X 轴标尺（顶部）
	for gx := 0; gx <= contentW; gx += gridStep {
		imgX := gx + rulerLeft
		// 刻度线
		for y := rulerTop - 4; y < rulerTop; y++ {
			dst.Set(imgX, y, rulerColor)
		}
		drawString(dst, face, imgX-10, rulerTop-5, rulerColor, strconv.Itoa(gx))
	}

	// Y 轴标尺（左侧）
	for gy := 0; gy <= contentH; gy += gridStep {
		imgY := gy + rulerTop
		// 刻度线
		for x := rulerLeft - 4; x < rulerLeft; x++ {
			dst.Set(x, imgY, rulerColor)
		}
		drawString(dst, face, 1, imgY+4, rulerColor, strconv.Itoa(gy))
	}

	out, err := os.Create(path)
	if err != nil {
		return
	}
	defer out.Close()
	_ = png.Encode(out, dst)
}

func drawString(dst *image.RGBA, face font.Face, x, y int, col color.RGBA, s string) {
	d := &font.Drawer{
		Dst:  dst,
		Src:  &image.Uniform{col},
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

func blendPixel(dst *image.RGBA, x, y int, c color.RGBA) {
	bg := dst.RGBAAt(x, y)
	a := uint16(c.A)
	inv := 255 - a
	dst.SetRGBA(x, y, color.RGBA{
		R: uint8((uint16(c.R)*a + uint16(bg.R)*inv) / 255),
		G: uint8((uint16(c.G)*a + uint16(bg.G)*inv) / 255),
		B: uint8((uint16(c.B)*a + uint16(bg.B)*inv) / 255),
		A: 255,
	})
}

// ─── Helpers ───

type windowInfo struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type uiElement struct {
	Role    string `json:"role"`
	Title   string `json:"title,omitzero"`
	Value   string `json:"value,omitzero"`
	RulerX  int    `json:"ruler_x"`
	RulerY  int    `json:"ruler_y"`
	Width   int    `json:"width,omitzero"`
	Height  int    `json:"height,omitzero"`
	ScreenX int    `json:"-"`
	ScreenY int    `json:"-"`
}

func tempFilePath(ext string) string {
	dir := os.TempDir()
	return filepath.Join(dir, "aiclaw-desktop-"+uuid.New().String()+ext)
}
