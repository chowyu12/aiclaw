package tools

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// view_image / current_time。
//
// 这两个工具补的是两个「不报错的错」：模型看不到磁盘上的图（只能读到二进制），
// 以及模型不知道今天几号（于是「最近三十期」算错，而且错得很自信）。

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func TestViewImageAttachesThePicture(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := fullRegistry(t)
	writePNG(t, filepath.Join(env.Workspace, "shot.png"), 40, 30)

	out, err := call(t, registry, "view_image", `{"path":"shot.png"}`, env)
	if err != nil {
		t.Fatalf("view_image: %v", err)
	}
	if !strings.Contains(out, "40×30") {
		t.Errorf("结果里应当说清原始尺寸：%q", out)
	}
	images := env.TakeAttachments()
	if len(images) != 1 {
		t.Fatalf("应当挂上一张图，实际 %d 张", len(images))
	}
	// 统一转成 JPEG：宿主那条路也是 JPEG，两边一致。
	if !bytes.HasPrefix(images[0], []byte{0xFF, 0xD8}) {
		t.Errorf("附件应当是 JPEG，实际开头是 % x", images[0][:4])
	}
}

func TestViewImageDownscalesLargePictures(t *testing.T) {
	// 图留在消息历史里，每一轮都重发一遍——一张 4K 截图原样进去就是几 MB × N 轮。
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := fullRegistry(t)
	writePNG(t, filepath.Join(env.Workspace, "big.png"), 3000, 1500)

	out, err := call(t, registry, "view_image", `{"path":"big.png"}`, env)
	if err != nil {
		t.Fatalf("view_image: %v", err)
	}
	if !strings.Contains(out, "已缩到") {
		t.Errorf("缩过的要说出来：%q", out)
	}
	images := env.TakeAttachments()
	decoded, _, err := image.DecodeConfig(bytes.NewReader(images[0]))
	if err != nil {
		t.Fatalf("附件解不开：%v", err)
	}
	if decoded.Width != maxImageEdge {
		t.Errorf("长边应当缩到 %d，实际 %d", maxImageEdge, decoded.Width)
	}
	if decoded.Height != 784 {
		t.Errorf("比例应当保持，实际 %d×%d", decoded.Width, decoded.Height)
	}
}

func TestViewImageRefusesNonImagesAndProtectedPaths(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := fullRegistry(t)
	if err := os.WriteFile(filepath.Join(env.Workspace, "a.txt"), []byte("不是图"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 说清楚「不是图片」，模型才知道换个做法，而不是对同一个路径反复重试。
	_, err := call(t, registry, "view_image", `{"path":"a.txt"}`, env)
	if err == nil || !strings.Contains(err.Error(), "解不开") {
		t.Errorf("非图片应当明确报错，得到 %v", err)
	}

	// 读图走的也是 ResolveRead，凭据目录同样拒绝。
	secrets := filepath.Join(env.Home, ".ssh")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(secrets, "x.png"), 4, 4)
	if _, err := call(t, registry, "view_image", `{"path":"~/.ssh/x.png"}`, env); err == nil {
		t.Error("凭据目录里的图也不该读")
	}
}

func TestCurrentTimeIsSpelledOutForTheModel(t *testing.T) {
	// 星期与时区都要有：「最近一周」按周几算，时区错了整件事偏一天。
	at := time.Date(2026, 9, 22, 14, 5, 0, 0, time.FixedZone("CST", 8*3600))
	line := DescribeNow(at)
	for _, want := range []string{"2026-09-22", "周二", "14:05", "CST", "UTC+8"} {
		if !strings.Contains(line, want) {
			t.Errorf("时间描述里缺 %q：%s", want, line)
		}
	}
}

func TestCurrentTimeToolNeedsNoArguments(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := fullRegistry(t)
	out, err := call(t, registry, "current_time", `{}`, env)
	if err != nil {
		t.Fatalf("current_time: %v", err)
	}
	if !strings.Contains(out, time.Now().Format("2006-01-02")) {
		t.Errorf("应当返回今天的日期：%q", out)
	}
	// 只读，不该弹审批——问一下几点钟不是有副作用的操作。
	tool, _ := registry.Get("current_time")
	if tool.Effect != EffectRead {
		t.Errorf("current_time 应当是只读的，实际 %s", tool.Effect)
	}
}
