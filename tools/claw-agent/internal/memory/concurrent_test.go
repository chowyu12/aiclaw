package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestConcurrentMemoryAppends(t *testing.T) {
	file := filepath.Join(t.TempDir(), "memory.md")
	const n = 16
	start := make(chan struct{})
	errors := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := Append(file, fmt.Sprintf("unique-entry-%02d", i))
			errors <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errors)
	failures := 0
	for err := range errors {
		if err != nil {
			failures++
		}
	}
	text, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	present := 0
	for i := 0; i < n; i++ {
		if strings.Contains(text, fmt.Sprintf("unique-entry-%02d", i)) {
			present++
		}
	}
	t.Logf("concurrent writes=%d errors=%d entries preserved=%d", n, failures, present)
	if failures > 0 || present != n {
		t.Errorf("concurrent appends lost entries or failed")
	}
}

func TestMemoryRejectsOversizedOrUnreadableInputsWithoutReplacement(t *testing.T) {
	for _, content := range []string{strings.Repeat("a", MaxBytes+10), "bad\xffinput"} {
		file := filepath.Join(t.TempDir(), "memory.md")
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Append(file, "new entry"); err == nil {
			t.Fatal("bad existing input accepted")
		}
		got, _ := os.ReadFile(file)
		if string(got) != content {
			t.Fatal("existing file replaced")
		}
	}
}
func TestMemoryReadLimitPreservesUTF8Boundary(t *testing.T) {
	file := filepath.Join(t.TempDir(), "memory.md")
	content := strings.Repeat("a", MaxBytes-1) + "中文"
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := Load(file)
	if err == nil || len(text) > MaxBytes || !utf8.ValidString(text) {
		t.Fatal(len(text), err)
	}
}
