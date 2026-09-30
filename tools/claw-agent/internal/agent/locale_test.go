package agent

import (
	"testing"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
)

// 界面切到英文之后，给人看的文字跟着换；而认这些文字的地方两种语言都认。
func TestEnglishLocale(t *testing.T) {
	i18n.SetDefault(i18n.English)
	defer i18n.SetDefault(i18n.Chinese)

	if got := summarizeCall(llm.ToolCall{Name: "interrupt_agent", Arguments: `{"target":"worker"}`}); got != "Interrupt worker" {
		t.Errorf("步骤摘要：%q", got)
	}
	result := toolErrorResult("boom")
	if result != "Error: boom" {
		t.Errorf("失败结果：%q", result)
	}
	// 旧存档里是中文前缀，新的是英文前缀：都算失败。
	for _, output := range []string{result, "错误：boom"} {
		if !isToolErrorResult(output) {
			t.Errorf("没认出失败结果：%q", output)
		}
	}
	if isToolErrorResult("all good") {
		t.Error("正常结果被当成失败")
	}
	if !IsInterrupted(i18n.D("已中断")) || !IsInterrupted("已中断") || IsInterrupted("boom") {
		t.Error("中断认错了")
	}
	status := i18n.D("已挂载 {n} 个工具（约占 {tokens} 上下文）", "n", 3, "tokens", "1K")
	if !isMountedStatus(status) || !isMountedStatus("已挂载 3 个工具") {
		t.Errorf("挂载状态没认出来：%q", status)
	}
	if isMountedStatus(i18n.D("挂载失败：{err}", "err", "x")) {
		t.Error("挂载失败被当成挂载成功")
	}
}
