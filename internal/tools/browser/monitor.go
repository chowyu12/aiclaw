package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type consoleEntry struct {
	Level     string `json:"level"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type networkEntry struct {
	Method   string `json:"method"`
	URL      string `json:"url"`
	Status   int    `json:"status,omitzero"`
	MimeType string `json:"mime_type,omitzero"`
	Time     string `json:"time"`
	at       time.Time `json:"-"` // 响应该条目的 wall-clock，用于 networkidle
}

type eventMonitor struct {
	mu             sync.Mutex
	consoleBuf     []consoleEntry
	consoleMaxSize int
	networkReqs    map[string]*networkEntry
	networkBuf     []networkEntry
	networkMaxSize int
}

func newEventMonitor() *eventMonitor {
	return &eventMonitor{
		consoleMaxSize: 200,
		networkReqs:    make(map[string]*networkEntry),
		networkMaxSize: 100,
	}
}

func (m *eventMonitor) addConsole(level, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry := consoleEntry{
		Level:     level,
		Text:      truncate(text, 500),
		Timestamp: time.Now().Format("15:04:05.000"),
	}
	m.consoleBuf = append(m.consoleBuf, entry)
	if len(m.consoleBuf) > m.consoleMaxSize {
		m.consoleBuf = m.consoleBuf[len(m.consoleBuf)-m.consoleMaxSize:]
	}
}

func (m *eventMonitor) addRequest(reqID, method, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.networkReqs[reqID] = &networkEntry{
		Method: method,
		URL:    truncate(url, 200),
		Time:   time.Now().Format("15:04:05.000"),
	}
}

func (m *eventMonitor) addResponse(reqID string, status int, mimeType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req, ok := m.networkReqs[reqID]; ok {
		req.Status = status
		req.MimeType = mimeType
		req.at = time.Now()
		m.networkBuf = append(m.networkBuf, *req)
		if len(m.networkBuf) > m.networkMaxSize {
			m.networkBuf = m.networkBuf[len(m.networkBuf)-m.networkMaxSize:]
		}
		delete(m.networkReqs, reqID)
	}
}

func (m *eventMonitor) lastNetworkActivity() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.networkBuf) == 0 {
		return time.Time{}
	}
	last := m.networkBuf[len(m.networkBuf)-1]
	if !last.at.IsZero() {
		return last.at
	}
	t, _ := time.Parse("15:04:05.000", last.Time)
	return t
}

func (m *eventMonitor) pendingRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.networkReqs)
}

func (m *eventMonitor) getConsole(level string, clear bool) []consoleEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []consoleEntry
	for _, e := range m.consoleBuf {
		if level == "" || e.Level == level {
			result = append(result, e)
		}
	}
	if clear {
		m.consoleBuf = m.consoleBuf[:0]
	}
	return result
}

func (m *eventMonitor) getNetwork(filter string, clear bool) []networkEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []networkEntry
	for _, e := range m.networkBuf {
		if filter == "" || strings.Contains(e.URL, filter) {
			result = append(result, e)
		}
	}
	if clear {
		m.networkBuf = m.networkBuf[:0]
		m.networkReqs = make(map[string]*networkEntry)
	}
	return result
}

func (bm *browserManager) setupMonitor(tabCtx context.Context) {
	bm.monitor = newEventMonitor()
	bm.attachTabMonitor(tabCtx)
}

// attachTabMonitor 将 console/network 监听挂到指定 tab ctx；新开 tab 须再调用一次。
func (bm *browserManager) attachTabMonitor(tabCtx context.Context) {
	if bm.monitor == nil {
		return
	}
	chromedp.ListenTarget(tabCtx, bm.handleMonitorEvent)
}

func (bm *browserManager) handleMonitorEvent(ev any) {
	if bm.monitor == nil {
		return
	}
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		var parts []string
		for _, arg := range e.Args {
			if arg.Value != nil {
				var v any
				if json.Unmarshal(arg.Value, &v) == nil {
					parts = append(parts, fmt.Sprint(v))
				}
			} else if arg.Description != "" {
				parts = append(parts, arg.Description)
			} else if arg.UnserializableValue != "" {
				parts = append(parts, string(arg.UnserializableValue))
			}
		}
		bm.monitor.addConsole(e.Type.String(), strings.Join(parts, " "))
	case *network.EventRequestWillBeSent:
		bm.monitor.addRequest(string(e.RequestID), e.Request.Method, e.Request.URL)
	case *network.EventResponseReceived:
		bm.monitor.addResponse(string(e.RequestID), int(e.Response.Status), e.Response.MimeType)
	}
}

func (bm *browserManager) actionConsole(p browserParams) (string, error) {
	if bm.monitor == nil {
		return browserJSON("ok", true, "entries", []consoleEntry{}, "message", "monitor not initialized"), nil
	}
	entries := bm.monitor.getConsole(p.Level, p.Clear)
	if entries == nil {
		entries = []consoleEntry{}
	}
	data, _ := json.Marshal(map[string]any{
		"ok":      true,
		"count":   len(entries),
		"entries": entries,
	})
	return string(data), nil
}

func (bm *browserManager) actionNetwork(p browserParams) (string, error) {
	if bm.monitor == nil {
		return browserJSON("ok", true, "entries", []networkEntry{}, "message", "monitor not initialized"), nil
	}
	entries := bm.monitor.getNetwork(p.Filter, p.Clear)
	if entries == nil {
		entries = []networkEntry{}
	}
	data, _ := json.Marshal(map[string]any{
		"ok":      true,
		"count":   len(entries),
		"entries": entries,
	})
	return string(data), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
