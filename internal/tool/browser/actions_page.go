package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	cdpNetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/chowyu12/aiclaw/internal/tool/result"
)

func (bm *browserManager) actionScreenshot(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, cancel := mergedActionContextMax(tabCtx, reqCtx, 90*time.Second)
	defer cancel()

	var buf []byte

	switch {
	case p.Ref != "":
		sel, selErr := bm.refSelector(p.TargetID, p.Ref)
		if selErr != nil {
			return "", selErr
		}
		if err := chromedp.Run(runCtx, chromedp.Screenshot(sel, &buf, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("screenshot element: %w", err)
		}
	case p.FullPage:
		if err := chromedp.Run(runCtx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			return "", fmt.Errorf("full screenshot: %w", err)
		}
	default:
		if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var captureErr error
			buf, captureErr = page.CaptureScreenshot().Do(ctx)
			return captureErr
		})); err != nil {
			return "", fmt.Errorf("screenshot: %w", err)
		}
	}

	filePath := bm.tempFilePath(".png")
	if err := os.WriteFile(filePath, buf, 0o644); err != nil {
		return "", fmt.Errorf("save screenshot: %w", err)
	}

	return result.NewFileResult(filePath, "image/png", "Browser screenshot"), nil
}

func (bm *browserManager) actionSnapshot(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, cancel := mergedActionContext(tabCtx, reqCtx)
	defer cancel()
	return bm.takeSnapshot(runCtx, p.TargetID, p.Selector)
}

func (bm *browserManager) actionGetText(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, cancel := mergedActionContext(tabCtx, reqCtx)
	defer cancel()

	js := `(document.body&&document.body.innerText||'').substring(0,10000)`
	if p.Ref != "" {
		sel, selErr := bm.refSelector(p.TargetID, p.Ref)
		if selErr != nil {
			return "", selErr
		}
		js = fmt.Sprintf(`(function(){var el=document.querySelector(%q);return el?el.innerText.substring(0,10000):''})()`, sel)
	} else if p.Selector != "" {
		js = fmt.Sprintf(`(function(){var el=document.querySelector(%q);return el?el.innerText.substring(0,10000):''})()`, p.Selector)
	}

	var text string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &text)); err != nil {
		return "", fmt.Errorf("get_text: %w", err)
	}

	return wrapUntrustedContent(text), nil
}

func (bm *browserManager) actionEvaluate(reqCtx context.Context, p browserParams) (string, error) {
	if p.Expression == "" {
		return "", fmt.Errorf("expression is required for evaluate")
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}

	runCtx, cancel := mergedActionContextMax(tabCtx, reqCtx, defaultInteractionDeadline)
	defer cancel()

	var evalResult any
	if err := chromedp.Run(runCtx, chromedp.Evaluate(p.Expression, &evalResult)); err != nil {
		return "", fmt.Errorf("evaluate: %w", err)
	}

	data, _ := json.MarshalIndent(evalResult, "", "  ")
	return wrapUntrustedContent(string(data)), nil
}

func (bm *browserManager) actionPDF(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, cancel := mergedActionContextMax(tabCtx, reqCtx, interactionDeadlineCap)
	defer cancel()

	var buf []byte
	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var printErr error
		buf, _, printErr = page.PrintToPDF().Do(ctx)
		return printErr
	})); err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}

	filePath := bm.tempFilePath(".pdf")
	if err := os.WriteFile(filePath, buf, 0o644); err != nil {
		return "", fmt.Errorf("save pdf: %w", err)
	}

	return result.NewFileResult(filePath, "application/pdf", "Browser page PDF"), nil
}

func (bm *browserManager) actionScroll(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	if p.Ref != "" {
		sel, selErr := bm.refSelector(p.TargetID, p.Ref)
		if selErr != nil {
			return "", selErr
		}
		if err := chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("scroll to element: %w", err)
		}
		return browserJSON("ok", true, "scrolled_to", p.Ref), nil
	}

	if p.Selector != "" {
		if err := chromedp.Run(runCtx, chromedp.ScrollIntoView(p.Selector, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("scroll to selector: %w", err)
		}
		return browserJSON("ok", true, "scrolled_to", p.Selector), nil
	}

	js := fmt.Sprintf(`window.scrollTo(0,%d)`, p.ScrollY)
	if p.ScrollY == 0 {
		js = `window.scrollTo(0,document.body.scrollHeight)`
	}
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, nil)); err != nil {
		return "", fmt.Errorf("scroll: %w", err)
	}

	return browserJSON("ok", true), nil
}

func (bm *browserManager) actionWait(reqCtx context.Context, p browserParams) (string, error) {
	if p.WaitTime > 0 {
		d := time.Duration(p.WaitTime) * time.Millisecond
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-reqCtx.Done():
			return "", fmt.Errorf("wait: %w", reqCtx.Err())
		case <-t.C:
		}
		return browserJSON("ok", true, "waited_ms", p.WaitTime), nil
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()
	waitCtx, cancel := context.WithTimeout(runCtx, 15*time.Second)
	defer cancel()

	if p.WaitSelector != "" {
		if err := chromedp.Run(waitCtx, chromedp.WaitVisible(p.WaitSelector, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("wait: selector %q not visible within timeout: %w", p.WaitSelector, err)
		}
		return browserJSON("ok", true, "found_selector", p.WaitSelector), nil
	}

	if p.WaitText != "" {
		return bm.pollWait(waitCtx, 500*time.Millisecond, "text", p.WaitText, func() (string, bool) {
			var text string
			_ = chromedp.Run(waitCtx, chromedp.Evaluate(`document.body.innerText`, &text))
			if strings.Contains(text, p.WaitText) {
				return browserJSON("ok", true, "found_text", p.WaitText), true
			}
			return "", false
		})
	}

	if p.WaitURL != "" {
		return bm.pollWait(waitCtx, 500*time.Millisecond, "URL", p.WaitURL, func() (string, bool) {
			var currentURL string
			_ = chromedp.Run(waitCtx, chromedp.Location(&currentURL))
			if strings.Contains(currentURL, p.WaitURL) {
				return browserJSON("ok", true, "url", currentURL), true
			}
			return "", false
		})
	}

	if p.WaitFn != "" {
		return bm.pollWait(waitCtx, 300*time.Millisecond, "JS predicate", p.WaitFn, func() (string, bool) {
			var result bool
			js := fmt.Sprintf(`Boolean(%s)`, p.WaitFn)
			if err := chromedp.Run(waitCtx, chromedp.Evaluate(js, &result)); err == nil && result {
				return browserJSON("ok", true, "predicate", p.WaitFn), true
			}
			return "", false
		})
	}

	if p.WaitLoad != "" {
		switch p.WaitLoad {
		case "networkidle":
			return bm.waitNetworkIdle(waitCtx)
		case "domcontentloaded":
			if err := chromedp.Run(waitCtx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
				return "", fmt.Errorf("wait domcontentloaded: %w", err)
			}
			return browserJSON("ok", true, "load_state", "domcontentloaded"), nil
		case "load":
			js := `new Promise(r=>{if(document.readyState==='complete')r(true);else window.addEventListener('load',()=>r(true))})`
			var ok bool
			if err := chromedp.Run(waitCtx, chromedp.Evaluate(js, &ok)); err != nil {
				return "", fmt.Errorf("wait load: %w", err)
			}
			return browserJSON("ok", true, "load_state", "load"), nil
		default:
			return "", fmt.Errorf("unknown wait_load value %q (use networkidle/domcontentloaded/load)", p.WaitLoad)
		}
	}

	return browserJSON("ok", true, "message", "no wait condition specified"), nil
}

// pollWait 对三种轮询式 wait（text/url/fn）提取共用骨架。
func (bm *browserManager) pollWait(waitCtx context.Context, interval time.Duration, kind, value string, check func() (string, bool)) (string, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return "", fmt.Errorf("wait timeout: %s %q not matched", kind, value)
		case <-ticker.C:
			if result, ok := check(); ok {
				return result, nil
			}
		}
	}
}

func (bm *browserManager) waitNetworkIdle(waitCtx context.Context) (string, error) {
	idleThreshold := 2 * time.Second
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			return "", fmt.Errorf("wait timeout: network did not become idle")
		case <-ticker.C:
			if bm.monitor == nil {
				time.Sleep(idleThreshold)
				return browserJSON("ok", true, "load_state", "networkidle"), nil
			}
			pending := bm.monitor.pendingRequests()
			if pending > 0 {
				continue
			}
			last := bm.monitor.lastNetworkActivity()
			if last.IsZero() || time.Since(last) > idleThreshold {
				return browserJSON("ok", true, "load_state", "networkidle"), nil
			}
		}
	}
}

func (bm *browserManager) actionDialog(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	accept := true
	if p.Accept != nil {
		accept = *p.Accept
	}

	action := page.HandleJavaScriptDialog(accept)
	if p.PromptText != "" {
		action = action.WithPromptText(p.PromptText)
	}

	if err := chromedp.Run(runCtx, action); err != nil {
		return "", fmt.Errorf("dialog: %w", err)
	}

	return browserJSON("ok", true, "accepted", accept), nil
}

const extractTableJS = `(function(sel){
  var tbl = sel ? document.querySelector(sel) : document.querySelector('table');
  if(!tbl) return JSON.stringify({error:'no table found'});
  var headers = [];
  var hRow = tbl.querySelector('thead tr') || tbl.querySelector('tr');
  if(hRow){
    hRow.querySelectorAll('th,td').forEach(function(c){ headers.push(c.innerText.trim()); });
  }
  var rows = [];
  var bodyRows = tbl.querySelectorAll('tbody tr');
  if(bodyRows.length===0) bodyRows = tbl.querySelectorAll('tr');
  var startIdx = (tbl.querySelector('thead')) ? 0 : 1;
  for(var i=startIdx;i<bodyRows.length&&i<500;i++){
    var cells = bodyRows[i].querySelectorAll('td,th');
    var row = {};
    cells.forEach(function(c,j){
      var key = (j<headers.length && headers[j]) ? headers[j] : 'col_'+j;
      row[key] = c.innerText.trim().substring(0,200);
    });
    rows.push(row);
  }
  return JSON.stringify({headers:headers,rows:rows,count:rows.length});
})`

func (bm *browserManager) actionExtractTable(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	sel := p.Selector
	if p.Ref != "" {
		resolved, selErr := bm.refSelector(p.TargetID, p.Ref)
		if selErr != nil {
			return "", selErr
		}
		sel = resolved
	}

	js := extractTableJS
	if sel != "" {
		js += fmt.Sprintf(`(%q)`, sel)
	} else {
		js += `(null)`
	}

	var resultJSON string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &resultJSON)); err != nil {
		return "", fmt.Errorf("extract_table: %w", err)
	}

	return wrapUntrustedContent(resultJSON), nil
}

func (bm *browserManager) actionResize(reqCtx context.Context, p browserParams) (string, error) {
	if p.Width <= 0 || p.Height <= 0 {
		return "", fmt.Errorf("width and height must be positive integers")
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetDeviceMetricsOverride(int64(p.Width), int64(p.Height), 1.0, false).Do(ctx)
	})); err != nil {
		return "", fmt.Errorf("resize: %w", err)
	}

	return browserJSON("ok", true, "width", p.Width, "height", p.Height), nil
}

func (bm *browserManager) actionHighlight(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	js := fmt.Sprintf(`(function(){
		document.querySelectorAll('.__agent_highlight').forEach(function(el){el.remove()});
		var target=document.querySelector(%q);
		if(!target) return 'element not found';
		var rect=target.getBoundingClientRect();
		var overlay=document.createElement('div');
		overlay.className='__agent_highlight';
		overlay.style.cssText='position:fixed;z-index:999999;pointer-events:none;border:3px solid #FF4500;background:rgba(255,69,0,0.15);border-radius:4px;'+
			'top:'+rect.top+'px;left:'+rect.left+'px;width:'+rect.width+'px;height:'+rect.height+'px;'+
			'transition:opacity 0.3s;';
		document.body.appendChild(overlay);
		setTimeout(function(){overlay.style.opacity='0';setTimeout(function(){overlay.remove()},300)},3000);
		return 'ok';
	})()`, sel)

	var highlightResult string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &highlightResult)); err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	if highlightResult != "ok" {
		return "", fmt.Errorf("highlight: %s", highlightResult)
	}
	return browserJSON("ok", true, "highlighted", p.Ref), nil
}

// --- Cookie management ---

func (bm *browserManager) actionCookies(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	op := p.Operation
	if op == "" {
		op = "get"
	}

	switch op {
	case "get":
		var cookies []*cdpNetwork.Cookie
		if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var getErr error
			cookies, getErr = cdpNetwork.GetCookies().Do(ctx)
			return getErr
		})); err != nil {
			return "", fmt.Errorf("get cookies: %w", err)
		}
		type cookieView struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		}
		views := make([]cookieView, 0, len(cookies))
		for _, c := range cookies {
			views = append(views, cookieView{
				Name: c.Name, Value: c.Value,
				Domain: c.Domain, Path: c.Path,
			})
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "count": len(views), "cookies": views})
		return string(data), nil

	case "set":
		if p.CookieName == "" || p.CookieValue == "" {
			return "", fmt.Errorf("cookie_name and cookie_value are required for set")
		}
		cookieURL := p.CookieURL
		if cookieURL == "" {
			var u string
			_ = chromedp.Run(runCtx, chromedp.Location(&u))
			cookieURL = u
		}
		if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			expr := cdpNetwork.SetCookie(p.CookieName, p.CookieValue).WithURL(cookieURL)
			if p.CookieDomain != "" {
				expr = expr.WithDomain(p.CookieDomain)
			}
			return expr.Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("set cookie: %w", err)
		}
		return browserJSON("ok", true, "name", p.CookieName), nil

	case "clear":
		if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return cdpNetwork.ClearBrowserCookies().Do(ctx)
		})); err != nil {
			return "", fmt.Errorf("clear cookies: %w", err)
		}
		return browserJSON("ok", true, "message", "cookies cleared"), nil

	default:
		return "", fmt.Errorf("unknown cookie operation: %s (use get/set/clear)", op)
	}
}

// --- Storage management ---

func (bm *browserManager) actionStorage(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	storageType := p.StorageType
	if storageType == "" {
		storageType = "local"
	}
	jsObj := "localStorage"
	if storageType == "session" {
		jsObj = "sessionStorage"
	}

	op := p.Operation
	if op == "" {
		op = "get"
	}

	switch op {
	case "get":
		js := fmt.Sprintf(`(function(){var s=%s;var r={};for(var i=0;i<s.length;i++){var k=s.key(i);r[k]=s.getItem(k)}return JSON.stringify(r)})()`, jsObj)
		if p.Key != "" {
			js = fmt.Sprintf(`%s.getItem(%q)||''`, jsObj, p.Key)
		}
		var result string
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &result)); err != nil {
			return "", fmt.Errorf("storage get: %w", err)
		}
		return wrapUntrustedContent(result), nil

	case "set":
		if p.Key == "" {
			return "", fmt.Errorf("key is required for storage set")
		}
		js := fmt.Sprintf(`%s.setItem(%q,%q)`, jsObj, p.Key, p.Value)
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, nil)); err != nil {
			return "", fmt.Errorf("storage set: %w", err)
		}
		return browserJSON("ok", true, "key", p.Key), nil

	case "clear":
		js := fmt.Sprintf(`%s.clear()`, jsObj)
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, nil)); err != nil {
			return "", fmt.Errorf("storage clear: %w", err)
		}
		return browserJSON("ok", true, "message", storageType+"Storage cleared"), nil

	default:
		return "", fmt.Errorf("unknown storage operation: %s (use get/set/clear)", op)
	}
}
