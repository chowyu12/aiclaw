package parser

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestExtractPPTXUsesSlideOrder(t *testing.T) {
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, item := range []struct {
		name string
		text string
	}{{"ppt/slides/slide10.xml", "tenth"}, {"ppt/slides/slide2.xml", "second"}, {"ppt/slides/slide1.xml", "first"}} {
		file, err := writer.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(`<p><t>` + item.text + `</t></p>`))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	text, err := ExtractText("application/vnd.openxmlformats-officedocument.presentationml.presentation", bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	first, second, tenth := strings.Index(text, "first"), strings.Index(text, "second"), strings.Index(text, "tenth")
	if first < 0 || second < first || tenth < second {
		t.Fatalf("slides are not in numeric order: %q", text)
	}
}
