package handler

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/pkg/httputil"
	"github.com/google/uuid"
)

type FileHandler struct {
	store     store.Store
	uploadCfg config.UploadConfig
}

func NewFileHandler(s store.Store, uploadCfg config.UploadConfig) *FileHandler {
	return &FileHandler{store: s, uploadCfg: uploadCfg}
}

func (h *FileHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/files", h.Upload)
	mux.HandleFunc("GET /api/v1/files", h.List)
	mux.HandleFunc("GET /api/v1/files/{uuid}", h.Download)
	mux.HandleFunc("GET /public/files/{uuid}", h.Download)
	mux.HandleFunc("DELETE /api/v1/files/{uuid}", h.Delete)
}

func (h *FileHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(h.uploadCfg.MaxSize); err != nil {
		httputil.BadRequest(w, "文件过大或请求格式错误")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "缺少文件")
		return
	}
	defer file.Close()

	conversationID, _ := strconv.ParseInt(r.FormValue("conversation_id"), 10, 64)

	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		if mt := mime.TypeByExtension(filepath.Ext(header.Filename)); mt != "" {
			contentType = mt
		} else {
			contentType = "application/octet-stream"
		}
	}

	fileType := classifyFile(contentType, header.Filename)
	fileUUID := uuid.New().String()
	ext := filepath.Ext(header.Filename)
	subDir := time.Now().Format("2006-01")
	dirPath := filepath.Join(h.uploadCfg.Dir, subDir)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		log.WithError(err).Error("create upload directory failed")
		httputil.InternalError(w, "创建上传目录失败")
		return
	}
	storageName := fileUUID + ext
	storagePath := filepath.Join(dirPath, storageName)

	data, err := io.ReadAll(io.LimitReader(file, h.uploadCfg.MaxSize+1))
	if err != nil {
		httputil.InternalError(w, "读取文件失败")
		return
	}
	if int64(len(data)) > h.uploadCfg.MaxSize {
		httputil.BadRequest(w, fmt.Sprintf("文件超过大小限制 %dMB", h.uploadCfg.MaxSize/(1<<20)))
		return
	}

	if err := os.WriteFile(storagePath, data, 0o644); err != nil {
		log.WithError(err).Error("save upload file failed")
		httputil.InternalError(w, "保存文件失败")
		return
	}

	var textContent string
	if fileType != model.FileTypeImage {
		text, err := parser.ExtractText(contentType, bytes.NewReader(data))
		if err != nil {
			log.WithError(err).WithField("filename", header.Filename).Warn("extract text failed, storing without text content")
		} else {
			textContent = text
		}
	}

	f := &model.File{
		UUID:           fileUUID,
		ConversationID: conversationID,
		Filename:       header.Filename,
		ContentType:    contentType,
		FileSize:       int64(len(data)),
		FileType:       fileType,
		StoragePath:    storagePath,
		TextContent:    textContent,
	}
	if err := h.store.CreateFile(r.Context(), f); err != nil {
		log.WithError(err).Error("save file record failed")
		os.Remove(storagePath)
		httputil.InternalError(w, "保存文件记录失败")
		return
	}

	httputil.OK(w, f)
}

func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uuid")
	f, err := h.store.GetFileByUUID(r.Context(), uid)
	if err != nil {
		httputil.NotFound(w, "文件不存在")
		return
	}

	data, err := os.ReadFile(f.StoragePath)
	if err != nil {
		httputil.InternalError(w, "读取文件失败")
		return
	}

	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, f.Filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (h *FileHandler) List(w http.ResponseWriter, r *http.Request) {
	conversationID, _ := strconv.ParseInt(r.URL.Query().Get("conversation_id"), 10, 64)
	if conversationID == 0 {
		httputil.BadRequest(w, "缺少 conversation_id")
		return
	}
	files, err := h.store.ListFilesByConversation(r.Context(), conversationID)
	if err != nil {
		httputil.InternalError(w, "查询文件列表失败")
		return
	}
	httputil.OK(w, files)
}

func (h *FileHandler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uuid")
	f, err := h.store.GetFileByUUID(r.Context(), uid)
	if err != nil {
		httputil.NotFound(w, "文件不存在")
		return
	}
	if err := h.store.DeleteFile(r.Context(), f.ID); err != nil {
		httputil.InternalError(w, "删除文件失败")
		return
	}
	os.Remove(f.StoragePath)
	httputil.OK(w, nil)
}

func classifyFile(contentType, filename string) model.FileType {
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
