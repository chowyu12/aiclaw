package agent

import (
	"context"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// registerDefaultHooks 将框架内置的副作用逻辑注册为 HookAgentDone 钩子。
// 注册顺序：SkillTracking → MemOS → SessionMemory（从重要到次要）。
func registerDefaultHooks(hooks *HookRegistry) {
	hooks.Register(HookAgentDone, skillTrackingHook)
	hooks.Register(HookAgentDone, memosHook)
	hooks.Register(HookAgentDone, sessionMemoryHook)
}

// skillTrackingHook 记录本轮执行中实际使用过的技能。
func skillTrackingHook(ctx context.Context, _ HookEvent, p *HookPayload) HookAction {
	if p.Tracker == nil || len(p.CalledTools) == 0 {
		return HookContinue
	}

	usedSkills := make(map[string]bool)
	for toolName := range p.CalledTools {
		if skillName, ok := p.ToolSkillMap[toolName]; ok {
			usedSkills[skillName] = true
		}
	}

	for _, sk := range p.Skills {
		if !usedSkills[sk.Name] {
			continue
		}
		var calledToolNames []string
		for toolName, skillName := range p.ToolSkillMap {
			if skillName == sk.Name && p.CalledTools[toolName] {
				calledToolNames = append(calledToolNames, toolName)
			}
		}

		input := sk.Instruction
		if input == "" {
			input = "(no instruction)"
		}
		output := fmt.Sprintf("used %d tools: %s", len(calledToolNames), strings.Join(calledToolNames, ", "))
		p.Tracker.RecordStep(ctx, model.StepSkillMatch, sk.Name, input, output, model.StepSuccess, "", 0, 0, &model.StepMetadata{
			SkillName: sk.Name, SkillTools: calledToolNames,
		})
		log.WithFields(log.Fields{"skill": sk.Name, "used_tools": calledToolNames}).Info("[Skill] skill used")
	}
	return HookContinue
}

// memosHook 异步将本轮对话存入 MemOS 长期记忆。
func memosHook(_ context.Context, _ HookEvent, p *HookPayload) HookAction {
	if p.Agent != nil {
		storeMemories(p.UserMsg, p.Content, p.Agent)
	}
	return HookContinue
}

// sessionMemoryHook 将本轮执行摘要追加到会话笔记文件。
func sessionMemoryHook(_ context.Context, _ HookEvent, p *HookPayload) HookAction {
	if p.ConvUUID == "" {
		return HookContinue
	}
	var toolNames []string
	if p.Tracker != nil {
		for _, step := range p.Tracker.Steps() {
			if step.StepType == model.StepToolCall && step.Status == model.StepSuccess {
				toolNames = append(toolNames, step.Name)
			}
		}
	}
	appendSessionMemory(p.ConvUUID, p.UserMsg, toolNames, p.Content)
	return HookContinue
}
