package agent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

const audioBudgetMS int64 = 180_000
const audioFileLimit = 8 << 20

func nativeAudio(config protocol.ModelConfig) bool {
	api := config.APIProtocol.Normalized()
	return config.Capabilities.AudioInput == "supported" && (api == model.APIChatCompletions || api == model.APIGemini)
}
func audioRoute(config protocol.ModelConfig) string {
	return fmt.Sprintf("%d|%s|%s|%s", config.ProviderID, config.BaseURL, config.APIProtocol.Normalized(), config.Model)
}

// Duration comes from the WAV data size and byte rate, not compressed file size.
// Unknown durations consume the entire retention budget so they cannot accumulate.
func audioDuration(data []byte) int64 {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return audioBudgetMS
	}
	var rate, size uint32
	for offset := 12; offset+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if n > len(data)-start {
			return audioBudgetMS
		}
		if string(data[offset:offset+4]) == "fmt " && n >= 16 {
			rate = binary.LittleEndian.Uint32(data[start+8 : start+12])
		}
		if string(data[offset:offset+4]) == "data" {
			size = uint32(n)
		}
		offset = start + n + (n % 2)
	}
	if rate == 0 || size == 0 {
		return audioBudgetMS
	}
	return max(1, int64(size)*1000/int64(rate))
}
func readAudio(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > audioFileLimit {
		return nil, fmt.Errorf("audio must be a regular file of at most 8 MiB")
	}
	// Read through the opened descriptor; limit even if the file grows after Stat.
	data, err := io.ReadAll(io.LimitReader(f, audioFileLimit+1))
	if err == nil && len(data) > audioFileLimit {
		err = fmt.Errorf("audio exceeds 8 MiB")
	}
	return data, err
}
func (s *Session) prepareAttachedAudio(ctx context.Context, paths []string) (string, []llm.Audio) {
	if !nativeAudio(s.config.Model) {
		return s.transcribeAttached(ctx, paths), nil
	}
	var audio []llm.Audio
	var fallback []string
	var note strings.Builder
	var used int64
	for _, path := range paths {
		format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if format != "wav" && format != "mp3" {
			fallback = append(fallback, path)
			continue
		}
		data, err := readAudio(path)
		if err != nil {
			fmt.Fprintf(&note, "\n[Audio %s was not sent: %v]", filepath.Base(path), err)
			continue
		}
		duration := audioDuration(data)
		if used+duration > audioBudgetMS {
			fmt.Fprintf(&note, "\n[Audio %s was not sent: native audio exceeds the 180-second request budget]", filepath.Base(path))
			continue
		}
		used += duration
		audio = append(audio, llm.Audio{Path: path, Format: format, DurationMS: duration, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	return note.String() + s.transcribeAttached(ctx, fallback), audio
}

// Materialize only the newest clips fitting the duration budget. Paths and local
// rejection evidence stay in storage; adapter requests receive bytes only.
func (s *Session) audioMessages(config protocol.ModelConfig) []llm.Message {
	messages := s.snapshotMessages()
	route := audioRoute(config)
	var used int64
	for i := len(messages) - 1; i >= 0; i-- {
		original := messages[i].Audio
		messages[i].Audio = nil
		for _, audio := range original {
			reason := ""
			switch {
			case slices.Contains(audio.RejectedRoutes, route):
				reason = "previously rejected by this model route"
			case !nativeAudio(config):
				reason = "this model route does not accept native audio"
			case used+audio.DurationMS > audioBudgetMS:
				reason = "outside the retained audio duration budget"
			default:
				data, err := readAudio(audio.Path)
				if err != nil {
					reason = "local audio is unavailable"
				} else if audio.SHA256 != "" && audio.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
					reason = "local audio changed since attachment"
				} else {
					used += audio.DurationMS
					audio.Data = data
					messages[i].Audio = append(messages[i].Audio, audio)
				}
			}
			if reason != "" {
				messages[i].Content += "\n[Audio " + filepath.Base(audio.Path) + " omitted: " + reason + "]"
			}
		}
	}
	return messages
}
func (s *Session) recordAudioRejection(config protocol.ModelConfig, messages []llm.Message, err error) {
	var apiErr *llm.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		return
	}
	switch apiErr.Code {
	case "invalid_audio", "audio_decoding_error", "unsupported_audio_format":
	default:
		return
	}
	// A request-level error cannot identify which of multiple clips was rejected.
	var paths []string
	for _, m := range messages {
		for _, a := range m.Audio {
			paths = append(paths, a.Path)
		}
	}
	if len(paths) != 1 {
		return
	}
	route := audioRoute(config)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		s.messages[i].Audio = append([]llm.Audio(nil), s.messages[i].Audio...)
		for j := range s.messages[i].Audio {
			a := &s.messages[i].Audio[j]
			if a.Path == paths[0] && !slices.Contains(a.RejectedRoutes, route) {
				a.RejectedRoutes = append(append([]string(nil), a.RejectedRoutes...), route)
			}
		}
	}
}
