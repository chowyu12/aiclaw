package wecomaibot

import "testing"

func TestMixedToUserVisibleText(t *testing.T) {
	msg := &MixedMessage{
		Mixed: MixedContent{
			MsgItem: []MixedMsgItem{
				{MsgType: "text", Text: &TextContent{Content: "  hello  "}},
				{MsgType: "image", Image: &ImageContent{URL: "https://example.com/a.png"}},
			},
		},
	}
	got := MixedToUserVisibleText(msg)
	want := "hello\n[图片] https://example.com/a.png"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	urls := CollectImageURLsFromMixed(msg)
	if len(urls) != 1 || urls[0] != "https://example.com/a.png" {
		t.Fatalf("urls=%v", urls)
	}
}

func TestImageToUserVisibleText(t *testing.T) {
	msg := &ImageMessage{Image: ImageContent{URL: "https://x/y.jpg"}}
	if got := ImageToUserVisibleText(msg); got != "[图片] https://x/y.jpg" {
		t.Fatalf("got %q", got)
	}
}
