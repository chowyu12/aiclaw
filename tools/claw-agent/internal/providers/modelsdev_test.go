package providers

import (
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// models.dev 的解析。
//
// 映射错了不会报错，只会「我同步了，那个视觉模型还是不在候选里」——用户
// 无从排查，所以按真实数据的形状逐条钉。样本取自那份表里实际出现过的组合。
const sample = `{
  "openrouter": {
    "models": {
      "google/gemini-3-pro": {
        "id": "google/gemini-3-pro",
        "modalities": {"input": ["audio","image","pdf","text","video"], "output": ["text"]}
      },
      "openai/gpt-5.4": {
        "id": "openai/gpt-5.4",
        "modalities": {"input": ["text"], "output": ["text"]}
      }
    }
  },
  "digitalocean": {
    "models": {
      "stable-diffusion-3.5-large": {
        "id": "stable-diffusion-3.5-large",
        "modalities": {"input": ["text"], "output": ["image"]}
      },
      "mimo-tts": {
        "id": "mimo-tts",
        "modalities": {"input": ["text"], "output": ["audio"]}
      },
      "green-s": {
        "id": "green-s",
        "modalities": {"input": ["audio"], "output": ["text"]}
      },
      "only-video": {
        "id": "only-video",
        "modalities": {"input": ["text"], "output": ["video"]}
      }
    }
  }
}`

func catalog(t *testing.T) *Catalog {
	t.Helper()
	parsed, err := ParseCatalog([]byte(sample))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	return parsed
}

func TestRolesFromModalities(t *testing.T) {
	c := catalog(t)
	for _, tc := range []struct {
		model string
		want  []protocol.ModelRole
		why   string
	}{
		{"google/gemini-3-pro", []protocol.ModelRole{protocol.RoleVision, protocol.RoleSTT}, "输入有图有音频"},
		{"openai/gpt-5.4", nil, "纯文本模型不该有任何标记"},
		{"stable-diffusion-3.5-large", []protocol.ModelRole{protocol.RoleImage}, "输出是图"},
		{"mimo-tts", []protocol.ModelRole{protocol.RoleTTS}, "输出是音频"},
		{"green-s", []protocol.ModelRole{protocol.RoleSTT}, "输入是音频"},
		{"only-video", nil, "视频不映射：内核没有处理它的路径"},
	} {
		got := c.Roles(tc.model)
		if len(got) != len(tc.want) {
			t.Errorf("%s 的能力 = %v，想要 %v（%s）", tc.model, got, tc.want, tc.why)
			continue
		}
		for _, want := range tc.want {
			if !hasRole(got, want) {
				t.Errorf("%s 少了 %s（%s）", tc.model, want, tc.why)
			}
		}
	}
}

func TestRolesFallsBackToBareName(t *testing.T) {
	c := catalog(t)
	// 同一个模型在 OpenRouter 里填 `google/gemini-3-pro`，在官方端点里填
	// `gemini-3-pro`——两种写法都要认出来。
	if roles := c.Roles("gemini-3-pro"); len(roles) == 0 {
		t.Error("去掉 provider 前缀之后应当仍能匹配")
	}
	if roles := c.Roles("完全不存在的模型"); roles != nil {
		t.Errorf("没有的模型应当返回 nil，实际 %v", roles)
	}
}

func TestRolesIgnoresCaseAndSpace(t *testing.T) {
	c := catalog(t)
	if roles := c.Roles("  MIMO-TTS  "); len(roles) != 1 || roles[0] != protocol.RoleTTS {
		t.Errorf("大小写与空格不该影响匹配：%v", roles)
	}
}

func TestRolesDoesNotMatchByPrefix(t *testing.T) {
	c := catalog(t)
	// `gpt-5.4` 与 `gpt-5.4-mini` 是两个能力不同的模型。按前缀匹配会把
	// 前者的能力安到后者头上，那种错没人查得出来。
	if roles := c.Roles("gpt-5.4-mini"); roles != nil {
		t.Errorf("不该按前缀匹配：%v", roles)
	}
}

func TestParseCatalogRejectsGarbage(t *testing.T) {
	if _, err := ParseCatalog([]byte("not json")); err == nil {
		t.Error("坏数据应当报错，而不是当成一张空表——空表会让同步静默无效")
	}
}
