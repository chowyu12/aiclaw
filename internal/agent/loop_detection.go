package agent

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	loopHistoryMax = 32

	// 连续完全相同的工具调用（名称+参数）达到该次数后，下一次调用将被拦截。
	loopConsecutiveIdenticalBlock = 5

	// 在最近 loopToolSearchWindow 次工具调用中，tool_search 超过该次数则拦截下一次 tool_search。
	loopToolSearchWindow          = 12
	loopToolSearchMaxInWindow     = 6
	loopPingPongTailLen           = 6
)

type loopToolCall struct {
	name string
	args string
}

// toolLoopDetector 在一次 Execute / ExecuteStream 会话内检测无进展的重复工具调用（参考 OpenClaw loop-detection 思路的轻量实现）。
type toolLoopDetector struct {
	history []loopToolCall
	l       *log.Entry
}

func newToolLoopDetector(l *log.Entry) *toolLoopDetector {
	if l == nil {
		l = log.NewEntry(log.StandardLogger())
	}
	return &toolLoopDetector{l: l}
}

func (d *toolLoopDetector) reset() {
	d.history = d.history[:0]
}

// check 若本次调用应被拦截，返回 true 及给模型看的说明（不写入历史）。
func (d *toolLoopDetector) check(name, args string) (blocked bool, message string) {
	a := strings.TrimSpace(args)

	if n := d.consecutiveTail(name, a); n >= loopConsecutiveIdenticalBlock-1 {
		return true, fmt.Sprintf(
			"[loop_guard] 已连续 %d 次完全相同的 %s 调用，疑似陷入循环。请改变参数、换用其他工具，或直接给出最终回答。",
			n+1, name)
	}

	if name == toolSearchName {
		if c := d.countNameInWindow(toolSearchName, loopToolSearchWindow); c >= loopToolSearchMaxInWindow {
			return true, "[loop_guard] tool_search 在最近若干次调用中过于频繁。当前列表中已有可用工具，请直接调用它们完成任务，勿再搜索。"
		}
	}

	if d.wouldCompletePingPong(name) {
		return true, "[loop_guard] 检测到两个工具交替重复调用（ping-pong），无进展。请合并步骤、换工具或输出结论。"
	}

	return false, ""
}

func (d *toolLoopDetector) record(name, args string) {
	a := strings.TrimSpace(args)
	d.history = append(d.history, loopToolCall{name: name, args: a})
	if len(d.history) > loopHistoryMax {
		d.history = d.history[len(d.history)-loopHistoryMax:]
	}
}

func (d *toolLoopDetector) consecutiveTail(name, args string) int {
	n := 0
	for i := len(d.history) - 1; i >= 0; i-- {
		if d.history[i].name == name && d.history[i].args == args {
			n++
		} else {
			break
		}
	}
	return n
}

func (d *toolLoopDetector) countNameInWindow(toolName string, window int) int {
	if window <= 0 || len(d.history) == 0 {
		return 0
	}
	start := len(d.history) - window
	if start < 0 {
		start = 0
	}
	c := 0
	for i := start; i < len(d.history); i++ {
		if d.history[i].name == toolName {
			c++
		}
	}
	return c
}

// wouldCompletePingPong：若将 name 追加到历史末尾，最后 loopPingPongTailLen 个均为 A,B,A,B,A,B（A≠B），则视为 ping-pong。
func (d *toolLoopDetector) wouldCompletePingPong(name string) bool {
	if len(d.history) < loopPingPongTailLen-1 {
		return false
	}
	tail := make([]string, 0, loopPingPongTailLen)
	from := len(d.history) - (loopPingPongTailLen - 1)
	for i := from; i < len(d.history); i++ {
		tail = append(tail, d.history[i].name)
	}
	tail = append(tail, name)
	return isStrictAlternatingPairPattern(tail)
}

func isStrictAlternatingPairPattern(names []string) bool {
	if len(names) != loopPingPongTailLen {
		return false
	}
	a, b := names[0], names[1]
	if a == b {
		return false
	}
	for i, n := range names {
		want := a
		if i%2 == 1 {
			want = b
		}
		if n != want {
			return false
		}
	}
	return true
}
