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

const builtinWebSearchContent = `Built-in model web search is enabled. The model request includes enable_search=true, so you can answer questions about recent news, real-time data, latest versions, prices, policies, and similar time-sensitive topics without saying that you do not know or that your knowledge is outdated.
This capability is part of the model runtime. Answer directly and include sources when factual claims require them.
Prefer authoritative sources and include links for concrete facts.
- If the user describes only a topic and does not provide a URL, use built-in model web search.
- If the user message contains a concrete http(s):// URL, use the web_fetch tool to fetch that URL.`

const externalWebSearchContent = `External web search is enabled. A web_search tool is available through the configured search engine.
Use web_search for recent news, real-time data, latest versions, prices, policies, and similar time-sensitive topics when the user did not provide a concrete URL.
Prefer authoritative sources and include links for concrete facts.
- If the user message contains a concrete http(s):// URL, use the web_fetch tool to fetch that URL.
- Base factual claims on web_search or web_fetch results.`

type messagesBuildInput struct {
	Agent            *model.Agent
	Skills           []model.Skill
	History          []openai.ChatCompletionMessage
	UserMsg          string
	AgentTools       []model.Tool
	ToolSkillMap     map[string]string
	Files            []*model.File
	PersistentMemory string
	PlanBlock        string
	ToolSearchMode   bool
	WebSearchEnabled bool
	WebSearchMode    string
	WS               *workspace.Workspace
}

func buildMessages(in messagesBuildInput) []openai.ChatCompletionMessage {
	systemPrompt := buildSystemPrompt(in.Agent, in.Skills, in.AgentTools, in.ToolSkillMap, in.ToolSearchMode, in.WebSearchEnabled, in.WebSearchMode, in.WS)

	if in.PersistentMemory != "" {
		systemPrompt += "\n\n" + in.PersistentMemory
	}
	if in.PlanBlock != "" {
		systemPrompt += "\n\n" + in.PlanBlock
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
				content += fmt.Sprintf("\n... [File truncated at 500KB. Use the read tool to inspect the remaining content: %s]", f.StoragePath)
			} else {
				content += "\n... [File truncated at 500KB]"
			}
		}
		sb.WriteString(xmlBlock("file", attrs, content))
		sb.WriteString("\n\n")
	}

	sb.WriteString(xmlBlock("user_message", "", userMsg))

	return sb.String()
}

func buildSystemPrompt(ag *model.Agent, skills []model.Skill, agentTools []model.Tool, toolSkillMap map[string]string, toolSearchMode, webSearchEnabled bool, webSearchMode string, ws *workspace.Workspace) string {
	l := log.WithField("agent", ag.Name)

	var parts []string

	// <instructions>: custom agent prompt (role, behavior, and constraints)
	basePrompt := ag.SystemPrompt
	if basePrompt != "" {
		l.WithField("len", len(ag.SystemPrompt)).Debug("[Prompt]  base prompt loaded")
	} else {
		basePrompt = "You are a personal assistant running inside Aiclaw."
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
		parts = append(parts, xmlBlock("web_search", "", webSearchPromptContent(webSearchMode)))
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
					body.WriteString("Detailed instructions: ")
					body.WriteString(filepath.Join(skillDir, "SKILL.md"))
					body.WriteString("\n")
				}
			}
			if names := skillToolNames[sk.Name]; len(names) > 0 {
				body.WriteString("Related tools: ")
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
			strategy.WriteString(`- Answer knowledge questions directly, including concept explanations, reasoning, advice, comparisons, writing, translation, and math.
- Use tools for operational tasks such as file reading/writing, command execution, information retrieval, web extraction, or when the user explicitly asks you to act.
- For complex tasks with 3 or more steps, first use the plan tool to create a runtime plan. The plan is for execution progress only; do not include the plan body in the final answer.
- During execution, follow <plan_state>: prioritize the current step, and use plan revise/update with a reason when the plan needs to change.
- When uncertain, use sub_agent(mode=explore) to investigate before making changes.
- Use web_search for recent or time-sensitive web information only when the web_search tool is listed; use web_fetch only for concrete URLs from the user.
- Base answers on real tool outputs. Do not fabricate data.`)
		}
		if hasTools && toolSearchMode {
			strategy.WriteString("\n- If you need a tool that is not listed, call tool_search once and then use the discovered tool directly.")
		}
		if hasSkills {
			strategy.WriteString("\n- Prefer a matching skill when the task fits it. Before using the skill, read its SKILL.md for full instructions. Resolve relative paths from the SKILL.md directory.")
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

func webSearchPromptContent(mode string) string {
	if mode == model.WebSearchModeExternal {
		return externalWebSearchContent
	}
	return builtinWebSearchContent
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
