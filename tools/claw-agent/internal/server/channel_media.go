package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // 注册解码器：微信、企业微信发来的图可能是 GIF
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 通道消息里的图片与文件怎么交给这一轮。
//
// 与桌面端发附件（apps/desktop/src/renderer/attachments.ts）是同一套规则：图片走
// 视觉通道，音频交给听写模型，文本文件并进正文；此外每个文件都落一份在收件箱，
// 正文里写明路径——模型能用读文件的工具再去看，用户也能在本机找到它。

const (
	// maxImageEdge 与桌面端缩放的上限一致。再大对识别没有帮助，只是更贵。
	maxImageEdge = 1568
	// keepImageBytes 以下且尺寸不超的图原样交出去，不重新编码。
	keepImageBytes = 1 << 20
	// maxRawImageBytes 是解不开（WebP、HEIC）时原样交出去的上限，与内核 limitImages 一致。
	maxRawImageBytes = 4 << 20
	// maxInlineTextBytes 与桌面端 MAX_TEXT_BYTES 一致。
	maxInlineTextBytes = 128 << 10
	// maxAudioBytes 与桌面端 MAX_AUDIO_BYTES 一致（多数听写服务卡在 25MB）。
	maxAudioBytes = 25 << 20
)

var (
	imageExtensions = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "bmp": true}
	audioExtensions = map[string]bool{
		"mp3": true, "m4a": true, "mp4": true, "wav": true, "ogg": true, "opus": true,
		"webm": true, "flac": true, "aac": true,
	}
	textExtensions = map[string]bool{
		"txt": true, "md": true, "markdown": true, "json": true, "jsonl": true, "yaml": true, "yml": true,
		"toml": true, "ini": true, "conf": true, "env": true, "csv": true, "tsv": true, "log": true,
		"sql": true, "sh": true, "bash": true, "zsh": true, "py": true, "go": true, "rs": true, "java": true,
		"kt": true, "c": true, "h": true, "cc": true, "cpp": true, "hpp": true, "cs": true, "rb": true,
		"php": true, "swift": true, "ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true,
		"vue": true, "css": true, "scss": true, "less": true, "html": true, "htm": true, "xml": true,
		"svg": true, "gradle": true, "properties": true, "diff": true, "patch": true,
	}
)

// applyMedia 按宿主最新推来的配置刷新这个通道会话的看图与听写角色。
//
// 通道会话是内核自己建的，建的时候没有角色配置——于是对话模型不认图时，用户
// 发来的图既没有视觉模型转述、模型自己也看不了。每轮开始前刷一遍，用户在设置
// 页里换了视觉模型，下一条消息就用新的。
func (g *channelGateway) applyMedia(ctx context.Context, session *agent.Session, binding *model.ChannelBinding) {
	s := g.server
	s.channelMu.Lock()
	roles := s.channelRoles
	s.channelMu.Unlock()
	session.SetMediaRoles(roles.Vision, roles.STT, g.modelSeesImages(ctx, binding.ProviderID, binding.ModelName))
}

// modelSeesImages 看模型清单里的 #vision 标记，与桌面端 session.ts 的 modelSeesImages
// 同一条规则：标了 vision 才算认图，没有标记就是只做对话；拿不到清单、清单里
// 没有这个模型时按「看得懂」处理——宁可让不认图的模型报一次上游错误，也不要
// 让认图的模型永远只读到二手描述。
func (g *channelGateway) modelSeesImages(ctx context.Context, providerID int64, modelName string) bool {
	s := g.server
	if s.providers == nil || providerID == 0 {
		return true
	}
	list, err := s.providers.List(ctx)
	if err != nil {
		return true
	}
	for _, provider := range list {
		if provider.ID != providerID {
			continue
		}
		for _, entry := range provider.Models {
			parsed := protocol.ParseModelMark(entry)
			if parsed.Name != strings.TrimSpace(modelName) {
				continue
			}
			// 没有 # 的清单项就是只做对话（见 protocol.ProviderView.Models）。
			for _, role := range parsed.Roles {
				if role == protocol.RoleVision {
					return true
				}
			}
			return false
		}
	}
	return true
}

// prepareAttachments 把一条入站消息整理成这一轮的正文、图片与音频路径。
func (g *channelGateway) prepareAttachments(sessionID string, message pluginpkg.Inbound) (string, [][]byte, []string) {
	text := strings.TrimSpace(message.Text)
	var notes []string
	var images [][]byte
	var audioPaths []string

	addImage := func(data []byte) bool {
		shrunk, err := shrinkImage(data)
		if err != nil {
			return false
		}
		images = append(images, shrunk)
		return true
	}
	for _, data := range message.Images {
		if !addImage(data) {
			notes = append(notes, "[用户发来一张图片，但格式不支持或太大，没能转交]")
		}
	}

	for _, file := range message.Files {
		name := safeFileName(file.Name)
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		path, err := g.saveInbound(sessionID, name, file.Data)
		where := ""
		if err != nil {
			g.server.options.Logf("通道文件落盘失败：%v", err)
		} else {
			where = "，已存到 " + path
		}
		size := humanSize(len(file.Data))

		switch {
		case imageExtensions[ext] && addImage(file.Data):
			// 当文件发来的图片：照样交给视觉通道，用户的意思就是「看看这张图」。
			notes = append(notes, fmt.Sprintf("[用户以文件形式发来图片 %s（%s）%s，已随消息附上]", name, size, where))
		case audioExtensions[ext] && len(file.Data) <= maxAudioBytes && path != "":
			audioPaths = append(audioPaths, path)
			notes = append(notes, fmt.Sprintf("[用户发来音频 %s（%s）%s]", name, size, where))
		case isText(ext, file.Data):
			body, truncated := clampText(file.Data)
			note := ""
			if truncated {
				note = fmt.Sprintf("（只贴了前 %dKB）", maxInlineTextBytes>>10)
			}
			notes = append(notes, fmt.Sprintf("附件 %s%s%s：\n```\n%s\n```", name, note, where, body))
		default:
			notes = append(notes, fmt.Sprintf("[用户发来文件 %s（%s）%s]", name, size, where))
		}
	}

	if len(notes) > 0 {
		if text != "" {
			text += "\n\n"
		}
		text += strings.Join(notes, "\n\n")
	}
	return text, images, audioPaths
}

// saveInbound 把入站文件写进收件箱：~/.aiclaw/inbox/<会话>/<时间>-<文件名>。
//
// 不放在会话库所在的 userData 下：那个目录在保护名单里，模型读不到。
func (g *channelGateway) saveInbound(sessionID, name string, data []byte) (string, error) {
	base := ""
	if appDB := strings.TrimSpace(g.server.options.AppDB); appDB != "" {
		base = filepath.Dir(appDB)
	} else if home, err := os.UserHomeDir(); err == nil {
		base = filepath.Join(home, ".aiclaw")
	} else {
		return "", err
	}
	dir := filepath.Join(base, "inbox", safeFileName(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// safeFileName 只留文件名本身：名字是外部发来的，带 ../ 或路径分隔符就能写到别处去。
func safeFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if len(name) > 120 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		// 先截主干、去掉切坏的多字节字符，再接回扩展名：反过来做会把扩展名当成
		// 「坏掉的尾巴」一起去掉。
		stem := name[:120-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	return name
}

func isText(ext string, data []byte) bool {
	if !textExtensions[ext] && ext != "" {
		return false
	}
	sample := data
	if len(sample) > 8<<10 {
		sample = sample[:8<<10]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	// 截断处可能切在一个多字节字符中间，去掉尾巴再判断。
	for i := 0; i < 3 && len(sample) > 0 && !utf8.Valid(sample); i++ {
		sample = sample[:len(sample)-1]
	}
	return len(data) > 0 && utf8.Valid(sample)
}

func clampText(data []byte) (string, bool) {
	if len(data) <= maxInlineTextBytes {
		return string(data), false
	}
	cut := data[:maxInlineTextBytes]
	for len(cut) > 0 && !utf8.Valid(cut) {
		cut = cut[:len(cut)-1]
	}
	return string(cut), true
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// shrinkImage 把一张图缩到最长边不超过 maxImageEdge，重新编码成 JPEG。
//
// 桌面端发图时由渲染层用 canvas 缩；通道发来的是手机拍的原图（常常 3–5MB、
// 4000px），不缩的话内核的 limitImages 会把超过 4MB 的整张丢掉——而且不说。
// 解不开的格式（WebP、HEIC）不大就原样交出去，大了报错由调用方说明。
func shrinkImage(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if len(data) > 0 && len(data) <= maxRawImageBytes {
			return data, nil
		}
		return nil, fmt.Errorf("图片解不开：%w", err)
	}
	if len(data) <= keepImageBytes && config.Width <= maxImageEdge && config.Height <= maxImageEdge {
		return data, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("图片解不开：%w", err)
	}
	scaled := scaleDown(src, maxImageEdge)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, scaled, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// scaleDown 用区域平均把图缩到最长边不超过 edge。标准库没有缩放，而引一个图像库
// 只为这一处不值得；区域平均缩小时不出锯齿，对识别足够。透明处按白底合成。
func scaleDown(src image.Image, edge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := max(width, height)
	if longest <= edge {
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				dst.Set(x, y, onWhite(src.At(bounds.Min.X+x, bounds.Min.Y+y)))
			}
		}
		return dst
	}
	newWidth := max(1, width*edge/longest)
	newHeight := max(1, height*edge/longest)
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		y0 := bounds.Min.Y + y*height/newHeight
		y1 := max(y0+1, bounds.Min.Y+(y+1)*height/newHeight)
		for x := 0; x < newWidth; x++ {
			x0 := bounds.Min.X + x*width/newWidth
			x1 := max(x0+1, bounds.Min.X+(x+1)*width/newWidth)
			var r, g, b, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := onWhite(src.At(sx, sy))
					r += uint64(c.R)
					g += uint64(c.G)
					b += uint64(c.B)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 255})
		}
	}
	return dst
}

func onWhite(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	// RGBA() 是预乘过 alpha 的 16 位值：白底合成 = 值 + (1-alpha)*白。
	white := uint32(0xffff) - a
	return color.RGBA{R: uint8((r + white) >> 8), G: uint8((g + white) >> 8), B: uint8((b + white) >> 8), A: 255}
}
