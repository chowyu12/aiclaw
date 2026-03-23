package agent

import (
	"fmt"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

func TestToolLoopDetector_consecutiveIdentical(t *testing.T) {
	d := newToolLoopDetector(log.NewEntry(log.StandardLogger()))
	for i := range 4 {
		blocked, _ := d.check("web_fetch", `{"url":"https://x.com"}`)
		if blocked {
			t.Fatalf("call %d: unexpected block", i+1)
		}
		d.record("web_fetch", `{"url":"https://x.com"}`)
	}
	blocked, msg := d.check("web_fetch", `{"url":"https://x.com"}`)
	if !blocked {
		t.Fatal("expected block on 5th identical call")
	}
	if !strings.Contains(msg, "loop_guard") {
		t.Fatalf("message should mention loop_guard, got %q", msg)
	}
}

func TestToolLoopDetector_toolSearchBurst(t *testing.T) {
	d := newToolLoopDetector(log.NewEntry(log.StandardLogger()))
	// 每次不同参数，避免命中「连续相同调用」规则，只测窗口内 tool_search 次数上限
	for i := range loopToolSearchMaxInWindow {
		args := fmt.Sprintf(`{"query":"q%d"}`, i)
		blocked, _ := d.check(toolSearchName, args)
		if blocked {
			t.Fatalf("call %d: unexpected block", i+1)
		}
		d.record(toolSearchName, args)
	}
	blocked, msg := d.check(toolSearchName, `{"query":"final"}`)
	if !blocked {
		t.Fatal("expected block after too many tool_search in window")
	}
	if !strings.Contains(msg, "tool_search") {
		t.Fatalf("expected tool_search hint in %q", msg)
	}
}

func TestToolLoopDetector_pingPong(t *testing.T) {
	d := newToolLoopDetector(log.NewEntry(log.StandardLogger()))
	d.record("a", `{}`)
	d.record("b", `{}`)
	d.record("a", `{}`)
	d.record("b", `{}`)
	d.record("a", `{}`)
	blocked, _ := d.check("b", `{}`)
	if !blocked {
		t.Fatal("expected ping-pong block on completing A,B,A,B,A,B")
	}
}

func TestUseLazyToolSearch(t *testing.T) {
	if UseLazyToolSearch(true, ToolSearchAutoFullThreshold) {
		t.Error("at threshold should not use lazy mode")
	}
	if UseLazyToolSearch(true, ToolSearchAutoFullThreshold+1) {
		// ok
	} else {
		t.Error("above threshold should use lazy mode")
	}
	if UseLazyToolSearch(false, 100) {
		t.Error("disabled agent flag should not use lazy mode")
	}
}
