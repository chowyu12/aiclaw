package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// webSearchPromptSection 在开启内置联网搜索时注入 system prompt。
// 目的：让模型明确知道自身具备实时检索能力，区分 web_search（自动）与 web_fetch（用户显式提供 URL）。
const webSearchPromptSection = `

## 联网搜索
- 当前已开启**内置联网搜索**：你可以直接回答涉及近期资讯、实时数据、最新版本/价格/政策等问题，无需声明"我不知道"或"知识截止于…"。
- 该搜索为模型自身能力，**不是函数工具**；不要尝试去调用一个叫 web_search 的工具，也不要在回复里写出工具调用。直接给出带来源的结论即可。
- 需要返回具体事实时，优先引用检索到的权威来源，并附上链接。
- 区分场景：
  - 用户只描述主题 / 没给 URL → 直接使用内置联网搜索。
  - 用户消息中出现具体 http(s):// URL → 使用 web_fetch 工具抓取该 URL 内容。
`

type messagesBuildInput struct {
	Agent            *model.Agent
	Skills           []model.Skill
	History          []openai.ChatCompletionMessage
	UserMsg          string
	AgentTools       []model.Tool
	ToolSkillMap     map[string]string
	Files            []*model.File
	PersistentMemory string
	SessionMemory    string
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
	if in.SessionMemory != "" {
		systemPrompt += "\n\n<session_notes>\n" + in.SessionMemory + "\n</session_notes>"
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

	var textFiles []*model.File
	var imageFiles []*model.File
	for _, f := range in.Files {
		if f.IsImage() && f.StoragePath != "" {
			imageFiles = append(imageFiles, f)
		} else if f.TextContent != "" {
			textFiles = append(textFiles, f)
		} else if f.StoragePath != "" {
			data, err := os.ReadFile(f.StoragePath)
			if err == nil {
				text, err := parser.ExtractText(f.ContentType, bytes.NewReader(data))
				if err == nil && text != "" {
					f.TextContent = text
					textFiles = append(textFiles, f)
					continue
				}
			}
			log.WithField("file", f.Filename).Warn("[Execute] document text extraction failed, skipping")
		}
	}

	userText := in.UserMsg
	if len(textFiles) > 0 {
		var sb strings.Builder
		sb.WriteString("以下是用户提供的参考文件内容:\n\n")
		for _, f := range textFiles {
			sb.WriteString(fmt.Sprintf("--- [文件: %s] ---\n%s\n---\n\n", f.Filename, f.TextContent))
		}
		sb.WriteString("用户消息: ")
		sb.WriteString(in.UserMsg)
		userText = sb.String()
	}

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

func buildSystemPrompt(ag *model.Agent, skills []model.Skill, agentTools []model.Tool, toolSkillMap map[string]string, toolSearchMode, webSearchEnabled bool, ws *workspace.Workspace) string {
	l := log.WithField("agent", ag.Name)

	var sb strings.Builder
	if ag.SystemPrompt != "" {
		sb.WriteString(ag.SystemPrompt)
		l.WithField("len", len(ag.SystemPrompt)).Debug("[Prompt]  base prompt loaded")
	} else {
		sb.WriteString("你是一个运行在 Aiclaw 内部的个人助手。")
	}

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
		sb.WriteString(webSearchPromptSection)
	}

	if !hasSkills && !hasTools {
		result := sb.String()
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
		sb.WriteString("\n\n## 技能\n")
		for _, sk := range skills {
			if sk.Instruction == "" && sk.Description == "" {
				l.WithField("skill", sk.Name).Debug("[Prompt]  skill has no content, skipped")
				continue
			}
			sb.WriteString("\n### " + sk.Name + "\n")

			if sk.Description != "" {
				sb.WriteString(sk.Description + "\n")
			}
			if ws != nil {
				if skillDir := ws.SkillDir(sk.DirName); skillDir != "" {
					sb.WriteString("详细指令: " + filepath.Join(skillDir, "SKILL.md") + "\n")
				}
			}
			l.WithField("skill", sk.Name).Debug("[Prompt]  skill summary injected (two-phase)")

			if names := skillToolNames[sk.Name]; len(names) > 0 {
				sb.WriteString("关联工具: " + strings.Join(names, ", ") + "\n")
			}
		}
	}

	if hasTools || hasSkills {
		sb.WriteString("\n\n## 执行策略\n")
	}

	if hasTools {
		sb.WriteString(`
**判断原则**: 知识性问题（概念解释、原理分析、经验建议、方案对比、写作翻译、数学推理）直接回答。操作性问题（文件读写、命令执行、信息检索、网页抓取）或用户明确要求动手时，使用工具。

**工作方式**:
- 复杂任务（3+ 步骤）先用 todo 规划，逐项推进
- 不确定时先用 sub_agent(mode=explore) 探索，再动手
- 基于工具返回的真实数据回答，不编造
`)
	}

	if hasTools && toolSearchMode {
		sb.WriteString("- 需要工具但不在列表中时，调用 tool_search 搜索一次，搜到后直接使用\n")
	}

	if hasSkills {
		sb.WriteString("- 问题匹配某项技能时优先使用。使用前先 read 其 SKILL.md 了解完整用法，指令中的相对路径以 SKILL.md 所在目录为基准\n")
	}

	result := sb.String()
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
