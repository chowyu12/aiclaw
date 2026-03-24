package browser

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	cdpTarget "github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type browserParams struct {
	Action       string      `json:"action"`
	URL          string      `json:"url"`
	Ref          string      `json:"ref"`
	Text         string      `json:"text"`
	Expression   string      `json:"expression"`
	Selector     string      `json:"selector"`
	FullPage     bool        `json:"full_page"`
	Submit       bool        `json:"submit"`
	Slowly       bool        `json:"slowly"`
	Button       string      `json:"button"`
	DoubleClick  bool        `json:"double_click"`
	StartRef     string      `json:"start_ref"`
	EndRef       string      `json:"end_ref"`
	Values       []string    `json:"values"`
	Fields       []formField `json:"fields"`
	TargetID     string      `json:"target_id"`
	WaitTime     int         `json:"wait_time"`
	WaitText     string      `json:"wait_text"`
	WaitSelector string      `json:"wait_selector"`
	WaitURL      string      `json:"wait_url"`
	WaitFn       string      `json:"wait_fn"`
	WaitLoad     string      `json:"wait_load"`
	Accept       *bool       `json:"accept"`
	PromptText   string      `json:"prompt_text"`
	Paths        []string    `json:"paths"`
	ScrollY      int         `json:"scroll_y"`

	// Console/Network monitoring
	Level  string `json:"level"`
	Filter string `json:"filter"`
	Clear  bool   `json:"clear"`

	// Cookie management
	Operation    string `json:"operation"`
	CookieName   string `json:"cookie_name"`
	CookieValue  string `json:"cookie_value"`
	CookieURL    string `json:"cookie_url"`
	CookieDomain string `json:"cookie_domain"`

	// Storage management
	StorageType string `json:"storage_type"`
	Key         string `json:"key"`
	Value       string `json:"value"`

	// Press key
	KeyName string `json:"key_name"`

	// Resize viewport
	Width  int `json:"width"`
	Height int `json:"height"`

	// Device emulation
	Device      string `json:"device"`
	ColorScheme string `json:"color_scheme"`
}

type formField struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type tabInfo struct {
	id           string
	ctx          context.Context
	cancel       context.CancelFunc
	url          string
	title        string
	cdpTargetID  string
	cdpSessionID string
	canceledBy   string
	canceledTime time.Time
}

const (
	defaultIdleTimeout = 10 * time.Minute
	defaultMaxTabs     = 10
)

type browserManager struct {
	// opMu 序列化所有「工具 Handler」与「closeBrowser/idle 关闭」；否则 idle 里 allocCancel 可与正在执行的 chromedp 并发，
	// tabCtx 被关掉后 snapshot/get_text 会立刻 context canceled（与 Agent 超时无关）。
	opMu        sync.Mutex
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
	tabs        map[string]*tabInfo
	tabOrder    []string // 按创建顺序记录 tabID，用于淘汰最旧 tab
	activeTab   string
	tabRefs     map[string]map[string]elementInfo // tabID -> snapshot ref -> element
	started     bool
	remote      bool
	tmpDir      string
	visible     bool
	monitor     *eventMonitor
	viewWidth   int
	viewHeight  int
	userAgent   string
	proxy       string
	cdpEndpoint string
	idleTimeout time.Duration
	maxTabs     int
	idleTimer   *time.Timer
	// CDP 事件归因：target/session -> tab 映射；用于定位「外部关闭 target」。
	cdpTargetToTab  map[string]string
	cdpSessionToTab map[string]string
	// targetLifecycleTabs 已为哪些 tabID 注册过 ListenTarget 生命周期回调（recover 同 id 新 ctx 需重挂）。
	targetLifecycleTabs map[string]bool
}

var defaultBrowser = &browserManager{
	tabs:            make(map[string]*tabInfo),
	tabRefs:         make(map[string]map[string]elementInfo),
	cdpTargetToTab:  make(map[string]string),
	cdpSessionToTab: make(map[string]string),
	targetLifecycleTabs: make(map[string]bool),
}

func SetVisible(v bool) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.visible = v
}

func SetViewport(width, height int) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.viewWidth = width
	defaultBrowser.viewHeight = height
}

func SetUserAgent(ua string) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.userAgent = ua
}

func SetProxy(proxy string) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.proxy = proxy
}

func SetCDPEndpoint(endpoint string) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.cdpEndpoint = endpoint
}

func SetIdleTimeout(d time.Duration) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.idleTimeout = d
}

func SetMaxTabs(n int) {
	defaultBrowser.mu.Lock()
	defer defaultBrowser.mu.Unlock()
	defaultBrowser.maxTabs = n
}

func Shutdown() {
	defaultBrowser.closeBrowser()
}

func Handler(ctx context.Context, args string) (string, error) {
	var p browserParams
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if p.Action == "" {
		return "", fmt.Errorf("action is required")
	}

	bm := defaultBrowser

	if p.Action == "close" {
		return bm.closeBrowser()
	}

	bm.opMu.Lock()
	defer bm.opMu.Unlock()

	if err := bm.ensureStarted(); err != nil {
		return "", fmt.Errorf("start browser: %w", err)
	}
	// 任意一次 browser 调用（含失败）都顺延 idle，避免「evaluate 失败后未 reset」与短 idle 叠加误关会话。
	defer bm.resetIdleTimer()

	var result string
	var err error

	switch p.Action {
	case "navigate":
		result, err = bm.actionNavigate(ctx, p)
	case "screenshot":
		result, err = bm.actionScreenshot(ctx, p)
	case "snapshot":
		result, err = bm.actionSnapshot(ctx, p)
	case "get_text":
		result, err = bm.actionGetText(ctx, p)
	case "evaluate":
		result, err = bm.actionEvaluate(ctx, p)
	case "pdf":
		result, err = bm.actionPDF(ctx, p)
	case "click":
		result, err = bm.actionClick(ctx, p)
	case "type":
		result, err = bm.actionType(ctx, p)
	case "hover":
		result, err = bm.actionHover(ctx, p)
	case "drag":
		result, err = bm.actionDrag(ctx, p)
	case "select":
		result, err = bm.actionSelect(ctx, p)
	case "fill_form":
		result, err = bm.actionFillForm(ctx, p)
	case "scroll":
		result, err = bm.actionScroll(ctx, p)
	case "upload":
		result, err = bm.actionUpload(ctx, p)
	case "wait":
		result, err = bm.actionWait(ctx, p)
	case "dialog":
		result, err = bm.actionDialog(ctx, p)
	case "tabs":
		result, err = bm.actionTabs()
	case "open_tab":
		result, err = bm.actionOpenTab(ctx, p)
	case "close_tab":
		result, err = bm.actionCloseTab(p)
	case "console":
		result, err = bm.actionConsole(p)
	case "network":
		result, err = bm.actionNetwork(p)
	case "cookies":
		result, err = bm.actionCookies(ctx, p)
	case "storage":
		result, err = bm.actionStorage(ctx, p)
	case "press":
		result, err = bm.actionPress(ctx, p)
	case "back":
		result, err = bm.actionBack(ctx, p)
	case "forward":
		result, err = bm.actionForward(ctx, p)
	case "reload":
		result, err = bm.actionReload(ctx, p)
	case "extract_table":
		result, err = bm.actionExtractTable(ctx, p)
	case "resize":
		result, err = bm.actionResize(ctx, p)
	case "set_device":
		result, err = bm.actionSetDevice(ctx, p)
	case "set_media":
		result, err = bm.actionSetMedia(ctx, p)
	case "highlight":
		result, err = bm.actionHighlight(ctx, p)
	default:
		return "", fmt.Errorf("unknown action: %s", p.Action)
	}

	return result, err
}

func (bm *browserManager) ensureStarted() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.started {
		return nil
	}

	tmpBase := workspace.Tmp()
	if tmpBase == "" {
		tmpBase = os.TempDir()
	}
	tmpDir, err := os.MkdirTemp(tmpBase, "browser-agent-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	bm.tmpDir = tmpDir

	if bm.cdpEndpoint != "" {
		if err := bm.startRemote(); err != nil {
			os.RemoveAll(tmpDir)
			bm.tmpDir = ""
			return err
		}
	} else {
		if err := bm.startLocal(); err != nil {
			os.RemoveAll(tmpDir)
			bm.tmpDir = ""
			return err
		}
	}

	return nil
}

func (bm *browserManager) startRemote() error {
	wsURL, err := discoverBrowserWSURL(bm.cdpEndpoint)
	if err != nil {
		return fmt.Errorf("discover CDP endpoint: %w", err)
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), wsURL)
	bm.allocCtx = allocCtx
	bm.allocCancel = allocCancel
	bm.remote = true

	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(log.Errorf))
	if err := chromedp.Run(tabCtx, network.Enable(), runtime.Enable()); err != nil {
		tabCancel()
		allocCancel()
		return fmt.Errorf("init remote tab: %w", err)
	}

	bm.setupMonitor(tabCtx)
	bm.addTab(tabCtx, tabCancel, "about:blank", "New Tab")

	log.WithFields(log.Fields{"tab": bm.activeTab, "endpoint": bm.cdpEndpoint}).Info("[Browser] connected to existing browser")
	return nil
}

func (bm *browserManager) startLocal() error {
	headless := !bm.visible
	w := cmp.Or(bm.viewWidth, 1280)
	h := cmp.Or(bm.viewHeight, 720)
	ua := cmp.Or(bm.userAgent, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", headless),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-background-networking", false),
		chromedp.WindowSize(w, h),
		chromedp.UserAgent(ua),
	)
	if bm.proxy != "" {
		opts = append(opts, chromedp.ProxyServer(bm.proxy))
	}
	if bm.visible {
		log.Info("[Browser] starting in visible mode (non-headless)")
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bm.allocCtx = allocCtx
	bm.allocCancel = allocCancel

	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(log.Errorf))
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		runtime.Enable(),
		chromedp.Navigate("about:blank"),
	); err != nil {
		tabCancel()
		allocCancel()
		return fmt.Errorf("init browser: %w", err)
	}

	bm.setupMonitor(tabCtx)
	bm.addTab(tabCtx, tabCancel, "about:blank", "New Tab")

	log.WithField("tab", bm.activeTab).Info("[Browser] started")
	return nil
}

func (bm *browserManager) addTab(tabCtx context.Context, tabCancel context.CancelFunc, tabURL, title string) string {
	tabID := uuid.New().String()[:8]
	bm.tabs[tabID] = &tabInfo{
		id: tabID, ctx: tabCtx, cancel: tabCancel,
		url: tabURL, title: title,
	}
	bm.captureTabIdentityLocked(tabID, tabCtx)
	bm.attachTargetLifecycleMonitorLocked(tabID, tabCtx)
	bm.tabOrder = append(bm.tabOrder, tabID)
	bm.activeTab = tabID
	bm.started = true
	return tabID
}

func (bm *browserManager) effectiveIdleTimeout() time.Duration {
	if bm.idleTimeout > 0 {
		return bm.idleTimeout
	}
	return defaultIdleTimeout
}

func (bm *browserManager) effectiveMaxTabs() int {
	if bm.maxTabs > 0 {
		return bm.maxTabs
	}
	return defaultMaxTabs
}

func (bm *browserManager) resetIdleTimer() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.started {
		return
	}
	timeout := bm.effectiveIdleTimeout()
	if bm.idleTimer != nil {
		bm.idleTimer.Stop()
	}
	bm.idleTimer = time.AfterFunc(timeout, func() {
		log.WithField("timeout", timeout).Info("[Browser] idle timeout reached, auto-closing")
		bm.closeBrowser()
	})
}

func (bm *browserManager) evictOldestTab() {
	limit := bm.effectiveMaxTabs()
	for len(bm.tabs) >= limit {
		var victim string
		for _, id := range bm.tabOrder {
			if id == bm.activeTab {
				continue
			}
			if _, ok := bm.tabs[id]; ok {
				victim = id
				break
			}
		}
		if victim == "" {
			return
		}
		bm.removeFromTabOrder(victim)
		if _, ok := bm.tabs[victim]; ok {
			bm.cancelTabLocked(victim, "evict_oldest_tab")
			bm.unregisterTabIdentityLocked(victim)
			delete(bm.tabs, victim)
			delete(bm.tabRefs, victim)
			log.WithField("tab", victim).Info("[Browser] evicted oldest tab (tab limit reached)")
		}
	}
}

func discoverBrowserWSURL(endpoint string) (string, error) {
	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
		return endpoint, nil
	}

	endpoint = strings.TrimSuffix(endpoint, "/")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint + "/json/version")
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("parse CDP version response: %w", err)
	}
	if info.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl in response from %s", endpoint)
	}
	return info.WebSocketDebuggerURL, nil
}

func (bm *browserManager) closeBrowser() (string, error) {
	bm.opMu.Lock()
	defer bm.opMu.Unlock()

	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.started {
		return browserJSON("ok", true, "message", "browser not running"), nil
	}

	if bm.idleTimer != nil {
		bm.idleTimer.Stop()
		bm.idleTimer = nil
	}

	for _, tab := range bm.tabs {
		if tab == nil {
			continue
		}
		bm.cancelTabLocked(tab.id, "close_browser")
	}
	if bm.allocCancel != nil {
		bm.allocCancel()
	}
	if bm.tmpDir != "" {
		os.RemoveAll(bm.tmpDir)
	}

	wasRemote := bm.remote
	bm.tabs = make(map[string]*tabInfo)
	bm.tabOrder = nil
	bm.tabRefs = make(map[string]map[string]elementInfo)
	bm.cdpTargetToTab = make(map[string]string)
	bm.cdpSessionToTab = make(map[string]string)
	bm.targetLifecycleTabs = make(map[string]bool)
	bm.activeTab = ""
	bm.started = false
	bm.remote = false
	bm.tmpDir = ""
	bm.monitor = nil

	if wasRemote {
		log.Info("[Browser] disconnected (tabs closed, browser still running)")
		return browserJSON("ok", true, "message", "disconnected, tabs closed"), nil
	}
	log.Info("[Browser] closed")
	return browserJSON("ok", true, "message", "browser closed"), nil
}

// removeTabEntryLocked 从会话中移除 tab（须在持有 bm.mu 下调用）。
func (bm *browserManager) removeTabEntryLocked(id string) {
	if id == "" {
		return
	}
	if bm.targetLifecycleTabs != nil {
		delete(bm.targetLifecycleTabs, id)
	}
	bm.unregisterTabIdentityLocked(id)
	delete(bm.tabs, id)
	bm.removeFromTabOrder(id)
	delete(bm.tabRefs, id)
	if bm.activeTab != id {
		return
	}
	bm.activeTab = ""
	for _, tid := range bm.tabOrder {
		if _, ok := bm.tabs[tid]; ok {
			bm.activeTab = tid
			return
		}
	}
}

// attachTargetLifecycleMonitorLocked 在页会话上监听 Target.* 事件；调用方须已持有 bm.mu。
// chromedp 对带 sessionId 的 CDP 消息只投递到对应 Target 的 ListenTarget，不会进 ListenBrowser。
func (bm *browserManager) attachTargetLifecycleMonitorLocked(tabID string, tabCtx context.Context) {
	if bm.targetLifecycleTabs == nil {
		bm.targetLifecycleTabs = make(map[string]bool)
	}
	if bm.targetLifecycleTabs[tabID] {
		return
	}
	bm.targetLifecycleTabs[tabID] = true
	chromedp.ListenTarget(context.WithoutCancel(tabCtx), bm.handleTargetLifecycleEvent)
}

func (bm *browserManager) handleTargetLifecycleEvent(ev any) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	switch e := ev.(type) {
	case *cdpTarget.EventTargetDestroyed:
		tid := normCDPMapKey(string(e.TargetID))
		tabID := bm.cdpTargetToTab[tid]
		if tabID == "" {
			log.WithField("cdp_target_id", tid).Debug("[Browser] CDP target destroyed (no mapped tab)")
			return
		}
		bm.markTabCanceledByLocked(tabID, "cdp_target_destroyed")
		log.WithFields(log.Fields{"tab": tabID, "cdp_target_id": tid}).Warn("[Browser] CDP target destroyed")
	case *cdpTarget.EventTargetCrashed:
		tid := normCDPMapKey(string(e.TargetID))
		tabID := bm.cdpTargetToTab[tid]
		if tabID == "" {
			return
		}
		bm.markTabCanceledByLocked(tabID, "cdp_target_crashed")
		log.WithFields(log.Fields{
			"tab":           tabID,
			"cdp_target_id": tid,
			"status":        e.Status,
			"error_code":    e.ErrorCode,
		}).Warn("[Browser] CDP target crashed")
	case *cdpTarget.EventDetachedFromTarget:
		sid := normCDPMapKey(string(e.SessionID))
		tabID := bm.cdpSessionToTab[sid]
		if tabID == "" {
			log.WithField("cdp_session_id", sid).Debug("[Browser] CDP detached (no mapped tab)")
			return
		}
		bm.markTabCanceledByLocked(tabID, "cdp_detached_from_target")
		log.WithFields(log.Fields{"tab": tabID, "cdp_session_id": sid}).Warn("[Browser] CDP detached from target")
	}
}

// normCDPMapKey CDP 事件里的 targetId/sessionId 与 chromedp 内存串可能大小写不一致，统一小写再查表。
func normCDPMapKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (bm *browserManager) captureTabIdentityLocked(tabID string, tabCtx context.Context) {
	tab, ok := bm.tabs[tabID]
	if !ok || tab == nil {
		return
	}
	c := chromedp.FromContext(tabCtx)
	if c == nil || c.Target == nil {
		return
	}
	tid := normCDPMapKey(string(c.Target.TargetID))
	sid := normCDPMapKey(string(c.Target.SessionID))
	tab.cdpTargetID = tid
	tab.cdpSessionID = sid
	if tid != "" {
		bm.cdpTargetToTab[tid] = tabID
	}
	if sid != "" {
		bm.cdpSessionToTab[sid] = tabID
	}
}

func (bm *browserManager) unregisterTabIdentityLocked(tabID string) {
	tab, ok := bm.tabs[tabID]
	if !ok || tab == nil {
		return
	}
	if tab.cdpTargetID != "" {
		delete(bm.cdpTargetToTab, normCDPMapKey(tab.cdpTargetID))
	}
	if tab.cdpSessionID != "" {
		delete(bm.cdpSessionToTab, normCDPMapKey(tab.cdpSessionID))
	}
}

func (bm *browserManager) markTabCanceledByLocked(tabID, reason string) {
	tab, ok := bm.tabs[tabID]
	if !ok || tab == nil || tab.canceledBy != "" {
		return
	}
	tab.canceledBy = reason
	tab.canceledTime = time.Now()
}

// cancelTabLocked 记录取消来源并取消 tab（须在持有 bm.mu 下调用）。
func (bm *browserManager) cancelTabLocked(id, reason string) {
	tab, ok := bm.tabs[id]
	if !ok || tab == nil {
		return
	}
	if tab.canceledBy == "" {
		tab.canceledBy = strings.TrimSpace(reason)
		tab.canceledTime = time.Now()
	}
	tab.cancel()
}

func (bm *browserManager) getTabCtx(reqCtx context.Context, targetID string) (context.Context, error) {
	bm.mu.Lock()

	id := targetID
	if id == "" {
		id = bm.activeTab
	}
	if id == "" {
		bm.mu.Unlock()
		return nil, fmt.Errorf("no active tab")
	}
	tab, ok := bm.tabs[id]
	if !ok {
		bm.mu.Unlock()
		return nil, fmt.Errorf("tab %q not found", id)
	}
	if tab.ctx.Err() == nil {
		ctx := tab.ctx
		bm.mu.Unlock()
		return ctx, nil
	}

	recoverURL := tab.url
	fields := log.Fields{
		"tab":          id,
		"recover_url":  recoverURL,
		"ctx_err":      tab.ctx.Err().Error(),
		"cdp_target_id": tab.cdpTargetID,
		"cdp_session_id": tab.cdpSessionID,
		"canceled_by":  tab.canceledBy,
		"canceled_at":  tab.canceledTime,
		"started_tabs": len(bm.tabs),
	}
	if c := context.Cause(tab.ctx); c != nil {
		fields["context_cause"] = c.Error()
	}
	if tab.canceledBy == "" {
		fields["canceled_by_note"] = "no_browser_event_match; check_cdp_id_case_or_unmap_race"
		if bm.allocCtx != nil && bm.allocCtx.Err() != nil {
			fields["alloc_ctx_err"] = bm.allocCtx.Err().Error()
		}
	}
	log.WithFields(fields).Warn("[Browser] tab CDP session lost; recovering (re-attach + navigate to last URL, same target_id)")
	bm.removeTabEntryLocked(id)
	bm.mu.Unlock()

	ctx, err := bm.attemptRecoverTab(reqCtx, recoverURL, id)
	if err != nil {
		return nil, err
	}
	return ctx, nil
}

// attemptRecoverTab 在 alloc 仍可用时新建 chromedp tab，导航到 recoverURL（不安全或空则用 about:blank）。
// reuseID 非空时复用该 target_id，便于 LLM 继续传同一 target_id。
func (bm *browserManager) attemptRecoverTab(reqCtx context.Context, recoverURL, reuseID string) (context.Context, error) {
	bm.mu.Lock()
	if !bm.started {
		bm.mu.Unlock()
		return nil, fmt.Errorf("browser session not active; run action navigate again")
	}
	allocCtx := bm.allocCtx
	bm.mu.Unlock()
	if allocCtx == nil {
		return nil, fmt.Errorf("browser allocator missing; run action navigate again")
	}

	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(log.Errorf))
	bm.attachTabMonitor(tabCtx)

	if err := chromedp.Run(tabCtx, network.Enable(), runtime.Enable()); err != nil {
		tabCancel()
		return nil, fmt.Errorf("recover tab: enable domains: %w", err)
	}

	navURL := strings.TrimSpace(recoverURL)
	if navURL != "" && navURL != "about:blank" {
		if err := isURLSafe(navURL); err != nil {
			navURL = "about:blank"
		}
	}
	if navURL == "" {
		navURL = "about:blank"
	}

	var navErr error
	if navURL != "about:blank" {
		navErr = runChromedpNavigate(tabCtx, reqCtx, navURL)
	} else {
		navErr = chromedp.Run(tabCtx, chromedp.Navigate(navURL))
		if navErr == nil {
			_ = chromedp.Run(tabCtx, chromedp.WaitReady("body", chromedp.ByQuery))
		}
	}
	if navErr != nil {
		tabCancel()
		return nil, fmt.Errorf("recover tab: navigate: %w", navErr)
	}

	tabID := reuseID
	if tabID == "" {
		tabID = uuid.New().String()[:8]
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	if _, exists := bm.tabs[tabID]; exists {
		tabCancel()
		return nil, fmt.Errorf("recover tab: target id collision %q", tabID)
	}
	bm.tabs[tabID] = &tabInfo{
		id: tabID, ctx: tabCtx, cancel: tabCancel,
		url: navURL, title: "",
	}
	bm.captureTabIdentityLocked(tabID, tabCtx)
	bm.attachTargetLifecycleMonitorLocked(tabID, tabCtx)
	if !slices.Contains(bm.tabOrder, tabID) {
		bm.tabOrder = append(bm.tabOrder, tabID)
	}
	bm.activeTab = tabID
	if bm.tabRefs == nil {
		bm.tabRefs = make(map[string]map[string]elementInfo)
	}
	bm.tabRefs[tabID] = make(map[string]elementInfo)

	log.WithFields(log.Fields{"tab": tabID, "url": navURL}).Info("[Browser] CDP tab recovered")
	return tabCtx, nil
}

// effectiveTabID 解析 target_id（空则 active），并校验 tab 存在。
func (bm *browserManager) effectiveTabID(targetID string) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	id := targetID
	if id == "" {
		id = bm.activeTab
	}
	if id == "" {
		return "", fmt.Errorf("no active tab")
	}
	if _, ok := bm.tabs[id]; !ok {
		return "", fmt.Errorf("tab %q not found", id)
	}
	return id, nil
}

// refSelector 校验 ref 是否存在于指定 tab 最近一次 snapshot 中（targetID 空表示 active）。
func (bm *browserManager) refSelector(targetID, ref string) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	id := targetID
	if id == "" {
		id = bm.activeTab
	}
	if id == "" {
		return "", fmt.Errorf("no active tab")
	}
	if _, ok := bm.tabs[id]; !ok {
		return "", fmt.Errorf("tab %q not found", id)
	}
	m := bm.tabRefs[id]
	if m == nil {
		return "", fmt.Errorf("ref %q not found on tab %q, run snapshot on this tab first", ref, id)
	}
	if _, ok := m[ref]; !ok {
		return "", fmt.Errorf("ref %q not found, run snapshot action first", ref)
	}
	return fmt.Sprintf(`[data-agent-ref="%s"]`, ref), nil
}

func (bm *browserManager) resolveSelector(p browserParams) (string, error) {
	if p.Ref != "" {
		return bm.refSelector(p.TargetID, p.Ref)
	}
	if p.Selector != "" {
		return p.Selector, nil
	}
	return "", fmt.Errorf("ref or selector is required")
}

// refMeta 返回最近一次 snapshot 中该 ref 的元数据（无记录则 ok=false）。
func (bm *browserManager) refMeta(targetID, ref string) (elementInfo, bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	id := targetID
	if id == "" {
		id = bm.activeTab
	}
	if bm.tabRefs == nil {
		return elementInfo{}, false
	}
	m := bm.tabRefs[id]
	if m == nil {
		return elementInfo{}, false
	}
	el, ok := m[ref]
	return el, ok
}

func (bm *browserManager) tempFilePath(ext string) string {
	return filepath.Join(bm.tmpDir, fmt.Sprintf("browser_%s%s", uuid.New().String()[:8], ext))
}

// updateTabInfo 用 runCtx 对应 Tab 的当前 URL/标题更新 tabs 映射。tabID 为空时使用 activeTab（仅作兜底）。
func (bm *browserManager) updateTabInfo(runCtx context.Context, tabID string) {
	var currentURL, title string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	_ = chromedp.Run(runCtx, chromedp.Title(&title))

	bm.mu.Lock()
	defer bm.mu.Unlock()
	id := tabID
	if id == "" {
		id = bm.activeTab
	}
	if tab, ok := bm.tabs[id]; ok {
		if currentURL != "" {
			tab.url = currentURL
		}
		if title != "" {
			tab.title = title
		}
	}
}

// resolveBrowserUploadPath 将路径解析为绝对路径，并限制在 workspace.Root() 之下（未初始化 workspace 时仅做 Clean/Abs，供测试等场景）。
func resolveBrowserUploadPath(path string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("invalid upload path: %w", err)
	}
	root := workspace.Root()
	if root == "" {
		return clean, nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("workspace root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)
	rel, err := filepath.Rel(rootAbs, clean)
	if err != nil {
		return "", fmt.Errorf("upload path must be under workspace: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("upload path must be under workspace root")
	}
	return clean, nil
}

func isURLSafe(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("blocked scheme %q: only http/https allowed", scheme)
	}

	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("blocked host: localhost")
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("blocked private/loopback IP: %s", host)
		}
	}

	return nil
}

func fetchPageText(runCtx context.Context, maxLen int) string {
	js := fmt.Sprintf(`(document.body&&document.body.innerText||'').substring(0,%d)`, maxLen)
	var text string
	_ = chromedp.Run(runCtx, chromedp.Evaluate(js, &text))
	return strings.TrimSpace(text)
}

func wrapUntrustedContent(content string) string {
	return "[UNTRUSTED_WEB_CONTENT_START]\n" + content + "\n[UNTRUSTED_WEB_CONTENT_END]"
}

func browserJSON(fields ...any) string {
	m := make(map[string]any)
	for i := 0; i+1 < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok {
			m[key] = fields[i+1]
		}
	}
	data, _ := json.Marshal(m)
	return string(data)
}
