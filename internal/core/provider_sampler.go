package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/chowyu12/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
)

// ProviderResolver lets the core resolve the user-owned Provider record
// without coupling the Session package to a database implementation.
type ProviderResolver interface {
	GetProvider(context.Context, int64) (*model.Provider, error)
}

// ProviderSampler adapts every existing AIClaw Provider implementation to the
// new Session sampler interface. It deliberately reads the Provider ID and
// model from the Thread's durable configuration.
type ProviderSampler struct{ Resolver ProviderResolver }

func (s ProviderSampler) Sample(ctx context.Context, request SamplingRequest, emit func(string) error) (SamplingResult, error) {
	if s.Resolver == nil {
		return SamplingResult{}, fmt.Errorf("provider resolver is required")
	}
	p, err := s.Resolver.GetProvider(ctx, request.Thread.ProviderID)
	if err != nil {
		return SamplingResult{}, fmt.Errorf("load provider: %w", err)
	}
	client, err := provider.NewFromProvider(p)
	if err != nil {
		return SamplingResult{}, err
	}
	messages := make([]openai.ChatCompletionMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		translated, err := translateSamplingMessage(message)
		if err != nil {
			return SamplingResult{}, err
		}
		messages = append(messages, translated)
	}
	tools := make([]openai.Tool, 0, len(request.Tools))
	for _, definition := range request.Tools {
		tools = append(tools, openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: definition.Name, Description: definition.Description, Parameters: definition.Schema}})
	}
	stream, err := client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: request.Thread.ModelName, Messages: messages, Tools: tools,
		Stream: true, StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return SamplingResult{}, err
	}
	defer stream.Close()

	var text strings.Builder
	var toolCalls []openai.ToolCall
	tokens := 0
	for {
		response, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return SamplingResult{}, recvErr
		}
		if response.Usage != nil {
			tokens = response.Usage.TotalTokens
		}
		if len(response.Choices) == 0 {
			continue
		}
		delta := response.Choices[0].Delta
		if delta.Content != "" {
			text.WriteString(delta.Content)
			if emit != nil {
				if err := emit(delta.Content); err != nil {
					return SamplingResult{}, err
				}
			}
		}
		for _, streamed := range delta.ToolCalls {
			index := 0
			if streamed.Index != nil {
				index = *streamed.Index
			}
			for len(toolCalls) <= index {
				toolCalls = append(toolCalls, openai.ToolCall{Type: openai.ToolTypeFunction})
			}
			if streamed.ID != "" {
				toolCalls[index].ID = streamed.ID
			}
			if streamed.Type != "" {
				toolCalls[index].Type = streamed.Type
			}
			toolCalls[index].Function.Name += streamed.Function.Name
			toolCalls[index].Function.Arguments += streamed.Function.Arguments
		}
	}
	result := SamplingResult{Text: text.String(), Tokens: tokens}
	for _, call := range toolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return result, nil
}
