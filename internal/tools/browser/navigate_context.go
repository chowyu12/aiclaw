package browser

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	log "github.com/sirupsen/logrus"
)

const (
	defaultNavigateDeadline = 60 * time.Second
	heavyNavigateDeadline   = 30 * time.Second

	postNavigateExtract = 12 * time.Second
	heavyPostExtract    = 6 * time.Second

	// defaultInteractionDeadline 用于 click/type 等：tabCtx 本身通常无 deadline，避免 chromedp 无限等待。
	defaultInteractionDeadline = 60 * time.Second
	interactionDeadlineCap     = 180 * time.Second

	// 分阶段超时：避免 SendKeys/Click 在错误 ref 上占满整段交互截止时间。
	interactionWaitVisibleTimeout  = 12 * time.Second
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

// navigateMergedDeadline 合并站点策略上限与请求 / Agent ctx 的截止时间。
func navigateMergedDeadline(reqCtx context.Context, rawURL string) (deadline time.Time, postExtract time.Duration) {
	host := hostFromRawURL(rawURL)
	heavy := isHeavyDynamicSite(host)

	navMax := defaultNavigateDeadline
	postExtract = postNavigateExtract
	if heavy {
		navMax = heavyNavigateDeadline
		postExtract = heavyPostExtract
	}

	now := time.Now()
	deadline = now.Add(navMax)
	if d, ok := reqCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !deadline.After(now) {
		deadline = now.Add(navMax)
	}
	return deadline, postExtract
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
	if tabCtx.Err() != nil {
		return tabCtx, func() {}
	}
	if maxDur > interactionDeadlineCap {
		maxDur = interactionDeadlineCap
	}
	now := time.Now()
	deadline := now.Add(maxDur)
	if d, ok := reqCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	// WithDeadline(parent, 已过期时间) 会得到「立即可取消」的 ctx，chromedp 会立刻报 context canceled。
	if !deadline.After(now) {
		deadline = now.Add(maxDur)
	}
	ctx, cancel := context.WithDeadline(tabCtx, deadline)
	// 请求/Agent 在动作中途取消时，结束 chromedp.Run。
	if reqCtx.Err() != nil {
		cancel()
		return ctx, func() {}
	}
	stop := context.AfterFunc(reqCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// DOM-first 导航策略常量。
//
// chromedp.Navigate 阻塞到 Page.loadEventFired（所有资源加载完成），
// 但大多数现代网站 DOM 在 2-5 秒即可交互，load 事件可能需要 30 秒+（广告/长连接/流媒体）。
// 策略：给 load 事件一个短窗口，超时后只要 DOM ready 即视为成功。
const (
	defaultLoadWait = 8 * time.Second
	heavyLoadWait   = 4 * time.Second
	domProbeTimeout = 8 * time.Second
)

// runChromedpNavigate 以 DOM 可用为主要成功标准，load 事件为快速路径：
//  1. 用短超时尝试 chromedp.Navigate（等 loadEventFired）；简单页面会在此阶段直接完成。
//  2. 若 load 超时，探测 DOM body 是否已可交互——ready 则返回成功。
//  3. 两者都失败才返回错误。
func runChromedpNavigate(tabCtx, reqCtx context.Context, rawURL string) error {
	deadline, _ := navigateMergedDeadline(reqCtx, rawURL)

	host := hostFromRawURL(rawURL)
	loadWait := defaultLoadWait
	if isHeavyDynamicSite(host) {
		loadWait = heavyLoadWait
	}

	// Phase 1: 快速路径——等 load 事件，简单页面几秒就能完成。
	navDL := minTime(time.Now().Add(loadWait), deadline)
	navCtx, navCancel := context.WithDeadline(tabCtx, navDL)
	err := chromedp.Run(navCtx, chromedp.Navigate(rawURL))
	navCancel()

	if err == nil {
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	// Phase 2: load 未触发——探测 DOM 是否已可交互（body ready）。
	probeDL := minTime(time.Now().Add(domProbeTimeout), deadline)
	if !probeDL.After(time.Now()) {
		return err
	}
	probeCtx, probeCancel := context.WithDeadline(tabCtx, probeDL)
	probeErr := chromedp.Run(probeCtx, chromedp.WaitReady("body", chromedp.ByQuery))
	probeCancel()
	if probeErr != nil {
		return err
	}

	log.WithFields(log.Fields{"url": rawURL, "load_wait": loadWait}).
		Debug("[Browser] navigate: load event timed out but DOM is ready, continuing")
	return nil
}
