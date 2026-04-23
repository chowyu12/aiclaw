package agent

import (
	"context"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// registerDefaultHooks 将框架内置的副作用逻辑注册为 HookAgentDone 钩子。
func registerDefaultHooks(hooks *HookRegistry) {
	hooks.Register(HookAgentDone, skillTrackingHook)
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

