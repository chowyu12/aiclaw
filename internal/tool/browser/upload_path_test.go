package browser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

func TestResolveBrowserUploadPath_UnderWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.Init(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workspace.ResetRootForTesting)

	rel := filepath.Join("tmp", "browser_upload_test.txt")
	full := filepath.Join(dir, rel)
	if err := os.WriteFile(full, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveBrowserUploadPath(full)
	if err != nil {
		t.Fatalf("resolveBrowserUploadPath: %v", err)
	}
	if resolved == "" {
		t.Fatal("empty resolved path")
	}
}

func TestResolveBrowserUploadPath_RejectsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := workspace.Init(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workspace.ResetRootForTesting)

	outsideRoot := t.TempDir()
	outFile := filepath.Join(outsideRoot, "secret.txt")
	if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveBrowserUploadPath(outFile)
	if err == nil {
		t.Fatal("expected error for path outside workspace root")
	}
}
