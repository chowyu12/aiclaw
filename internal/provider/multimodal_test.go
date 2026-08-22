package provider

import (
	"testing"

	openai "github.com/chowyu12/go-openai"
)

func TestClaudeAndGeminiPreserveImageParts(t *testing.T) {
	message := openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		MultiContent: []openai.ChatMessagePart{
			{Type: openai.ChatMessagePartTypeText, Text: "describe"},
			{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,iVBORw0KGgo="}},
		},
	}
	_, claudeMessages := convertToClaudeMessages([]openai.ChatCompletionMessage{message})
	if len(claudeMessages) != 1 {
		t.Fatalf("Claude messages = %d", len(claudeMessages))
	}
	claudeBlocks, ok := claudeMessages[0].Content.([]claudeContentBlock)
	if !ok || len(claudeBlocks) != 2 || claudeBlocks[1].Source == nil || claudeBlocks[1].Source.MediaType != "image/png" {
		t.Fatalf("Claude image block missing: %#v", claudeMessages[0].Content)
	}

	_, geminiMessages := convertToGeminiContents([]openai.ChatCompletionMessage{message})
	if len(geminiMessages) != 1 || len(geminiMessages[0].Parts) != 2 || geminiMessages[0].Parts[1].InlineData == nil {
		t.Fatalf("Gemini image part missing: %#v", geminiMessages)
	}
	if geminiMessages[0].Parts[1].InlineData.MimeType != "image/png" {
		t.Fatalf("Gemini mime = %q", geminiMessages[0].Parts[1].InlineData.MimeType)
	}
}
