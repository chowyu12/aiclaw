package browser

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	defaultNavigateDeadline = 60 * time.Second
	heavyNavigateDeadline   = 30 * time.Second

	defaultBodyWait = 14 * time.Second
	heavyBodyWait   = 5 * time.Second

	postNavigateExtract = 12 * time.Second
	heavyPostExtract    = 6 * time.Second

	// defaultInteractionDeadline 用于 click/type 等：tabCtx 本身通常无 deadline，避免 chromedp 无限等待。
	defaultInteractionDeadline = 60 * time.Second
	interactionDeadlineCap     = 180 * time.Second

	// 分阶段超时：避免 SendKeys/Click 在错误 ref 上占满整段交互截止时间。
	interactionWaitVisibleTimeout = 12 * time.Second
	interactionPointerPhaseTimeout = 24 * time.Second
)

func hostFromRawURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// isHeavyDynamicSite 对资源多、长连接多或风控常见的站点缩短导航与等待时间，避免 chromedp 长时间等不到稳定 load。
func isHeavyDynamicSite(host string) bool {
	switch {
	case strings.Contains(host, "baidu."),
		strings.HasSuffix(host, "baidu.com"),
		strings.Contains(host, "weibo.com"),
		strings.Contains(host, "weixin.qq.com"),
		strings.Contains(host, "twitter.com"),
		strings.Contains(host, "x.com"):
		return true
	default:
		return false
	}
}

// navigateMergedDeadline 合并：站点策略上限、请求 / Agent ctx 的截止时间。
func navigateMergedDeadline(reqCtx context.Context, rawURL string) (deadline time.Time, bodyWait, postExtract time.Duration) {
	host := hostFromRawURL(rawURL)
	heavy := isHeavyDynamicSite(host)

	navMax := defaultNavigateDeadline
	bodyWait = defaultBodyWait
	postExtract = postNavigateExtract
	if heavy {
		navMax = heavyNavigateDeadline
		bodyWait = heavyBodyWait
		postExtract = heavyPostExtract
	}

	deadline = time.Now().Add(navMax)
	if d, ok := reqCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline, bodyWait, postExtract
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// mergedActionContext 合并 Agent/HTTP 请求截止时间 defaultInteractionDeadline，用于 chromedp.Run。
func mergedActionContext(tabCtx, reqCtx context.Context) (context.Context, context.CancelFunc) {
	return mergedActionContextMax(tabCtx, reqCtx, defaultInteractionDeadline)
}

// mergedActionContextMax 与 mergedActionContext 相同，但允许单次动作使用更长上限（如 slowly 输入、多字段表单）。
func mergedActionContextMax(tabCtx, reqCtx context.Context, maxDur time.Duration) (context.Context, context.CancelFunc) {
	if maxDur > interactionDeadlineCap {
		maxDur = interactionDeadlineCap
	}
	deadline := time.Now().Add(maxDur)
	if d, ok := reqCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return context.WithDeadline(tabCtx, deadline)
}

// runChromedpNavigate 使用与 reqCtx 合并后的截止时间执行 Navigate + body WaitReady（短等待策略由 URL 主机名决定）。
func runChromedpNavigate(tabCtx, reqCtx context.Context, rawURL string) error {
	deadline, bodyWait, _ := navigateMergedDeadline(reqCtx, rawURL)
	navCtx, cancel := context.WithDeadline(tabCtx, deadline)
	defer cancel()
	if err := chromedp.Run(navCtx, chromedp.Navigate(rawURL)); err != nil {
		return err
	}
	bodyDL := minTime(time.Now().Add(bodyWait), deadline)
	if !bodyDL.After(time.Now()) {
		return nil
	}
	bodyCtx, bodyCancel := context.WithDeadline(tabCtx, bodyDL)
	defer bodyCancel()
	_ = chromedp.Run(bodyCtx, chromedp.WaitReady("body", chromedp.ByQuery))
	return nil
}
