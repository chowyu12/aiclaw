package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // 注册解码器：模型遇到什么格式不由我们决定
	"image/jpeg"
	_ "image/png" //
	"math"
	"os"
)

/*
view_image：让模型看本机上已有的一张图。

为什么需要：视觉通道早就通了（`llm.Message.Images`，computer use 的截屏走的
就是它），用户也能把图贴进对话——但模型**自己在磁盘上发现**一张截图时看不了，
只能读到一串二进制。「把桌面上那张报错截图看一下」这种要求现在做不到。

两条硬性处理，都不是可选的：

  1. **缩到长边 1568 再送。** 图会留在消息历史里，每一轮都重发一遍；一张 4K
     截图原样进去就是几 MB × N 轮。这个数字与渲染层贴图那条路一致。
  2. **重新编码成 JPEG。** 一是统一格式（宿主那边也统一成 JPEG），
     二是顺带把体积压下来。

缩放自己写而不是引第三方库：区域平均是二十来行，而多一个依赖要跟着升级、
还要确认它不引 cgo（`make nocgo` 守着那条线）。
*/

const (
	// maxImageFileBytes 是原始文件的上限。超过基本不是「一张截图」，
	// 而模型也不需要看一张 20MB 的扫描件才能回答问题。
	maxImageFileBytes = 12 << 20
	// maxImageEdge 与渲染层贴图一致：再大对模型看清楚没有帮助，只是更贵。
	maxImageEdge = 1568
)

func viewImageTool() Tool {
	return Tool{
		Name: "view_image",
		Description: "看一张本机已有的图片（截图、照片、图表都行）。" +
			"图会作为画面送给你，不是文字。路径相对工作区解析，也可以给绝对路径。",
		Effect: EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "图片文件路径"},
		}, "path"),
		Handler: func(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("读取失败：%w", err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s 是一个目录，不是图片", args.Path)
			}
			if info.Size() > maxImageFileBytes {
				return "", fmt.Errorf(
					"图片太大（%.1f MB，上限 %d MB）", float64(info.Size())/(1<<20), maxImageFileBytes>>20,
				)
			}

			file, err := os.Open(path)
			if err != nil {
				return "", fmt.Errorf("读取失败：%w", err)
			}
			defer file.Close()

			source, format, err := image.Decode(file)
			if err != nil {
				// 说清楚是「不是图片/不支持这个格式」，模型才知道该换个做法
				//（比如先用命令转一下），而不是反复重试同一个路径。
				return "", fmt.Errorf("解不开这个图片（支持 png / jpeg / gif）：%w", err)
			}

			scaled := downscale(source, maxImageEdge)
			var buffer bytes.Buffer
			if err := jpeg.Encode(&buffer, scaled, &jpeg.Options{Quality: 82}); err != nil {
				return "", fmt.Errorf("图片转码失败：%w", err)
			}
			env.Attach(buffer.Bytes())

			original := source.Bounds()
			final := scaled.Bounds()
			note := ""
			if final.Dx() != original.Dx() {
				note = fmt.Sprintf("，已缩到 %d×%d", final.Dx(), final.Dy())
			}
			return fmt.Sprintf(
				"已加载 %s（%s，%d×%d%s）。画面在下一条消息里。",
				args.Path, format, original.Dx(), original.Dy(), note,
			), nil
		},
	}
}

// downscale 把图缩到长边不超过 edge。小图原样返回，不放大。
//
// 用区域平均而不是最近邻：截图里全是文字，最近邻缩完锯齿严重，模型认字会出错。
func downscale(source image.Image, edge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= edge || longest == 0 {
		return source
	}
	// 长边取整成 edge，短边按比例四舍五入。
	// 用浮点乘出来的话，3000×(1568/3000) 会得到 1567.999…，截断成 1567——
	// 差一个像素不影响观感，但会让「长边等于上限」这件事变得说不准。
	targetW, targetH := edge, int(math.Round(float64(height)*float64(edge)/float64(width)))
	if height > width {
		targetW, targetH = int(math.Round(float64(width)*float64(edge)/float64(height))), edge
	}
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}

	// 先转成 RGBA，省得对每种色彩模型分别处理。
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Src)

	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		y0 := y * height / targetH
		y1 := (y + 1) * height / targetH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < targetW; x++ {
			x0 := x * width / targetW
			x1 := (x + 1) * width / targetW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, a, count uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					offset := rgba.PixOffset(sx, sy)
					r += uint32(rgba.Pix[offset])
					g += uint32(rgba.Pix[offset+1])
					b += uint32(rgba.Pix[offset+2])
					a += uint32(rgba.Pix[offset+3])
					count++
				}
			}
			out.Set(x, y, color.RGBA{
				R: uint8(r / count), G: uint8(g / count), B: uint8(b / count), A: uint8(a / count),
			})
		}
	}
	return out
}
