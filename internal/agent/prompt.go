package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/parser"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type messagesBuildInput struct {
	Agent          *model.Agent
	Skills         []model.Skill
	History        []openai.ChatCompletionMessage
	UserMsg        string
	AgentTools     []model.Tool
	ToolSkillMap   map[string]string
	Files          []*model.File
	MemosContext   string
	ToolSearchMode bool
}

func buildMessages(in messagesBuildInput) []openai.ChatCompletionMessage {
	systemPrompt := buildSystemPrompt(in.Agent, in.Skills, in.AgentTools, in.ToolSkillMap, in.ToolSearchMode)

	if in.MemosContext != "" {
		systemPrompt += "\n\n## 相关记忆\n以下是从长期记忆中检索到的与当前对话相关的信息，请参考但不要盲目依赖：\n<memories>\n" + in.MemosContext + "\n</memories>"
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

func buildSystemPrompt(ag *model.Agent, skills []model.Skill, agentTools []model.Tool, toolSkillMap map[string]string, toolSearchMode bool) string {
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
			if skillDir := workspace.SkillDir(sk.DirName); skillDir != "" {
				sb.WriteString("详细指令: " + filepath.Join(skillDir, "SKILL.md") + "\n")
			}
			l.WithField("skill", sk.Name).Debug("[Prompt]  skill summary injected (two-phase)")

			if names := skillToolNames[sk.Name]; len(names) > 0 {
				sb.WriteString("关联工具: " + strings.Join(names, ", ") + "\n")
			}
		}
	}

	var strategies []string
	if hasTools && toolSearchMode {
		strategies = append(strategies,
			"**按需搜索**: 需要某工具但它不在列表中时，调用 tool_search 搜索。搜到后工具会自动加入可用列表，直接调用即可",
			"**避免重复搜索**: 每类需求搜索一次即可，不要对同一类工具反复搜索。如果搜索结果显示工具已在可用列表中，立即使用它们",
			"**先搜后用**: 正确流程是 tool_search → 得到工具 → 直接调用工具完成任务。不要在搜索和使用之间犹豫",
		)
	} else if hasTools {
		strategies = append(strategies,
			"**工具优先**: 当问题可通过工具获得更准确或实时的结果时，必须调用工具，禁止仅凭内置知识推测",
		)
	}
	if hasSkills {
		strategies = append(strategies, "**技能路由**: 若问题匹配某项技能，优先使用该技能及其关联工具")
	}
	if hasSkills {
		strategies = append(strategies, "**技能详情**: 需要使用某项技能时，先用 read 工具读取其详细指令文件，了解完整用法后再执行。指令文件中的相对路径以 SKILL.md 所在目录为基准，例如 SKILL.md 路径为 /a/b/SKILL.md，引用 ./refs/doc.md 时应读取 /a/b/refs/doc.md")
	}
	if hasTools {
		strategies = append(strategies,
			"**组合调用**: 复杂问题可串联或并行调用多个工具",
			"**结果驱动**: 基于工具返回的真实数据生成回答，不编造或臆测信息",
		)
	}
	if len(strategies) > 0 {
		sb.WriteString("\n\n## 执行策略\n\n")
		for i, s := range strategies {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
		}
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
