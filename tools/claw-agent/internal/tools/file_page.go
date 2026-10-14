package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// Skip lines before applying the page budget; a long line resumes by byte offset.
func readFilePage(ctx context.Context, path string, offset, limit int, byteOffset *int64) (string, error) {
	if offset < 0 || limit < 0 || (byteOffset != nil && (*byteOffset < 0 || offset > 0)) {
		return "", errors.New("invalid offset/limit; use either offset or byte_offset")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("read_file requires a regular file")
	}
	var position int64
	line := 1
	if byteOffset != nil {
		position = *byteOffset
		if position > info.Size() {
			return "", errors.New("byte_offset exceeds file size")
		}
		if _, err = file.Seek(position, io.SeekStart); err != nil {
			return "", err
		}
	}
	reader := bufio.NewReader(file)
	for line < offset {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		part, err := reader.ReadSlice('\n')
		position += int64(len(part))
		if len(part) > 0 && part[len(part)-1] == '\n' {
			line++
		}
		if err == io.EOF {
			return "", fmt.Errorf("offset %d exceeds file line count", offset)
		}
		if err != nil && err != bufio.ErrBufferFull {
			return "", err
		}
	}
	var out strings.Builder
	readLines := 0
	for out.Len() < MaxOutputPreview && (limit == 0 || readLines < limit) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		part, err := reader.ReadSlice('\n')
		keep := min(len(part), MaxOutputPreview-out.Len())
		out.Write(part[:keep])
		position += int64(keep)
		if keep == len(part) && len(part) > 0 && part[len(part)-1] == '\n' {
			readLines++
			line++
		}
		if keep < len(part) || err == io.EOF {
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			return "", err
		}
	}
	text := out.String()
	// Avoid splitting a trailing UTF-8 rune. A bad leading byte offset is rejected.
	for removed := 0; len(text) > 0 && !utf8.ValidString(text) && removed < utf8.UTFMax-1; removed++ {
		text = text[:len(text)-1]
		position--
	}
	if !utf8.ValidString(text) {
		return "", errors.New("file page is not valid UTF-8; byte_offset must be on a character boundary")
	}
	if position < info.Size() {
		text += "\n\n" + i18n.D("[内容已截断]") + fmt.Sprintf(" Partial file page; continue with read_file(path=%q, byte_offset=%d). Do not infer missing content from this page.", path, position)
	}
	return text, nil
}
