package tools

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsOfficeReplacementPreservesFileWhileReaderHoldsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xlsx")
	if err := writeFileAtomic(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	// Go's Windows os.Open does not grant FILE_SHARE_DELETE.
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := writeFileAtomic(path, []byte("replacement")); err == nil {
		t.Fatal("replaced a file that is open without delete sharing")
	}
	if got, err := io.ReadAll(reader); err != nil || string(got) != "original" {
		t.Fatalf("open reader lost the original: %q, %v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "report.xlsx" {
		t.Fatalf("failed replacement left temporary files: %v, %v", entries, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement content = %q, %v", got, err)
	}
}
