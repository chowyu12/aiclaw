package agent

// 多模态：把对话之外的几件事外包给专门的模型。
//
// 为什么是工具而不是「换个更强的对话模型」：看图、听写、朗读、画图是四种不同
// 的模型，没有哪个对话模型四样都好，而用户手上往往各有一个便宜的专用模型。
// 所以形状是「主模型负责对话，需要时把这一件事外包出去」——调用由内核发起，
// 端点与 Key 按角色的 ProviderID 现查（见 providers 包），不经协议帧。
//
// **没配的角色不注册工具。** 给模型一个用不了的工具，它会调、会失败、会重试，
// 而失败原因（「你没配」）它无从修复——不如让它看不见。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// generatedDir 是生成物的落点，相对会话工作区。
//
// 落盘而不是只回给模型：一张图模型自己看不了几眼就丢进历史了，而用户要的是
// 一个能打开、能发给别人的文件。目录固定，用户知道去哪儿找。
const generatedDir = "generated"

// registerMediaTools 按已配的角色注册多模态工具。
func (s *Session) registerMediaTools() error {
	if image := s.config.Roles.Image; image.Configured() {
		if err := s.registry.Register(s.generateImageTool(image)); err != nil {
			return err
		}
	}
	if stt := s.config.Roles.STT; stt.Configured() {
		if err := s.registry.Register(s.transcribeTool(stt)); err != nil {
			return err
		}
	}
	if tts := s.config.Roles.TTS; tts.Configured() {
		if err := s.registry.Register(s.speakTool(tts)); err != nil {
			return err
		}
	}
	return nil
}

// roleClient 按角色建一个模型客户端。端点与 Key 由 keyFor 现查。
func (s *Session) roleClient(role protocol.RoleModel) (*llm.Client, error) {
	config := protocol.ModelConfig{ProviderID: role.ProviderID, Model: role.Model}
	apiKey, err := s.keyFor(&config)
	if err != nil {
		return nil, err
	}
	return llm.New(config.BaseURL, apiKey, 0)
}

func (s *Session) generateImageTool(role protocol.RoleModel) tools.Tool {
	return tools.Tool{
		Name: "generate_image",
		Description: "按文字描述生成一张图，存进工作区的 " + generatedDir + "/ 并展示给用户。" +
			"描述要具体：画面内容、风格、构图都写清楚，模型不会追问。",
		// 花钱、出网、在磁盘上留东西：默认档位下要确认。
		Effect: tools.EffectExternal,
		Schema: mediaSchema(map[string]any{
			"prompt": map[string]any{"type": "string", "description": "画面描述，越具体越好"},
			"size": map[string]any{
				"type": "string", "description": "尺寸，形如 1024x1024；不传用服务的默认值",
			},
		}, "prompt"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Prompt string `json:"prompt"`
				Size   string `json:"size"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("参数不是合法 JSON 对象：%w", err)
			}
			if strings.TrimSpace(args.Prompt) == "" {
				return "", fmt.Errorf("prompt 不能为空")
			}
			if err := env.RequestApproval(
				ctx, tools.EffectExternal, protocol.ApprovalTool,
				"用 "+role.Model+" 生成图片", args.Prompt, "会调用外部模型服务并产生费用",
			); err != nil {
				return "", err
			}
			client, err := s.roleClient(role)
			if err != nil {
				return "", err
			}
			data, err := client.GenerateImage(ctx, role.Model, args.Prompt, args.Size)
			if err != nil {
				return "", err
			}
			path, err := saveGenerated(env, "image", detectImageExt(data), data)
			if err != nil {
				return "", err
			}
			// 同时给模型看一眼：它要判断这张图是不是用户要的，才能决定重画还是收工。
			env.Attach(data)
			// 界面按这个路径把图画出来。
			tools.Produce(ctx, path)
			return fmt.Sprintf("已生成并保存到 %s（%.0f KB）。画面在下一条消息里。",
				path, float64(len(data))/1024), nil
		},
	}
}

func (s *Session) transcribeTool(role protocol.RoleModel) tools.Tool {
	return tools.Tool{
		Name: "transcribe_audio",
		Description: "把一段音频转成文字（录音、会议、语音消息都行）。" +
			"路径相对工作区解析，也可以给绝对路径。",
		Effect: tools.EffectExternal,
		Schema: mediaSchema(map[string]any{
			"path": map[string]any{"type": "string", "description": "音频文件路径"},
		}, "path"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("参数不是合法 JSON 对象：%w", err)
			}
			path, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			if err := env.RequestApproval(
				ctx, tools.EffectExternal, protocol.ApprovalTool,
				"转写 "+args.Path, path, "音频会被发到外部模型服务",
			); err != nil {
				return "", err
			}
			audio, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("读取失败：%w", err)
			}
			client, err := s.roleClient(role)
			if err != nil {
				return "", err
			}
			text, err := client.Transcribe(ctx, role.Model, filepath.Base(path), audio)
			if err != nil {
				return "", err
			}
			if text == "" {
				return "转写结果是空的——这段音频里可能没有语音。", nil
			}
			return text, nil
		},
	}
}

func (s *Session) speakTool(role protocol.RoleModel) tools.Tool {
	return tools.Tool{
		Name: "speak",
		Description: "把一段文字读成语音，存进工作区的 " + generatedDir + "/ 并给用户一个可播放的文件。" +
			"用户明确要「读出来」「生成音频」时才用，别每条回答都读。",
		Effect: tools.EffectExternal,
		Schema: mediaSchema(map[string]any{
			"text":  map[string]any{"type": "string", "description": "要读的文字"},
			"voice": map[string]any{"type": "string", "description": "音色名；不传用服务的默认值"},
		}, "text"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
			var args struct {
				Text  string `json:"text"`
				Voice string `json:"voice"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("参数不是合法 JSON 对象：%w", err)
			}
			if strings.TrimSpace(args.Text) == "" {
				return "", fmt.Errorf("text 不能为空")
			}
			if err := env.RequestApproval(
				ctx, tools.EffectExternal, protocol.ApprovalTool,
				"用 "+role.Model+" 合成语音", firstLine(args.Text, 200), "会调用外部模型服务并产生费用",
			); err != nil {
				return "", err
			}
			client, err := s.roleClient(role)
			if err != nil {
				return "", err
			}
			audio, ext, err := client.Speak(ctx, role.Model, args.Text, args.Voice)
			if err != nil {
				return "", err
			}
			path, err := saveGenerated(env, "speech", ext, audio)
			if err != nil {
				return "", err
			}
			tools.Produce(ctx, path)
			return fmt.Sprintf("已合成并保存到 %s（%.0f KB）。", path, float64(len(audio))/1024), nil
		},
	}
}

// transcribeAttached 把随消息附来的音频转成文字。
//
// 转不动不让整轮失败：把原因接在消息里告诉模型，它据此换个做法（比如请用户
// 直接打字）。静默丢掉的话，用户发了一段录音、模型回了一句不相干的话，
// 而两边都不知道发生了什么。
func (s *Session) transcribeAttached(ctx context.Context, paths []string) string {
	role := s.config.Roles.STT
	if len(paths) == 0 {
		return ""
	}
	if !role.Configured() {
		return fmt.Sprintf("\n\n[附了 %d 段音频，但没有配听写模型，没能转成文字]", len(paths))
	}
	client, err := s.roleClient(role)
	if err != nil {
		return fmt.Sprintf("\n\n[附了 %d 段音频，但听写模型不可用：%v]", len(paths), err)
	}
	var out strings.Builder
	for _, path := range paths {
		name := filepath.Base(path)
		audio, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(&out, "\n\n[音频 %s 读不了：%v]", name, err)
			continue
		}
		text, err := client.Transcribe(ctx, role.Model, name, audio)
		if err != nil {
			fmt.Fprintf(&out, "\n\n[音频 %s 转写失败：%v]", name, err)
			continue
		}
		if strings.TrimSpace(text) == "" {
			fmt.Fprintf(&out, "\n\n[音频 %s 里没有听出语音]", name)
			continue
		}
		fmt.Fprintf(&out, "\n\n[音频 %s 的转写：\n%s]", name, text)
	}
	return out.String()
}

// describeImages 用视觉模型把图片转成文字。
//
// 对话模型不认图时走这条：不转的话那几张图要么被上游拒绝（一句看不懂的报错），
// 要么被静默忽略（模型答非所问，而用户以为它看见了）。转出来的描述接在用户
// 消息后面，并且**明说这是转述**——模型据此知道自己看的是二手信息。
func (s *Session) describeImages(ctx context.Context, images [][]byte) string {
	role := s.config.Roles.Vision
	if !role.Configured() || len(images) == 0 {
		return ""
	}
	client, err := s.roleClient(role)
	if err != nil {
		return fmt.Sprintf("\n\n[附带了 %d 张图，但视觉模型不可用：%v]", len(images), err)
	}
	messages := []llm.Message{{
		Role: llm.RoleUser,
		Content: "详细描述这些图片里的内容：文字、数据、界面元素、错误信息都要写出来。" +
			"这段描述会替代图片本身交给另一个模型，所以不要遗漏细节，也不要加入推测。",
		Images: images,
	}}
	// 不流式往外发：这段转述是给模型看的中间结果，不该出现在用户的时间线上。
	response, err := client.Stream(ctx, llm.Request{Model: role.Model, Messages: messages}, nil)
	if err != nil {
		return fmt.Sprintf("\n\n[附带了 %d 张图，但转述失败：%v]", len(images), err)
	}
	text := strings.TrimSpace(response.Content)
	if text == "" {
		return ""
	}
	return fmt.Sprintf("\n\n[以下是随消息附带的 %d 张图片的转述（由 %s 生成，你看到的不是原图）：\n%s]",
		len(images), role.Model, text)
}

// saveGenerated 把生成物写进工作区的 generated/，返回相对路径。
//
// 文件名带时间戳：同一个会话里连出三张图，用 image.png 会互相覆盖，而用户
// 往往要的正是那三张的对比。
func saveGenerated(env *tools.Env, kind, ext string, data []byte) (string, error) {
	name := fmt.Sprintf("%s/%s-%s.%s", generatedDir, kind, time.Now().Format("20060102-150405"), ext)
	path, _, err := env.ResolveWrite(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("建生成目录失败：%w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("保存失败：%w", err)
	}
	return name, nil
}

// detectImageExt 按魔数认格式。服务不一定说它给的是什么，而后缀错了
// 用户双击打不开。
func detectImageExt(data []byte) string {
	switch {
	case len(data) > 8 && string(data[1:4]) == "PNG":
		return "png"
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		return "jpg"
	case len(data) > 12 && string(data[8:12]) == "WEBP":
		return "webp"
	}
	return "png"
}

// mediaSchema 拼一份工具参数 schema。tools 包里那个是私有的，这里重来一遍。
func mediaSchema(properties map[string]any, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "object", "properties": properties,
		"required": required, "additionalProperties": false,
	})
	return raw
}
