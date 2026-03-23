package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

type deviceProfile struct {
	Width     int
	Height    int
	Scale     float64
	Mobile    bool
	UserAgent string
}

var deviceProfiles = map[string]deviceProfile{
	"iPhone SE":      {Width: 375, Height: 667, Scale: 2.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPhone 14":      {Width: 390, Height: 844, Scale: 3.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPhone 14 Pro":  {Width: 393, Height: 852, Scale: 3.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPhone 15 Pro":  {Width: 393, Height: 852, Scale: 3.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"},
	"iPad":           {Width: 768, Height: 1024, Scale: 2.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"iPad Pro":       {Width: 1024, Height: 1366, Scale: 2.0, Mobile: true, UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"Pixel 7":        {Width: 412, Height: 915, Scale: 2.625, Mobile: true, UserAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36"},
	"Galaxy S23":     {Width: 360, Height: 780, Scale: 3.0, Mobile: true, UserAgent: "Mozilla/5.0 (Linux; Android 14; SM-S911B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36"},
	"Desktop HD":     {Width: 1920, Height: 1080, Scale: 1.0, Mobile: false, UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	"Desktop":        {Width: 1280, Height: 720, Scale: 1.0, Mobile: false, UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	"Laptop":         {Width: 1366, Height: 768, Scale: 1.0, Mobile: false, UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	"MacBook Pro 14": {Width: 1512, Height: 982, Scale: 2.0, Mobile: false, UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
}

func (bm *browserManager) actionSetDevice(reqCtx context.Context, p browserParams) (string, error) {
	if p.Device == "" {
		names := make([]string, 0, len(deviceProfiles))
		for name := range deviceProfiles {
			names = append(names, name)
		}
		return browserJSON("ok", true, "available_devices", names), nil
	}

	profile, ok := deviceProfiles[p.Device]
	if !ok {
		names := make([]string, 0, len(deviceProfiles))
		for name := range deviceProfiles {
			names = append(names, name)
		}
		return "", fmt.Errorf("unknown device %q, available: %v", p.Device, names)
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		err := emulation.SetDeviceMetricsOverride(int64(profile.Width), int64(profile.Height), profile.Scale, profile.Mobile).Do(ctx)
		if err != nil {
			return err
		}
		return emulation.SetUserAgentOverride(profile.UserAgent).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("set_device: %w", err)
	}

	return browserJSON("ok", true, "device", p.Device, "width", profile.Width, "height", profile.Height, "mobile", profile.Mobile), nil
}

func (bm *browserManager) actionSetMedia(reqCtx context.Context, p browserParams) (string, error) {
	scheme := p.ColorScheme
	if scheme == "" {
		scheme = "no-preference"
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetEmulatedMedia().
			WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: scheme},
			}).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("set_media: %w", err)
	}

	return browserJSON("ok", true, "color_scheme", scheme), nil
}
