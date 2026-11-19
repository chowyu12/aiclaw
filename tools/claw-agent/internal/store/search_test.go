package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestUnicodeSearchAndSnippetsShareCaseFolding(t *testing.T) {
	for _, tc := range []struct{ body, query string }{
		{"中文 😀 Straße", "STRASSE"}, {"emoji 😀 ΟΣ", "ος"},
		{"长前缀 " + string(rune(0x212A)) + " 中文目标 😀", "目标"},
		{"İ 开头 中文 KEY", "key"}, {"😀😀中文", "😀中文"},
		{`literal 100% _ \\ and quote "`, `100% _`},
	} {
		t.Run(tc.query, func(t *testing.T) {
			s := newStore(t)
			save(t, s, "match", "unrelated", [2]string{"user", tc.body})
			save(t, s, "system-only", "other", [2]string{"system", tc.body})
			got, err := s.Search(context.Background(), tc.query, 1)
			if err != nil || len(got) != 1 || got[0].ID != "match" || got[0].Snippet != tc.body {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
	s := newStore(t)
	save(t, s, "title", "Straße")
	got, err := s.Search(context.Background(), "STRASSE", 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("title fold: %+v %v", got, err)
	}
}

func TestSearchFindsAndStreamsSnippetsWithLiteralWhitespace(t *testing.T) {
	for _, separator := range []string{"  ", "\t", "\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", separator), func(t *testing.T) {
			s := newStore(t)
			query := "项目" + separator + "名字"
			body := "检查 " + query + " 是否正确"
			save(t, s, "match", "unrelated", [2]string{"assistant", body})
			save(t, s, "collapsed", "unrelated", [2]string{"assistant", "检查 项目 名字 是否正确"})
			var streamed []Summary
			found, err := s.SearchStream(t.Context(), query, 10, func(batch []Summary) {
				streamed = append(streamed, batch...)
			})
			if err != nil || len(found) != 1 || found[0].ID != "match" || found[0].Snippet != body {
				t.Fatalf("search returned %+v, %v", found, err)
			}
			if len(streamed) != 1 || streamed[0].ID != "match" || streamed[0].Snippet != body {
				t.Fatalf("streamed %+v", streamed)
			}
		})
	}
}

func TestFoldedSnippetUsesOriginalRunePositions(t *testing.T) {
	body := "K" + strings.Repeat("前", 60) + "Straße😀" + strings.Repeat("后", 60)
	raw, _ := json.Marshal([]map[string]string{{"Role": "user", "Content": body}})
	got := snippet(string(raw), "STRASSE")
	if !utf8.ValidString(got) || !strings.Contains(got, "Straße😀") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatal(got)
	}
}

func TestSearchStreamsMetadataBeforeBodyAndHonorsCancellation(t *testing.T) {
	s := newStore(t)
	save(t, s, "title", "needle title", [2]string{"user", "unrelated"})
	time.Sleep(time.Millisecond)
	save(t, s, "body", "other", [2]string{"user", "needle body"})
	var batches [][]Summary
	final, err := s.SearchStream(context.Background(), "needle", 50, func(items []Summary) { batches = append(batches, append([]Summary(nil), items...)) })
	if err != nil || len(batches) != 2 || batches[0][0].ID != "title" || len(final) != 2 || final[0].ID != "body" {
		t.Fatalf("partial/final: %+v %+v %v", batches, final, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	_, err = s.SearchStream(ctx, "needle", 50, func([]Summary) { count++; cancel() })
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal("cancel continued scan", err, count)
	}
}
