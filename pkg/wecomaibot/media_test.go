package wecomaibot

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 与官方 SDK 一致：AES-256-CBC，IV 取密钥前 16 字节，PKCS#7 按 32 字节补齐。
func encryptLikeWeCom(t *testing.T, plain, key []byte) []byte {
	t.Helper()
	pad := 32 - len(plain)%32
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(out, padded)
	return out
}

func TestDownloadMediaDecryptsAndReadsFilename(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plain := []byte("一份报告的内容，长度随便，不是 32 的倍数")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''%E6%8A%A5%E5%91%8A.pdf`)
		_, _ = w.Write(encryptLikeWeCom(t, plain, key))
	}))
	defer server.Close()

	// 密钥不带 = 填充也要认（Node 的 base64 解码是宽松的，线上见过）。
	aesKey := base64.RawStdEncoding.EncodeToString(key)
	data, name, err := DownloadMedia(context.Background(), MediaRef{URL: server.URL, AESKey: aesKey})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, plain) {
		t.Errorf("解密结果不对：%q", data)
	}
	if name != "报告.pdf" {
		t.Errorf("文件名 = %q", name)
	}
}

func TestDecryptMediaRejectsWrongKey(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	wrong := []byte("ffffffffffffffffffffffffffffffff")
	encrypted := encryptLikeWeCom(t, []byte("hello"), key)
	if _, err := DecryptMedia(encrypted, base64.StdEncoding.EncodeToString(wrong)); err == nil {
		t.Error("密钥不对应报错，而不是返回一堆乱码")
	}
}
