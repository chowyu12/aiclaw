package wecomaibot

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
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

// MaxInboundMediaBytes 是单个入站媒体的下载上限（企业微信文件本身上限 100MB）。
// 再大的文件模型也读不完，而下载会卡住这个会话的回复。
const MaxInboundMediaBytes = 50 << 20

// DownloadMedia 下载一个入站图片或文件并解密，返回明文与文件名（取自
// Content-Disposition，可能为空）。
//
// 解密与官方 SDK（WecomTeam/aibot-node-sdk 的 crypto.ts）一致：aeskey 是 base64
// 的 32 字节密钥，AES-256-CBC，IV 取密钥前 16 字节，PKCS#7 按 32 字节补齐。
// 没有 aeskey 时按明文返回（回调模式的旧消息）。
func DownloadMedia(ctx context.Context, ref MediaRef) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("下载失败 HTTP %d：%s", resp.StatusCode, body)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxInboundMediaBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("下载失败：%w", err)
	}
	if len(data) > MaxInboundMediaBytes {
		return nil, "", fmt.Errorf("文件超过 %d MB", MaxInboundMediaBytes>>20)
	}
	name := filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	if ref.AESKey == "" {
		return data, name, nil
	}
	plain, err := DecryptMedia(data, ref.AESKey)
	if err != nil {
		return nil, "", err
	}
	return plain, name, nil
}

// DecryptMedia 按企业微信的规则解密一个媒体文件。
func DecryptMedia(ciphertext []byte, aesKey string) ([]byte, error) {
	aesKey = strings.TrimSpace(aesKey)
	key, err := base64.StdEncoding.DecodeString(aesKey)
	if err != nil {
		// Node 的 base64 解码不认填充也能过，这边照样放宽：有的 key 不带 =。
		key, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(aesKey, "="))
		if err != nil {
			return nil, fmt.Errorf("aeskey 不是 base64：%w", err)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("aeskey 应是 32 字节，实际 %d 字节", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度 %d 不是 16 的倍数", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 32 || pad > len(plain) {
		return nil, fmt.Errorf("PKCS#7 填充不对（%d），密钥可能不对", pad)
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return nil, errors.New("PKCS#7 填充不一致，密钥可能不对")
		}
	}
	return plain[:len(plain)-pad], nil
}

// filenameFromDisposition 从 Content-Disposition 取文件名，先认 RFC 5987 的 filename*。
func filenameFromDisposition(header string) string {
	if header == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(header); err == nil {
		if name := params["filename"]; name != "" {
			if decoded, err := url.PathUnescape(name); err == nil {
				return filepath.Base(decoded)
			}
			return filepath.Base(name)
		}
	}
	return ""
}
