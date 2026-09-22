package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// 会话搜索。
//
// 只按标题找不够：标题是第一句话自动截的，而人回来找的往往是中间提过的
// 某个表名、某个报错。所以正文也要找，并且要给出命中的那一段——
// 一列「未命名会话」里挑一个，和看见「…db_data_service 那张表…」，
// 是两种完全不同的体验。

func save(t *testing.T, s *Store, id, title string, contents ...[2]string) {
	t.Helper()
	type message struct {
		Role    string
		Content string
	}
	messages := make([]message, 0, len(contents))
	for _, pair := range contents {
		messages = append(messages, message{Role: pair[0], Content: pair[1]})
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), Session{
		ID: id, Title: title, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Config: json.RawMessage(`{}`), Messages: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSearchFindsKeywordInMessagesAndReturnsSnippet(t *testing.T) {
	s := newStore(t)
	save(t, s, "a", "查销售", [2]string{"user", "帮我看看 db_data_service 这张表昨天的量"})
	save(t, s, "b", "别的事", [2]string{"user", "帮我改一下 README"})

	found, err := s.Search(context.Background(), "db_data_service", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "a" {
		t.Fatalf("应当只命中 a，实际 %+v", found)
	}
	if found[0].Snippet == "" {
		t.Error("命中了却没有片段——搜索结果里最有用的就是这一段")
	}
}

func TestSearchSkipsTheSystemPromptWhenPickingASnippet(t *testing.T) {
	// 系统提示词里有工作目录、工具名、技能说明。拿它当片段，用户看到的是
	// 一段和自己的问题无关的话。
	s := newStore(t)
	save(t, s, "a", "工具",
		[2]string{"system", "可用工具：read_file、write_file"},
		[2]string{"user", "read_file 为什么读不到那个文件"},
	)
	found, err := s.Search(context.Background(), "read_file", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("应当命中 1 条，实际 %d", len(found))
	}
	if got := found[0].Snippet; got == "" || !contains(got, "为什么读不到") {
		t.Errorf("片段应当取自用户那句话，实际 %q", got)
	}
}

func TestSearchTreatsWildcardsAsLiteralText(t *testing.T) {
	// 搜「100%」不该变成「100 加任意内容」——那会把整库都匹配上。
	s := newStore(t)
	save(t, s, "a", "涨了", [2]string{"user", "这个指标涨了 100% 了"})
	save(t, s, "b", "没涨", [2]string{"user", "这个指标 100 没动"})

	found, err := s.Search(context.Background(), "100%", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "a" {
		t.Fatalf("通配符应当按字面匹配，实际命中 %d 条", len(found))
	}
}

func TestSearchWithEmptyKeywordListsEverything(t *testing.T) {
	s := newStore(t)
	save(t, s, "a", "一", [2]string{"user", "x"})
	save(t, s, "b", "二", [2]string{"user", "y"})
	found, err := s.Search(context.Background(), "  ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Errorf("空关键词应当等于列全部，实际 %d 条", len(found))
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
