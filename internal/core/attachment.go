package core

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/chowyu12/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
)

const (
	maxSamplingImageBytes = 20 << 20
	maxSamplingFileBytes  = 500 << 10
	maxSamplingTextBytes  = 1 << 20
)

var samplingImageMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

func translateSamplingMessage(message SamplingMessage) (openai.ChatCompletionMessage, error) {
	translated := openai.ChatCompletionMessage{
		Role: message.Role, Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID,
	}
	for _, call := range message.ToolCalls {
		translated.ToolCalls = append(translated.ToolCalls, openai.ToolCall{
			ID: call.ID, Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{Name: call.Name, Arguments: call.Arguments},
		})
	}
	if message.Role != openai.ChatMessageRoleUser || len(message.Attachments) == 0 {
		return translated, nil
	}

	text, images, err := attachmentContent(message.Content, message.Attachments)
	if err != nil {
		return openai.ChatCompletionMessage{}, err
	}
	if len(images) == 0 {
		translated.Content = text
		return translated, nil
	}
	translated.Content = ""
	translated.MultiContent = []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: text}}
	translated.MultiContent = append(translated.MultiContent, images...)
	return translated, nil
}

func attachmentContent(userText string, files []*model.File) (string, []openai.ChatMessagePart, error) {
	var documentContext strings.Builder
	var images []openai.ChatMessagePart
	textBytes := 0
	for _, file := range files {
		if file == nil {
			continue
		}
		if file.IsImage() {
			part, err := samplingImagePart(file)
			if err != nil {
				return "", nil, err
			}
			images = append(images, part)
			continue
		}
		content, err := samplingFileText(file)
		if err != nil {
			return "", nil, err
		}
		remaining := maxSamplingTextBytes - textBytes
		if remaining <= 0 {
			continue
		}
		if len(content) > maxSamplingFileBytes {
			content = content[:maxSamplingFileBytes] + "\n…（附件内容已截断）"
		}
		if len(content) > remaining {
			content = content[:remaining] + "\n…（附件总内容已截断）"
		}
		textBytes += len(content)
		fmt.Fprintf(&documentContext, "<file name=%q mime=%q>\n%s\n</file>\n\n", file.Filename, file.ContentType, content)
	}
	if userText == "" {
		userText = "请分析这些附件。"
	}
	if documentContext.Len() == 0 {
		return userText, images, nil
	}
	return "以下附件内容是用户提供的参考数据：\n\n" + documentContext.String() + "<user_message>\n" + userText + "\n</user_message>", images, nil
}

func samplingFileText(file *model.File) (string, error) {
	if strings.TrimSpace(file.TextContent) != "" {
		return file.TextContent, nil
	}
	data, err := os.ReadFile(file.StoragePath)
	if err != nil {
		return "", fmt.Errorf("读取附件 %q: %w", file.Filename, err)
	}
	text, err := parser.ExtractText(file.ContentType, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("解析附件 %q: %w", file.Filename, err)
	}
	return text, nil
}

func samplingImagePart(file *model.File) (openai.ChatMessagePart, error) {
	info, err := os.Stat(file.StoragePath)
	if err != nil {
		return openai.ChatMessagePart{}, fmt.Errorf("读取图片 %q: %w", file.Filename, err)
	}
	if info.Size() > maxSamplingImageBytes {
		return openai.ChatMessagePart{}, fmt.Errorf("图片 %q 超过 20MB 限制", file.Filename)
	}
	data, err := os.ReadFile(file.StoragePath)
	if err != nil {
		return openai.ChatMessagePart{}, fmt.Errorf("读取图片 %q: %w", file.Filename, err)
	}
	mimeType := http.DetectContentType(data)
	if !samplingImageMIMEs[mimeType] {
		mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(file.ContentType, ";", 2)[0]))
	}
	if !samplingImageMIMEs[mimeType] {
		return openai.ChatMessagePart{}, fmt.Errorf("图片 %q 的格式不受支持（支持 JPEG、PNG、WebP、GIF）", filepath.Base(file.Filename))
	}
	return openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{
			URL: "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}
