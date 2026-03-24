package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	log "github.com/sirupsen/logrus"
)

func (bm *browserManager) actionClick(reqCtx context.Context, p browserParams) (string, error) {
	sel, err := bm.resolveSelector(p)
	if err != nil {
		return "", err
	}

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

	switch {
	case p.DoubleClick:
		if err := bm.clickNormal(runCtx, sel, p.Ref, true); err != nil {
			return "", err
		}
	case p.Button == "right":
		if err := syntheticMouseClick(runCtx, sel, 2); err != nil {
			return "", fmt.Errorf("right click failed")
		}
	case p.Button == "middle":
		if err := syntheticMouseClick(runCtx, sel, 1); err != nil {
			return "", fmt.Errorf("middle click failed")
		}
	default:
		if err := bm.clickNormal(runCtx, sel, p.Ref, false); err != nil {
			return "", err
		}
	}

	time.Sleep(300 * time.Millisecond)
	bm.updateTabInfo(runCtx, tabID)

	var currentURL string
	_ = chromedp.Run(runCtx, chromedp.Location(&currentURL))
	return browserJSON("ok", true, "url", currentURL), nil
}

// clickNormal scroll → wait visible → click/doubleClick，失败时尝试 JS 合成点击。
func (bm *browserManager) clickNormal(runCtx context.Context, sel, ref string, double bool) error {
	_ = chromedp.Run(runCtx, chromedp.ScrollIntoView(sel, chromedp.ByQuery))

	visTo, v1 := context.WithTimeout(runCtx, interactionWaitVisibleTimeout)
	if err := chromedp.Run(visTo, chromedp.WaitVisible(sel, chromedp.ByQuery)); err != nil {
		v1()
		action := "click"
		if double {
			action = "double click"
		}
		return fmt.Errorf("%s: ref %q not visible: %w", action, ref, err)
	}
	v1()

	clickTo, c1 := context.WithTimeout(runCtx, interactionPointerPhaseTimeout)
	var clickAction chromedp.Action
	if double {
		clickAction = chromedp.DoubleClick(sel, chromedp.ByQuery)
	} else {
		clickAction = chromedp.Click(sel, chromedp.ByQuery)
	}
	if err := chromedp.Run(clickTo, clickAction); err != nil {
		c1()
		if double {
			return fmt.Errorf("double click: %w", err)
		}
		var st string
		if err2 := chromedp.Run(runCtx, chromedp.Evaluate(jsSyntheticPrimaryClick(sel), &st)); err2 != nil {
			return fmt.Errorf("click: %w", err)
		}
		if st != "ok" {
			return fmt.Errorf("click: %w (fallback: %s)", err, st)
		}
		return nil
	}
	c1()
	return nil
}

// syntheticMouseClick 通过 JS dispatchEvent 模拟中键/右键点击。
func syntheticMouseClick(runCtx context.Context, sel string, button int) error {
	evtName := "click"
	if button == 2 {
		evtName = "contextmenu"
	}
	js := fmt.Sprintf(`(function(){var el=document.querySelector(%q);if(!el)return false;el.dispatchEvent(new MouseEvent(%q,{bubbles:true,cancelable:true,button:%d}));return true})()`,
		sel, evtName, button)
	var ok bool
	if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &ok)); err != nil || !ok {
		return fmt.Errorf("mouse button %d click failed on %q", button, sel)
	}
	return nil
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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

var keyMap = map[string]string{
	"Enter": kb.Enter, "Tab": kb.Tab, "Escape": kb.Escape,
	"Backspace": kb.Backspace, "Delete": kb.Delete,
	"ArrowUp": kb.ArrowUp, "ArrowDown": kb.ArrowDown,
	"ArrowLeft": kb.ArrowLeft, "ArrowRight": kb.ArrowRight,
	"Home": kb.Home, "End": kb.End,
	"PageUp": kb.PageUp, "PageDown": kb.PageDown,
	"Space": " ",
	"F1": kb.F1, "F2": kb.F2, "F3": kb.F3, "F4": kb.F4,
	"F5": kb.F5, "F6": kb.F6, "F7": kb.F7, "F8": kb.F8,
	"F9": kb.F9, "F10": kb.F10, "F11": kb.F11, "F12": kb.F12,
}

func (bm *browserManager) actionPress(reqCtx context.Context, p browserParams) (string, error) {
	key := p.KeyName
	if key == "" {
		return "", fmt.Errorf("key_name is required for press")
	}

	tabCtx, err := bm.getTabCtx(reqCtx, p.TargetID)
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
