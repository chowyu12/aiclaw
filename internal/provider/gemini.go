package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

type geminiAdapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func newGeminiAdapter(apiKey, baseURL string) *geminiAdapter {
	return &geminiAdapter{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Transport: &loggingTransport{inner: http.DefaultTransport}},
	}
}

// ── OpenAI → Gemini request conversion ──────────────────────────

type geminiRequest struct {
	Contents          []geminiContent      `json:"contents"`
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl     `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig     `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	InlineData       *geminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResp     `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFuncResp struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations,omitempty"`
}

type geminiFuncDecl struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiGenConfig struct {
	Temperature      *float32 `json:"temperature,omitempty"`
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
}

func buildGeminiRequest(req openai.ChatCompletionRequest) geminiRequest {
	gr := geminiRequest{}

	if req.Temperature > 0 {
		t := req.Temperature
		gr.GenerationConfig = &geminiGenConfig{Temperature: &t}
	}
	if req.MaxCompletionTokens > 0 {
		if gr.GenerationConfig == nil {
			gr.GenerationConfig = &geminiGenConfig{}
		}
		gr.GenerationConfig.MaxOutputTokens = req.MaxCompletionTokens
	}

	gr.SystemInstruction, gr.Contents = convertToGeminiContents(req.Messages)
	gr.Tools = convertToGeminiTools(req.Tools)
	return gr
}

func convertToGeminiContents(msgs []openai.ChatCompletionMessage) (*geminiContent, []geminiContent) {
	var system *geminiContent
	var contents []geminiContent

	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case openai.ChatMessageRoleSystem:
			if system == nil {
				system = &geminiContent{Parts: []geminiPart{{Text: m.Content}}}
			} else {
				system.Parts = append(system.Parts, geminiPart{Text: m.Content})
			}

		case openai.ChatMessageRoleUser:
			parts := userMsgToGeminiParts(m)
			contents = appendGeminiContent(contents, "user", parts)

		case openai.ChatMessageRoleAssistant:
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
			}

		case openai.ChatMessageRoleTool:
			var funcParts []geminiPart
			funcParts = append(funcParts, toolMsgToGeminiPart(m))
			for i+1 < len(msgs) && msgs[i+1].Role == openai.ChatMessageRoleTool {
				i++
				funcParts = append(funcParts, toolMsgToGeminiPart(msgs[i]))
			}
			contents = appendGeminiContent(contents, "user", funcParts)
		}
	}
	return system, contents
}

func appendGeminiContent(contents []geminiContent, role string, parts []geminiPart) []geminiContent {
	if len(contents) > 0 && contents[len(contents)-1].Role == role {
		contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
		return contents
	}
	return append(contents, geminiContent{Role: role, Parts: parts})
}

func userMsgToGeminiParts(m openai.ChatCompletionMessage) []geminiPart {
	if len(m.MultiContent) == 0 {
		return []geminiPart{{Text: m.Content}}
	}
	var parts []geminiPart
	for _, p := range m.MultiContent {
		switch p.Type {
		case openai.ChatMessagePartTypeText:
			parts = append(parts, geminiPart{Text: p.Text})
		case openai.ChatMessagePartTypeImageURL:
			if p.ImageURL != nil {
				if gp := parseDataURLToGeminiInline(p.ImageURL.URL); gp != nil {
					parts = append(parts, *gp)
				}
			}
		}
	}
	return parts
}

func parseDataURLToGeminiInline(dataURL string) *geminiPart {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil
	}
	rest := dataURL[5:]
	semiIdx := strings.Index(rest, ";base64,")
	if semiIdx < 0 {
		return nil
	}
	return &geminiPart{
		InlineData: &geminiInlineData{
			MimeType: rest[:semiIdx],
			Data:     rest[semiIdx+8:],
		},
	}
}

func toolMsgToGeminiPart(m openai.ChatCompletionMessage) geminiPart {
	var resp any
	if err := json.Unmarshal([]byte(m.Content), &resp); err != nil {
		resp = map[string]any{"result": m.Content}
	}
	return geminiPart{
		FunctionResponse: &geminiFuncResp{
			Name:     m.Name,
			Response: resp,
		},
	}
}

func convertToGeminiTools(tools []openai.Tool) []geminiToolDecl {
	if len(tools) == 0 {
		return nil
	}
	var decls []geminiFuncDecl
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		decls = append(decls, geminiFuncDecl{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	if len(decls) == 0 {
		return nil
	}
	return []geminiToolDecl{{FunctionDeclarations: decls}}
}

// ── Gemini response → OpenAI response conversion ───────────────

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	Error         *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func geminiToOpenAIResponse(gr *geminiResponse, model string) openai.ChatCompletionResponse {
	resp := openai.ChatCompletionResponse{Model: model}

	if gr.UsageMetadata != nil {
		resp.Usage = openai.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}

	if len(gr.Candidates) == 0 {
		return resp
	}

	cand := gr.Candidates[0]
	var textParts []string
	var toolCalls []openai.ToolCall
	tcIdx := 0

	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			argsBytes, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   fmt.Sprintf("call_%d", tcIdx),
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsBytes),
				},
			})
			tcIdx++
		}
	}

	finishReason := openai.FinishReasonStop
	if len(toolCalls) > 0 {
		finishReason = openai.FinishReasonToolCalls
	} else if cand.FinishReason == "MAX_TOKENS" {
		finishReason = openai.FinishReasonLength
	}

	resp.Choices = []openai.ChatCompletionChoice{{
		Index: 0,
		Message: openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   strings.Join(textParts, ""),
			ToolCalls: toolCalls,
		},
		FinishReason: finishReason,
	}}
	return resp
}

// ── Non-streaming implementation ────────────────────────────────

func (a *geminiAdapter) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	gr := buildGeminiRequest(req)

	body, _ := json.Marshal(gr)
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", a.baseURL, req.Model, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return openai.ChatCompletionResponse{}, fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(respBody))
	}

	var gr2 geminiResponse
	if err := json.Unmarshal(respBody, &gr2); err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("parse response: %w", err)
	}
	if gr2.Error != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("gemini error %d: %s", gr2.Error.Code, gr2.Error.Message)
	}

	return geminiToOpenAIResponse(&gr2, req.Model), nil
}

// ── Streaming implementation ────────────────────────────────────

func (a *geminiAdapter) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (ChatStream, error) {
	gr := buildGeminiRequest(req)

	body, _ := json.Marshal(gr)
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?key=%s&alt=sse", a.baseURL, req.Model, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini stream request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(errBody))
	}

	return &geminiStream{
		body:    resp.Body,
		scanner: bufio.NewScanner(resp.Body),
		model:   req.Model,
	}, nil
}

var _ LLMProvider = (*geminiAdapter)(nil)

// ── Gemini SSE stream → OpenAI ChatStream ───────────────────────

type geminiStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	model   string
	done    bool
	tcIdx   int // global tool call index counter
}

func (s *geminiStream) Recv() (openai.ChatCompletionStreamResponse, error) {
	for {
		if s.done {
			return openai.ChatCompletionStreamResponse{}, io.EOF
		}
		data, ok := s.nextSSEData()
		if !ok {
			s.done = true
			return openai.ChatCompletionStreamResponse{}, io.EOF
		}

		var chunk geminiResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return openai.ChatCompletionStreamResponse{}, fmt.Errorf("gemini stream error %d: %s", chunk.Error.Code, chunk.Error.Message)
		}

		if len(chunk.Candidates) == 0 {
			if chunk.UsageMetadata != nil {
				return openai.ChatCompletionStreamResponse{
					Model: s.model,
					Usage: &openai.Usage{
						PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
						CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
						TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
					},
				}, nil
			}
			continue
		}

		cand := chunk.Candidates[0]
		resp := openai.ChatCompletionStreamResponse{Model: s.model}

		var delta openai.ChatCompletionStreamChoiceDelta
		hasContent := false

		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				delta.Content += part.Text
				hasContent = true
			}
			if part.FunctionCall != nil {
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				idx := s.tcIdx
				s.tcIdx++
				delta.ToolCalls = append(delta.ToolCalls, openai.ToolCall{
					Index: &idx,
					ID:    fmt.Sprintf("call_%d", idx),
					Type:  openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
				hasContent = true
			}
		}

		var finishReason openai.FinishReason
		if cand.FinishReason == "STOP" {
			finishReason = openai.FinishReasonStop
		} else if cand.FinishReason == "MAX_TOKENS" {
			finishReason = openai.FinishReasonLength
		} else if len(delta.ToolCalls) > 0 && cand.FinishReason != "" {
			finishReason = openai.FinishReasonToolCalls
		}

		if !hasContent && finishReason == "" && chunk.UsageMetadata == nil {
			continue
		}

		resp.Choices = []openai.ChatCompletionStreamChoice{{
			Delta:        delta,
			FinishReason: finishReason,
		}}
		if chunk.UsageMetadata != nil {
			resp.Usage = &openai.Usage{
				PromptTokens:     chunk.UsageMetadata.PromptTokenCount,
				CompletionTokens: chunk.UsageMetadata.CandidatesTokenCount,
				TotalTokens:      chunk.UsageMetadata.TotalTokenCount,
			}
		}
		return resp, nil
	}
}

func (s *geminiStream) Close() error {
	return s.body.Close()
}

func (s *geminiStream) nextSSEData() (json.RawMessage, bool) {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			return json.RawMessage(strings.TrimPrefix(line, "data: ")), true
		}
	}
	return nil, false
}

// ── Gemini remote models ────────────────────────────────────────

func fetchGeminiModels(ctx context.Context, apiKey, baseURL string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1beta/models?key=%s&pageSize=100", baseURL, apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch gemini models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("gemini models API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name string `json:"name"` // "models/gemini-2.0-flash"
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}

	var models []string
	for _, m := range result.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name != "" && strings.Contains(name, "gemini") {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return models, nil
}
