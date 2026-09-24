package wechatlink

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func encryptECB(t *testing.T, plain, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return out
}

// 线上两种 aes_key 编码都要认：图片是 base64(原始 16 字节)，文件是 base64(hex 串)。
// 早先只认后一种，图片下载下来解不开。
func TestParseAESKeyAcceptsBothEncodings(t *testing.T) {
	key := []byte("0123456789abcdef")
	raw := base64.StdEncoding.EncodeToString(key)
	hexed := base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key)))
	for name, encoded := range map[string]string{"raw": raw, "hex": hexed, "no padding": base64.RawStdEncoding.EncodeToString(key)} {
		got, err := parseAESKey(encoded)
		if err != nil || !bytes.Equal(got, key) {
			t.Errorf("%s: got %x err %v", name, got, err)
		}
	}
	if _, err := parseAESKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("长度不对的密钥应报错")
	}
}

func TestDownloadImagePrefersHexKeyAndFullURL(t *testing.T) {
	key := []byte("fedcba9876543210")
	plain := []byte("\x89PNG\r\n\x1a\n一张图")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encryptECB(t, plain, key))
	}))
	defer server.Close()

	got, err := DownloadImage(context.Background(), ImageSource{
		// media.aes_key 故意给错：有 image_item.aeskey 时应该用它。
		Media:  &MediaInfo{FullURL: server.URL, AESKey: base64.StdEncoding.EncodeToString([]byte("wrongwrongwrong!"))},
		HexKey: hex.EncodeToString(key),
	})
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("got %q err %v", got, err)
	}

	file, err := DownloadFile(context.Background(), FileSource{Name: "a.txt", Media: &MediaInfo{
		FullURL: server.URL, AESKey: base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key))),
	}})
	if err != nil || !bytes.Equal(file, plain) {
		t.Fatalf("file got %q err %v", file, err)
	}
}

func TestExtractContentCollectsVoiceFilesAndQuotedImages(t *testing.T) {
	message := extractContent([]MessageItem{
		{Type: ItemTypeText, TextItem: &TextItem{Text: "看看这个"}},
		{Type: ItemTypeVoice, VoiceItem: &VoiceItem{Text: "语音转的字"}},
		{Type: ItemTypeFile, FileItem: &FileItem{FileName: "报告.pdf", Media: &MediaInfo{EncryptQueryParam: "q"}}},
		{Type: ItemTypeText, TextItem: &TextItem{Text: "这是什么"}, RefMsg: &RefMessage{MessageItem: &MessageItem{
			Type: ItemTypeImage, ImageItem: &ImageItem{Media: &MediaInfo{EncryptQueryParam: "img"}, AESKey: "00"},
		}}},
	})
	if message.Text != "看看这个\n语音转的字\n这是什么" {
		t.Errorf("text = %q", message.Text)
	}
	if len(message.Files) != 1 || message.Files[0].Name != "报告.pdf" {
		t.Errorf("files = %+v", message.Files)
	}
	if len(message.Images) != 1 || message.Images[0].HexKey != "00" {
		t.Errorf("引用的图片也要收进来：%+v", message.Images)
	}
}
