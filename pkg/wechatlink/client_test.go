package wechatlink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

/*
中继请求的形状。

这两条都属于「发出去了、服务端回 ret=0、手机上却什么都没有」那一类故障：
没有错误可查，日志里也不会有东西，只能靠钉住请求体本身来防。
*/

// capture 起一个假中继，记下每个路径收到的请求体。
func capture(t *testing.T) (*Client, map[string]map[string]any, *[]map[string]any) {
	t.Helper()
	last := map[string]map[string]any{}
	var sends []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		last[r.URL.Path] = body
		if r.URL.Path == "/ilink/bot/sendmessage" {
			sends = append(sends, body)
		}
		switch r.URL.Path {
		case "/ilink/bot/getconfig":
			io.WriteString(w, `{"ret":0,"typing_ticket":"ticket-1"}`)
		default:
			io.WriteString(w, `{"ret":0}`)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient(&Credentials{BotToken: "t", ILinkBotID: "bot-1", BaseURL: server.URL})
	return client, last, &sends
}

// client_id 是服务端的去重键：一直传空串，第一条之后的回复会被当成重复丢掉。
func TestEverySendCarriesAFreshClientID(t *testing.T) {
	client, _, sends := capture(t)
	for _, text := range []string{"第一条", "第二条", "第三条"} {
		if err := client.SendMessage(context.Background(), "u1", "ctx-1", "", text); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for index, body := range *sends {
		msg, _ := body["msg"].(map[string]any)
		id, _ := msg["client_id"].(string)
		if id == "" {
			t.Fatalf("第 %d 条没有 client_id", index+1)
		}
		if seen[id] {
			t.Fatalf("第 %d 条的 client_id 与之前重复：%s", index+1, id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("三条消息应有三个不同的 client_id，实际 %d 个", len(seen))
	}
}

// 调用方给了 client_id 就用它的：重发同一条消息时要能靠它去重。
func TestCallerSuppliedClientIDIsKept(t *testing.T) {
	client, last, _ := capture(t)
	if err := client.SendMessage(context.Background(), "u1", "ctx-1", "mine-1", "文字"); err != nil {
		t.Fatal(err)
	}
	msg, _ := last["/ilink/bot/sendmessage"]["msg"].(map[string]any)
	if msg["client_id"] != "mine-1" {
		t.Errorf("调用方给的 client_id 应保留：%v", msg["client_id"])
	}
}

// channel_version 是服务端做兼容路由的依据；早先只有 getupdates 带了它，
// 发送类请求一律是空的。
func TestEveryRequestCarriesTheChannelVersion(t *testing.T) {
	client, last, _ := capture(t)
	ctx := context.Background()
	if err := client.SendMessage(ctx, "u1", "ctx-1", "", "文字"); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTyping(ctx, "u1", "ctx-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := client.GetUpdates(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/ilink/bot/sendmessage",
		"/ilink/bot/getconfig",
		"/ilink/bot/sendtyping",
		"/ilink/bot/getupdates",
	} {
		body, ok := last[path]
		if !ok {
			t.Errorf("%s 没有被调用", path)
			continue
		}
		base, _ := body["base_info"].(map[string]any)
		if base["channel_version"] != protocolVersion {
			t.Errorf("%s 的 channel_version = %v，应为 %s", path, base["channel_version"], protocolVersion)
		}
	}
}

// 业务错误藏在 HTTP 200 里，漏掉就成了「发成功了但没到」。
func TestBusinessErrorInsideHTTP200Fails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ret":1002,"errmsg":"invalid token"}`)
	}))
	t.Cleanup(server.Close)
	client := NewClient(&Credentials{BotToken: "t", ILinkBotID: "b", BaseURL: server.URL})
	err := client.SendMessage(context.Background(), "u1", "c", "", "文字")
	if err == nil {
		t.Fatal("ret!=0 应当报错")
	}
	if _, _, _, err := client.GetUpdates(context.Background(), ""); err == nil {
		t.Fatal("getupdates 的 ret!=0 也应当报错")
	}
}
