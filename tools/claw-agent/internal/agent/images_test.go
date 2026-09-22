package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 贴进来的图片要一路走到模型，并且在恢复会话时还画得出来。
//
// 三条边界分别有各自的失败方式：张数/大小不兜住会把上下文和会话库一起撑坏；
// 历史里不还原的话，点开旧会话「这是什么问题」下面的截图没了，对话读不懂；
// 压缩时不丢图的话，压缩本身救不回这个会话——图往往比前后所有文字都贵。

func png(size int) []byte { return bytes.Repeat([]byte{0x89}, size) }

func TestUserImagesReachTheModelAndTheTimeline(t *testing.T) {
	model := &fakeModel{script: []string{sseText("看到了。")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	emitter := &recordingEmitter{approve: true}

	session.RunTurn(context.Background(), "t1", "这是什么问题", [][]byte{png(64)}, nil, emitter)

	var user *llm.Message
	for i := range session.messages {
		if session.messages[i].Role == llm.RoleUser {
			user = &session.messages[i]
		}
	}
	if user == nil || len(user.Images) != 1 {
		t.Fatalf("用户消息上应当带着 1 张图，实际 %+v", user)
	}
	if !emitter.find(protocol.NotifyItemCompleted, `"images"`) {
		t.Errorf("用户条目里应当带上图片，事件：%v", emitter.methods())
	}
}

func TestHistoryRestoresUserImages(t *testing.T) {
	model := &fakeModel{script: []string{sseText("看到了。")}}
	session := newTestSession(t, model, protocol.ApprovalOnWrite)
	session.RunTurn(context.Background(), "t1", "这是什么问题", [][]byte{png(64)}, nil, &recordingEmitter{approve: true})

	ctx := context.Background()
	db := newTestStore(t)
	if err := session.Save(ctx, db); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(ctx, db, "test", StaticKey("sk-test"), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(loaded.Close)

	for _, item := range loaded.History() {
		if item.Kind == protocol.ItemUserMessage {
			if len(item.Images) != 1 {
				t.Errorf("恢复出来的用户条目应当还带着那张图，实际 %d 张", len(item.Images))
			}
			return
		}
	}
	t.Error("历史里没有用户条目")
}

func TestOversizedOrTooManyImagesAreDropped(t *testing.T) {
	// 4 张封顶，单张 4MB 封顶。超限的整张丢掉而不是截断——截断的图片解不出来，
	// 上游只会回一个看不懂的 400。
	kept := limitImages([][]byte{png(10), png(10), png(10), png(10), png(10)})
	if len(kept) != 4 {
		t.Errorf("应当只留 4 张，实际 %d", len(kept))
	}
	if got := limitImages([][]byte{png(5 << 20)}); got != nil {
		t.Errorf("超大的单张应当丢掉，实际留了 %d 字节", len(got[0]))
	}
	if got := limitImages([][]byte{png(3 << 20), png(3 << 20), png(3 << 20)}); len(got) != 2 {
		t.Errorf("总量超过 8MB 时应当只留前两张，实际 %d 张", len(got))
	}
	if limitImages(nil) != nil {
		t.Error("没有图片时应当仍是 nil")
	}
}

func TestCompactionDropsImages(t *testing.T) {
	out := buildCompactedHistory([]llm.Message{
		{Role: llm.RoleSystem, Content: "系统"},
		{Role: llm.RoleUser, Content: "看这张图", Images: [][]byte{png(64)}},
	}, "摘要")
	for _, message := range out {
		if len(message.Images) != 0 {
			t.Fatalf("压缩后的历史里不该还有图片：%+v", message)
		}
	}
	if !strings.Contains(out[len(out)-1].Content, "摘要") {
		t.Error("摘要应当在最后一条")
	}
}
