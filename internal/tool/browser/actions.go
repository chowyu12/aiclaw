package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	cdpNetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/tool/result"
)

func (bm *browserManager) actionNavigate(reqCtx context.Context, p browserParams) (string, error) {
	if p.URL == "" {
		return "", fmt.Errorf("url is required for navigate")
	}
	if err := isURLSafe(p.URL); err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
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
	bm.mu.Unlock()

	deadline, _, postWait := navigateMergedDeadline(reqCtx, p.URL)
	extDL := minTime(time.Now().Add(postWait), deadline)
	extCtx, extCancel := context.WithDeadline(tabCtx, extDL)
	defer extCancel()

	bm.updateTabInfo(extCtx, tabID)

	var title string
	_ = chromedp.Run(extCtx, chromedp.Title(&title))

	pageText := fetchPageText(extCtx, 3000)
	meta := browserJSON("ok", true, "url", p.URL, "title", title)
	if pageText == "" {
		return meta, nil
	}
	return meta + "\n\n" + wrapUntrustedContent(pageText), nil
}

func (bm *browserManager) actionScreenshot(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
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
	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, cancel := mergedActionContext(tabCtx, reqCtx)
	defer cancel()
	return bm.takeSnapshot(runCtx, p.TargetID, p.Selector)
}

func (bm *browserManager) actionGetText(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}

	// innerText / 大 DOM 的 evaluate 可能较慢，与 snapshot 等一致使用默认交互窗口（原 10s 易误判为 context canceled）
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
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

// refuseTypeIfRefNotTextField 根据 snapshot 元数据快速拒绝明显不能输入的 ref（按钮、链接等）。
func (bm *browserManager) refuseTypeIfRefNotTextField(targetID, ref string) error {
	meta, ok := bm.refMeta(targetID, ref)
	if !ok {
		return nil
	}
	tag := strings.ToLower(meta.Tag)
	role := strings.ToLower(meta.Role)
	switch tag {
	case "button", "select", "a", "summary":
		return fmt.Errorf("ref %q is <%s>, not a text field; use click or choose input/textarea from snapshot", ref, tag)
	}
	if tag == "input" {
		it := strings.ToLower(meta.Type)
		switch it {
		case "button", "submit", "reset", "checkbox", "radio", "file", "hidden", "image":
			return fmt.Errorf("ref %q is input[type=%s], cannot type text; pick type=text/search or textarea", ref, meta.Type)
		}
	}
	switch role {
	case "button", "link", "tab", "menuitem", "option", "checkbox", "radio", "switch":
		return fmt.Errorf("ref %q has role=%s, not for typing; pick searchbox/textbox or input/textarea", ref, meta.Role)
	}
	return nil
}

func (bm *browserManager) actionClick(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	switch {
	case p.DoubleClick:
		_ = chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))
		visTo, v1 := context.WithTimeout(runCtx, interactionWaitVisibleTimeout)
		if err := chromedp.Run(visTo, chromedp.WaitVisible(sel, chromedp.ByQuery)); err != nil {
			v1()
			return "", fmt.Errorf("double click: ref %q not visible: %w", p.Ref, err)
		}
		v1()
		clickTo, c1 := context.WithTimeout(runCtx, interactionPointerPhaseTimeout)
		if err := chromedp.Run(clickTo, chromedp.DoubleClick(sel, chromedp.ByQuery)); err != nil {
			c1()
			return "", fmt.Errorf("double click: %w", err)
		}
		c1()
	case p.Button == "right":
		js := fmt.Sprintf(`(function(){var el=document.querySelector(%q);if(!el)return false;el.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,button:2}));return true})()`, sel)
		var ok bool
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &ok)); err != nil || !ok {
			return "", fmt.Errorf("right click failed")
		}
	case p.Button == "middle":
		js := fmt.Sprintf(`(function(){var el=document.querySelector(%q);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,button:1}));return true})()`, sel)
		var ok bool
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &ok)); err != nil || !ok {
			return "", fmt.Errorf("middle click failed")
		}
	default:
		_ = chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))
		visTo, v1 := context.WithTimeout(runCtx, interactionWaitVisibleTimeout)
		if err := chromedp.Run(visTo, chromedp.WaitVisible(sel, chromedp.ByQuery)); err != nil {
			v1()
			return "", fmt.Errorf("click: ref %q not visible (stale ref or overlay?): %w", p.Ref, err)
		}
		v1()
		clickTo, c1 := context.WithTimeout(runCtx, interactionPointerPhaseTimeout)
		if err := chromedp.Run(clickTo, chromedp.Click(sel, chromedp.ByQuery)); err != nil {
			c1()
			var st string
			if err2 := chromedp.Run(runCtx, chromedp.Evaluate(jsSyntheticPrimaryClick(sel), &st)); err2 != nil {
				return "", fmt.Errorf("click: %w", err)
			}
			if st != "ok" {
				return "", fmt.Errorf("click: %w (fallback: %s)", err, st)
			}
		}
		c1()
	}

	time.Sleep(300 * time.Millisecond)
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

func (bm *browserManager) actionType(reqCtx context.Context, p browserParams) (string, error) {
	if p.Text == "" {
		return "", fmt.Errorf("text is required for type")
	}
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}
	if err := bm.refuseTypeIfRefNotTextField(p.TargetID, p.Ref); err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}

	maxDur := defaultInteractionDeadline
	if p.Slowly {
		maxDur += time.Duration(len(p.Text)) * 200 * time.Millisecond
	}
	runCtx, runCancel := mergedActionContextMax(tabCtx, reqCtx, maxDur)
	defer runCancel()

	_ = chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))

	waitVis, wvCancel := context.WithTimeout(runCtx, interactionWaitVisibleTimeout)
	if err := chromedp.Run(waitVis, chromedp.WaitVisible(sel, chromedp.ByQuery)); err != nil {
		wvCancel()
		return "", fmt.Errorf("type: ref %q not visible in time (wrong/stale ref or overlay?): %w", p.Ref, err)
	}
	wvCancel()

	tryTypeWithFallback := func(text string) error {
		keysTo, kCancel := context.WithTimeout(runCtx, interactionPointerPhaseTimeout)
		sendErr := chromedp.Run(keysTo, chromedp.SendKeys(sel, text, chromedp.ByQuery))
		kCancel()
		if sendErr == nil {
			return nil
		}
		var status string
		if err2 := chromedp.Run(runCtx, chromedp.Evaluate(jsSetFormControlValue(sel, text), &status)); err2 != nil {
			return sendErr
		}
		switch status {
		case "ok":
			return nil
		case "missing":
			return fmt.Errorf("ref %q not in DOM (re-run snapshot)", p.Ref)
		case "not_editable":
			return fmt.Errorf("ref %q is not INPUT/TEXTAREA/contenteditable: %w", p.Ref, sendErr)
		default:
			return sendErr
		}
	}

	if p.Slowly {
		for _, ch := range p.Text {
			oneKey, oneCancel := context.WithTimeout(runCtx, 8*time.Second)
			chErr := chromedp.Run(oneKey, chromedp.SendKeys(sel, string(ch), chromedp.ByQuery))
			oneCancel()
			if chErr != nil {
				if fbErr := tryTypeWithFallback(p.Text); fbErr != nil {
					return "", fmt.Errorf("type slowly: %w", chErr)
				}
				break
			}
			time.Sleep(80 * time.Millisecond)
		}
	} else {
		if err := tryTypeWithFallback(p.Text); err != nil {
			return "", fmt.Errorf("type: %w", err)
		}
	}

	if p.Submit {
		tabID, tabErr := bm.effectiveTabID(p.TargetID)
		if tabErr != nil {
			return "", tabErr
		}
		subTo, subCancel := context.WithTimeout(runCtx, 8*time.Second)
		if err := chromedp.Run(subTo, chromedp.SendKeys(sel, "\r", chromedp.ByQuery)); err != nil {
			log.WithError(err).Warn("[Browser] submit (Enter) failed")
		}
		subCancel()
		time.Sleep(500 * time.Millisecond)
		bm.updateTabInfo(runCtx, tabID)
	}

	return browserJSON("ok", true), nil
}

func (bm *browserManager) actionHover(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	js := fmt.Sprintf(`(function(){
		var el=document.querySelector(%q);
		if(!el) return false;
		el.dispatchEvent(new MouseEvent('mouseover',{bubbles:true}));
		el.dispatchEvent(new MouseEvent('mouseenter',{bubbles:true}));
		return true;
	})()`, sel)

	var ok bool
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &ok)); err != nil || !ok {
		return "", fmt.Errorf("hover failed on %q", sel)
	}

	return browserJSON("ok", true), nil
}

func (bm *browserManager) actionDrag(reqCtx context.Context, p browserParams) (string, error) {
	if p.StartRef == "" || p.EndRef == "" {
		return "", fmt.Errorf("start_ref and end_ref are required for drag")
	}

	startSel, err := bm.refSelector(p.TargetID, p.StartRef)
	if err != nil {
		return "", err
	}
	endSel, err := bm.refSelector(p.TargetID, p.EndRef)
	if err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	js := fmt.Sprintf(`(function(){
		var s=document.querySelector(%q), e=document.querySelector(%q);
		if(!s||!e) return 'elements not found';
		var dt=new DataTransfer();
		var sr=s.getBoundingClientRect(), er=e.getBoundingClientRect();
		s.dispatchEvent(new DragEvent('dragstart',{bubbles:true,cancelable:true,dataTransfer:dt,clientX:sr.left+sr.width/2,clientY:sr.top+sr.height/2}));
		e.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:dt,clientX:er.left+er.width/2,clientY:er.top+er.height/2}));
		e.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt,clientX:er.left+er.width/2,clientY:er.top+er.height/2}));
		s.dispatchEvent(new DragEvent('dragend',{bubbles:true,cancelable:true,dataTransfer:dt}));
		return 'ok';
	})()`, startSel, endSel)

	var dragResult string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &dragResult)); err != nil {
		return "", fmt.Errorf("drag: %w", err)
	}
	if dragResult != "ok" {
		return "", fmt.Errorf("drag: %s", dragResult)
	}

	return browserJSON("ok", true), nil
}

func (bm *browserManager) actionSelect(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}
	if len(p.Values) == 0 {
		return "", fmt.Errorf("values is required for select")
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	valuesJS, _ := json.Marshal(p.Values)
	js := fmt.Sprintf(`(function(){
		var el=document.querySelector(%q);
		if(!el||el.tagName!=='SELECT') return 'not a select element';
		var vals=%s;
		Array.from(el.options).forEach(function(opt){
			opt.selected=vals.indexOf(opt.value)>=0;
		});
		el.dispatchEvent(new Event('change',{bubbles:true}));
		return 'ok';
	})()`, sel, string(valuesJS))

	var selectResult string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &selectResult)); err != nil {
		return "", fmt.Errorf("select: %w", err)
	}
	if selectResult != "ok" {
		return "", fmt.Errorf("select: %s", selectResult)
	}

	return browserJSON("ok", true), nil
}

func (bm *browserManager) actionFillForm(reqCtx context.Context, p browserParams) (string, error) {
	if len(p.Fields) == 0 {
		return "", fmt.Errorf("fields is required for fill_form")
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}

	extra := time.Duration(len(p.Fields)) * 30 * time.Second
	if extra > 90*time.Second {
		extra = 90 * time.Second
	}
	maxDur := defaultInteractionDeadline + extra
	runCtx, runCancel := mergedActionContextMax(tabCtx, reqCtx, maxDur)
	defer runCancel()

	var filled int
	for _, f := range p.Fields {
		sel, selErr := bm.refSelector(p.TargetID, f.Ref)
		if selErr != nil {
			return "", fmt.Errorf("fill field %s: %w", f.Ref, selErr)
		}

		clearJS := fmt.Sprintf(`(function(){var el=document.querySelector(%q);if(el){el.value='';el.dispatchEvent(new Event('input',{bubbles:true}))}})()`, sel)
		_ = chromedp.Run(runCtx, chromedp.Evaluate(clearJS, nil))

		_ = chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))
		if err := chromedp.Run(runCtx, chromedp.SendKeys(sel, f.Value, chromedp.ByQuery)); err != nil {
			return "", fmt.Errorf("fill field %s: %w", f.Ref, err)
		}
		filled++
	}

	return browserJSON("ok", true, "filled", filled), nil
}

func (bm *browserManager) actionScroll(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

func (bm *browserManager) actionUpload(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}
	if len(p.Paths) == 0 {
		return "", fmt.Errorf("paths is required for upload")
	}

	resolvedPaths := make([]string, 0, len(p.Paths))
	for _, path := range p.Paths {
		resolved, pathErr := resolveBrowserUploadPath(path)
		if pathErr != nil {
			return "", pathErr
		}
		if _, statErr := os.Stat(resolved); statErr != nil {
			return "", fmt.Errorf("file not found: %s", path)
		}
		resolvedPaths = append(resolvedPaths, resolved)
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	if err := chromedp.Run(runCtx, chromedp.SetUploadFiles(sel, resolvedPaths, chromedp.ByQuery)); err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}

	changeJS := fmt.Sprintf(`(function(){var el=document.querySelector(%q);if(el)el.dispatchEvent(new Event('change',{bubbles:true}))})()`, sel)
	_ = chromedp.Run(runCtx, chromedp.Evaluate(changeJS, nil))

	return browserJSON("ok", true, "uploaded", len(resolvedPaths)), nil
}

func (bm *browserManager) actionWait(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}

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
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return "", fmt.Errorf("wait timeout: text %q not found", p.WaitText)
			case <-ticker.C:
				var text string
				_ = chromedp.Run(waitCtx, chromedp.Evaluate(`document.body.innerText`, &text))
				if strings.Contains(text, p.WaitText) {
					return browserJSON("ok", true, "found_text", p.WaitText), nil
				}
			}
		}
	}

	if p.WaitURL != "" {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return "", fmt.Errorf("wait timeout: URL %q not matched", p.WaitURL)
			case <-ticker.C:
				var currentURL string
				_ = chromedp.Run(waitCtx, chromedp.Location(&currentURL))
				if strings.Contains(currentURL, p.WaitURL) {
					return browserJSON("ok", true, "url", currentURL), nil
				}
			}
		}
	}

	if p.WaitFn != "" {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return "", fmt.Errorf("wait timeout: JS predicate %q never returned truthy", p.WaitFn)
			case <-ticker.C:
				var result bool
				js := fmt.Sprintf(`Boolean(%s)`, p.WaitFn)
				if err := chromedp.Run(waitCtx, chromedp.Evaluate(js, &result)); err == nil && result {
					return browserJSON("ok", true, "predicate", p.WaitFn), nil
				}
			}
		}
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
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

	data, _ := json.Marshal(map[string]any{"tabs": tabs})
	return string(data), nil
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

	tab, ok := bm.tabs[targetID]
	if !ok {
		return "", fmt.Errorf("tab %q not found", targetID)
	}

	if len(bm.tabs) <= 1 {
		return "", fmt.Errorf("cannot close the last tab, use close action to stop browser")
	}

	tab.cancel()
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

func (bm *browserManager) removeFromTabOrder(tabID string) {
	for i, id := range bm.tabOrder {
		if id == tabID {
			bm.tabOrder = append(bm.tabOrder[:i], bm.tabOrder[i+1:]...)
			return
		}
	}
}

// --- Cookie management ---

func (bm *browserManager) actionCookies(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
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
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

// --- Press key ---

var keyMap = map[string]string{
	"Enter":      kb.Enter,
	"Tab":        kb.Tab,
	"Escape":     kb.Escape,
	"Backspace":  kb.Backspace,
	"Delete":     kb.Delete,
	"ArrowUp":    kb.ArrowUp,
	"ArrowDown":  kb.ArrowDown,
	"ArrowLeft":  kb.ArrowLeft,
	"ArrowRight": kb.ArrowRight,
	"Home":       kb.Home,
	"End":        kb.End,
	"PageUp":     kb.PageUp,
	"PageDown":   kb.PageDown,
	"Space":      " ",
	"F1":         kb.F1,
	"F2":         kb.F2,
	"F3":         kb.F3,
	"F4":         kb.F4,
	"F5":         kb.F5,
	"F6":         kb.F6,
	"F7":         kb.F7,
	"F8":         kb.F8,
	"F9":         kb.F9,
	"F10":        kb.F10,
	"F11":        kb.F11,
	"F12":        kb.F12,
}

func (bm *browserManager) actionPress(reqCtx context.Context, p browserParams) (string, error) {
	key := p.KeyName
	if key == "" {
		return "", fmt.Errorf("key_name is required for press")
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()

	kbKey, ok := keyMap[key]
	if !ok {
		if len(key) == 1 {
			kbKey = key
		} else {
			return "", fmt.Errorf("unknown key %q, supported: Enter, Tab, Escape, Backspace, Delete, ArrowUp/Down/Left/Right, Home, End, PageUp/Down, Space, or single character", key)
		}
	}

	if err := chromedp.Run(runCtx, chromedp.KeyEvent(kbKey)); err != nil {
		return "", fmt.Errorf("press key: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	return browserJSON("ok", true, "key", key), nil
}

// --- Navigation history ---

func (bm *browserManager) actionBack(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()
	if err := chromedp.Run(runCtx, chromedp.NavigateBack()); err != nil {
		return "", fmt.Errorf("back: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

func (bm *browserManager) actionForward(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
	if err != nil {
		return "", err
	}
	tabID, err := bm.effectiveTabID(p.TargetID)
	if err != nil {
		return "", err
	}
	runCtx, runCancel := mergedActionContext(tabCtx, reqCtx)
	defer runCancel()
	if err := chromedp.Run(runCtx, chromedp.NavigateForward()); err != nil {
		return "", fmt.Errorf("forward: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

func (bm *browserManager) actionReload(reqCtx context.Context, p browserParams) (string, error) {
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

// --- Extract table ---

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
	tabCtx, err := bm.getTabCtx(p.TargetID)
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

// --- Resize viewport ---

func (bm *browserManager) actionResize(reqCtx context.Context, p browserParams) (string, error) {
	if p.Width <= 0 || p.Height <= 0 {
		return "", fmt.Errorf("width and height must be positive integers")
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
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

// --- Highlight element ---

func (bm *browserManager) actionHighlight(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}

	tabCtx, err := bm.getTabCtx(p.TargetID)
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

	var result string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &result)); err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	if result != "ok" {
		return "", fmt.Errorf("highlight: %s", result)
	}
	return browserJSON("ok", true, "highlighted", p.Ref), nil
}

// jsSetFormControlValue 用原生方式设置可编辑控件（应对部分网站上 SendKeys 长时间等焦点）。
func jsSetFormControlValue(sel, text string) string {
	data, _ := json.Marshal(text)
	return fmt.Sprintf(`(function(){
		var el = document.querySelector(%q);
		if (!el) return "missing";
		var t = %s;
		try { el.focus(); } catch (e) {}
		if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") {
			el.value = t;
			el.dispatchEvent(new Event("input", { bubbles: true }));
			el.dispatchEvent(new Event("change", { bubbles: true }));
			return "ok";
		}
		if (el.isContentEditable) {
			el.textContent = t;
			try {
				el.dispatchEvent(new InputEvent("input", { bubbles: true, data: t, inputType: "insertText" }));
			} catch (e) {
				el.dispatchEvent(new Event("input", { bubbles: true }));
			}
			return "ok";
		}
		return "not_editable";
	})()`, sel, string(data))
}

// jsSyntheticPrimaryClick 在元素中心派发 mouse 序列（Click CDP 失败时的回退）。
func jsSyntheticPrimaryClick(sel string) string {
	return fmt.Sprintf(`(function(){
		var el = document.querySelector(%q);
		if (!el) return "missing";
		var r = el.getBoundingClientRect();
		if (r.width === 0 && r.height === 0) return "no_rect";
		var x = r.left + Math.max(1, r.width / 2);
		var y = r.top + Math.max(1, r.height / 2);
		["mousedown","mouseup","click"].forEach(function(type){
			el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window, clientX: x, clientY: y, button: 0 }));
		});
		return "ok";
	})()`, sel)
}
