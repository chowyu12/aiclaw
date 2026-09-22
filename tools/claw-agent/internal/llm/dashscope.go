package llm

// 百炼（DashScope）的原生多模态接口。
//
// 百炼的 OpenAI 兼容模式只覆盖对话与向量：`/images/generations`、`/audio/speech`、
// `/audio/transcriptions` 在 compatible-mode 下都是**空的 404**——没有响应体，
// 用户面前只剩一句「上游返回 404」，从那句话联想不到「这家的画图要走另一条路」。
// 官方文档也明说 Qwen-Image 系列不支持兼容模式。
//
// 所以这里按端点的主机名认出百炼，三件事改打它的原生地址：
//
//	POST {root}/services/aigc/multimodal-generation/generation
//
// 画图、听写、朗读都是这一个地址，靠 model 与 input 的形状区分。root 由用户填的
// 兼容地址推出来（`/compatible-mode/v1` → `/api/v1`），所以「模型服务」页不用
// 多一个字段，Key 也还是同一把。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
)

// dialect 标记一个端点在对话之外的接口上说哪种方言。
type dialect int

const (
	// dialectOpenAI 是默认：OpenAI 的 /images、/audio 那一套。
	dialectOpenAI dialect = iota
	// dialectDashScope 是百炼：多模态走原生 multimodal-generation。
	dialectDashScope
)

// detectDialect 按主机名认端点。
//
// 只看主机名不看路径：用户可能填 `…/compatible-mode/v1`，也可能填带工作空间的
// `{id}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`，两种都是百炼。
func detectDialect(baseURL string) dialect {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return dialectOpenAI
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "dashscope.aliyuncs.com",
		host == "dashscope-intl.aliyuncs.com",
		strings.HasSuffix(host, ".maas.aliyuncs.com"):
		return dialectDashScope
	}
	return dialectOpenAI
}

// dashScopeGenerationURL 从兼容地址推出原生多模态接口的地址。
//
// `https://dashscope.aliyuncs.com/compatible-mode/v1` →
// `https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation`。
// 用户直接填了 `/api/v1` 的也认。
func dashScopeGenerationURL(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	root := strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/compatible-mode/v1")
	root = strings.TrimSuffix(root, "/api/v1")
	parsed.Path = root + "/api/v1/services/aigc/multimodal-generation/generation"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// dashScopeSize 把 OpenAI 写法的尺寸换成百炼写法：`1024x1024` → `1024*1024`。
func dashScopeSize(size string) string {
	size = strings.ToLower(strings.TrimSpace(size))
	return strings.ReplaceAll(size, "x", "*")
}

// dashScopeOutput 是原生接口的响应外壳。三件事的结果都在 output 里，形状不同。
type dashScopeOutput struct {
	Output struct {
		Choices []struct {
			Message struct {
				Content []struct {
					Image string `json:"image"`
					Text  string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Audio struct {
			URL  string `json:"url"`
			Data string `json:"data"`
		} `json:"audio"`
	} `json:"output"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// dashScopeGenerateImage 用 Qwen-Image / 万相出一张图。
func (c *Client) dashScopeGenerateImage(ctx context.Context, model, prompt, size string) ([]byte, error) {
	parameters := map[string]any{"n": 1, "watermark": false, "prompt_extend": true}
	if converted := dashScopeSize(size); converted != "" {
		parameters["size"] = converted
	}
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": map[string]any{
			"messages": []any{map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"text": prompt}},
			}},
		},
		"parameters": parameters,
	})
	if err != nil {
		return nil, err
	}
	var parsed dashScopeOutput
	if err := c.dashScopeCall(ctx, payload, &parsed); err != nil {
		return nil, err
	}
	for _, choice := range parsed.Output.Choices {
		for _, part := range choice.Message.Content {
			if part.Image != "" {
				return c.fetch(ctx, part.Image)
			}
		}
	}
	return nil, fmt.Errorf("图片服务没有返回任何图片")
}

// dashScopeSpeak 用 qwen-tts 系列朗读，返回 wav 字节。
//
// 音色名与 OpenAI 不通（这边是 Cherry、Serena…），调用方没指定时用 Cherry；
// 用户传了 alloy 这种 OpenAI 音色，原样送过去上游会报 400 并说清楚哪些可选。
func (c *Client) dashScopeSpeak(ctx context.Context, model, text, voice string) ([]byte, string, error) {
	if strings.TrimSpace(voice) == "" || strings.EqualFold(voice, "alloy") {
		voice = "Cherry"
	}
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": map[string]any{"text": text, "voice": voice},
	})
	if err != nil {
		return nil, "", err
	}
	var parsed dashScopeOutput
	if err := c.dashScopeCall(ctx, payload, &parsed); err != nil {
		return nil, "", err
	}
	if data := parsed.Output.Audio.Data; data != "" {
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, "", fmt.Errorf("语音数据解不开：%w", err)
		}
		return decoded, "wav", nil
	}
	if link := parsed.Output.Audio.URL; link != "" {
		audio, err := c.fetch(ctx, link)
		if err != nil {
			return nil, "", err
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(stripQuery(link))), ".")
		if ext == "" {
			ext = "wav"
		}
		return audio, ext, nil
	}
	return nil, "", fmt.Errorf("语音服务既没给数据也没给地址")
}

// dashScopeTranscribe 用 qwen-asr 系列听写。音频以 data URI 内联送过去。
func (c *Client) dashScopeTranscribe(ctx context.Context, model, name string, audio []byte) (string, error) {
	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	dataURI := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(audio)
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": map[string]any{
			"messages": []any{
				map[string]any{"role": "system", "content": []any{map[string]any{"text": ""}}},
				map[string]any{"role": "user", "content": []any{map[string]any{"audio": dataURI}}},
			},
		},
		"parameters": map[string]any{"asr_options": map[string]any{"enable_itn": true}},
	})
	if err != nil {
		return "", err
	}
	var parsed dashScopeOutput
	if err := c.dashScopeCall(ctx, payload, &parsed); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, choice := range parsed.Output.Choices {
		for _, part := range choice.Message.Content {
			out.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// dashScopeCall 打一次原生接口并解出响应。
func (c *Client) dashScopeCall(ctx context.Context, payload []byte, into *dashScopeOutput) error {
	ctx, cancel := context.WithTimeout(ctx, mediaTimeout)
	defer cancel()
	raw, err := c.postJSONTo(ctx, dashScopeGenerationURL(c.baseURL), payload)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("百炼的返回看不懂：%w", err)
	}
	// 200 里也可能带业务错误码。
	if into.Code != "" {
		return fmt.Errorf("上游返回 %s：%s", into.Code, into.Message)
	}
	return nil
}

// stripQuery 去掉 URL 的查询串，只为取扩展名。
func stripQuery(link string) string {
	if index := strings.IndexByte(link, '?'); index >= 0 {
		return link[:index]
	}
	return link
}
