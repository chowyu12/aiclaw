package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

type fileEmitter struct {
	recordingEmitter
	requests []protocol.AttachmentRequest
}

func (e *fileEmitter) RequestAttachment(_ context.Context, request protocol.AttachmentRequest) (protocol.AttachmentResult, error) {
	e.requests = append(e.requests, request)
	return protocol.AttachmentResult{Text: `{"text":"page two from original","nextOffset":64}`}, nil
}
func attachmentSession(t *testing.T, model *fakeModel, enabled bool) *Session {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(model.handler))
	t.Cleanup(endpoint.Close)
	s, err := New(context.Background(), "actual-session", protocol.SessionStartParams{
		Model: protocol.ModelConfig{BaseURL: endpoint.URL, Model: "fake"}, Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass, EnableAttachments: enabled,
	}, StaticKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func TestAttachmentsAreOptInAndSurviveRefresh(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := attachmentSession(t, &fakeModel{}, enabled)
		_, exists := s.registry.Get("read_attachment")
		if exists != enabled || refreshOf(s.Config()).EnableAttachments != enabled {
			t.Fatal("attachment capability mismatch")
		}
		db := newTestStore(t)
		if err := s.Save(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		refresh := refreshOf(s.Config())
		loaded, err := Load(context.Background(), db, s.ID, StaticKey("sk-test"), &refresh)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(loaded.Close)
		_, exists = loaded.registry.Get("read_attachment")
		if exists != enabled {
			t.Fatal("attachment capability lost on resume")
		}
	}
}
func TestAttachmentToolUsesTrustedSessionAndPreservesReadRange(t *testing.T) {
	m := &fakeModel{script: []string{sseToolCalls([3]string{"read", "read_attachment", `{"sessionId":"forged","attachmentId":"file-id","mode":"read","section":2,"offset":16,"limit":32,"member":"report.pdf"}`}), sseText("Done")}}
	s := attachmentSession(t, m, true)
	e := &fileEmitter{}
	s.RunTurn(context.Background(), "turn", "Read the attachment", nil, nil, e)
	if len(e.requests) != 1 {
		t.Fatalf("requests: %+v", e.requests)
	}
	r := e.requests[0]
	if r.SessionID != "actual-session" || r.Section != 2 || r.Offset != 16 || r.Limit != 32 || r.Member != "report.pdf" {
		t.Fatalf("bad request: %+v", r)
	}
	if !e.find(protocol.NotifyItemCompleted, "page two from original") {
		t.Fatal("host file result missing")
	}
}
func TestAttachmentToolFailsClearlyWithoutDesktopHost(t *testing.T) {
	m := &fakeModel{script: []string{sseToolCalls([3]string{"read", "read_attachment", `{}`}), sseText("Unavailable")}}
	s := attachmentSession(t, m, true)
	e := &recordingEmitter{}
	s.RunTurn(context.Background(), "turn", "Read attachment", nil, nil, e)
	if !e.find(protocol.NotifyItemCompleted, "requires the desktop host") {
		t.Fatal("missing safe host error")
	}
}
