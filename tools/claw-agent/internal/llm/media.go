package llm

// 对话之外的三个 OpenAI 兼容端点：文生图、语音转文字、文字转语音。
//
// 与 Stream 共用同一个 Client（同一个端点、同一把 Key、同一个连接池），
// 只是打不同的路径。它们都是一次请求一次结果，没有流式——所以不走 SSE 那套。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// mediaTimeout 是这三个端点的上限。
//
// 比对话宽松：出图动辄十几秒，长音频转写更久；但不能没有上限——上游卡住时
// 用户面前是一个一直转的工具调用，而他不知道该等还是该中断。
const mediaTimeout = 5 * time.Minute

// maxAudioBytes 是送去转写的音频上限。大多数服务自己也卡在 25MB。
const maxAudioBytes = 25 << 20

// GenerateImage 按提示词出一张图，返回 PNG/JPEG 的原始字节。
//
// 兼容两种返回：b64_json（直接给字节）与 url（再去下一次）。DashScope、
// OpenAI 走前者，有些网关走后者——两种都认，省得用户按服务挑写法。
func (c *Client) GenerateImage(ctx context.Context, model, prompt, size string) ([]byte, error) {
	body := map[string]any{"model": model, "prompt": prompt, "n": 1}
	if strings.TrimSpace(size) != "" {
		body["size"] = size
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()

	raw, err := c.postJSON(ctx, "/images/generations", payload)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("图片服务的返回看不懂：%w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("图片服务没有返回任何图片")
	}
	if b64 := parsed.Data[0].B64; b64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("图片数据解不开：%w", err)
		}
		return decoded, nil
	}
	if url := parsed.Data[0].URL; url != "" {
		return c.fetch(ctx, url)
	}
	return nil, fmt.Errorf("图片服务既没给数据也没给地址")
}

// Transcribe 把一段音频转成文字。name 只用来让服务端认出格式。
func (c *Client) Transcribe(ctx context.Context, model, name string, audio []byte) (string, error) {
	if len(audio) > maxAudioBytes {
		return "", fmt.Errorf("音频太大（%.1f MB，上限 %d MB）", float64(len(audio))/(1<<20), maxAudioBytes>>20)
	}
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	part, err := form.CreateFormFile("file", filepath.Base(name))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := form.WriteField("model", model); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", &buffer)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", form.FormDataContentType())
	raw, err := c.do(request)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// 有些服务直接回纯文本。
		return strings.TrimSpace(string(raw)), nil
	}
	return strings.TrimSpace(parsed.Text), nil
}

// Speak 把文字读成音频，返回音频字节与格式后缀。
func (c *Client) Speak(ctx context.Context, model, text, voice string) ([]byte, string, error) {
	if strings.TrimSpace(voice) == "" {
		voice = "alloy"
	}
	payload, err := json.Marshal(map[string]any{
		"model": model, "input": text, "voice": voice, "response_format": "mp3",
	})
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	audio, err := c.do(request)
	if err != nil {
		return nil, "", err
	}
	return audio, "mp3", nil
}

// postJSON 发一次 JSON 请求并读回整个响应体。
func (c *Client) postJSON(ctx context.Context, path string, payload []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	return c.do(request)
}

// do 发请求并读回响应体，把上游的错误原样带出来。
//
// 带出上游的话是刻意的：这里最常见的失败是「这个模型不支持这个端点」或者
// 「这把 Key 没开这个能力」，而那两句只有上游说得清楚。
func (c *Client) do(request *http.Request) ([]byte, error) {
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败：%w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 %d：%s", response.StatusCode, upstreamMessage(body))
	}
	return body, nil
}

// fetch 取一个 URL 的内容。图片服务给 url 而不是字节时用。
func (c *Client) fetch(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// 不带 Authorization：那是图床的临时地址，把 Key 发给第三方没有道理。
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载生成的图片失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载生成的图片失败：HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 64<<20))
}

// upstreamMessage 从错误响应里挑出那句人话。
func upstreamMessage(body []byte) string {
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return parsed.Error.Message
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300]
	}
	return text
}
