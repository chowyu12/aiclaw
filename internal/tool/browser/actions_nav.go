package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	log "github.com/sirupsen/logrus"
)

// postNavigateSummary 导航完成后获取 title + 可选正文摘要，供 navigate/open_tab 共用。
func postNavigateSummary(tabCtx, reqCtx context.Context, targetURL string) string {
	deadline, _, postWait := navigateMergedDeadline(reqCtx, targetURL)
	extDL := minTime(time.Now().Add(postWait), deadline)
	extCtx, extCancel := context.WithDeadline(tabCtx, extDL)
	defer extCancel()

	var title string
	_ = chromedp.Run(extCtx, chromedp.Title(&title))
	pageText := fetchPageText(extCtx, 3000)

	meta := browserJSON("ok", true, "url", targetURL, "title", title)
	if pageText == "" {
		return meta
	}
	return meta + "\n\n" + wrapUntrustedContent(pageText)
}

func (bm *browserManager) actionNavigate(reqCtx context.Context, p browserParams) (string, error) {
	if p.URL == "" {
		return "", fmt.Errorf("url is required for navigate")
	}
	if err := isURLSafe(p.URL); err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}

	if err := runChromedpNavigate(tabCtx, reqCtx, p.URL); err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}

	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	bm.mu.Lock()
	if bm.tabRefs == nil {
		bm.tabRefs = make(map[string]map[string]elementInfo)
	}
	bm.tabRefs[tabID] = make(map[string]elementInfo)
	if t, ok := bm.tabs[tabID]; ok {
		t.url = p.URL
	}
	bm.mu.Unlock()

	bm.updateTabInfo(tabCtx, tabID)
	return postNavigateSummary(tabCtx, reqCtx, p.URL), nil
}

func (bm *browserManager) actionOpenTab(reqCtx context.Context, p browserParams) (string, error) {
	targetURL := "about:blank"
	if p.URL != "" {
		if err := isURLSafe(p.URL); err != nil {
			return "", err
		}
		targetURL = p.URL
	}

	bm.mu.Lock()
	bm.evictOldestTab()
	tabCtx, tabCancel := chromedp.NewContext(bm.allocCtx, chromedp.WithErrorf(log.Errorf))
	bm.attachTabMonitor(tabCtx)
	bm.mu.Unlock()

	var err error
	if targetURL != "about:blank" {
		err = runChromedpNavigate(tabCtx, reqCtx, targetURL)
	} else {
		err = chromedp.Run(tabCtx, chromedp.Navigate(targetURL))
		if err == nil {
			_ = chromedp.Run(tabCtx, chromedp.WaitReady("body", chromedp.ByQuery))
		}
	}
	if err != nil {
		tabCancel()
		return "", fmt.Errorf("open_tab: %w", err)
	}

	deadline, _, postWait := navigateMergedDeadline(reqCtx, targetURL)
	extDL := minTime(time.Now().Add(postWait), deadline)
	extCtx, extCancel := context.WithDeadline(tabCtx, extDL)
	defer extCancel()

	var title string
	_ = chromedp.Run(extCtx, chromedp.Title(&title))
	pageText := fetchPageText(extCtx, 3000)

	bm.mu.Lock()
	tabID := bm.addTab(tabCtx, tabCancel, targetURL, title)
	if bm.tabRefs == nil {
		bm.tabRefs = make(map[string]map[string]elementInfo)
	}
	bm.tabRefs[tabID] = make(map[string]elementInfo)
	bm.mu.Unlock()

	meta := browserJSON("ok", true, "target_id", tabID, "url", targetURL, "title", title)
	if pageText == "" {
		return meta, nil
	}
	return meta + "\n\n" + wrapUntrustedContent(pageText), nil
}

func (bm *browserManager) actionCloseTab(p browserParams) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	targetID := p.TargetID
	if targetID == "" {
		targetID = bm.activeTab
	}
	if _, ok := bm.tabs[targetID]; !ok {
		return "", fmt.Errorf("tab %q not found", targetID)
	}
	if len(bm.tabs) <= 1 {
		return "", fmt.Errorf("cannot close the last tab, use close action to stop browser")
	}

	bm.cancelTabLocked(targetID, "close_tab")
	bm.unregisterTabIdentityLocked(targetID)
	delete(bm.tabs, targetID)
	bm.removeFromTabOrder(targetID)
	delete(bm.tabRefs, targetID)

	if bm.activeTab == targetID {
		for id := range bm.tabs {
			bm.activeTab = id
			break
		}
	}

	log.WithField("tab", targetID).Info("[Browser] tab closed")
	return browserJSON("ok", true, "closed", targetID, "active", bm.activeTab), nil
}

func (bm *browserManager) actionTabs() (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	type tabEntry struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Title  string `json:"title"`
		Active bool   `json:"active"`
	}
	tabs := make([]tabEntry, 0, len(bm.tabs))
	for _, t := range bm.tabs {
		tabs = append(tabs, tabEntry{
			ID: t.id, URL: t.url, Title: t.title,
			Active: t.id == bm.activeTab,
		})
	}
	return browserJSON("ok", true, "tabs", tabs), nil
}

// navHistoryAction 执行导航历史操作（back/forward/reload），消除三者之间的重复。
func (bm *browserManager) navHistoryAction(reqCtx context.Context, p browserParams, action chromedp.Action, name string) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()
	if err := chromedp.Run(runCtx, action); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	time.Sleep(500 * time.Millisecond)
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

func (bm *browserManager) actionBack(reqCtx context.Context, p browserParams) (string, error) {
	return bm.navHistoryAction(reqCtx, p, chromedp.NavigateBack(), "back")
}

func (bm *browserManager) actionForward(reqCtx context.Context, p browserParams) (string, error) {
	return bm.navHistoryAction(reqCtx, p, chromedp.NavigateForward(), "forward")
}

func (bm *browserManager) actionReload(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()
	if err := chromedp.Run(runCtx, chromedp.Reload()); err != nil {
		return "", fmt.Errorf("reload: %w", err)
	}
	_ = chromedp.Run(runCtx, chromedp.WaitReady("body", chromedp.ByQuery))
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

func (bm *browserManager) removeFromTabOrder(tabID string) {
	for i, id := range bm.tabOrder {
		if id == tabID {
			bm.tabOrder = append(bm.tabOrder[:i], bm.tabOrder[i+1:]...)
			return
		}
	}
}
