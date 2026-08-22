package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/chowyu12/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestTranslateSamplingMessageIncludesDocumentAndImage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "pixel.png")
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(imagePath, png, 0o600); err != nil {
		t.Fatal(err)
	}

	translated, err := translateSamplingMessage(SamplingMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: "summarize both",
		Attachments: []*model.File{
			{Filename: "notes.md", ContentType: "text/markdown", FileType: model.FileTypeText, TextContent: "attachment evidence"},
			{Filename: "pixel.png", ContentType: "image/png", FileType: model.FileTypeImage, StoragePath: imagePath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if translated.Content != "" || len(translated.MultiContent) != 2 {
		t.Fatalf("unexpected multimodal message: %+v", translated)
	}
	if !strings.Contains(translated.MultiContent[0].Text, "attachment evidence") || !strings.Contains(translated.MultiContent[0].Text, "summarize both") {
		t.Fatalf("document text missing: %q", translated.MultiContent[0].Text)
	}
	if image := translated.MultiContent[1].ImageURL; image == nil || !strings.HasPrefix(image.URL, "data:image/png;base64,") {
		t.Fatalf("image data URL missing: %+v", translated.MultiContent[1])
	}
	encoded, err := json.Marshal(translated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"image_url"`) {
		t.Fatalf("OpenAI-compatible JSON lost image part: %s", encoded)
	}
}

func TestTranslateSamplingMessageRejectsUnsupportedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector.svg")
	if err := os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := translateSamplingMessage(SamplingMessage{
		Role:        openai.ChatMessageRoleUser,
		Attachments: []*model.File{{Filename: "vector.svg", ContentType: "image/svg+xml", FileType: model.FileTypeImage, StoragePath: path}},
	})
	if err == nil {
		t.Fatal("unsupported image format was accepted")
	}
}
