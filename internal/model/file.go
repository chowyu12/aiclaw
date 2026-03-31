package model

import (
	"strings"
	"time"
)

type FileType string

const (
	FileTypeText     FileType = "text"
	FileTypeImage    FileType = "image"
	FileTypeDocument FileType = "document"
)

type File struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID           string    `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	ConversationID int64     `json:"conversation_id" gorm:"index;not null"`
	MessageID      int64     `json:"message_id" gorm:"index;default:0"`
	Filename       string    `json:"filename" gorm:"size:255;not null"`
	ContentType    string    `json:"content_type" gorm:"size:100"`
	FileSize       int64     `json:"file_size" gorm:"default:0"`
	FileType       FileType  `json:"file_type" gorm:"size:50;not null"`
	StoragePath    string    `json:"-" gorm:"size:500"`
	TextContent    string    `json:"text_content,omitzero" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at"`
}

func (f *File) IsImage() bool {
	return f.FileType == FileTypeImage
}

func (f *File) IsTextual() bool {
	return f.FileType == FileTypeText || f.FileType == FileTypeDocument
}

// ClassifyFileType 根据 Content-Type 和文件名推断文件类型。
var docExts = []string{".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt"}
var docMIMEKeywords = []string{"pdf", "word", "excel", "spreadsheet", "presentation", "officedocument"}

func ClassifyFileType(contentType, filename string) FileType {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)
	if strings.HasPrefix(ct, "image/") {
		return FileTypeImage
	}
	for _, ext := range docExts {
		if strings.HasSuffix(fn, ext) {
			return FileTypeDocument
		}
	}
	for _, kw := range docMIMEKeywords {
		if strings.Contains(ct, kw) {
			return FileTypeDocument
		}
	}
	return FileTypeText
}
