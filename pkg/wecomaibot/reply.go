package wecomaibot

import (
	"context"
	"fmt"
	"os"
)

// ReplyText 向消息发送者发送文字流式回复（finish=true，单段完结）。
func (c *WSClient) ReplyText(frame *WsFrame, text string) error {
	streamID := fmt.Sprintf("stream_%s", frame.Headers.ReqID)
	_, err := c.ReplyStream(frame, streamID, text, true, nil, nil)
	return err
}

// ReplyImageByMediaID 上传图片为临时素材，获取 media_id 后发送图片消息（最佳体验）。
// storagePath 为本地文件路径，filename 用于上传时的文件名展示。
// 需要提供企业的 corpID + corpSecret 以换取 access_token。
func (c *WSClient) ReplyImageByMediaID(ctx context.Context, frame *WsFrame, corpID, corpSecret, storagePath, filename string) error {
	token, err := GetAccessToken(ctx, corpID, corpSecret)
	if err != nil {
		return fmt.Errorf("get access_token: %w", err)
	}
	data, err := os.ReadFile(storagePath)
	if err != nil {
		return fmt.Errorf("read image %s: %w", filename, err)
	}
	mediaID, err := UploadTempMedia(ctx, token, "image", filename, data)
	if err != nil {
		return fmt.Errorf("upload temp media %s: %w", filename, err)
	}
	body := map[string]any{
		"msgtype": "image",
		"image":   map[string]string{"media_id": mediaID},
	}
	_, err = c.Reply(frame, body, "")
	return err
}

// ReplyNewsCard 以图文卡片形式展示图片（降级方案，不需要 corp 级凭证）。
// title 为卡片标题，imgURL 同时作为封面图和跳转链接。
func (c *WSClient) ReplyNewsCard(frame *WsFrame, title, imgURL string) error {
	body := map[string]any{
		"msgtype": "news",
		"news": map[string]any{
			"articles": []map[string]string{
				{
					"title":       title,
					"description": "AI 生成的图片",
					"url":         imgURL,
					"picurl":      imgURL,
				},
			},
		},
	}
	_, err := c.Reply(frame, body, "")
	return err
}
