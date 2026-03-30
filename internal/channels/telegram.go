package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// Telegram Bot Webhook：https://core.telegram.org/bots/api#setwebhook

type telegramAdapter struct{}

func (telegramAdapter) HandleGET(ch ChannelConfig, q url.Values) WebhookHTTP {
	// 部分代理健康检查
	_ = ch
	if q.Get("ping") != "" {
		return WebhookHTTP{Status: 200, Body: []byte("pong")}
	}
	return WebhookHTTP{Status: 200, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func (telegramAdapter) HandlePOST(ch ChannelConfig, body []byte, _ string, _ http.Header) (WebhookHTTP, *Inbound) {
	var upd telegramUpdate
	if err := json.Unmarshal(body, &upd); err != nil {
		return jsonOK(), nil
	}
	if upd.Message == nil {
		return jsonOK(), nil
	}
	msg := upd.Message
	content := strings.TrimSpace(msg.Text)
	if content == "" {
		content = strings.TrimSpace(msg.Caption)
	}

	var files []model.ChatFile
	token := cfgString(ch.ConfigJSON, "bot_token", "token")
	if len(msg.Photo) > 0 && token != "" {
		best := msg.Photo[len(msg.Photo)-1]
		if localPath := telegramDownloadFile(token, best.FileID); localPath != "" {
			files = append(files, model.ChatFile{
				Type:           model.ChatFileImage,
				TransferMethod: model.TransferRemoteURL,
				URL:            localPath,
			})
		}
	}

	if content == "" && len(files) > 0 {
		content = "请描述这张图片"
	}
	if content == "" && len(files) == 0 {
		return jsonOK(), nil
	}

	senderID := ""
	if msg.From != nil {
		senderID = strconv.FormatInt(msg.From.ID, 10)
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	return jsonOK(), &Inbound{
		ThreadKey: chatID,
		SenderID:  senderID,
		Text:      content,
		Files:     files,
		RawMeta: map[string]any{
			"message_id": msg.MessageID,
			"chat_type":  msg.Chat.Type,
		},
	}
}

func (telegramAdapter) Reply(ctx context.Context, ch ChannelConfig, in *Inbound, text string) error {
	if in == nil {
		return nil
	}
	token := cfgString(ch.ConfigJSON, "bot_token", "token")
	if token == "" {
		return fmt.Errorf("telegram: config.bot_token 未配置")
	}
	api := "https://api.telegram.org/bot" + token + "/sendMessage"
	form := url.Values{}
	form.Set("chat_id", in.ThreadKey)
	form.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage: %s: %s", resp.Status, bytes.TrimSpace(respBody))
	}
	log.WithFields(log.Fields{"channel_uuid": ch.UUID, "chat_id": in.ThreadKey}).Debug("[telegram] sendMessage ok")
	return nil
}

type telegramUpdate struct {
	Message *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int              `json:"message_id"`
	From      *telegramUser    `json:"from"`
	Chat      telegramChat     `json:"chat"`
	Text      string           `json:"text"`
	Caption   string           `json:"caption"`
	Photo     []telegramPhoto  `json:"photo"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type telegramPhoto struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size"`
}

// telegramDownloadFile 通过 Bot API 获取文件路径并下载到临时文件。
func telegramDownloadFile(botToken, fileID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", botToken, url.QueryEscape(fileID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		log.WithError(err).Warn("[telegram] getFile request failed")
		return ""
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.WithError(err).Warn("[telegram] getFile failed")
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || !result.OK || result.Result.FilePath == "" {
		log.WithField("file_id", fileID).Warn("[telegram] getFile: invalid response")
		return ""
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", botToken, result.Result.FilePath)
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return ""
	}
	dlResp, err := client.Do(dlReq)
	if err != nil {
		log.WithError(err).Warn("[telegram] download file failed")
		return ""
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(dlResp.Body, 20<<20))
	if err != nil {
		log.WithError(err).Warn("[telegram] read file body failed")
		return ""
	}

	ext := filepath.Ext(result.Result.FilePath)
	if ext == "" {
		ext = ".jpg"
	}
	tmpFile, err := os.CreateTemp("", "tg-img-*"+ext)
	if err != nil {
		log.WithError(err).Warn("[telegram] create temp file failed")
		return ""
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write(data); err != nil {
		log.WithError(err).Warn("[telegram] write temp file failed")
		os.Remove(tmpFile.Name())
		return ""
	}
	log.WithFields(log.Fields{"file_id": fileID, "path": tmpFile.Name(), "size": len(data)}).Debug("[telegram] image downloaded")
	return tmpFile.Name()
}
