package browser

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsURLSafe(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid_http", "http://example.com", false},
		{"valid_https", "https://example.com/path?q=1", false},
		{"blocked_localhost", "http://localhost:8080", true},
		{"blocked_127", "http://127.0.0.1/admin", true},
		{"blocked_loopback_v6", "http://[::1]/api", true},
		{"blocked_private_10", "http://10.0.0.1/internal", true},
		{"blocked_private_172", "http://172.16.0.1/db", true},
		{"blocked_private_192", "http://192.168.1.1/router", true},
		{"blocked_file_scheme", "file:///etc/passwd", true},
		{"blocked_ftp_scheme", "ftp://example.com/file", true},
		{"valid_public_ip", "http://8.8.8.8/dns", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := isURLSafe(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("isURLSafe(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestWrapUntrustedContent(t *testing.T) {
	content := "Hello World"
	r := wrapUntrustedContent(content)

	if !strings.HasPrefix(r, "[UNTRUSTED_WEB_CONTENT_START]") {
		t.Error("missing start marker")
	}
	if !strings.HasSuffix(r, "[UNTRUSTED_WEB_CONTENT_END]") {
		t.Error("missing end marker")
	}
	if !strings.Contains(r, content) {
		t.Error("content not included")
	}
}

func TestBrowserJSON(t *testing.T) {
	r := browserJSON("ok", true, "url", "https://example.com", "count", 42)
	var m map[string]any
	if err := json.Unmarshal([]byte(r), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if m["url"] != "https://example.com" {
		t.Errorf("url = %v", m["url"])
	}
	if m["count"] != float64(42) {
		t.Errorf("count = %v", m["count"])
	}
}

func TestBrowserParams_Parse(t *testing.T) {
	input := `{"action":"click","ref":"e5","double_click":true}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Action != "click" {
		t.Errorf("action = %q", p.Action)
	}
	if p.Ref != "e5" {
		t.Errorf("ref = %q", p.Ref)
	}
	if !p.DoubleClick {
		t.Error("double_click should be true")
	}
}

func TestBrowserParams_ParseFillForm(t *testing.T) {
	input := `{"action":"fill_form","fields":[{"ref":"e1","value":"hello","type":"text"},{"ref":"e2","value":"pass","type":"password"}]}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(p.Fields) != 2 {
		t.Fatalf("fields len = %d, want 2", len(p.Fields))
	}
	if p.Fields[0].Ref != "e1" || p.Fields[0].Value != "hello" {
		t.Errorf("field[0] = %+v", p.Fields[0])
	}
}

func TestBrowserParams_ParseDialog(t *testing.T) {
	input := `{"action":"dialog","accept":false,"prompt_text":"test input"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Accept == nil || *p.Accept != false {
		t.Error("accept should be false")
	}
	if p.PromptText != "test input" {
		t.Errorf("prompt_text = %q", p.PromptText)
	}
}

func TestHandler_MissingAction(t *testing.T) {
	_, err := Handler(t.Context(), `{}`)
	if err == nil || !strings.Contains(err.Error(), "action is required") {
		t.Errorf("expected 'action is required', got %v", err)
	}
}

func TestHandler_UnknownAction(t *testing.T) {
	_, err := Handler(t.Context(), `{"action":"fly"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action', got %v", err)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	_, err := Handler(t.Context(), `not json`)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid arguments error, got %v", err)
	}
}

func TestBrowserManager_CloseNotRunning(t *testing.T) {
	bm := &browserManager{
		tabs:    make(map[string]*tabInfo),
		tabRefs: make(map[string]map[string]elementInfo),
	}
	r, err := bm.closeBrowser()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(r, "not running") {
		t.Errorf("expected 'not running' in result, got %q", r)
	}
}

func TestBrowserManager_RefSelector_NotFound(t *testing.T) {
	bm := &browserManager{
		tabs: map[string]*tabInfo{
			"t1": {id: "t1"},
		},
		activeTab: "t1",
		tabRefs:   make(map[string]map[string]elementInfo),
	}
	_, err := bm.refSelector("", "e99")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got %v", err)
	}
}

func TestBrowserManager_RefSelector_Found(t *testing.T) {
	bm := &browserManager{
		tabs: map[string]*tabInfo{
			"t1": {id: "t1"},
		},
		activeTab: "t1",
		tabRefs: map[string]map[string]elementInfo{
			"t1": {"e1": {Ref: "e1", Tag: "button", Text: "Submit"}},
		},
	}
	sel, err := bm.refSelector("", "e1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel != `[data-agent-ref="e1"]` {
		t.Errorf("selector = %q", sel)
	}
}

func TestRefuseTypeIfRefNotTextField(t *testing.T) {
	bm := &browserManager{
		tabs: map[string]*tabInfo{
			"t1": {id: "t1"},
		},
		activeTab: "t1",
		tabRefs: map[string]map[string]elementInfo{
			"t1": {
				"eBtn": {Ref: "eBtn", Tag: "button", Text: "Search"},
				"eIn":  {Ref: "eIn", Tag: "input", Type: "text"},
			},
		},
	}
	if err := bm.refuseTypeIfRefNotTextField("", "eBtn"); err == nil {
		t.Fatal("expected error for button ref")
	}
	if err := bm.refuseTypeIfRefNotTextField("", "eIn"); err != nil {
		t.Fatalf("text input should be allowed: %v", err)
	}
}

func TestBrowserManager_ResolveSelector(t *testing.T) {
	bm := &browserManager{
		tabs: map[string]*tabInfo{
			"t1": {id: "t1"},
		},
		activeTab: "t1",
		tabRefs: map[string]map[string]elementInfo{
			"t1": {"e3": {Ref: "e3", Tag: "input"}},
		},
	}

	t.Run("by_ref", func(t *testing.T) {
		sel, err := bm.resolveSelector(browserParams{Ref: "e3"})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if sel != `[data-agent-ref="e3"]` {
			t.Errorf("selector = %q", sel)
		}
	})

	t.Run("by_selector", func(t *testing.T) {
		sel, err := bm.resolveSelector(browserParams{Selector: "#myInput"})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if sel != "#myInput" {
			t.Errorf("selector = %q", sel)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := bm.resolveSelector(browserParams{})
		if err == nil {
			t.Error("expected error for missing ref and selector")
		}
	})
}

func TestEvictOldestTab_EvictsNonActivePreservesTabOrder(t *testing.T) {
	noopCancel := func() {}
	bm := &browserManager{
		tabs: map[string]*tabInfo{
			"oldid": {id: "oldid", cancel: noopCancel},
			"newid": {id: "newid", cancel: noopCancel},
		},
		tabOrder:  []string{"oldid", "newid"},
		activeTab: "oldid",
		maxTabs:   1,
		tabRefs: map[string]map[string]elementInfo{
			"oldid": {"e1": {Ref: "e1"}},
			"newid": {"e2": {Ref: "e2"}},
		},
	}
	bm.evictOldestTab()
	if len(bm.tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(bm.tabs))
	}
	if _, ok := bm.tabs["oldid"]; !ok {
		t.Fatal("active tab should remain")
	}
	if _, ok := bm.tabs["newid"]; ok {
		t.Fatal("non-active tab should be evicted")
	}
	if _, ok := bm.tabRefs["newid"]; ok {
		t.Fatal("evicted tab refs should be removed")
	}
	for _, id := range bm.tabOrder {
		if _, ok := bm.tabs[id]; !ok {
			t.Errorf("tabOrder %q not in tabs", id)
		}
	}
}

func TestEventMonitor_LastNetworkActivityWallClock(t *testing.T) {
	m := newEventMonitor()
	m.addRequest("r1", "GET", "https://example.com/a")
	before := time.Now()
	m.addResponse("r1", 200, "text/html")
	after := time.Now()
	ts := m.lastNetworkActivity()
	if ts.Before(before) || ts.After(after.Add(2*time.Second)) {
		t.Fatalf("lastNetworkActivity = %v, want in [%v, %v+2s]", ts, before, after)
	}
}

func TestFormatSnapshot(t *testing.T) {
	r := snapshotResult{
		URL:   "https://example.com",
		Title: "Test Page",
		Elements: []elementInfo{
			{Ref: "e1", Tag: "a", Href: "/about", Text: "About Us"},
			{Ref: "e2", Tag: "button", Text: "Submit"},
			{Ref: "e3", Tag: "input", Type: "text", Placeholder: "Search..."},
		},
		Text: "Welcome to test page",
	}

	output := formatSnapshot(r)

	if !strings.Contains(output, "https://example.com") {
		t.Error("missing URL")
	}
	if !strings.Contains(output, "Test Page") {
		t.Error("missing title")
	}
	if !strings.Contains(output, "[e1]") || !strings.Contains(output, "About Us") {
		t.Error("missing element e1")
	}
	if !strings.Contains(output, "[e2]") || !strings.Contains(output, "<button") {
		t.Error("missing element e2")
	}
	if !strings.Contains(output, "[e3]") || !strings.Contains(output, `type="text"`) {
		t.Error("missing element e3")
	}
	if !strings.Contains(output, `placeholder="Search..."`) {
		t.Error("missing placeholder")
	}
	if !strings.Contains(output, "Page Text") {
		t.Error("missing page text section")
	}
}

func TestFormatSnapshot_Empty(t *testing.T) {
	r := snapshotResult{
		URL:   "about:blank",
		Title: "",
	}

	output := formatSnapshot(r)
	if !strings.Contains(output, "No interactive elements found") {
		t.Error("expected 'No interactive elements' message")
	}
}

func TestFormatSnapshot_LongHref(t *testing.T) {
	longHref := strings.Repeat("a", 200)
	r := snapshotResult{
		URL:   "https://example.com",
		Title: "Test",
		Elements: []elementInfo{
			{Ref: "e1", Tag: "a", Href: longHref},
		},
	}

	output := formatSnapshot(r)
	if strings.Contains(output, longHref) {
		t.Error("long href should be truncated")
	}
	if !strings.Contains(output, "...") {
		t.Error("truncated href should end with ...")
	}
}

func TestFormatSnapshot_NameField(t *testing.T) {
	r := snapshotResult{
		URL:   "https://example.com",
		Title: "Form",
		Elements: []elementInfo{
			{Ref: "e1", Tag: "input", Type: "text", Name: "username", Placeholder: "Enter name"},
		},
	}
	output := formatSnapshot(r)
	if !strings.Contains(output, `name="username"`) {
		t.Error("missing name attribute in output")
	}
}

// --- New action parameter parsing tests ---

func TestBrowserParams_ParseConsole(t *testing.T) {
	input := `{"action":"console","level":"error","clear":true}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Action != "console" {
		t.Errorf("action = %q", p.Action)
	}
	if p.Level != "error" {
		t.Errorf("level = %q", p.Level)
	}
	if !p.Clear {
		t.Error("clear should be true")
	}
}

func TestBrowserParams_ParseNetwork(t *testing.T) {
	input := `{"action":"network","filter":"api","clear":false}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Filter != "api" {
		t.Errorf("filter = %q", p.Filter)
	}
}

func TestBrowserParams_ParseCookies(t *testing.T) {
	input := `{"action":"cookies","operation":"set","cookie_name":"session","cookie_value":"abc123","cookie_domain":".example.com"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Operation != "set" {
		t.Errorf("operation = %q", p.Operation)
	}
	if p.CookieName != "session" {
		t.Errorf("cookie_name = %q", p.CookieName)
	}
	if p.CookieValue != "abc123" {
		t.Errorf("cookie_value = %q", p.CookieValue)
	}
	if p.CookieDomain != ".example.com" {
		t.Errorf("cookie_domain = %q", p.CookieDomain)
	}
}

func TestBrowserParams_ParseStorage(t *testing.T) {
	input := `{"action":"storage","operation":"set","storage_type":"session","key":"token","value":"xyz"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.StorageType != "session" {
		t.Errorf("storage_type = %q", p.StorageType)
	}
	if p.Key != "token" {
		t.Errorf("key = %q", p.Key)
	}
	if p.Value != "xyz" {
		t.Errorf("value = %q", p.Value)
	}
}

func TestBrowserParams_ParsePress(t *testing.T) {
	input := `{"action":"press","key_name":"Enter"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.KeyName != "Enter" {
		t.Errorf("key_name = %q", p.KeyName)
	}
}

func TestBrowserParams_ParseResize(t *testing.T) {
	input := `{"action":"resize","width":1920,"height":1080}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Width != 1920 {
		t.Errorf("width = %d", p.Width)
	}
	if p.Height != 1080 {
		t.Errorf("height = %d", p.Height)
	}
}

func TestBrowserParams_ParseSetDevice(t *testing.T) {
	input := `{"action":"set_device","device":"iPhone 14"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Device != "iPhone 14" {
		t.Errorf("device = %q", p.Device)
	}
}

func TestBrowserParams_ParseSetMedia(t *testing.T) {
	input := `{"action":"set_media","color_scheme":"dark"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.ColorScheme != "dark" {
		t.Errorf("color_scheme = %q", p.ColorScheme)
	}
}

func TestBrowserParams_ParseWaitFn(t *testing.T) {
	input := `{"action":"wait","wait_fn":"window.ready===true"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.WaitFn != "window.ready===true" {
		t.Errorf("wait_fn = %q", p.WaitFn)
	}
}

func TestBrowserParams_ParseWaitLoad(t *testing.T) {
	input := `{"action":"wait","wait_load":"networkidle"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.WaitLoad != "networkidle" {
		t.Errorf("wait_load = %q", p.WaitLoad)
	}
}

func TestBrowserParams_ParseExtractTable(t *testing.T) {
	input := `{"action":"extract_table","selector":"table.results"}`
	var p browserParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Selector != "table.results" {
		t.Errorf("selector = %q", p.Selector)
	}
}

// --- Event monitor unit tests ---

func TestEventMonitor_Console(t *testing.T) {
	m := newEventMonitor()

	m.addConsole("error", "something went wrong")
	m.addConsole("info", "loaded OK")
	m.addConsole("error", "another error")

	all := m.getConsole("", false)
	if len(all) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(all))
	}

	errors := m.getConsole("error", false)
	if len(errors) != 2 {
		t.Fatalf("expected 2 error entries, got %d", len(errors))
	}

	infos := m.getConsole("info", false)
	if len(infos) != 1 {
		t.Fatalf("expected 1 info entry, got %d", len(infos))
	}

	cleared := m.getConsole("", true)
	if len(cleared) != 3 {
		t.Fatalf("expected 3 before clear, got %d", len(cleared))
	}
	afterClear := m.getConsole("", false)
	if len(afterClear) != 0 {
		t.Fatalf("expected 0 after clear, got %d", len(afterClear))
	}
}

func TestEventMonitor_ConsoleRingBuffer(t *testing.T) {
	m := newEventMonitor()
	m.consoleMaxSize = 5

	for i := range 10 {
		m.addConsole("log", strings.Repeat("x", i+1))
	}

	all := m.getConsole("", false)
	if len(all) != 5 {
		t.Fatalf("expected 5 entries (ring buffer), got %d", len(all))
	}
	if len(all[0].Text) != 6 {
		t.Errorf("expected oldest entry text len 6, got %d", len(all[0].Text))
	}
}

func TestEventMonitor_Network(t *testing.T) {
	m := newEventMonitor()

	m.addRequest("r1", "GET", "https://example.com/api/data")
	m.addRequest("r2", "POST", "https://example.com/api/submit")
	m.addResponse("r1", 200, "application/json")
	m.addResponse("r2", 404, "text/html")

	all := m.getNetwork("", false)
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}

	filtered := m.getNetwork("submit", false)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered entry, got %d", len(filtered))
	}
	if filtered[0].Status != 404 {
		t.Errorf("status = %d, want 404", filtered[0].Status)
	}
}

func TestEventMonitor_NetworkRingBuffer(t *testing.T) {
	m := newEventMonitor()
	m.networkMaxSize = 3

	for i := range 5 {
		id := strings.Repeat("r", i+1)
		m.addRequest(id, "GET", "https://example.com/"+id)
		m.addResponse(id, 200, "text/plain")
	}

	all := m.getNetwork("", false)
	if len(all) != 3 {
		t.Fatalf("expected 3 entries (ring buffer), got %d", len(all))
	}
}

func TestEventMonitor_PendingRequests(t *testing.T) {
	m := newEventMonitor()
	m.addRequest("r1", "GET", "https://example.com/a")
	m.addRequest("r2", "GET", "https://example.com/b")

	if m.pendingRequests() != 2 {
		t.Errorf("pending = %d, want 2", m.pendingRequests())
	}

	m.addResponse("r1", 200, "text/html")
	if m.pendingRequests() != 1 {
		t.Errorf("pending = %d, want 1", m.pendingRequests())
	}
}

// --- Console/Network action tests (without browser) ---

func TestActionConsole_NilMonitor(t *testing.T) {
	bm := &browserManager{
		tabs:    make(map[string]*tabInfo),
		tabRefs: make(map[string]map[string]elementInfo),
	}
	result, err := bm.actionConsole(browserParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "monitor not initialized") {
		t.Errorf("expected 'monitor not initialized', got %q", result)
	}
}

func TestActionNetwork_NilMonitor(t *testing.T) {
	bm := &browserManager{
		tabs:    make(map[string]*tabInfo),
		tabRefs: make(map[string]map[string]elementInfo),
	}
	result, err := bm.actionNetwork(browserParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "monitor not initialized") {
		t.Errorf("expected 'monitor not initialized', got %q", result)
	}
}

// --- Device profile tests ---

func TestDeviceProfiles(t *testing.T) {
	expected := []string{"iPhone 14", "iPhone SE", "iPad", "Pixel 7", "Galaxy S23", "Desktop", "Desktop HD", "Laptop"}
	for _, name := range expected {
		if _, ok := deviceProfiles[name]; !ok {
			t.Errorf("device profile %q not found", name)
		}
	}
}

func TestDeviceProfile_Values(t *testing.T) {
	p := deviceProfiles["iPhone 14"]
	if p.Width != 390 || p.Height != 844 {
		t.Errorf("iPhone 14 dimensions = %dx%d, want 390x844", p.Width, p.Height)
	}
	if !p.Mobile {
		t.Error("iPhone 14 should be mobile")
	}
	if p.UserAgent == "" {
		t.Error("iPhone 14 should have user agent")
	}

	d := deviceProfiles["Desktop"]
	if d.Mobile {
		t.Error("Desktop should not be mobile")
	}
}

// --- Key map tests ---

func TestKeyMap(t *testing.T) {
	expected := []string{"Enter", "Tab", "Escape", "Backspace", "Delete", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown", "Space", "F1", "F12"}
	for _, k := range expected {
		if _, ok := keyMap[k]; !ok {
			t.Errorf("key %q not in keyMap", k)
		}
	}
}

// --- Truncate tests ---

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	r := truncate("hello world", 5)
	if r != "hello..." {
		t.Errorf("truncate = %q, want 'hello...'", r)
	}
}
