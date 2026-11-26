package feishu

import (
	"encoding/json"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestNormalizeGroupMention(t *testing.T) {
	var event larkim.P2MessageReceiveV1
	if err := json.Unmarshal([]byte(`{"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"user1"}},"message":{"message_id":"msg1","chat_id":"chat1","thread_id":"topic1","chat_type":"group","message_type":"text","content":"{\"text\":\"@_user_1 hello\"}","mentions":[{"key":"@_user_1","id":{"open_id":"bot1"}}]}}}`), &event); err != nil {
		t.Fatal(err)
	}
	msg, id, ok := normalize(&event, "bot1")
	if !ok || msg.ExternalKey != "chat1/topic1" || msg.Text != "hello" || msg.SenderID != "user1" || id != "msg1" {
		t.Fatalf("got %+v %q %t", msg, id, ok)
	}
	if _, _, ok := normalize(&event, "different-bot"); ok {
		t.Fatal("another bot was tagged")
	}
}
