package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
)

func testGateway(t *testing.T) (*channelGateway, string) {
	t.Helper()
	dir := t.TempDir()
	return &channelGateway{server: &Server{options: Options{
		AppDB: filepath.Join(dir, "aiclaw.db"),
		Logf:  t.Logf,
	}}}, dir
}

func bigJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// 手机拍的原图动辄几 MB、四千像素；不缩的话内核会把超过 4MB 的整张丢掉，而且不说。
func TestChannelImagesAreScaledDown(t *testing.T) {
	gateway, _ := testGateway(t)
	original := bigJPEG(t, 4000, 3000)
	_, images, _ := gateway.prepareAttachments("c_1", pluginpkg.Inbound{Images: [][]byte{original}})
	if len(images) != 1 {
		t.Fatalf("图片没交出去：%d 张", len(images))
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(images[0]))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width != maxImageEdge || config.Height != maxImageEdge*3/4 {
		t.Errorf("缩放后是 %dx%d", config.Width, config.Height)
	}
	if len(images[0]) >= len(original) {
		t.Errorf("缩放后没变小：%d → %d", len(original), len(images[0]))
	}

	small := bigJPEG(t, 200, 100)
	_, images, _ = gateway.prepareAttachments("c_1", pluginpkg.Inbound{Images: [][]byte{small}})
	if len(images) != 1 || !bytes.Equal(images[0], small) {
		t.Error("小图应原样交出去，不重新编码")
	}
}

func TestChannelFilesAreSavedAndDescribed(t *testing.T) {
	gateway, dir := testGateway(t)
	text, images, audio := gateway.prepareAttachments("c_1", pluginpkg.Inbound{
		Text: "帮我看看",
		Files: []pluginpkg.InboundFile{
			{Name: "notes.md", Data: []byte("# 标题\n正文")},
			{Name: "报告.pdf", Data: []byte("%PDF-1.7 \x00\x01binary")},
			{Name: "../../etc/evil.sh", Data: []byte("echo hi")},
			{Name: "voice.m4a", Data: []byte("fake audio")},
			{Name: "photo.jpg", Data: bigJPEG(t, 100, 100)},
		},
	})
	if !strings.HasPrefix(text, "帮我看看") {
		t.Errorf("用户原话应在最前：%q", text)
	}
	if !strings.Contains(text, "附件 notes.md") || !strings.Contains(text, "# 标题\n正文") {
		t.Errorf("文本文件应并进正文：%q", text)
	}
	if !strings.Contains(text, "[用户发来文件 报告.pdf") || strings.Contains(text, "%PDF") {
		t.Errorf("二进制文件只说明、不并进正文：%q", text)
	}
	if len(audio) != 1 || !strings.HasSuffix(audio[0], "voice.m4a") {
		t.Errorf("音频应交给听写：%v", audio)
	}
	if len(images) != 1 {
		t.Errorf("以文件形式发来的图片也应作为图片交出去：%d 张", len(images))
	}

	inbox := filepath.Join(dir, "inbox", "c_1")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("每个文件都应落一份在收件箱，实际 %d 个", len(entries))
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "..") || strings.Contains(entry.Name(), "/") {
			t.Errorf("外部给的文件名不能带路径：%s", entry.Name())
		}
		if !strings.Contains(text, filepath.Join(inbox, entry.Name())) {
			t.Errorf("正文里应写明 %s 存在哪", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "etc", "evil.sh")); err == nil {
		t.Error("文件写到收件箱外面去了")
	}
}

func TestUnsupportedImageIsSaidNotDropped(t *testing.T) {
	gateway, _ := testGateway(t)
	huge := bytes.Repeat([]byte{0xAB}, maxRawImageBytes+1)
	text, images, _ := gateway.prepareAttachments("c_1", pluginpkg.Inbound{Images: [][]byte{huge}})
	if len(images) != 0 || !strings.Contains(text, "没能转交") {
		t.Errorf("解不开的大图要说一声：text=%q images=%d", text, len(images))
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"../../x.txt":  "x.txt",
		`..\..\y.txt`:  "y.txt",
		"":             "file",
		"..":           "file",
		"a\x00b:c.txt": "a_b_c.txt",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("长", 100) + ".pdf"
	if got := safeFileName(long); len(got) > 120 || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("长文件名：%q（%d 字节）", got, len(got))
	}
}
