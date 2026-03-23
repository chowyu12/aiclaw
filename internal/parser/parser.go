package parser

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

const maxTextLen = 50 * 1024 // 50KB

func ExtractText(contentType string, r io.Reader) (string, error) {
	switch {
	case strings.Contains(contentType, "pdf"):
		return extractPDF(r)
	case strings.Contains(contentType, "spreadsheet") || strings.Contains(contentType, "excel") || strings.HasSuffix(contentType, ".sheet"):
		return extractXLSX(r)
	case strings.Contains(contentType, "wordprocessingml") || strings.Contains(contentType, "msword"):
		return extractDOCX(r)
	default:
		return extractPlainText(r)
	}
}

func extractPlainText(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxTextLen+1))
	if err != nil {
		return "", err
	}
	return truncate(string(data)), nil
}

func extractPDF(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read pdf: %w", err)
	}
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("parse pdf: %w", err)
	}
	var buf strings.Builder
	for i := range reader.NumPage() {
		page := reader.Page(i + 1)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		buf.WriteString(text)
		buf.WriteString("\n")
		if buf.Len() > maxTextLen {
			break
		}
	}
	return truncate(buf.String()), nil
}

func extractXLSX(r io.Reader) (string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return "", fmt.Errorf("parse xlsx: %w", err)
	}
	defer f.Close()

	var buf strings.Builder
	for _, sheet := range f.GetSheetList() {
		buf.WriteString(fmt.Sprintf("=== Sheet: %s ===\n", sheet))
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for _, row := range rows {
			buf.WriteString(strings.Join(row, "\t"))
			buf.WriteString("\n")
			if buf.Len() > maxTextLen {
				break
			}
		}
		if buf.Len() > maxTextLen {
			break
		}
	}
	return truncate(buf.String()), nil
}

func extractDOCX(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read docx: %w", err)
	}
	zipReader, err := newZipReader(data)
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	for _, f := range zipReader.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open document.xml: %w", err)
		}
		defer rc.Close()
		text, err := extractXMLText(rc)
		if err != nil {
			return "", err
		}
		return truncate(text), nil
	}
	return "", fmt.Errorf("document.xml not found in docx")
}

func truncate(s string) string {
	if len(s) > maxTextLen {
		return s[:maxTextLen]
	}
	return s
}
