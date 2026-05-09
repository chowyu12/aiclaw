package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	toolresult "github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

func TestPersistToolFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fileName string
		mimeType string
		content  []byte
		wantType model.FileType
	}{
		{
			name:     "document pdf",
			fileName: "report.pdf",
			mimeType: "application/pdf",
			content:  []byte("%PDF-1.4 test pdf"),
			wantType: model.FileTypeDocument,
		},
		{
			name:     "text markdown",
			fileName: "notes.md",
			mimeType: "text/markdown",
			content:  []byte("# summary\nhello"),
			wantType: model.FileTypeText,
		},
		{
			name:     "image png",
			fileName: "chart.png",
			mimeType: "image/png",
			content:  []byte{0x89, 'P', 'N', 'G'},
			wantType: model.FileTypeImage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tmpRoot := t.TempDir()
			ws, err := workspace.New(filepath.Join(tmpRoot, "ws"))
			if err != nil {
				t.Fatalf("workspace.New() error = %v", err)
			}

			srcPath := filepath.Join(tmpRoot, tc.fileName)
			if writeErr := os.WriteFile(srcPath, tc.content, 0o644); writeErr != nil {
				t.Fatalf("os.WriteFile() error = %v", writeErr)
			}

			store := newMockStore()
			exec := newTestExecutorWithWS(store, NewToolRegistry(), &mockLLMProvider{}, ws)
			ec := &execContext{
				ctx:  workspace.WithWorkspace(context.Background(), ws),
				conv: &model.Conversation{ID: 42},
				l:    log.NewEntry(log.New()),
			}

			got := exec.persistToolFile(context.Background(), ec, toolresult.NewFileResult(srcPath, tc.mimeType, "tool output"))
			if got == nil {
				t.Fatal("persistToolFile() returned nil")
			}
			if got.FileType != tc.wantType {
				t.Fatalf("persistToolFile() file type = %q, want %q", got.FileType, tc.wantType)
			}
			if got.Filename != tc.fileName {
				t.Fatalf("persistToolFile() filename = %q, want %q", got.Filename, tc.fileName)
			}
			if got.ConversationID != 42 {
				t.Fatalf("persistToolFile() conversation_id = %d, want 42", got.ConversationID)
			}
			if filepath.Dir(got.StoragePath) != ws.Uploads() {
				t.Fatalf("persistToolFile() storage dir = %q, want under %q", filepath.Dir(got.StoragePath), ws.Uploads())
			}

			data, err := os.ReadFile(got.StoragePath)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error = %v", got.StoragePath, err)
			}
			if string(data) != string(tc.content) {
				t.Fatalf("persisted content mismatch: got %q want %q", string(data), string(tc.content))
			}

			stored, err := store.GetFileByUUID(context.Background(), got.UUID)
			if err != nil {
				t.Fatalf("store.GetFileByUUID() error = %v", err)
			}
			if stored.FileType != tc.wantType {
				t.Fatalf("stored file type = %q, want %q", stored.FileType, tc.wantType)
			}
		})
	}
}

func TestPersistToolFileIgnoresNonFileResult(t *testing.T) {
	t.Parallel()

	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatalf("workspace.New() error = %v", err)
	}

	store := newMockStore()
	exec := newTestExecutorWithWS(store, NewToolRegistry(), &mockLLMProvider{}, ws)
	ec := &execContext{
		ctx:  workspace.WithWorkspace(context.Background(), ws),
		conv: &model.Conversation{ID: 7},
		l:    log.NewEntry(log.New()),
	}

	got := exec.persistToolFile(context.Background(), ec, "plain text result")
	if got != nil {
		t.Fatalf("persistToolFile() = %#v, want nil", got)
	}
}
