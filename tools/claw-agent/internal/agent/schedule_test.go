package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func scheduleSession(t *testing.T, model *fakeModel, enabled bool, title string) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(server.Close)
	session, err := New(context.Background(), "test", protocol.SessionStartParams{
		Model:          protocol.ModelConfig{BaseURL: server.URL, Model: "fake"},
		Workdir:        t.TempDir(),
		ApprovalPolicy: protocol.ApprovalOnWrite,
		EnableSchedule: enabled,
		Title:          title,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	session.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(session.Close)
	return session
}

func TestScheduleToolsOnlyWhenEnabled(t *testing.T) {
	off := scheduleSession(t, &fakeModel{}, false, "")
	if strings.Contains(strings.Join(off.Tools(), ","), "schedule_") {
		t.Errorf("没开时不该有定时任务工具：%v", off.Tools())
	}
	on := scheduleSession(t, &fakeModel{}, true, "")
	names := strings.Join(on.Tools(), ",")
	for _, want := range []string{"schedule_create", "schedule_list", "schedule_delete"} {
		if !strings.Contains(names, want) {
			t.Errorf("缺 %s：%v", want, on.Tools())
		}
	}
}

// 建定时任务要问（它会在用户不在场时自己跑），审批框里写清楚什么时候、做什么；
// 同意之后请求带着当前工作区到宿主。列出来不问。
func TestScheduleCreateAsksAndCarriesWorkspace(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "schedule_create", `{"name":"邮件汇总","prompt":"汇总未读邮件","kind":"weekly","time":"09:00","days":[1,3]}`}),
		sseToolCalls([3]string{"c2", "schedule_list", `{}`}),
		sseText("建好了。"),
	}}
	session := scheduleSession(t, model, true, "")
	emitter := &recordingEmitter{approve: true}
	session.RunTurn(context.Background(), "t1", "每周一三早上汇总邮件", nil, nil, emitter)

	if len(emitter.approvals) != 1 {
		t.Fatalf("只有新建要问：%+v", emitter.approvals)
	}
	detail := emitter.approvals[0].Detail
	for _, want := range []string{"邮件汇总", "每周一、周三 09:00", "汇总未读邮件", session.config.Workdir} {
		if !strings.Contains(detail, want) {
			t.Errorf("审批框里缺 %q：\n%s", want, detail)
		}
	}
	if len(emitter.schedule) != 2 || emitter.schedule[0].Action != "create" || emitter.schedule[1].Action != "list" {
		t.Fatalf("两次都应到宿主：%+v", emitter.schedule)
	}
	task := emitter.schedule[0].Task
	if task == nil || task.Workspace != session.config.Workdir || task.Kind != "weekly" || len(task.Days) != 2 {
		t.Errorf("任务内容不对：%+v", task)
	}
	if !emitter.find(protocol.NotifyItemCompleted, "已处理：create") {
		t.Error("宿主的回应应进工具结果")
	}
}

// 用户拒绝就不到宿主。
func TestScheduleCreateDeniedStaysLocal(t *testing.T) {
	model := &fakeModel{script: []string{
		sseToolCalls([3]string{"c1", "schedule_create", `{"name":"a","prompt":"b","kind":"daily","time":"08:00"}`}),
		sseText("好的，不建了。"),
	}}
	session := scheduleSession(t, model, true, "")
	emitter := &recordingEmitter{approve: false}
	session.RunTurn(context.Background(), "t1", "建一个", nil, nil, emitter)
	if len(emitter.schedule) != 0 {
		t.Errorf("拒绝之后不该建：%+v", emitter.schedule)
	}
}

// 定时任务开的会话带着任务名作标题，不被第一条消息（那段自动发起的说明）盖掉。
func TestStartTitleKept(t *testing.T) {
	session := scheduleSession(t, &fakeModel{script: []string{sseText("好。")}}, false, "⏰ 日报 · 今天 09:00")
	session.RunTurn(context.Background(), "t1", "（这是定时任务「日报」自动发起的）写日报", nil, nil, &recordingEmitter{approve: true})
	if session.Title != "⏰ 日报 · 今天 09:00" {
		t.Errorf("标题被改了：%q", session.Title)
	}
}
