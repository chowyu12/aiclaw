package wecomaibot

import "testing"

func TestMixedKeepsTextAndCollectsImagesSeparately(t *testing.T) {
	msg := &MixedMessage{
		Mixed: MixedContent{
			MsgItem: []MixedMsgItem{
				{MsgType: "text", Text: &TextContent{Content: "  hello  "}},
				{MsgType: "image", Image: &ImageContent{URL: "https://example.com/a", AESKey: "k"}},
			},
		},
	}
	if got := MixedToUserVisibleText(msg); got != "hello" {
		t.Fatalf("文字里不该再有加密链接：%q", got)
	}
	refs := CollectImagesFromMixed(msg)
	if len(refs) != 1 || refs[0].URL != "https://example.com/a" || refs[0].AESKey != "k" {
		t.Fatalf("refs=%v", refs)
	}
}

func TestQuotedImageIsCollected(t *testing.T) {
	text, images, _ := quoteParts(&QuoteContent{MsgType: "image", Image: &ImageContent{URL: "https://x/y", AESKey: "k"}})
	if text != "" || len(images) != 1 {
		t.Fatalf("text=%q images=%v", text, images)
	}
}
