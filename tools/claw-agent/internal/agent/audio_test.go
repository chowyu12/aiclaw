package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func wavFixture(seconds int) []byte {
	b := make([]byte, 44+seconds*16000*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}
func TestAudioDurationBudgetFallbackAndDurableRejection(t *testing.T) {
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	config := s.config.Model
	config.APIProtocol = model.APIChatCompletions
	config.Capabilities.AudioInput = "supported"
	s.config.Model = config
	path := filepath.Join(t.TempDir(), "clip.wav")
	if err := os.WriteFile(path, wavFixture(2), 0600); err != nil {
		t.Fatal(err)
	}
	if got := audioDuration(wavFixture(2)); got != 2000 {
		t.Fatal(got)
	}
	if audioDuration([]byte("mp3")) != audioBudgetMS {
		t.Fatal("unknown duration must consume whole budget")
	}
	note, audio := s.prepareAttachedAudio(context.Background(), []string{path})
	if note != "" || len(audio) != 1 {
		t.Fatalf("%s %+v", note, audio)
	}
	s.messages = append(s.messages, llm.Message{ID: "u", Role: llm.RoleUser, Content: "listen", Audio: audio})
	messages := s.audioMessages(config)
	if len(messages[len(messages)-1].Audio[0].Data) == 0 {
		t.Fatal("missing wire bytes")
	}
	s.recordAudioRejection(config, messages, &llm.Error{Status: 400, Code: "invalid_request"})
	if len(s.audioMessages(config)[len(messages)-1].Audio) != 1 {
		t.Fatal("generic 400 must not reject clip")
	}
	s.recordAudioRejection(config, messages, &llm.Error{Status: 400, Code: "invalid_audio"})
	db := newTestStore(t)
	if err := s.Save(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	saved, err := db.Load(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var restored []llm.Message
	if err = json.Unmarshal(saved.Messages, &restored); err != nil {
		t.Fatal(err)
	}
	s.messages = restored
	filtered := s.audioMessages(config)
	if len(filtered[len(filtered)-1].Audio) != 0 || !strings.Contains(filtered[len(filtered)-1].Content, "previously rejected") {
		t.Fatal("rejection not durable")
	}
	config.Model += "-another"
	if len(s.audioMessages(config)[len(filtered)-1].Audio) != 1 {
		t.Fatal("rejection leaked to another model")
	}
	s.config.Model.Capabilities.AudioInput = ""
	note, audio = s.prepareAttachedAudio(context.Background(), []string{path})
	if len(audio) != 0 || !strings.Contains(note, "speech-to-text") {
		t.Fatal("unknown capability must fall back to STT")
	}
	// Retain newest clips, never silently send all historical audio.
	s.messages = []llm.Message{{Content: "old", Audio: []llm.Audio{{Path: path, Format: "wav", DurationMS: audioBudgetMS}}}, {Content: "new", Audio: []llm.Audio{{Path: path, Format: "wav", DurationMS: 2000}}}}
	filtered = s.audioMessages(config)
	if len(filtered[0].Audio) != 0 || len(filtered[1].Audio) != 1 {
		t.Fatal("duration retention budget not enforced")
	}
}

func TestNativeAudioRejectionDoesNotRetryOrReplay(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		if len(requests) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"code":"invalid_audio","message":"fixture audio rejection"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseText("done"))
	}))
	defer server.Close()
	s, err := New(context.Background(), "audio-wire", protocol.SessionStartParams{Model: protocol.ModelConfig{BaseURL: server.URL, Model: "fixture", APIProtocol: model.APIChatCompletions, Capabilities: model.ModelCapabilities{AudioInput: "supported"}}, Workdir: t.TempDir(), ApprovalPolicy: protocol.ApprovalBypass}, StaticKey("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "clip.wav")
	os.WriteFile(path, wavFixture(1), 0600)
	s.RunTurn(context.Background(), "t", "listen", nil, []string{path}, &recordingEmitter{approve: true})
	if len(requests) != 1 || !strings.Contains(requests[0], "input_audio") {
		t.Fatalf("request count/wire: %d", len(requests))
	}
	s.RunTurn(context.Background(), "t2", "continue", nil, nil, &recordingEmitter{approve: true})
	if len(requests) != 2 || strings.Contains(requests[1], "input_audio") || !strings.Contains(requests[1], "previously rejected") {
		t.Fatalf("replayed rejected audio: %+v", requests)
	}
}

func TestUnknownAudioCapabilityUsesConfiguredSTT(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("wrong endpoint %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.FormValue("model") != "transcriber" {
			t.Errorf("wrong role %s", r.FormValue("model"))
		}
		io.WriteString(w, `{"text":"fixture transcript"}`)
	}))
	defer server.Close()
	s := newTestSession(t, &fakeModel{}, protocol.ApprovalBypass)
	s.config.Roles.STT = protocol.RoleModel{ProviderID: 1, Model: "transcriber"}
	s.keyFor = func(config *protocol.ModelConfig) (string, error) { config.BaseURL = server.URL; return "fixture", nil }
	path := filepath.Join(t.TempDir(), "clip.wav")
	os.WriteFile(path, wavFixture(1), 0600)
	note, audio := s.prepareAttachedAudio(context.Background(), []string{path})
	if calls != 1 || len(audio) != 0 || !strings.Contains(note, "fixture transcript") {
		t.Fatalf("fallback failed %d %s", calls, note)
	}
}
