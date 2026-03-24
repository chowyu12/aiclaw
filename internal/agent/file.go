package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/tool"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

func (e *Executor) loadRemoteFile(ctx context.Context, rawURL string, chatFileType model.ChatFileType) *model.File {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	l := log.WithField("url", rawURL)
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		l.WithError(err).Warn("[Execute] invalid file URL, skipping")
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		l.WithError(err).Warn("[Execute] fetch file URL failed, skipping")
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20+1))
	resp.Body.Close()
	if err != nil {
		l.WithError(err).Warn("[Execute] read file URL body failed, skipping")
		return nil
	}
	if int64(len(data)) > 20<<20 {
		l.Warn("[Execute] file URL too large (>20MB), skipping")
		return nil
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	filename := path.Base(rawURL)
	if filename == "" || filename == "." || filename == "/" {
		filename = "remote_file"
	}

	fileType := chatFileTypeToFileType(chatFileType, ct, filename)
	f := &model.File{
		UUID:        rawURL,
		Filename:    filename,
		ContentType: ct,
		FileSize:    int64(len(data)),
		FileType:    fileType,
	}

	ext := filepath.Ext(filename)
	if ext == "" && strings.HasPrefix(ct, "image/") {
		ext = "." + strings.TrimPrefix(strings.SplitN(ct, ";", 2)[0], "image/")
	}
	tmpDir := workspace.AgentTmpFromCtx(ctx)
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("ai-agent-url-%d%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		l.WithError(err).Warn("[Execute] save temp file failed, skipping")
		return nil
	}
	f.StoragePath = tmpPath

	if fileType == model.FileTypeText || fileType == model.FileTypeDocument {
		text, err := parser.ExtractText(ct, bytes.NewReader(data))
		if err != nil {
			l.WithError(err).Warn("[Execute] extract text from URL failed, using raw")
			text = string(data)
			if len(text) > 50*1024 {
				text = text[:50*1024]
			}
		}
		f.TextContent = text
	}

	l.WithFields(log.Fields{"filename": filename, "type": string(fileType), "size": len(data)}).Info("[Execute] remote file loaded")
	return f
}

func chatFileTypeToFileType(chatType model.ChatFileType, contentType, filename string) model.FileType {
	switch chatType {
	case model.ChatFileImage:
		return model.FileTypeImage
	case model.ChatFileDocument:
		return model.FileTypeDocument
	case model.ChatFileAudio, model.ChatFileVideo:
		return model.FileTypeDocument
	default:
		return classifyContentType(contentType, filename)
	}
}

func classifyContentType(contentType, filename string) model.FileType {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)

	if strings.HasPrefix(ct, "image/") {
		return model.FileTypeImage
	}
	docExts := []string{".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt"}
	for _, ext := range docExts {
		if strings.HasSuffix(fn, ext) {
			return model.FileTypeDocument
		}
	}
	docTypes := []string{"pdf", "word", "excel", "spreadsheet", "presentation", "officedocument"}
	for _, dt := range docTypes {
		if strings.Contains(ct, dt) {
			return model.FileTypeDocument
		}
	}
	return model.FileTypeText
}

func (e *Executor) loadRequestFiles(ctx context.Context, chatFiles []model.ChatFile, conversationID int64) []*model.File {
	var files []*model.File
	seen := make(map[string]bool)

	for _, cf := range chatFiles {
		switch cf.TransferMethod {
		case model.TransferLocalFile:
			if cf.UploadFileID == "" {
				continue
			}
			f, err := e.store.GetFileByUUID(ctx, cf.UploadFileID)
			if err != nil {
				log.WithField("upload_file_id", cf.UploadFileID).WithError(err).Warn("[Execute] load uploaded file failed, skipping")
				continue
			}
			seen[f.UUID] = true
			files = append(files, f)
		case model.TransferRemoteURL:
			if cf.URL == "" {
				continue
			}
			if seen[cf.URL] {
				continue
			}
			f := e.loadRemoteFile(ctx, cf.URL, cf.Type)
			if f != nil {
				seen[cf.URL] = true
				files = append(files, f)
			}
		}
	}

	if conversationID > 0 {
		convFiles, err := e.store.ListFilesByConversation(ctx, conversationID)
		if err == nil {
			for _, f := range convFiles {
				if !seen[f.UUID] {
					seen[f.UUID] = true
					files = append(files, f)
				}
			}
		}
	}

	if len(files) > 0 {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, fmt.Sprintf("%s(%s)", f.Filename, f.FileType))
		}
		log.WithField("files", names).Info("[Execute] files loaded for context")
	}
	return files
}

func (e *Executor) buildToolResponseParts(ctx context.Context, toolCallID, toolName, toolResult string, ok bool, l *log.Entry) (openai.ChatCompletionMessage, []openai.ChatMessagePart) {
	toolMsg := func(content string) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    content,
			ToolCallID: toolCallID,
			Name:       toolName,
		}
	}

	if !ok {
		return toolMsg(toolResult), nil
	}

	fr := tool.ParseFileResult(toolResult)
	if fr == nil {
		return toolMsg(toolResult), nil
	}

	data, err := os.ReadFile(fr.Path)
	if err != nil {
		l.WithError(err).WithField("path", fr.Path).Warn("[Tool] << read file failed, using text fallback")
		return toolMsg(fr.Description), nil
	}

	l.WithFields(log.Fields{"tool": toolName, "path": fr.Path, "mime": fr.MimeType, "size": len(data)}).Info("[Tool] << attaching file to response")

	if strings.HasPrefix(fr.MimeType, "image/") {
		imgPart := imagePartFromData(fr.Path, fr.MimeType, data)
		return toolMsg(fr.Description), []openai.ChatMessagePart{imgPart}
	}

	content := string(data)
	const maxFileContent = 10_000
	if len(content) > maxFileContent {
		content = content[:maxFileContent] + "\n... (content truncated)"
	}
	return toolMsg(fmt.Sprintf("%s\n\n%s", fr.Description, content)), nil
}

func imagePartFromData(name, mimeType string, data []byte) openai.ChatMessagePart {
	if !strings.HasPrefix(mimeType, "image/") {
		if detected := http.DetectContentType(data); strings.HasPrefix(detected, "image/") {
			mimeType = detected
		} else {
			mimeType = "image/png"
		}
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	log.WithFields(log.Fields{"file": filepath.Base(name), "mime": mimeType, "size": len(data)}).Debug("[Execute] attaching image via base64")
	return openai.ChatMessagePart{
		Type:     openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{URL: dataURL},
	}
}

func imagePartForFile(f *model.File) (openai.ChatMessagePart, error) {
	data, err := os.ReadFile(f.StoragePath)
	if err != nil {
		return openai.ChatMessagePart{}, err
	}
	return imagePartFromData(f.Filename, f.ContentType, data), nil
}
