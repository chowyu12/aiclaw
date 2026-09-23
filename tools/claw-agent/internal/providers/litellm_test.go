package providers

import (
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// LiteLLM 那份表的解析，以及与 models.dev 合并时的取舍。
// 样本取自那份表里实际的条目形状。
const liteSample = `{
  "sample_spec": {"mode": "one of: chat, embedding, ...", "max_input_tokens": "max input tokens"},
  "whisper-1": {"litellm_provider": "openai", "mode": "audio_transcription"},
  "gpt-4o-mini-tts": {"litellm_provider": "openai", "mode": "audio_speech"},
  "dashscope/qwen-image-3.0": {"litellm_provider": "dashscope", "mode": "image_generation"},
  "dashscope/qwen3-vl-plus": {"mode": "chat", "supports_vision": true, "max_input_tokens": 260096, "max_tokens": 32768},
  "gpt-4o-audio-preview": {"mode": "chat", "supports_audio_input": true, "supports_audio_output": true, "max_input_tokens": 128000},
  "text-embedding-3-small": {"mode": "embedding", "max_input_tokens": 8191},
  "plain-chat": {"mode": "chat", "max_input_tokens": 32000},
  "broken": "not an object"
}`

func liteCatalog(t *testing.T) *Catalog {
	t.Helper()
	parsed, err := ParseLiteLLM([]byte(liteSample))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return parsed
}

func roleList(entry Entry) string {
	var out string
	for _, role := range entry.Roles {
		out += string(role) + ","
	}
	return out
}

func TestLiteLLMModesMapToRoles(t *testing.T) {
	c := liteCatalog(t)
	cases := map[string]string{
		"whisper-1":               "stt,",
		"gpt-4o-mini-tts":         "tts,",
		"qwen-image-3.0":          "image,", // 去前缀的写法也要认
		"dashscope/qwen3-vl-plus": "vision,",
		"gpt-4o-audio-preview":    "stt,tts,",
		"plain-chat":              "",
	}
	for name, want := range cases {
		entry, ok := c.Lookup(name)
		if !ok {
			t.Errorf("%s 应在索引里", name)
			continue
		}
		if got := roleList(entry); got != want {
			t.Errorf("%s 的角色 = %q，想要 %q", name, got, want)
		}
	}
	if entry, _ := c.Lookup("qwen3-vl-plus"); entry.Context != 260096 {
		t.Errorf("窗口应取 max_input_tokens：%d", entry.Context)
	}
	if entry, ok := c.Lookup("text-embedding-3-small"); !ok || !entry.NonChat || len(entry.Roles) != 0 {
		t.Errorf("向量模型：只记「不是聊天模型」，不给角色：%+v ok=%v", entry, ok)
	}
	if _, ok := c.Lookup("sample_spec"); ok {
		t.Error("sample_spec 是字段说明，不是模型")
	}
	if _, ok := c.Lookup("broken"); ok {
		t.Error("形状不对的条目应跳过")
	}
}

// 两份表合并：models.dev 因为「输入里有 image」把画图模型标成了看图模型，
// LiteLLM 说它是 image_generation——后者赢，看图标记去掉，画图标记保留。
func TestMergeDropsVisionFromGenerativeModels(t *testing.T) {
	modelsDev, err := ParseCatalog([]byte(`{
	  "alibaba": {"models": {
	    "qwen-image-3.0": {"id": "qwen-image-3.0", "modalities": {"input": ["text","image"], "output": ["image"]}},
	    "qwen3-vl-plus": {"id": "qwen3-vl-plus", "modalities": {"input": ["text","image"], "output": ["text"]}, "limit": {"context": 256000}}
	  }}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	merged := &Catalog{byName: map[string]Entry{}}
	// 两个顺序都试：先并谁不该影响结果。
	for _, order := range [][]*Catalog{{modelsDev, liteCatalog(t)}, {liteCatalog(t), modelsDev}} {
		merged = &Catalog{byName: map[string]Entry{}}
		for _, source := range order {
			merged.merge(source)
		}
		image, _ := merged.Lookup("qwen-image-3.0")
		if roleList(image) != "image," {
			t.Errorf("画图模型合并后 = %q，应只有 image", roleList(image))
		}
		vl, _ := merged.Lookup("qwen3-vl-plus")
		if roleList(vl) != "vision," || vl.Context != 260096 {
			t.Errorf("看图模型合并后 = %q 窗口 %d，应为 vision、窗口取大的 260096", roleList(vl), vl.Context)
		}
	}
	// 只在一份表里的也都在。
	for _, name := range []string{"whisper-1", "gpt-4o-mini-tts"} {
		if _, ok := merged.Lookup(name); !ok {
			t.Errorf("%s 只在 LiteLLM 里，合并后应能查到", name)
		}
	}
}

func TestParseLiteLLMRejectsGarbage(t *testing.T) {
	if _, err := ParseLiteLLM([]byte("nope")); err == nil {
		t.Error("坏数据应当报错")
	}
}

// 用户手动勾的标记不受合并影响：AutoMark 只加不减，这里确认 catalog 层的
// 「去 vision」只作用在表与表之间。
func TestAutoMarkKeepsManualVisionEvenWhenTablesDisagree(t *testing.T) {
	parsed := protocol.ParseModelMark("qwen-image-3.0#vision,image")
	entry := Entry{Roles: []protocol.ModelRole{protocol.RoleImage}, NonChat: true}
	for _, role := range entry.Roles {
		if !hasRole(parsed.Roles, role) {
			parsed.Roles = append(parsed.Roles, role)
		}
	}
	if got := protocol.FormatModelMark(parsed); got != "qwen-image-3.0#vision,image" {
		t.Errorf("手动勾的 vision 不该被表去掉：%s", got)
	}
}
