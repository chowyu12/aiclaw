package wechatlink

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cdnBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"

// MaxMediaBytes 是单个入站媒体的下载上限。再大的文件模型也读不完，
// 而下载会卡住这个会话的回复。
const MaxMediaBytes = 50 << 20

// DownloadImage 下载并解密一张入站图片。
//
// 密钥优先取 image_item.aeskey（hex），没有再用 media.aes_key——与官方插件
// （Tencent/openclaw-weixin）一致。两个都没有就按明文下。
func DownloadImage(ctx context.Context, image ImageSource) ([]byte, error) {
	if image.Media == nil {
		if strings.HasPrefix(image.URL, "http://") || strings.HasPrefix(image.URL, "https://") {
			return fetch(ctx, image.URL)
		}
		return nil, errors.New("图片没有下载地址")
	}
	var key []byte
	if hexKey := strings.TrimSpace(image.HexKey); hexKey != "" {
		decoded, err := hex.DecodeString(hexKey)
		if err != nil || len(decoded) != 16 {
			return nil, fmt.Errorf("image_item.aeskey 不是 16 字节的 hex：%q", hexKey)
		}
		key = decoded
	} else if image.Media.AESKey != "" {
		parsed, err := parseAESKey(image.Media.AESKey)
		if err != nil {
			return nil, err
		}
		key = parsed
	}
	return downloadMedia(ctx, image.Media, key)
}

// DownloadFile 下载并解密一个入站文件（语音、视频同样的格式）。
func DownloadFile(ctx context.Context, file FileSource) ([]byte, error) {
	if file.Media == nil {
		return nil, errors.New("文件没有下载地址")
	}
	if file.Media.AESKey == "" {
		return nil, errors.New("文件没有密钥")
	}
	key, err := parseAESKey(file.Media.AESKey)
	if err != nil {
		return nil, err
	}
	return downloadMedia(ctx, file.Media, key)
}

// parseAESKey 解出 16 字节的 AES-128 密钥。
//
// 线上见到两种编码（官方插件 pic-decrypt.ts 里写明了）：
//   - base64(16 字节原始密钥)：图片的 media.aes_key
//   - base64(32 个字符的 hex 串)：文件、语音、视频
//
// 早先只认第二种，图片的密钥解出来是乱的——下载成功、解密报「padding 不对」。
func parseAESKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// 有的 key 不带 = 填充。
		decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
		if err != nil {
			return nil, fmt.Errorf("aes_key 不是 base64：%w", err)
		}
	}
	if len(decoded) == 16 {
		return decoded, nil
	}
	if len(decoded) == 32 {
		if raw, err := hex.DecodeString(string(decoded)); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("aes_key 解出来是 %d 字节，既不是 16 字节密钥也不是 32 位 hex", len(decoded))
}

func downloadMedia(ctx context.Context, media *MediaInfo, key []byte) ([]byte, error) {
	target := strings.TrimSpace(media.FullURL)
	if target == "" {
		if media.EncryptQueryParam == "" {
			return nil, errors.New("媒体没有下载地址")
		}
		target = fmt.Sprintf("%s/download?encrypted_query_param=%s", cdnBaseURL, url.QueryEscape(media.EncryptQueryParam))
	}
	data, err := fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return data, nil
	}
	return decryptAESECB(data, key)
}

func fetch(ctx context.Context, target string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download from CDN: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("CDN download HTTP %d: %s", resp.StatusCode, string(body))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxMediaBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read CDN response: %w", err)
	}
	if len(data) > MaxMediaBytes {
		return nil, fmt.Errorf("文件超过 %d MB", MaxMediaBytes>>20)
	}
	return data, nil
}

func decryptAESECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度 %d 不是 16 的倍数", len(ciphertext))
	}
	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > aes.BlockSize || padLen == 0 {
		return nil, fmt.Errorf("invalid PKCS7 padding（密钥可能不对）")
	}
	return plaintext[:len(plaintext)-padLen], nil
}
