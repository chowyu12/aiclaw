package tools

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// The retained copy is bounded too; completeness is explicit when this cap is hit.
const MaxRetainedOutput = 8 * 1024 * 1024
const MaxOutputPreview = 24 * 1024

type OutputSaver func(context.Context, string, bool) (string, error)

type OutputBuffer struct {
	buffer   bytes.Buffer
	Complete bool
}

func (b *OutputBuffer) Len() int       { return b.buffer.Len() }
func (b *OutputBuffer) String() string { return b.buffer.String() }

func (b *OutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, MaxRetainedOutput-b.Len())
	_, _ = b.buffer.Write(p[:keep])
	if keep < n {
		b.Complete = false
	}
	return n, nil
}
func OutputPrefix(text string, size int) string {
	if len(text) <= size {
		return text
	}
	end := size
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
func OutputPreview(ctx context.Context, text string, size int, complete bool, save OutputSaver) string {
	text = strings.ToValidUTF8(text, "�")
	if len(text) <= size && complete {
		return text
	}
	preview := OutputPrefix(text, size) + "\n" + i18n.D("[输出已截断]") + "\nPartial output: do not infer missing data from this preview."
	if save == nil {
		return preview + " Full output is unavailable; narrow or paginate the source query."
	}
	id, err := save(ctx, text, complete)
	if err != nil {
		return preview + " Full output could not be saved: " + err.Error()
	}
	return preview + fmt.Sprintf(" output_id=%s; retained_complete=%t. Use read_tool_output with this output_id and offset=0, then follow nextOffset. The retained copy is capped at 8 MiB; complete=false means its tail is unavailable.", id, complete && len(text) <= MaxRetainedOutput)
}
