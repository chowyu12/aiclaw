package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type geminiMediaPart struct {
	Text       string `json:"text"`
	InlineData *struct {
		MIME string `json:"mimeType"`
		Data string `json:"data"`
	} `json:"inlineData"`
}

func (c *Client) geminiGenerate(ctx context.Context, model string, parts []any, generation map[string]any) ([]geminiMediaPart, error) {
	name := strings.TrimPrefix(model, "models/")
	if name == "" || strings.ContainsAny(name, "/?#") {
		return nil, fmt.Errorf("invalid Gemini model ID")
	}
	wire := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}}
	if len(generation) > 0 {
		wire["generationConfig"] = generation
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()
	req, err := c.request(ctx, http.MethodPost, "/models/"+url.PathEscape(name)+":generateContent", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	raw, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []geminiMediaPart `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Candidates) == 0 {
		return nil, fmt.Errorf("Gemini returned no media candidate")
	}
	candidate := response.Candidates[0]
	if candidate.FinishReason != "STOP" {
		return nil, incompleteNative(candidate.FinishReason)
	}
	return candidate.Content.Parts, nil
}

func (c *Client) geminiGenerateImage(ctx context.Context, model, prompt, size string) ([]byte, error) {
	if size != "" {
		return nil, fmt.Errorf("Gemini image generation does not accept OpenAI pixel sizes; omit size")
	}
	parts, err := c.geminiGenerate(ctx, model, []any{map[string]string{"text": prompt}}, map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}})
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if part.InlineData != nil && strings.HasPrefix(part.InlineData.MIME, "image/") {
			return base64.StdEncoding.DecodeString(part.InlineData.Data)
		}
	}
	return nil, fmt.Errorf("Gemini returned no generated image")
}

func (c *Client) geminiTranscribe(ctx context.Context, model, name string, audio []byte) (string, error) {
	if len(audio) > maxAudioBytes {
		return "", fmt.Errorf("audio exceeds 25 MiB")
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if mimeType == "" {
		return "", fmt.Errorf("unknown audio format: %s", filepath.Ext(name))
	}
	mimeType = strings.Split(mimeType, ";")[0]
	parts, err := c.geminiGenerate(ctx, model, []any{map[string]string{"text": "Transcribe this audio verbatim. Return only the transcription."}, map[string]any{"inlineData": map[string]string{"mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(audio)}}}, nil)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Gemini returned no transcription")
	}
	return strings.TrimSpace(text.String()), nil
}

func (c *Client) geminiSpeak(ctx context.Context, model, text, voice string) ([]byte, string, error) {
	if voice == "" {
		voice = "Kore"
	}
	parts, err := c.geminiGenerate(ctx, model, []any{map[string]string{"text": text}}, map[string]any{"responseModalities": []string{"AUDIO"}, "speechConfig": map[string]any{"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]string{"voiceName": voice}}}})
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts {
		if part.InlineData == nil || !strings.HasPrefix(part.InlineData.MIME, "audio/") {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
		if err != nil {
			return nil, "", err
		}
		mediaType, params, err := mime.ParseMediaType(part.InlineData.MIME)
		if err != nil {
			return nil, "", err
		}
		if mediaType == "audio/wav" || mediaType == "audio/x-wav" {
			return data, "wav", nil
		}
		if mediaType != "audio/L16" && mediaType != "audio/l16" && mediaType != "audio/pcm" {
			return nil, "", fmt.Errorf("unsupported Gemini audio format: %s", mediaType)
		}
		rate, err := strconv.Atoi(params["rate"])
		if err != nil || rate <= 0 || rate > 192000 {
			return nil, "", fmt.Errorf("missing or invalid PCM sample rate")
		}
		if len(data)%2 != 0 {
			return nil, "", fmt.Errorf("incomplete 16-bit PCM audio")
		}
		var wav bytes.Buffer
		wav.WriteString("RIFF")
		_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(data)))
		wav.WriteString("WAVEfmt ")
		for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
			_ = binary.Write(&wav, binary.LittleEndian, v)
		}
		wav.WriteString("data")
		_ = binary.Write(&wav, binary.LittleEndian, uint32(len(data)))
		wav.Write(data)
		return wav.Bytes(), "wav", nil
	}
	return nil, "", fmt.Errorf("Gemini returned no generated audio")
}
