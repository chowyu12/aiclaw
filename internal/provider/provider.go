package provider

import (
	"context"

	openai "github.com/chowyu12/go-openai"
)

type ChatStream interface {
	Recv() (openai.ChatCompletionStreamResponse, error)
	Close() error
}

type LLMProvider interface {
	CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
	CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (ChatStream, error)
}
