package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

type elementInfo struct {
	Ref         string `json:"ref"`
	Tag         string `json:"tag"`
	Text        string `json:"text"`
	Type        string `json:"type,omitzero"`
	Name        string `json:"name,omitzero"`
	Href        string `json:"href,omitzero"`
	Role        string `json:"role,omitzero"`
	Placeholder string `json:"placeholder,omitzero"`
	AriaLabel   string `json:"aria_label,omitzero"`
}

type snapshotResult struct {
	URL      string        `json:"url"`
	Title    string        `json:"title"`
	Elements []elementInfo `json:"elements"`
	Text     string        `json:"text"`
}

const snapshotJS = `(function(){
  document.querySelectorAll('[data-agent-ref]').forEach(function(el){
    el.removeAttribute('data-agent-ref');
  });

  var selectors = 'a,button,input,select,textarea,summary,' +
    '[role="button"],[role="link"],[role="tab"],' +
    '[role="checkbox"],[role="radio"],[role="switch"],' +
    '[role="menuitem"],[role="option"],[role="combobox"],' +
    '[role="listbox"],[role="slider"],[role="spinbutton"],' +
    '[role="searchbox"],[role="textbox"],' +
    '[tabindex]:not([tabindex="-1"]),' +
    '[onclick],[contenteditable="true"]';
  var nodes = document.querySelectorAll(selectors);
  var elements = [];
  var idx = 0;

  nodes.forEach(function(el){
    var rect = el.getBoundingClientRect();
    if(rect.width===0 && rect.height===0 &&
       el.tagName!=='INPUT' &&
       el.getAttribute('type')!=='hidden') return;
    if(el.disabled) return;

    idx++;
    var ref = 'e' + idx;
    el.setAttribute('data-agent-ref', ref);

    elements.push({
      ref: ref,
      tag: el.tagName.toLowerCase(),
      text: (el.innerText||el.value||'').trim().substring(0,100),
      type: el.getAttribute('type')||'',
      name: el.getAttribute('name')||'',
      href: el.getAttribute('href')||'',
      role: el.getAttribute('role')||'',
      placeholder: el.getAttribute('placeholder')||'',
      aria_label: el.getAttribute('aria-label')||''
    });
  });

  var pageText = (document.body && document.body.innerText||'').substring(0,8000);
  return JSON.stringify({
    url: location.href,
    title: document.title,
    elements: elements,
    text: pageText
  });
})()`

func (bm *browserManager) takeSnapshot(tabCtx context.Context, targetID, selector string) (string, error) {
	js := snapshotJS
	if selector != "" {
		js = fmt.Sprintf(`(function(){
  var root = document.querySelector(%q);
  if(!root) return JSON.stringify({url:location.href,title:document.title,elements:[],text:''});
  root.querySelectorAll('[data-agent-ref]').forEach(function(el){ el.removeAttribute('data-agent-ref'); });
  var selectors = 'a,button,input,select,textarea,summary,[role="button"],[role="link"],[role="tab"],[role="checkbox"],[role="radio"],[role="switch"],[role="menuitem"],[role="option"],[role="combobox"],[role="listbox"],[role="slider"],[role="spinbutton"],[role="searchbox"],[role="textbox"],[tabindex]:not([tabindex="-1"]),[onclick],[contenteditable="true"]';
  var nodes = root.querySelectorAll(selectors);
  var elements = []; var idx = 0;
  nodes.forEach(function(el){
    var rect = el.getBoundingClientRect();
    if(rect.width===0 && rect.height===0 && el.tagName!=='INPUT' && el.getAttribute('type')!=='hidden') return;
    if(el.disabled) return;
    idx++; var ref = 'e' + idx;
    el.setAttribute('data-agent-ref', ref);
    elements.push({ref:ref,tag:el.tagName.toLowerCase(),text:(el.innerText||el.value||'').trim().substring(0,100),type:el.getAttribute('type')||'',name:el.getAttribute('name')||'',href:el.getAttribute('href')||'',role:el.getAttribute('role')||'',placeholder:el.getAttribute('placeholder')||'',aria_label:el.getAttribute('aria-label')||''});
  });
  return JSON.stringify({url:location.href,title:document.title,elements:elements,text:(root.innerText||'').substring(0,8000)});
})()`, selector)
	}

	var resultJSON string
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(js, &resultJSON)); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}

	var snapResult snapshotResult
	if err := json.Unmarshal([]byte(resultJSON), &snapResult); err != nil {
		return "", fmt.Errorf("parse snapshot: %w", err)
	}

	tabID, err := bm.effectiveTabID(targetID)
	if err != nil {
		return "", err
	}

	bm.mu.Lock()
	if bm.tabRefs == nil {
		bm.tabRefs = make(map[string]map[string]elementInfo)
	}
	refMap := make(map[string]elementInfo, len(snapResult.Elements))
	for _, el := range snapResult.Elements {
		refMap[el.Ref] = el
	}
	bm.tabRefs[tabID] = refMap
	bm.mu.Unlock()

	return wrapUntrustedContent(formatSnapshot(snapResult)), nil
}

func formatSnapshot(r snapshotResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Page: %s - %q\n\n", r.URL, r.Title))

	if len(r.Elements) > 0 {
		sb.WriteString("Interactive Elements:\n")
		for _, el := range r.Elements {
			line := fmt.Sprintf("[%s] <%s", el.Ref, el.Tag)
			if el.Type != "" {
				line += fmt.Sprintf(` type="%s"`, el.Type)
			}
			if el.Name != "" {
				line += fmt.Sprintf(` name="%s"`, el.Name)
			}
			if el.Role != "" {
				line += fmt.Sprintf(` role="%s"`, el.Role)
			}
			if el.Href != "" {
				href := el.Href
				if len(href) > 80 {
					href = href[:80] + "..."
				}
				line += fmt.Sprintf(` href="%s"`, href)
			}
			line += ">"
			if el.Text != "" {
				line += fmt.Sprintf(" %q", el.Text)
			} else if el.Placeholder != "" {
				line += fmt.Sprintf(" placeholder=%q", el.Placeholder)
			} else if el.AriaLabel != "" {
				line += fmt.Sprintf(" aria-label=%q", el.AriaLabel)
			}
			sb.WriteString(line + "\n")
		}
	} else {
		sb.WriteString("No interactive elements found.\n")
	}

	if r.Text != "" {
		sb.WriteString("\n--- Page Text ---\n")
		sb.WriteString(r.Text)
	}

	return sb.String()
}
