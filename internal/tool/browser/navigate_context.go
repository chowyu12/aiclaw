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

	defaultBodyWait = 14 * time.Second
	heavyBodyWait   = 5 * time.Second

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

	now := time.Now()
	deadline = now.Add(navMax)
	if d, ok := reqCtx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	// 与 mergedActionContextMax 一致：避免 WithDeadline(parent, 已过期时间) 导致导航立刻 context canceled。
	if !deadline.After(now) {
		deadline = now.Add(navMax)
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

// loadWaitTimeout 是等待 load 事件的上限；超过后如果 DOM 已就绪则视为导航成功。
// chromedp.Navigate 会阻塞到 Page.loadEventFired，资源密集型网站（长连接/流媒体/广告）
// 可能永远不会触发该事件，从而耗尽整个工具超时。
const (
	defaultLoadWait = 25 * time.Second
	heavyLoadWait   = 10 * time.Second
	domProbeTimeout = 5 * time.Second
)

// runChromedpNavigate 分两阶段执行导航：
//  1. 用较短超时尝试完整 load（chromedp.Navigate 等待 loadEventFired）；
//  2. 若 load 超时，探测 DOM 是否已可用——若 body ready 则视为成功（页面可交互，仅残留资源未加载）。
func runChromedpNavigate(tabCtx, reqCtx context.Context, rawURL string) error {
	deadline, bodyWait, _ := navigateMergedDeadline(reqCtx, rawURL)

	host := hostFromRawURL(rawURL)
	loadWait := defaultLoadWait
	if isHeavyDynamicSite(host) {
		loadWait = heavyLoadWait
	}

	navDL := minTime(time.Now().Add(loadWait), deadline)
	navCtx, navCancel := context.WithDeadline(tabCtx, navDL)
	err := chromedp.Run(navCtx, chromedp.Navigate(rawURL))
	navCancel()

	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		// load 事件未在 loadWait 内触发——探测页面是否已部分可用。
		probeCtx, probeCancel := context.WithTimeout(tabCtx, domProbeTimeout)
		probeErr := chromedp.Run(probeCtx, chromedp.WaitReady("body", chromedp.ByQuery))
		probeCancel()
		if probeErr != nil {
			return err
		}
		log.WithFields(log.Fields{"url": rawURL, "load_wait": loadWait}).
			Debug("[Browser] navigate: load event timed out but DOM is ready, continuing")
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
