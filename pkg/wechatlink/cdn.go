package wechatlink

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cdnBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"

// DownloadFromCDN 从微信 CDN 下载文件。
// aesKeyBase64 非空时进行 AES-128-ECB 解密，为空时直接返回原始数据。
func DownloadFromCDN(ctx context.Context, encryptQueryParam, aesKeyBase64 string) ([]byte, error) {
	var aesKey []byte
	if aesKeyBase64 != "" {
		aesKeyHexBytes, err := base64.StdEncoding.DecodeString(aesKeyBase64)
		if err != nil {
			return nil, fmt.Errorf("decode AES key base64: %w", err)
		}
		aesKey, err = hex.DecodeString(string(aesKeyHexBytes))
		if err != nil {
			return nil, fmt.Errorf("decode AES key hex: %w", err)
		}
	}

	downloadURL := fmt.Sprintf("%s/download?encrypted_query_param=%s",
		cdnBaseURL, url.QueryEscape(encryptQueryParam))

	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from CDN: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CDN download HTTP %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read CDN response: %w", err)
	}

	if len(aesKey) > 0 {
		return decryptAESECB(data, aesKey)
	}
	return data, nil
}

// SaveToTemp 从微信 CDN 下载加密图片，解密后保存到系统临时目录，返回本地文件路径。
// aesKey 为空时跳过解密（直接使用原始响应数据）。
func SaveToTemp(ctx context.Context, encParam, aesKey string) (string, error) {
	data, err := DownloadFromCDN(ctx, encParam, aesKey)
	if err != nil {
		return "", fmt.Errorf("download CDN image: %w", err)
	}
	ext := DetectImageExt(data)
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("wechat-img-%d%s", time.Now().UnixNano(), ext))
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return "", fmt.Errorf("save image to temp: %w", err)
	}
	return tmpPath, nil
}

// DetectImageExt 根据文件字节内容检测图片扩展名（含点号，如 ".png"）。
func DetectImageExt(data []byte) string {
	ct := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(ct, "image/png"):
		return ".png"
	case strings.HasPrefix(ct, "image/gif"):
		return ".gif"
	case strings.HasPrefix(ct, "image/webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}

func decryptAESECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not a multiple of block size")
	}

	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}

	if len(plaintext) == 0 {
		return plaintext, nil
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > aes.BlockSize || padLen == 0 {
		return nil, fmt.Errorf("invalid PKCS7 padding")
	}
	return plaintext[:len(plaintext)-padLen], nil
}
