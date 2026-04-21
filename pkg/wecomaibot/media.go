package wecomaibot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"time"
)

const qyAPIBase = "https://qyapi.weixin.qq.com/cgi-bin"

// mediaHTTPClient 企业微信 media API 调用共享客户端；超时由 context 控制。
var mediaHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	},
}

type getTokenResp struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
}

type uploadMediaResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	MediaID string `json:"media_id"`
}

// GetAccessToken 用 corpid + corpsecret 换取 access_token。
func GetAccessToken(ctx context.Context, corpID, corpSecret string) (string, error) {
	url := fmt.Sprintf("%s/gettoken?corpid=%s&corpsecret=%s", qyAPIBase, corpID, corpSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result getTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("gettoken decode: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("gettoken errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	return result.AccessToken, nil
}

// UploadTempMedia 上传临时素材，返回 media_id（3 天有效）。
// mediaType 传 "image"，filename 用原始文件名（影响接收侧展示）。
func UploadTempMedia(ctx context.Context, accessToken, mediaType, filename string, data []byte) (string, error) {
	url := fmt.Sprintf("%s/media/upload?access_token=%s&type=%s", qyAPIBase, accessToken, mediaType)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("media", filepath.Base(filename))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, bytes.NewReader(data)); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("upload media read response: %w", err)
	}
	var result uploadMediaResp
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("upload media decode: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("upload media errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	return result.MediaID, nil
}
