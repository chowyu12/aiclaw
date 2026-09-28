package store

import (
	"context"
	"testing"
	"time"
)

// 用量：记下来的每一次模型调用、工具调用，按时间段汇总成设置页要的那几组数。
func TestUsageSummary(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Save(ctx, Session{ID: "s1", Title: "查航班", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	loc := time.FixedZone("CST", 8*3600)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, loc)
	threeDaysAgo := today.AddDate(0, 0, -3)
	longAgo := today.AddDate(0, 0, -40)

	for _, event := range []UsageEvent{
		{At: today, SessionID: "s1", Kind: UsageModel, Model: "deepseek", Input: 1000, Output: 200, Total: 1200},
		{At: today, SessionID: "s1", Kind: UsageModel, Model: "deepseek", Input: 500, Output: 100, Total: 600, Failed: true},
		{At: threeDaysAgo, SessionID: "s2", Kind: UsageModel, Model: "qwen", Input: 300, Output: 50, Total: 350},
		{At: today, SessionID: "s1", Kind: UsageTool, Tool: "read_file", Source: "builtin", DurationMS: 20},
		{At: today, SessionID: "s1", Kind: UsageTool, Tool: "read_file", Source: "builtin", DurationMS: 40, Failed: true},
		{At: today, SessionID: "s1", Kind: UsageTool, Tool: "load_skill", Source: "builtin", Detail: "storage-analyzer"},
		{At: today, SessionID: "s1", Kind: UsageTool, Tool: "aihot__search", Source: "mcp:aihot"},
		// 范围之外的不算。
		{At: longAgo, SessionID: "s3", Kind: UsageModel, Model: "old", Input: 99999, Total: 99999},
	} {
		db.RecordUsage(event)
	}
	db.flushUsage()

	since := today.AddDate(0, 0, -6)
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	summary, err := db.Usage(ctx, since, loc)
	if err != nil {
		t.Fatal(err)
	}
	totals := summary.Totals
	if totals.Input != 1800 || totals.Output != 350 || totals.Total != 2150 {
		t.Errorf("token 总量不对：%+v", totals)
	}
	if totals.ModelCalls != 3 || totals.ModelFailed != 1 || totals.ToolCalls != 4 || totals.ToolFailed != 1 || totals.Sessions != 2 {
		t.Errorf("次数不对：%+v", totals)
	}
	if len(summary.Days) != 7 {
		t.Fatalf("7 天应有 7 根柱子（没用的日子补 0）：%d", len(summary.Days))
	}
	if last := summary.Days[6]; last.Day != today.Format("2006-01-02") || last.Input != 1500 || last.ToolCalls != 4 {
		t.Errorf("今天那一格不对：%+v", last)
	}
	if summary.Days[3].Input != 300 {
		t.Errorf("三天前那一格不对：%+v", summary.Days[3])
	}
	if len(summary.Models) != 2 || summary.Models[0].Model != "deepseek" || summary.Models[0].Calls != 2 || summary.Models[0].Failed != 1 {
		t.Errorf("按模型：%+v", summary.Models)
	}
	if len(summary.Tools) != 3 || summary.Tools[0].Tool != "read_file" || summary.Tools[0].Calls != 2 || summary.Tools[0].AvgMS != 30 {
		t.Errorf("按工具：%+v", summary.Tools)
	}
	if len(summary.Skills) != 1 || summary.Skills[0].Skill != "storage-analyzer" {
		t.Errorf("按技能：%+v", summary.Skills)
	}
	if len(summary.Sessions) != 2 || summary.Sessions[0].SessionID != "s1" || summary.Sessions[0].Title != "查航班" {
		t.Errorf("按会话（标题从会话表补）：%+v", summary.Sessions)
	}
	if summary.FirstAt != longAgo.UnixMilli() {
		t.Errorf("最早一条的时间：%d", summary.FirstAt)
	}
}

func TestToolSource(t *testing.T) {
	if ToolSource("aihot__search") != "mcp:aihot" || ToolSource("read_file") != "builtin" {
		t.Error("MCP 工具按 server__tool 认")
	}
}
