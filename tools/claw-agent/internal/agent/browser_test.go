package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func browserSession(t *testing.T, model *fakeModel, policy protocol.ApprovalPolicy, enabled bool) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:        t.TempDir(),
		ApprovalPolicy: policy,
		EnableBrowser:  enabled,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)
	return session
}

func TestBrowserToolsOnlyWhenEnabled(t *testing.T) {
	off := browserSession(t, &fakeModel{}, protocol.ApprovalBypass, false)
	if strings.Contains(strings.Join(off.Tools(), ","), "browser_") {
		t.Errorf("没开浏览器时不该有浏览器工具：%v", off.Tools())
	}
	on := browserSession(t, &fakeModel{}, protocol.ApprovalBypass, true)
	names := strings.Join(on.Tools(), ",")
	for _, want := range []string{"browser_navigate", "browser_snapshot", "browser_click", "browser_type", "browser_extract", "browser_screenshot"} {
		if !strings.Contains(names, want) {
			t.Errorf("缺 %s：%v", want, on.Tools())
		}
	}
}

// 打开网址要问（出网，目标由模型决定）；页面内的点击不问——否则用户会被训练成闭眼点允许。
func TestNavigateAsksButClickDoesNot(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "browser_navigate", `{"url":"https://example.com"}`}),
		sseToolCalls([3]string{"c2", "browser_click", `{"index":3}`}),
		sseText("好。"),
	}}
	session := browserSession(t, model, protocol.ApprovalOnWrite, true)
	emitter := &recordingEmitter{approve: true, browserOK: true, browserResult: protocol.BrowserResult{Text: "页面：Example"}}
	session.RunTurn(context.Background(), "t1", "打开", nil, nil, emitter)

	if len(emitter.approvals) != 1 || !strings.Contains(emitter.approvals[0].Detail, "https://example.com") {
		t.Errorf("应只为打开网址问一次，且带上网址：%+v", emitter.approvals)
	}
	if len(emitter.browser) != 2 || emitter.browser[0].Action != protocol.BrowserNavigate || emitter.browser[1].Index != 3 {
		t.Errorf("两步都应到宿主：%+v", emitter.browser)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "页面：Example") {
		t.Error("宿主返回的文本应进工具结果")
	}
}

// 严格档位下每一步都问，包括点击。
func TestAlwaysPolicyAsksForEveryBrowserStep(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "browser_click", `{"index":1}`}),
		sseText("好。"),
	}}
	session := browserSession(t, model, protocol.ApprovalAlways, true)
	emitter := &recordingEmitter{approve: false, browserOK: true}
	session.RunTurn(context.Background(), "t1", "点", nil, nil, emitter)
	if len(emitter.approvals) != 1 {
		t.Errorf("严格档位下点击也要问：%+v", emitter.approvals)
	}
	if len(emitter.browser) != 0 {
		t.Error("被拒绝后不该到宿主")
	}
}

// 截图挂成附件让模型看见，与 computer_screenshot 同一条路。
func TestBrowserScreenshotAttachesImage(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "browser_screenshot", `{}`}),
		sseText("看到了。"),
	}}
	session := browserSession(t, model, protocol.ApprovalBypass, true)
	emitter := &recordingEmitter{approve: true, browserOK: true, browserResult: protocol.BrowserResult{
		Text: "已截图", ImageBase64: "iVBORw0KGgo=", Width: 1200, Height: 800,
	}}
	session.RunTurn(context.Background(), "t1", "截图", nil, nil, emitter)
	if !emitter.find(protocol.NotifyItemCompleted, "已截图") {
		t.Error("截图结果文本应到模型")
	}
	if len(model.requests) < 2 {
		t.Fatal("应当有第二次模型调用")
	}
	encoded, _ := json.Marshal(model.requests[1])
	if !strings.Contains(string(encoded), "image_url") {
		t.Error("截图应作为图片进下一次请求")
	}
}
