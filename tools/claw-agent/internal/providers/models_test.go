package providers

import (
	"reflect"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 清单按模型名合并：从端点拉回来的裸名字与已经标了能力的同名项不该成两条。
func TestMergeModelEntriesUnifiesByName(t *testing.T) {
	got := MergeModelEntries([]string{
		"qwen-image-3.0#vision,image",
		"gpt-5.4@272000",
		"qwen-image-3.0",
		"qwen-image-3.0#image@8192",
		" ",
		"GPT-5.4#vision",
	})
	want := []string{"qwen-image-3.0#vision,image@8192", "gpt-5.4#vision@272000"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("合并结果 = %v，想要 %v", got, want)
	}
}

func TestGuessRolesByName(t *testing.T) {
	cases := map[string]protocol.ModelRole{
		"qwen3-tts-flash":            protocol.RoleTTS,
		"MiniMax/speech-02-hd":       protocol.RoleTTS,
		"qwen3-asr-flash-2026-02-10": protocol.RoleSTT,
		"whisper-1":                  protocol.RoleSTT,
		"wan2.7-image-pro":           protocol.RoleImage,
		"gpt-image-2":                protocol.RoleImage,
		"qwen3-vl-plus":              protocol.RoleVision,
	}
	for name, want := range cases {
		got := GuessRolesByName(name)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s 猜成 %v，想要 %s", name, got, want)
		}
	}
	for _, name := range []string{"deepseek-v4.1-flash", "qwen3.5-omni-plus", "gpt-5.4"} {
		if got := GuessRolesByName(name); len(got) != 0 {
			t.Errorf("%s 不该猜出能力：%v", name, got)
		}
	}
}
