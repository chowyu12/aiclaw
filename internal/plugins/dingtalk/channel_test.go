package dingtalk

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestStreamCallbackAndReplyEndpoint(t *testing.T) {
	const event = `{"msgId":"msg1","conversationId":"chat1","conversationType":"2","conversationTitle":"Test group","senderId":"sender","msgtype":"text","isInAtList":true,"sessionWebhook":"https://oapi.dingtalk.com/robot/sendBySession?session=fixture","text":{"content":" hello "}}`
	var msg callback
	if err := json.Unmarshal([]byte(event), &msg); err != nil {
		t.Fatal(err)
	}
	got, ok := normalize(msg)
	if !ok || got.ExternalKey != "chat1" || got.SenderID != "sender" || got.Text != "hello" {
		t.Fatalf("got %+v %t", got, ok)
	}
	msg.IsInAtList = false
	if _, ok := normalize(msg); ok {
		t.Fatal("untargeted group message accepted")
	}
	for _, address := range []string{"https://oapi.dingtalk.com.other.test/robot/sendBySession", "http://oapi.dingtalk.com/robot/sendBySession", "https://oapi.dingtalk.com/another", "https://oapi.dingtalk.com:9000/robot/sendBySession"} {
		target, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		if serviceURL(target, "https") {
			t.Errorf("unexpected reply destination %s", address)
		}
	}
	pong := (&Channel{}).handle(frame{Type: "SYSTEM", Headers: map[string]string{"topic": "ping", "messageId": "ping-id"}, Data: "fixture"}, nil)
	if pong.Code != 200 || pong.Headers["messageId"] != "ping-id" || pong.Data != "fixture" {
		t.Fatalf("pong = %+v", pong)
	}
}
