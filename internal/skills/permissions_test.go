package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestExecutableSkillRequiresExplicitPermission(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"Unsafe","main":"main.py","tools":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('no')"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSkillDir(dir); err == nil {
		t.Fatal("executable skill without process.execute was accepted")
	}
}

func TestPermissionAliasesAreNormalized(t *testing.T) {
	permissions, err := NormalizePermissions([]string{"exec", "network", "filesystem:read", "exec"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{PermissionProcessExecute, PermissionNetworkAccess, PermissionFilesystemRead}
	if len(permissions) != len(want) {
		t.Fatalf("permissions = %#v", permissions)
	}
	for index := range want {
		if permissions[index] != want[index] {
			t.Fatalf("permissions = %#v, want %#v", permissions, want)
		}
	}
}

func TestRunnerRejectsEscapingEntrypoint(t *testing.T) {
	_, err := RunTool(context.Background(), t.TempDir(), "../main.py", "test", `{}`, nil, []string{PermissionProcessExecute}, time.Second)
	if err == nil {
		t.Fatal("escaping entry point was accepted")
	}
}

func TestValidateStoredExecutablePermission(t *testing.T) {
	skill := model.Skill{Name: "Runner", MainFile: "main.py", Permissions: model.JSON(`["process.execute"]`)}
	permissions, err := ValidateExecutable(skill)
	if err != nil || len(permissions) != 1 || permissions[0] != PermissionProcessExecute {
		t.Fatalf("permissions=%#v err=%v", permissions, err)
	}
}
