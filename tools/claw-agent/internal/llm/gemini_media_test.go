package llm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

func TestNativeMediaOperationsUseProtocolAndPreserveAudioFormat(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/fixture:generateContent" || r.Header.Get("x-goog-api-key") != "fixture-key" || r.Header.Get("Authorization") != "" {
			t.Error("wrong native media transport", r.URL.Path)
		}
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		requests = append(requests, string(raw))
		part := `{"text":"transcribed text"}`
		if strings.Contains(string(raw), `"IMAGE"`) {
			part = `{"inlineData":{"mimeType":"image/png","data":"AQID"}}`
		}
		if strings.Contains(string(raw), `"AUDIO"`) {
			part = `{"inlineData":{"mimeType":"audio/L16;codec=pcm;rate=24000","data":"AQIDBA=="}}`
		}
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[`+part+`]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()
	c, err := NewConfigured(Config{BaseURL: srv.URL + "/v1beta", APIKey: "fixture-key", Protocol: model.APIGemini, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	image, err := c.GenerateImage(context.Background(), "fixture", "draw", "")
	if err != nil || !bytes.Equal(image, []byte{1, 2, 3}) {
		t.Fatal("image operation failed", image, err)
	}
	text, err := c.Transcribe(context.Background(), "fixture", "test.wav", []byte{1, 2, 3})
	if err != nil || text != "transcribed text" {
		t.Fatal("native transcription failed", text, err)
	}
	audio, ext, err := c.Speak(context.Background(), "fixture", "hello", "")
	if err != nil || ext != "wav" || len(audio) != 48 || string(audio[:4]) != "RIFF" || binary.LittleEndian.Uint32(audio[24:28]) != 24000 || !bytes.Equal(audio[44:], []byte{1, 2, 3, 4}) {
		t.Fatal("PCM was not wrapped as playable WAV", audio, ext, err)
	}
	if len(requests) != 3 || !strings.Contains(requests[1], "AQID") || !strings.Contains(requests[2], `"voiceName":"Kore"`) {
		t.Fatal("native payload lost input/voice", requests)
	}
	if _, err = c.GenerateImage(context.Background(), "fixture", "draw", "1024x1024"); err == nil || len(requests) != 3 {
		t.Fatal("unsupported native image option was silently ignored")
	}
	c, _ = NewConfigured(Config{BaseURL: srv.URL + "/v1beta", APIKey: "fixture-key", Protocol: model.APIAnthropic, Timeout: time.Second})
	if _, err = c.GenerateImage(context.Background(), "fixture", "draw", ""); err == nil {
		t.Fatal("unsupported Claude generation exposed")
	}
	if _, err = c.Transcribe(context.Background(), "fixture", "test.wav", []byte{1}); err == nil {
		t.Fatal("unsupported Claude transcription exposed")
	}
	if _, _, err = c.Speak(context.Background(), "fixture", "text", ""); err == nil {
		t.Fatal("unsupported Claude speech exposed")
	}
	if len(requests) != 3 {
		t.Fatal("unsupported operations made network requests")
	}
}

func TestUnsignedImageDownloadsMayRedirectWithoutAPIHeaders(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" || r.Header.Get("x-goog-api-key") != "" {
			t.Error("image download leaked API credentials")
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/image", 302)
		} else {
			w.Write([]byte("image fixture"))
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "private-key", time.Second)
	data, err := c.fetch(context.Background(), srv.URL+"/start")
	if err != nil || string(data) != "image fixture" || calls != 2 {
		t.Fatal("unsigned image redirect failed", string(data), calls, err)
	}
}
