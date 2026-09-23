package server

import (
	"reflect"
	"testing"
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
