package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// 飞书渠道：事件订阅使用 github.com/larksuite/oapi-sdk-go/v3 的 EventDispatcher（解密、签名校验、路由）；
// 回复使用官方 Client 调用 im/v1/messages。

type feishuAdapter struct{}

func (feishuAdapter) HandleGET(_ ChannelConfig, _ url.Values) WebhookHTTP {
	return WebhookHTTP{Status: 200, ContentType: "application/json; charset=utf-8", Body: []byte(`{"ok":true}`)}
}

func (feishuAdapter) HandlePOST(ch ChannelConfig, body []byte, _ string, hdr http.Header) (WebhookHTTP, *Inbound) {
	if len(body) == 0 {
		return jsonOK(), nil
	}
	ver := cfgString(ch.ConfigJSON, "verification_token")
	enc := cfgString(ch.ConfigJSON, "encrypt_key")

	disp := dispatcher.NewEventDispatcher(ver, enc)
	disp.InitConfig(larkevent.WithLogLevel(larkcore.LogLevelError))

	appID := cfgString(ch.ConfigJSON, "app_id")
	appSecret := cfgString(ch.ConfigJSON, "app_secret")

	var inbound *Inbound
	disp.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		_ = ctx
		if event == nil || event.Event == nil || event.Event.Message == nil {
			return nil
		}
		msg := event.Event.Message
		msgType := deref(msg.MessageType)
		if msgType == "system" {
			return nil
		}
		if event.Event.Sender != nil && strings.EqualFold(deref(event.Event.Sender.SenderType), "app") {
			return nil
		}

		text := feishuMessageText(msgType, deref(msg.Content))
		var files []model.ChatFile

		if msgType == "image" {
			imageKey := feishuExtractImageKey(deref(msg.Content))
			if imageKey != "" && appID != "" && appSecret != "" {
				if localPath := feishuDownloadImage(imageKey, appID, appSecret); localPath != "" {
					files = append(files, model.ChatFile{
						Type:           model.ChatFileImage,
						TransferMethod: model.TransferRemoteURL,
						URL:            localPath,
					})
				}
			}
			if text == "" && len(files) > 0 {
				text = "请描述这张图片"
			}
		}

		if text == "" && len(files) == 0 {
			return nil
		}
		chatID := deref(msg.ChatId)
		if chatID == "" {
			return nil
		}
		sender := feishuSenderOpenID(event.Event.Sender)
		meta := map[string]any{
			"message_id":   deref(msg.MessageId),
			"message_type": msgType,
			"chat_type":    deref(msg.ChatType),
		}
		if eb := event.EventV2Base; eb != nil && eb.Header != nil {
			meta["tenant_key"] = eb.Header.TenantKey
		}
		inbound = &Inbound{
			ThreadKey: chatID,
			SenderID:  sender,
			Text:      text,
			Files:     files,
			RawMeta:   meta,
		}
		return nil
	})

	req := &larkevent.EventReq{
		Header:     feishuCloneHeader(hdr),
		Body:       body,
		RequestURI: "/webhooks/channels/" + ch.UUID,
	}
	resp := disp.Handle(context.Background(), req)
	return feishuEventRespToWebhook(resp), inbound
}

func feishuCloneHeader(h http.Header) map[string][]string {
	if h == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(h))
	for k, vv := range h {
		cp := make([]string, len(vv))
		copy(cp, vv)
		out[k] = cp
	}
	return out
}

func feishuEventRespToWebhook(resp *larkevent.EventResp) WebhookHTTP {
	if resp == nil {
		return WebhookHTTP{Status: http.StatusInternalServerError, ContentType: larkevent.DefaultContentType, Body: []byte(`{"msg":"empty response"}`)}
	}
	wh := WebhookHTTP{Status: resp.StatusCode, Body: resp.Body}
	if resp.Header != nil {
		if ct := resp.Header.Get(larkevent.ContentTypeHeader); ct != "" {
			wh.ContentType = ct
		}
	}
	return wh
}

func feishuMessageText(messageType, content string) string {
	mt := strings.TrimSpace(messageType)
	if mt != "" && mt != "text" {
		return ""
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var m struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m.Text)
}

func feishuExtractImageKey(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var m struct {
		ImageKey string `json:"image_key"`
	}
	if json.Unmarshal([]byte(content), &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.ImageKey)
}

func feishuDownloadImage(imageKey, appID, appSecret string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := lark.NewClient(appID, appSecret)
	req := larkim.NewGetImageReqBuilder().ImageKey(imageKey).Build()
	resp, err := client.Im.Image.Get(ctx, req)
	if err != nil {
		log.WithError(err).WithField("image_key", imageKey).Warn("[feishu] download image failed")
		return ""
	}
	if resp == nil || resp.File == nil {
		log.WithField("image_key", imageKey).Warn("[feishu] download image: empty response")
		return ""
	}

	ext := ".png"
	if resp.FileName != "" {
		if e := filepath.Ext(resp.FileName); e != "" {
			ext = e
		}
	}
	tmpFile, err := os.CreateTemp("", "feishu-img-*"+ext)
	if err != nil {
		log.WithError(err).Warn("[feishu] create temp file for image failed")
		return ""
	}
	defer tmpFile.Close()

	n, err := io.Copy(tmpFile, resp.File)
	if err != nil {
		log.WithError(err).Warn("[feishu] write image to temp file failed")
		os.Remove(tmpFile.Name())
		return ""
	}
	log.WithFields(log.Fields{"image_key": imageKey, "path": tmpFile.Name(), "size": n}).Info("[feishu] image downloaded")
	return tmpFile.Name()
}

func feishuSenderOpenID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	return deref(sender.SenderId.OpenId)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (feishuAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	if in == nil {
		return nil
	}
	appID := cfgString(ch.ConfigJSON, "app_id")
	secret := cfgString(ch.ConfigJSON, "app_secret")
	if appID == "" || secret == "" {
		return fmt.Errorf("feishu: config.app_id / app_secret 未配置")
	}
	chatID := strings.TrimSpace(in.ThreadKey)
	if chatID == "" {
		return fmt.Errorf("feishu: 缺少 chat_id")
	}
	contentBytes, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	client := lark.NewClient(appID, secret)
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("text").
			Content(string(contentBytes)).
			Build()).
		Build()
	resp, err := client.Im.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		code, msg := 0, ""
		if resp != nil {
			code = resp.Code
			msg = resp.Msg
		}
		return fmt.Errorf("feishu im.message.create: code=%d msg=%s", code, msg)
	}
	log.WithFields(log.Fields{"channel_uuid": ch.UUID, "chat_id": chatID}).Debug("[feishu] reply sent")
	return nil
}
