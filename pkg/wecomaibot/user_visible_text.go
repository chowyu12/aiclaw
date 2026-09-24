package wecomaibot

import (
	"strings"
)

// MediaRef 是一个入站的加密媒体：下载链接五分钟内有效，要用它自带的 AESKey 解密。
type MediaRef struct {
	URL    string
	AESKey string
}

// MixedToUserVisibleText 取图文混排里的文字部分。
//
// 图片不再写成「[图片] <url>」行：那个 url 是加密的临时链接，模型拿到它什么都
// 做不了，却会以为自己「看过」这张图。图片单独下载解密后作为图片交给模型。
func MixedToUserVisibleText(msg *MixedMessage) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, it := range msg.Mixed.MsgItem {
		mt := strings.ToLower(strings.TrimSpace(it.MsgType))
		if (mt == "text" || mt == "") && it.Text != nil {
			if t := strings.TrimSpace(it.Text.Content); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// CollectImagesFromMixed 取图文混排里的图片。
func CollectImagesFromMixed(msg *MixedMessage) []MediaRef {
	if msg == nil {
		return nil
	}
	var refs []MediaRef
	for _, it := range msg.Mixed.MsgItem {
		if strings.EqualFold(strings.TrimSpace(it.MsgType), "image") && it.Image != nil {
			if ref, ok := imageRef(it.Image); ok {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func imageRef(image *ImageContent) (MediaRef, bool) {
	if image == nil || strings.TrimSpace(image.URL) == "" {
		return MediaRef{}, false
	}
	return MediaRef{URL: strings.TrimSpace(image.URL), AESKey: strings.TrimSpace(image.AESKey)}, true
}

// quoteParts 取引用消息里的文字、图片与文件。用户常常引用一张图再问「这是什么」，
// 丢了引用模型就只看到一句「这是什么」。
func quoteParts(quote *QuoteContent) (text string, images []MediaRef, files []MediaRef) {
	if quote == nil {
		return "", nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(quote.MsgType)) {
	case "text":
		if quote.Text != nil {
			text = strings.TrimSpace(quote.Text.Content)
		}
	case "voice":
		if quote.Voice != nil {
			text = strings.TrimSpace(quote.Voice.Content)
		}
	case "image":
		if ref, ok := imageRef(quote.Image); ok {
			images = append(images, ref)
		}
	case "mixed":
		if quote.Mixed != nil {
			mixed := &MixedMessage{Mixed: *quote.Mixed}
			text = MixedToUserVisibleText(mixed)
			images = CollectImagesFromMixed(mixed)
		}
	case "file":
		if quote.File != nil && strings.TrimSpace(quote.File.URL) != "" {
			files = append(files, MediaRef{URL: strings.TrimSpace(quote.File.URL), AESKey: strings.TrimSpace(quote.File.AESKey)})
		}
	}
	return text, images, files
}
