package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewWriter(w http.ResponseWriter) (*Writer, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return &Writer{w: w, flusher: flusher}, true
}

func (s *Writer) WriteEvent(event, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *Writer) WriteJSON(event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.WriteEvent(event, string(data))
}

func (s *Writer) WriteDone() error {
	return s.WriteEvent("", "[DONE]")
}
