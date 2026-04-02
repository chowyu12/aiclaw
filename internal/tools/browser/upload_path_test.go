package browser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

func TestResolveBrowserUploadPath_UnderWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(tmpDir, "browser_upload_test.txt")
	if err := os.WriteFile(full, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveBrowserUploadPath(ws, full)
	if err != nil {
		t.Fatalf("resolveBrowserUploadPath: %v", err)
	}
	if resolved == "" {
		t.Fatal("empty resolved path")
	}
}

func TestResolveBrowserUploadPath_RejectsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	outsideRoot := t.TempDir()
	outFile := filepath.Join(outsideRoot, "secret.txt")
	if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = resolveBrowserUploadPath(ws, outFile)
	if err == nil {
		t.Fatal("expected error for path outside workspace root")
	}
}
