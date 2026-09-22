package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

/*
current_time：现在几点、今天几号。

模型不知道今天是哪天——它只知道自己训练到什么时候。于是「最近三十期」「上个月的
数据」「今天的新闻」这类要求全都会算错，而且**错得很自信**，没有任何报错。

系统提示词里已经写了一次开会话时的时间，但那只在开会话那一刻正确：一个会话跨
午夜、或者隔天再打开继续聊，提示词里那行就是旧的。所以再给一个工具，让模型在
真的需要算日期时能问一次。两者都留着：提示词那行管住大多数情况（不花一次调用），
工具管住跨天和「现在几点」。
*/
func currentTimeTool() Tool {
	return Tool{
		Name: "current_time",
		Description: "取本机当前的日期与时间（含星期与时区）。" +
			"凡是要算「今天/昨天/最近 N 天」「这个月」这类相对时间，先调它，不要猜。",
		Effect: EffectRead,
		Schema: schema(map[string]any{}),
		Handler: func(_ context.Context, _ json.RawMessage, _ *Env) (string, error) {
			return DescribeNow(time.Now()), nil
		},
	}
}

// DescribeNow 把一个时刻写成给模型看的一行。
//
// 导出是因为系统提示词也用它——两处用同一种写法，模型不用适应两种格式。
// 带上星期与时区：「最近一周」按周几算，而时区错了整件事就偏一天。
func DescribeNow(now time.Time) string {
	weekdays := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	zone, offset := now.Zone()
	return fmt.Sprintf(
		"%s %s %s（时区 %s，UTC%+d）",
		now.Format("2006-01-02"),
		weekdays[int(now.Weekday())],
		now.Format("15:04"),
		zone,
		offset/3600,
	)
}
