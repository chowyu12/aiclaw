package appservice

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	toolresult "github.com/chowyu12/aiclaw/internal/tools/result"
)

const (
	maxDesktopAttachments     = 10
	maxDesktopAttachmentBytes = 20 << 20
)

type DesktopAttachment struct {
	UUID        string `json:"uuid"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	FileSize    int64  `json:"file_size"`
	FileType    string `json:"file_type"`
	PreviewURL  string `json:"preview_url,omitempty"`
	Available   bool   `json:"available"`
}

// DesktopOutputFile is a local file produced or referenced by an assistant
// turn. Unlike an attachment it remains at its original path and can be
// opened or revealed directly from the conversation.
type DesktopOutputFile struct {
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	FileSize    int64  `json:"file_size"`
	FileType    string `json:"file_type"`
	PreviewURL  string `json:"preview_url,omitempty"`
	Description string `json:"description,omitempty"`
	Available   bool   `json:"available"`
}

var desktopDocumentMIMEs = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

var desktopImageMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

var desktopTextExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".jsonl": true,
	".csv": true, ".tsv": true, ".xml": true, ".yaml": true, ".yml": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".vue": true, ".html": true, ".css": true, ".scss": true, ".sql": true,
	".sh": true, ".zsh": true, ".toml": true, ".ini": true, ".conf": true, ".log": true,
}

func (s *Service) ChooseAttachments() ([]DesktopAttachment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	paths, err := s.dialogs.PickFiles(s.ctx, FilePicker{
		Title: "选择文件或图片",
		Filters: []FileFilter{
			{DisplayName: "支持的文件", Pattern: "*.jpg;*.jpeg;*.png;*.webp;*.gif;*.pdf;*.docx;*.xlsx;*.pptx;*.txt;*.md;*.markdown;*.json;*.jsonl;*.csv;*.tsv;*.xml;*.yaml;*.yml;*.go;*.py;*.js;*.ts;*.tsx;*.jsx;*.vue;*.html;*.css;*.scss;*.sql;*.sh;*.zsh;*.toml;*.ini;*.conf;*.log"},
			{DisplayName: "全部文件", Pattern: "*"},
		},
	})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return s.ImportAttachments(paths)
}

func (s *Service) OpenAttachment(fileUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	file, err := s.store.GetFileByUUID(s.ctx, strings.TrimSpace(fileUUID))
	if err != nil {
		return err
	}
	return launchDesktopFile(file.StoragePath, false)
}

func (s *Service) RevealAttachment(fileUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	file, err := s.store.GetFileByUUID(s.ctx, strings.TrimSpace(fileUUID))
	if err != nil {
		return err
	}
	return launchDesktopFile(file.StoragePath, true)
}

func (s *Service) OpenOutputFile(path string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return launchDesktopFile(path, false)
}

func (s *Service) RevealOutputFile(path string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return launchDesktopFile(path, true)
}

func launchDesktopFile(rawPath string, reveal bool) error {
	path, err := existingDesktopFile(rawPath)
	if err != nil {
		return err
	}
	var command *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		args := []string{path}
		if reveal {
			args = []string{"-R", path}
		}
		command = exec.Command("open", args...)
	case "windows":
		if reveal {
			command = exec.Command("explorer.exe", "/select,"+path)
		} else {
			command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
		}
	default:
		target := path
		if reveal {
			target = filepath.Dir(path)
		}
		command = exec.Command("xdg-open", target)
	}
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("打开本地文件: %s: %w", message, err)
		}
		return fmt.Errorf("打开本地文件: %w", err)
	}
	return nil
}

func existingDesktopFile(rawPath string) (string, error) {
	path := strings.TrimSpace(strings.TrimPrefix(rawPath, "file://"))
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("文件路径必须是绝对路径")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("文件不可用: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("路径不是普通文件")
	}
	return path, nil
}

func (s *Service) desktopOutputFiles(text string) []DesktopOutputFile {
	parsed := toolresult.ParseFileResults(text)
	items := make([]DesktopOutputFile, 0, len(parsed))
	for _, file := range parsed {
		items = appendOutputFile(items, s.desktopOutputFile(file))
	}
	return items
}

func appendOutputFile(items []DesktopOutputFile, item DesktopOutputFile) []DesktopOutputFile {
	for _, existing := range items {
		if existing.Path == item.Path {
			return items
		}
	}
	return append(items, item)
}

func (s *Service) desktopOutputFile(file toolresult.FileResult) DesktopOutputFile {
	item := DesktopOutputFile{
		Path: filepath.Clean(file.Path), Filename: filepath.Base(file.Path),
		ContentType: file.MimeType, Description: file.Description,
	}
	if item.ContentType == "" {
		item.ContentType = toolresult.MimeFromExt(filepath.Ext(item.Path))
	}
	item.FileType = string(model.ClassifyFileType(item.ContentType, item.Filename))
	info, err := os.Stat(item.Path)
	if err != nil || !info.Mode().IsRegular() {
		return item
	}
	item.Available = true
	item.FileSize = info.Size()
	if desktopImageMIMEs[item.ContentType] && info.Size() <= maxDesktopAttachmentBytes {
		item.PreviewURL = s.previewFor(PreviewFile{
			Path: item.Path, ContentType: item.ContentType, Size: info.Size(),
		})
	}
	return item
}

// ImportAttachments is also used by native drag and drop. Every selected file
// is copied into AIClaw's private local data directory before it is parsed.
func (s *Service) ImportAttachments(paths []string) ([]DesktopAttachment, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxDesktopAttachments {
		return nil, fmt.Errorf("一次最多添加 %d 个附件", maxDesktopAttachments)
	}
	result := make([]DesktopAttachment, 0, len(paths))
	for _, path := range paths {
		attachment, err := s.importAttachment(path)
		if err != nil {
			for _, imported := range result {
				_ = s.DiscardAttachment(imported.UUID)
			}
			return nil, err
		}
		result = append(result, attachment)
	}
	return result, nil
}

func (s *Service) importAttachment(sourcePath string) (DesktopAttachment, error) {
	sourcePath = filepath.Clean(strings.TrimSpace(sourcePath))
	info, err := os.Stat(sourcePath)
	if err != nil {
		return DesktopAttachment{}, fmt.Errorf("读取附件: %w", err)
	}
	if info.IsDir() {
		return DesktopAttachment{}, fmt.Errorf("暂不支持添加文件夹：%s", filepath.Base(sourcePath))
	}
	if info.Size() <= 0 {
		return DesktopAttachment{}, fmt.Errorf("附件为空：%s", filepath.Base(sourcePath))
	}
	if info.Size() > maxDesktopAttachmentBytes {
		return DesktopAttachment{}, fmt.Errorf("附件 %q 超过 20MB 限制", filepath.Base(sourcePath))
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return DesktopAttachment{}, fmt.Errorf("读取附件 %q: %w", filepath.Base(sourcePath), err)
	}
	filename := filepath.Base(sourcePath)
	contentType, fileType, err := classifyDesktopAttachment(filename, data)
	if err != nil {
		return DesktopAttachment{}, err
	}

	fileUUID := uuid.NewString()
	dir := filepath.Join(s.root, "attachments", fileUUID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return DesktopAttachment{}, fmt.Errorf("创建附件目录: %w", err)
	}
	destination := filepath.Join(dir, filename)
	if err := copyPrivateFile(sourcePath, destination); err != nil {
		_ = os.RemoveAll(dir)
		return DesktopAttachment{}, err
	}

	file := &model.File{
		UUID: fileUUID, Filename: filename, ContentType: contentType,
		FileSize: info.Size(), FileType: fileType, StoragePath: destination,
	}
	if file.IsTextual() {
		file.TextContent, err = parser.ExtractText(contentType, bytes.NewReader(data))
		if err != nil {
			_ = os.RemoveAll(dir)
			return DesktopAttachment{}, fmt.Errorf("无法解析附件 %q: %w", filename, err)
		}
	}
	if err := s.store.CreateFile(s.ctx, file); err != nil {
		_ = os.RemoveAll(dir)
		return DesktopAttachment{}, err
	}
	return s.desktopAttachment(file), nil
}

func classifyDesktopAttachment(filename string, data []byte) (string, model.FileType, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	sniffed := strings.ToLower(strings.SplitN(http.DetectContentType(data), ";", 2)[0])
	if strings.HasPrefix(sniffed, "image/") {
		if !desktopImageMIMEs[sniffed] {
			return "", "", fmt.Errorf("图片 %q 格式不受支持（支持 JPEG、PNG、WebP、GIF）", filename)
		}
		return sniffed, model.FileTypeImage, nil
	}
	if contentType, ok := desktopDocumentMIMEs[ext]; ok {
		return contentType, model.FileTypeDocument, nil
	}
	if desktopTextExtensions[ext] || strings.HasPrefix(sniffed, "text/") || sniffed == "application/json" || sniffed == "application/xml" {
		contentType := mime.TypeByExtension(ext)
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = sniffed
		}
		if contentType == "application/octet-stream" {
			contentType = "text/plain; charset=utf-8"
		}
		return contentType, model.FileTypeText, nil
	}
	return "", "", fmt.Errorf("不支持附件 %q；请选择图片、PDF、DOCX、XLSX、PPTX 或文本/代码文件", filename)
}

func normalizeAttachmentIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func copyPrivateFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开附件: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("保存附件: %w", err)
	}
	written, copyErr := io.Copy(out, io.LimitReader(in, maxDesktopAttachmentBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("保存附件: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("保存附件: %w", closeErr)
	}
	if written > maxDesktopAttachmentBytes {
		_ = os.Remove(destination)
		return fmt.Errorf("附件超过 20MB 限制")
	}
	return nil
}

func (s *Service) desktopAttachment(file *model.File) DesktopAttachment {
	item := DesktopAttachment{
		UUID: file.UUID, Filename: file.Filename, ContentType: file.ContentType,
		FileSize: file.FileSize, FileType: string(file.FileType), Available: true,
	}
	if file.IsImage() {
		item.PreviewURL = s.previewFor(PreviewFile{
			UUID: file.UUID, Path: file.StoragePath,
			ContentType: file.ContentType, Size: file.FileSize,
		})
	}
	return item
}

// previewFor asks the host for a preview URL, falling back to an inline data
// URI when the host has no scheme of its own.
func (s *Service) previewFor(file PreviewFile) string {
	if s.previewURL != nil {
		if url, ok := s.previewURL(file); ok {
			return url
		}
		return ""
	}
	if file.Size > maxDesktopAttachmentBytes {
		return ""
	}
	data, err := os.ReadFile(file.Path)
	if err != nil {
		return ""
	}
	return "data:" + file.ContentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func (s *Service) attachmentByUUID(fileUUID string) (DesktopAttachment, error) {
	file, err := s.store.GetFileByUUID(s.ctx, strings.TrimSpace(fileUUID))
	if err != nil {
		return DesktopAttachment{}, err
	}
	item := s.desktopAttachment(file)
	// Availability is whether the file is still on disk. Stat answers that
	// without reading the whole attachment into memory, which the preview no
	// longer needs either.
	if info, statErr := os.Stat(file.StoragePath); statErr != nil || !info.Mode().IsRegular() {
		item.Available = false
		item.PreviewURL = ""
	}
	return item, nil
}

func (s *Service) attachmentsByUUIDs(ids []string) []DesktopAttachment {
	result := make([]DesktopAttachment, 0, len(ids))
	for _, id := range ids {
		item, err := s.attachmentByUUID(id)
		if err == nil {
			result = append(result, item)
		}
	}
	return result
}

func (s *Service) DiscardAttachment(fileUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	file, err := s.store.GetFileByUUID(s.ctx, strings.TrimSpace(fileUUID))
	if err != nil {
		return err
	}
	if file.ThreadID != 0 || file.ConversationID != 0 {
		return fmt.Errorf("已发送的附件不能从历史会话中移除")
	}
	if err := s.store.DeleteFile(s.ctx, file.ID); err != nil {
		return err
	}
	return s.removeAttachmentStorage(file)
}

func (s *Service) removeAttachmentStorage(file *model.File) error {
	root := filepath.Clean(filepath.Join(s.root, "attachments")) + string(os.PathSeparator)
	dir := filepath.Clean(filepath.Dir(file.StoragePath))
	if !strings.HasPrefix(dir+string(os.PathSeparator), root) {
		return nil
	}
	return os.RemoveAll(dir)
}

func (s *Service) cleanupPendingAttachments() {
	files, err := s.store.ListPendingFilesBefore(s.ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		return
	}
	for _, file := range files {
		if s.store.DeleteFile(s.ctx, file.ID) == nil {
			_ = s.removeAttachmentStorage(file)
		}
	}
}
