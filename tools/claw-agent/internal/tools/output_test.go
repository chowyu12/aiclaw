package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func TestReadFileOffsetBeyondOldByteCap(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := fullRegistry(t)
	text := strings.Repeat("first line\n", 40000) + "DATA_AFTER_256KB\n"
	if err := os.WriteFile(filepath.Join(env.Workspace, "big.txt"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, registry, "read_file", `{"path":"big.txt","offset":40001,"limit":1}`, env)
	if err != nil || out != "DATA_AFTER_256KB\n" {
		t.Fatalf("%q %v", out, err)
	}
}
func TestReadFilePagesLongUnicodeLineWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	text := "x" + strings.Repeat("中文", 50000) + "TAIL"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	var complete strings.Builder
	var offset int64
	for {
		page, err := readFilePage(context.Background(), path, 0, 0, &offset)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page) {
			t.Fatal("invalid unicode")
		}
		at := strings.Index(page, "\n\n[内容已截断]")
		if at < 0 {
			complete.WriteString(page)
			break
		}
		complete.WriteString(page[:at])
		marker := "byte_offset="
		start := strings.LastIndex(page, marker) + len(marker)
		end := strings.Index(page[start:], ")") + start
		next, err := strconv.ParseInt(page[start:end], 10, 64)
		if err != nil || next <= offset {
			t.Fatal("bad cursor", page)
		}
		offset = next
	}
	if complete.String() != text {
		t.Fatal("lost file bytes across pages")
	}
	bad := int64(2)
	if _, err := readFilePage(context.Background(), path, 0, 0, &bad); err == nil {
		t.Fatal("accepted split rune offset")
	}
}
func TestCommandRetainsTailAndExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := fullRegistry(t)
	var saved string
	env.SaveOutput = func(_ context.Context, text string, complete bool) (string, error) {
		if !complete {
			t.Fatal("unexpected capture cap")
		}
		saved = text
		return strings.Repeat("a", 32), nil
	}
	args, _ := json.Marshal(map[string]any{"command": "awk 'BEGIN {for(i=0;i<10000;i++) print \"LINE\"; print \"LATE_DATA\"; exit 7}'"})
	out, err := call(t, registry, "run_command", string(args), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved, "LATE_DATA") || !strings.Contains(out, "output_id=") || !strings.Contains(out, "7") {
		t.Fatal("lost command result", out)
	}
	// Persistent shells must drain the whole result and preserve the exit status too.
	pool := NewShellPool()
	defer pool.Close()
	env.Shells = pool
	args, _ = json.Marshal(map[string]any{"command": "awk 'BEGIN {for(i=0;i<10000;i++) print \"LINE\"; print \"LATE_DATA\"; exit 7}'", "session": "new"})
	out, err = call(t, registry, "run_command", string(args), env)
	if err != nil || !strings.Contains(saved, "LATE_DATA") || !strings.Contains(out, "退出码 7") {
		t.Fatalf("persistent: %s %v", out, err)
	}
}
func TestOutputCaptureCapAndUTF8Preview(t *testing.T) {
	b := OutputBuffer{Complete: true}
	n, err := b.Write([]byte(strings.Repeat("a", MaxRetainedOutput+100)))
	if err != nil || n != MaxRetainedOutput+100 || b.Complete || b.Len() != MaxRetainedOutput {
		t.Fatal("bad cap")
	}
	preview := OutputPreview(context.Background(), strings.Repeat("中文", 100), 7, true, nil)
	if !utf8.ValidString(preview) || !strings.Contains(preview, "Partial") {
		t.Fatal(preview)
	}
}

func TestDeliberateHeadIsMarkedPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := fullRegistry(t)
	out, err := call(t, registry, "run_command", `{"command":"printf 'first\\nlast\\n' | head -n 1"}`, env)
	if err != nil || !strings.Contains(out, "Partial command selection") {
		t.Fatalf("%s %v", out, err)
	}
}
