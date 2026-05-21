package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// xmlBlock 将内容用 <tag> 包裹，attrs 为可选的属性字符串（如 `name="foo"`）。
func xmlBlock(tag, attrs, content string) string {
	open := "<" + tag
	if attrs != "" {
		open += " " + attrs
	}
	open += ">"
	return open + "\n" + content + "\n</" + tag + ">"
}

// webSearchPromptSection 在开启内置联网搜索时注入 system prompt。
// 目的：让模型明确知道自身具备实时检索能力，区分 web_search（自动）与 web_fetch（用户显式提供 URL）。
const webSearchContent = `当前已开启内置联网搜索：你可以直接回答涉及近期资讯、实时数据、最新版本/价格/政策等问题，无需声明"我不知道"或"知识截止于…"。
该搜索为模型自身能力，不是函数工具；不要尝试调用名为 web_search 的工具，也不要在回复中写出工具调用，直接给出带来源的结论即可。
需要返回具体事实时，优先引用权威来源并附上链接。
- 用户只描述主题 / 没给 URL → 使用内置联网搜索
- 用户消息中出现具体 http(s):// URL → 使用 web_fetch 工具抓取该 URL 内容`

type messagesBuildInput struct {
	Agent            *model.Agent
	Skills           []model.Skill
	History          []openai.ChatCompletionMessage
	UserMsg          string
	AgentTools       []model.Tool
	ToolSkillMap     map[string]string
	Files            []*model.File
	PersistentMemory string
	TodoBlock        string
	ToolSearchMode   bool
	WebSearchEnabled bool
	WS               *workspace.Workspace
}

func buildMessages(in messagesBuildInput) []openai.ChatCompletionMessage {
	systemPrompt := buildSystemPrompt(in.Agent, in.Skills, in.AgentTools, in.ToolSkillMap, in.ToolSearchMode, in.WebSearchEnabled, in.WS)

	if in.PersistentMemory != "" {
		systemPrompt += "\n\n" + in.PersistentMemory
	}
	if in.TodoBlock != "" {
		systemPrompt += "\n\n" + in.TodoBlock
	}

	var messages []openai.ChatCompletionMessage
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	messages = append(messages, in.History...)

	// oldStorageLimit 是之前的 50KB 存储截断上限。
	// TextContent 恰好达到此长度说明当初被截断，需从磁盘重新提取完整内容。
	const oldStorageLimit = 50 * 1024
	// maxInjectionBytes 是单个文件注入 prompt 的字节上限（约 125K tokens）。
	// 超过此限制时仍把路径告知 AI，让其用 readfile 工具分段读取剩余内容。
	const maxInjectionBytes = 500 * 1024

	var textFiles []*model.File
	var imageFiles []*model.File
	for _, f := range in.Files {
		if f.IsImage() && f.StoragePath != "" {
			imageFiles = append(imageFiles, f)
			continue
		}
		// 满足以下任一条件时从磁盘重新提取：
		// 1. DB 中没有缓存文本（新文件或远程文件首次访问）
		// 2. 文本长度恰好等于旧的 50KB 上限——说明当时被截断，现在需要完整版
		if f.StoragePath != "" && (f.TextContent == "" || len(f.TextContent) >= oldStorageLimit) {
			if data, err := os.ReadFile(f.StoragePath); err == nil {
				if text, err := parser.ExtractText(f.ContentType, bytes.NewReader(data)); err == nil && text != "" {
					f.TextContent = text
				}
			}
		}
		if f.TextContent != "" {
			textFiles = append(textFiles, f)
		} else if f.StoragePath != "" {
			log.WithField("file", f.Filename).Warn("[Execute] document text extraction failed, skipping")
		}
	}

	userText := buildUserMessage(in.UserMsg, textFiles, maxInjectionBytes)

	if len(imageFiles) > 0 {
		multiContent := []openai.ChatMessagePart{
			{Type: openai.ChatMessagePartTypeText, Text: userText},
		}
		for _, img := range imageFiles {
			part, err := imagePartForFile(img)
			if err != nil {
				log.WithError(err).WithField("file", img.Filename).Warn("[Execute] prepare image failed, skipping")
				continue
			}
			multiContent = append(multiContent, part)
		}
		messages = append(messages, openai.ChatCompletionMessage{
			Role:         openai.ChatMessageRoleUser,
			MultiContent: multiContent,
		})
	} else {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: userText,
		})
	}

	return messages
}

func buildUserMessage(userMsg string, textFiles []*model.File, maxInjectionBytes int) string {
	var sb strings.Builder
	sb.WriteString("Current time: ")
	sb.WriteString(time.Now().Format(time.RFC3339))
	sb.WriteString("\n\n")

	for _, f := range textFiles {
		content := f.TextContent
		attrs := fmt.Sprintf("name=%q", f.Filename)
		if f.StoragePath != "" {
			attrs += fmt.Sprintf(" path=%q", f.StoragePath)
		}
		if len(content) > maxInjectionBytes {
			content = content[:maxInjectionBytes]
			if f.StoragePath != "" {
				content += fmt.Sprintf("\n... [文件已截至 500KB，剩余内容请使用 read 工具读取: %s]", f.StoragePath)
			} else {
				content += "\n... [文件已截至 500KB]"
			}
		}
		sb.WriteString(xmlBlock("file", attrs, content))
		sb.WriteString("\n\n")
	}

	sb.WriteString(xmlBlock("user_message", "", userMsg))

	return sb.String()
}

func buildSystemPrompt(ag *model.Agent, skills []model.Skill, agentTools []model.Tool, toolSkillMap map[string]string, toolSearchMode, webSearchEnabled bool, ws *workspace.Workspace) string {
	l := log.WithField("agent", ag.Name)

	var parts []string

	// <instructions>: agent 自定义提示词（角色、行为、限制）
	basePrompt := ag.SystemPrompt
	if basePrompt != "" {
		l.WithField("len", len(ag.SystemPrompt)).Debug("[Prompt]  base prompt loaded")
	} else {
		basePrompt = "你是一个运行在 Aiclaw 内部的个人助手。"
	}
	parts = append(parts, xmlBlock("instructions", "", basePrompt))

	var enabledTools []model.Tool
	for _, t := range agentTools {
		if t.Enabled {
			enabledTools = append(enabledTools, t)
		}
	}

	hasSkills := false
	for _, sk := range skills {
		if sk.Instruction != "" || sk.Description != "" {
			hasSkills = true
			break
		}
	}
	hasTools := len(enabledTools) > 0

	if webSearchEnabled {
		parts = append(parts, xmlBlock("web_search", "", webSearchContent))
	}

	if !hasSkills && !hasTools {
		result := strings.Join(parts, "\n\n")
		l.WithField("total_len", len(result)).Debug("[Prompt]  system prompt built (minimal)")
		return result
	}

	skillToolNames := make(map[string][]string)
	for _, t := range enabledTools {
		if sn, ok := toolSkillMap[t.Name]; ok {
			skillToolNames[sn] = append(skillToolNames[sn], t.Name)
		}
	}

	if hasSkills {
		var skillParts []string
		for _, sk := range skills {
			if sk.Instruction == "" && sk.Description == "" {
				l.WithField("skill", sk.Name).Debug("[Prompt]  skill has no content, skipped")
				continue
			}
			var body strings.Builder
			if sk.Description != "" {
				body.WriteString(sk.Description)
				body.WriteString("\n")
			}
			if ws != nil {
				if skillDir := ws.SkillDir(sk.DirName); skillDir != "" {
					body.WriteString("详细指令: ")
					body.WriteString(filepath.Join(skillDir, "SKILL.md"))
					body.WriteString("\n")
				}
			}
			if names := skillToolNames[sk.Name]; len(names) > 0 {
				body.WriteString("关联工具: ")
				body.WriteString(strings.Join(names, ", "))
			}
			skillParts = append(skillParts, xmlBlock("skill", fmt.Sprintf("name=%q", sk.Name), strings.TrimRight(body.String(), "\n")))
			l.WithField("skill", sk.Name).Debug("[Prompt]  skill summary injected (two-phase)")
		}
		if len(skillParts) > 0 {
			parts = append(parts, xmlBlock("skills", "", strings.Join(skillParts, "\n")))
		}
	}

	if hasTools || hasSkills {
		var strategy strings.Builder
		if hasTools {
			strategy.WriteString(`- 知识性问题（概念解释、原理分析、经验建议、方案对比、写作翻译、数学推理）直接回答
- 操作性问题（文件读写、命令执行、信息检索、网页抓取）或用户明确要求动手时使用工具
- 复杂任务（3+ 步骤）先用 todo 规划，逐项推进
- 不确定时先用 sub_agent(mode=explore) 探索，再动手
- 基于工具返回的真实数据回答，不编造`)
		}
		if hasTools && toolSearchMode {
			strategy.WriteString("\n- 需要工具但不在列表中时，调用 tool_search 搜索一次，搜到后直接使用")
		}
		if hasSkills {
			strategy.WriteString("\n- 问题匹配某项技能时优先使用；使用前先 read 其 SKILL.md 了解完整用法，指令中的相对路径以 SKILL.md 所在目录为基准")
		}
		parts = append(parts, xmlBlock("execution_strategy", "", strategy.String()))
	}

	result := strings.Join(parts, "\n\n")
	l.WithFields(log.Fields{
		"total_len": len(result),
		"skills":    len(skills),
		"tools":     len(enabledTools),
	}).Debug("[Prompt]  system prompt built")
	return result
}

func buildLLMToolDefs(modelTools []model.Tool, mcpTools []Tool, skillTools []Tool) []openai.Tool {
	var result []openai.Tool

	for _, mt := range modelTools {
		if !mt.Enabled {
			continue
		}
		fd := &openai.FunctionDefinition{
			Name:        mt.Name,
			Description: mt.Description,
		}
		if len(mt.FunctionDef) > 0 {
			var def map[string]any
			if json.Unmarshal(mt.FunctionDef, &def) == nil {
				if desc, ok := def["description"].(string); ok && desc != "" {
					fd.Description = desc
				}
				if params, ok := def["parameters"]; ok {
					fd.Parameters = params
				}
			}
		}
		if fd.Parameters == nil {
			fd.Parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		result = append(result, openai.Tool{Type: openai.ToolTypeFunction, Function: fd})
	}

	for _, tools := range [][]Tool{mcpTools, skillTools} {
		for _, t := range tools {
			mt, ok := t.(*trackedTool)
			if !ok {
				continue
			}
			dt, ok := mt.baseTool.(*dynamicTool)
			if !ok {
				continue
			}
			params := dt.params
			if params == nil {
				params = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}
			result = append(result, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        dt.toolName,
					Description: dt.toolDesc,
					Parameters:  params,
				},
			})
		}
	}

	return result
}
