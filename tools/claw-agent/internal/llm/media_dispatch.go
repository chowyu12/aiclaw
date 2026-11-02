package llm

import (
	"context"
	"fmt"
)

// Operation support belongs to the selected protocol. Models must additionally
// support the operation; a provider's model list is not evidence of that support.
type mediaOperations struct {
	image      func(*Client, context.Context, string, string, string) ([]byte, error)
	transcribe func(*Client, context.Context, string, string, []byte) (string, error)
	speak      func(*Client, context.Context, string, string, string) ([]byte, string, error)
}

var openAIMedia = mediaOperations{(*Client).openAIGenerateImage, (*Client).openAITranscribe, (*Client).openAISpeak}
var geminiMedia = mediaOperations{(*Client).geminiGenerateImage, (*Client).geminiTranscribe, (*Client).geminiSpeak}

func (c *Client) GenerateImage(ctx context.Context, model, prompt, size string) ([]byte, error) {
	if c.adapter.media == nil {
		return nil, fmt.Errorf("API protocol %s does not support image generation", c.protocol)
	}
	return c.adapter.media.image(c, ctx, model, prompt, size)
}
func (c *Client) Transcribe(ctx context.Context, model, name string, audio []byte) (string, error) {
	if c.adapter.media == nil {
		return "", fmt.Errorf("API protocol %s does not support audio transcription", c.protocol)
	}
	return c.adapter.media.transcribe(c, ctx, model, name, audio)
}
func (c *Client) Speak(ctx context.Context, model, text, voice string) ([]byte, string, error) {
	if c.adapter.media == nil {
		return nil, "", fmt.Errorf("API protocol %s does not support speech generation", c.protocol)
	}
	return c.adapter.media.speak(c, ctx, model, text, voice)
}
