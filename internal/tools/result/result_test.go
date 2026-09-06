package result

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFileResultsFindsStructuredAndEmbeddedPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "report one.xlsx")
	second := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(first, []byte("sheet"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}

	items := ParseFileResults("生成完成：`" + first + "`\n预览图：" + second)
	if len(items) != 2 {
		t.Fatalf("file results = %+v", items)
	}
	if items[0].Path != first || items[0].MimeType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("first file = %+v", items[0])
	}
	if items[1].Path != second || items[1].MimeType != "image/png" {
		t.Fatalf("second file = %+v", items[1])
	}
}

func TestParseFileResultsRetainsMissingStructuredFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "removed.pdf")
	items := ParseFileResults(NewFileResult(path, "application/pdf", "Generated report"))
	if len(items) != 1 || items[0].Path != path || items[0].Description != "Generated report" {
		t.Fatalf("structured file result = %+v", items)
	}
}
