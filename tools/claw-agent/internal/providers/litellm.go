package providers

// 第二份能力表：LiteLLM 的 model_prices_and_context_window.json。
//
// 为什么要第二份。models.dev 是以聊天模型为中心的表，能力用 modalities 表达。
// 两个后果实际踩到了：一是画图模型 qwen-image 因为「输入里有 image」（它能改图）
// 被标成了看图模型，用户真选它去看图，结果是一个 400；二是 whisper、tts、dall-e
// 这类根本不在表里。LiteLLM 那份表多一个显式的 mode 字段——chat / image_generation /
// audio_speech / audio_transcription——「这个模型是干什么的」一眼分明，而且转写、
// 朗读、生图模型收得全。
//
// 两份取并集，但 **mode 说它不是聊天模型时，去掉按输入模态推出来的看图标记**：
// 一个 image_generation 模型接受图片输入是为了改图，不是为了看图回答问题。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// liteLLMURLs 按顺序试。GitHub 的 raw 地址在国内网络上常常超时，jsDelivr 是它的
// CDN 镜像，内容相同、快得多；镜像挂了再退回原地址。
var liteLLMURLs = []string{
	"https://cdn.jsdelivr.net/gh/BerriAI/litellm@main/model_prices_and_context_window.json",
	"https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json",
}

// liteLLMEntry 是那份表里一个模型的、我们用得上的字段。
type liteLLMEntry struct {
	Mode                string `json:"mode"`
	SupportsVision      bool   `json:"supports_vision"`
	SupportsAudioInput  bool   `json:"supports_audio_input"`
	SupportsAudioOutput bool   `json:"supports_audio_output"`
	MaxInputTokens      int    `json:"max_input_tokens"`
}

// ParseLiteLLM 把那份表解析成索引。键是 `provider/model` 或裸模型名，
// 与 models.dev 一样两种写法都收。
func ParseLiteLLM(body []byte) (*Catalog, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("LiteLLM 的数据看不懂：%w", err)
	}
	catalog := &Catalog{byName: map[string]Entry{}}
	for key, value := range raw {
		// 表里有一条 sample_spec 是字段说明，不是模型。
		if key == "sample_spec" {
			continue
		}
		var item liteLLMEntry
		if err := json.Unmarshal(value, &item); err != nil {
			continue // 个别条目形状不对不该拖垮整份表
		}
		entry := liteLLMRoles(item)
		if len(entry.Roles) == 0 && entry.Context == 0 && !entry.NonChat {
			continue
		}
		catalog.add(key, entry)
	}
	return catalog, nil
}

// liteLLMRoles 把 mode 与 supports_* 翻成我们的四个角色。
//
// 生成类 mode 直接对应一个角色；聊天类 mode 看 supports_*。embedding、rerank、
// 视频、实时语音这些没有对应的角色，只带上下文窗口（如果有）。
func liteLLMRoles(item liteLLMEntry) Entry {
	entry := Entry{Context: item.MaxInputTokens}
	switch strings.ToLower(item.Mode) {
	case "image_generation", "image_edit":
		entry.Roles = []protocol.ModelRole{protocol.RoleImage}
		entry.NonChat = true
	case "audio_speech":
		entry.Roles = []protocol.ModelRole{protocol.RoleTTS}
		entry.NonChat = true
	case "audio_transcription":
		entry.Roles = []protocol.ModelRole{protocol.RoleSTT}
		entry.NonChat = true
	case "video_generation", "embedding", "rerank", "moderation", "completion":
		entry.NonChat = true
	default: // chat / responses / realtime
		if item.SupportsVision {
			entry.Roles = append(entry.Roles, protocol.RoleVision)
		}
		if item.SupportsAudioInput {
			entry.Roles = append(entry.Roles, protocol.RoleSTT)
		}
		if item.SupportsAudioOutput {
			entry.Roles = append(entry.Roles, protocol.RoleTTS)
		}
	}
	return entry
}
