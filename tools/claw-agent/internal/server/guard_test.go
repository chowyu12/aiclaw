package server

import (
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestAppDBGuardsCoverSidecars(t *testing.T) {
	if got := appDBGuards("  "); got != nil {
		t.Errorf("没开应用库时不该有名单：%v", got)
	}
	want := []string{"/h/.aiclaw/aiclaw.db", "/h/.aiclaw/aiclaw.db-wal", "/h/.aiclaw/aiclaw.db-shm", "/h/.aiclaw/aiclaw.db-journal", "/h/.aiclaw/secret.key"}
	if got := appDBGuards("/h/.aiclaw/aiclaw.db"); !reflect.DeepEqual(got, want) {
		t.Errorf("名单不对：%v", got)
	}
}

func TestChannelSessionsKeepTheUnattendedPolicyOnResume(t *testing.T) {
	refresh := &protocol.SessionRefresh{ApprovalPolicy: protocol.ApprovalBypass, CodeMode: true}
	pinChannelPolicy("c_123", refresh)
	if refresh.ApprovalPolicy != protocol.ApprovalNever {
		t.Errorf("通道会话恢复后应仍是无人值守，得到 %q", refresh.ApprovalPolicy)
	}
	if !refresh.CodeMode {
		t.Error("其余配置项不该被动")
	}
	desktop := &protocol.SessionRefresh{ApprovalPolicy: protocol.ApprovalBypass}
	pinChannelPolicy("s_123", desktop)
	if desktop.ApprovalPolicy != protocol.ApprovalBypass {
		t.Error("桌面会话的档位应照宿主给的")
	}
	pinChannelPolicy("c_123", nil) // 不带 refresh 也不该炸
}
