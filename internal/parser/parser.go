package parser

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

const maxTextLen = 5 * 1024 * 1024 // 5MB

func ExtractText(contentType string, r io.Reader) (string, error) {
	switch {
	case strings.Contains(contentType, "pdf"):
		return extractPDF(r)
	case strings.Contains(contentType, "spreadsheet") || strings.Contains(contentType, "excel") || strings.HasSuffix(contentType, ".sheet"):
		return extractXLSX(r)
	case strings.Contains(contentType, "wordprocessingml") || strings.Contains(contentType, "msword"):
		return extractDOCX(r)
	case strings.Contains(contentType, "presentationml") || strings.Contains(contentType, "powerpoint"):
		return extractPPTX(r)
	default:
		return extractPlainText(r)
	}
}

func extractPPTX(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read pptx: %w", err)
	}
	zipReader, err := newZipReader(data)
	if err != nil {
		return "", fmt.Errorf("open pptx zip: %w", err)
	}
	type slide struct {
		name   string
		number int
		file   *zip.File
	}
	var slides []slide
	for _, file := range zipReader.File {
		if strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
			base := strings.TrimSuffix(strings.TrimPrefix(file.Name, "ppt/slides/slide"), ".xml")
			number, _ := strconv.Atoi(base)
			slides = append(slides, slide{name: file.Name, number: number, file: file})
		}
	}
	sort.Slice(slides, func(i, j int) bool {
		if slides[i].number != slides[j].number {
			return slides[i].number < slides[j].number
		}
		return slides[i].name < slides[j].name
	})
	if len(slides) == 0 {
		return "", fmt.Errorf("slides not found in pptx")
	}
	var result strings.Builder
	for index, item := range slides {
		rc, openErr := item.file.Open()
		if openErr != nil {
			continue
		}
		text, textErr := extractXMLText(rc)
		rc.Close()
		if textErr != nil {
			continue
		}
		fmt.Fprintf(&result, "=== Slide %d ===\n%s\n", index+1, text)
		if result.Len() > maxTextLen {
			break
		}
	}
	return truncate(result.String()), nil
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
