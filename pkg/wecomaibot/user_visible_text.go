package wecomaibot

import (
	"strings"
)

// MixedToUserVisibleText 将图文混排转为发给模型的用户侧文本（正文 + `[图片] url` 行）。
func MixedToUserVisibleText(msg *MixedMessage) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, it := range msg.Mixed.MsgItem {
		mt := strings.ToLower(strings.TrimSpace(it.MsgType))
		switch mt {
		case "text", "":
			if it.Text != nil {
				if t := strings.TrimSpace(it.Text.Content); t != "" {
					parts = append(parts, t)
				}
			}
		case "image":
			if it.Image != nil {
				if u := strings.TrimSpace(it.Image.URL); u != "" {
					parts = append(parts, "[图片] "+u)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// CollectImageURLsFromMixed 提取混排中的图片 URL。
func CollectImageURLsFromMixed(msg *MixedMessage) []string {
	if msg == nil {
		return nil
	}
	var urls []string
	for _, it := range msg.Mixed.MsgItem {
		if strings.EqualFold(strings.TrimSpace(it.MsgType), "image") && it.Image != nil {
			if u := strings.TrimSpace(it.Image.URL); u != "" {
				urls = append(urls, u)
			}
		}
	}
	return urls
}

// ImageToUserVisibleText 纯图片消息转用户侧文本。
func ImageToUserVisibleText(msg *ImageMessage) string {
	if msg == nil {
		return ""
	}
	u := strings.TrimSpace(msg.Image.URL)
	if u == "" {
		return ""
	}
	return "[图片] " + u
}
