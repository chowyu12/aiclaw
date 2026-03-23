package agent

import (
	"context"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

// agentRunState 表示单次 Execute / ExecuteStream 在 LLM 轮询前已准备好的公共状态（消息、工具映射、Tool Search 等）。
type agentRunState struct {
	Messages    []openai.ChatCompletionMessage
	ToolMap     map[string]Tool
	AllToolDefs []openai.Tool
	TSMode      bool
	Discovered  map[string]bool
}

// bootstrapAgentTurn：加载历史、落库用户消息、装配工具与 System Prompt 消息列表（execute / stream 共用）。
func (e *Executor) bootstrapAgentTurn(ctx context.Context, ec *execContext, streaming bool) (*agentRunState, error) {
	history, err := e.memory.LoadHistory(ctx, ec.conv.ID, ec.ag.HistoryLimit())
	if err != nil {
		ec.l.WithError(err).Error("[LLM] load history failed")
		return nil, err
	}

	if _, err := e.memory.SaveUserMessage(ctx, ec.conv.ID, ec.userMsg, ec.files); err != nil {
		ec.l.WithError(err).Error("[LLM] save user message failed")
		return nil, err
	}

	var toolMap map[string]Tool
	var allToolDefs []openai.Tool
	tsMode := false
	discovered := map[string]bool{}

	if ec.hasTools() {
		lcTools := e.registry.BuildTrackedTools(ec.agentTools, ec.tracker, ec.toolSkillMap)
		lcTools = append(lcTools, ec.mcpTools...)
		lcTools = append(lcTools, ec.skillTools...)
		toolMap = make(map[string]Tool, len(lcTools))
		for _, t := range lcTools {
			toolMap[t.Name()] = t
		}
		allToolDefs = buildLLMToolDefs(ec.agentTools, ec.mcpTools, ec.skillTools)

		tsMode = UseLazyToolSearch(ec.ag.ToolSearchEnabled, len(allToolDefs))
		streamPfx := ""
		if streaming {
			streamPfx = "stream + "
		}
		if tsMode {
			preloadSkillTools(ec.toolSkillMap, discovered)
			ec.l.WithFields(log.Fields{"total_tools": len(allToolDefs), "skill_preloaded": len(discovered)}).Info("[Execute]    mode = " + streamPfx + "tool-search")
		} else if ec.ag.ToolSearchEnabled && len(allToolDefs) > 0 {
			ec.l.WithFields(log.Fields{
				"total_tools": len(allToolDefs),
				"threshold":   ToolSearchAutoFullThreshold,
			}).Info("[Execute]    mode = " + streamPfx + "tool-augmented (tool search on, auto full catalog)")
		} else {
			if streaming {
				ec.l.Info("[Execute]    mode = stream + tool-augmented")
			} else {
				ec.l.Info("[Execute]    mode = tool-augmented")
			}
		}
	} else {
		if streaming {
			ec.l.Info("[Execute]    mode = stream")
		} else {
			ec.l.Info("[Execute]    mode = simple")
		}
	}

	memosContext := e.memory.RecallMemories(ctx, ec.userMsg, ec.ag)

	var msgTools []model.Tool
	var msgToolSkillMap map[string]string
	if !tsMode {
		msgTools = ec.agentTools
		msgToolSkillMap = ec.toolSkillMap
	}
	messages := buildMessages(messagesBuildInput{
		Agent:          ec.ag,
		Skills:         ec.skills,
		History:        history,
		UserMsg:        ec.userMsg,
		AgentTools:     msgTools,
		ToolSkillMap:   msgToolSkillMap,
		Files:          ec.files,
		MemosContext:   memosContext,
		ToolSearchMode: tsMode,
	})
	logMessages(ec.l, messages)

	return &agentRunState{
		Messages:    messages,
		ToolMap:     toolMap,
		AllToolDefs: allToolDefs,
		TSMode:      tsMode,
		Discovered:  discovered,
	}, nil
}

// toolsSentToLLM 返回当前轮次应随请求下发的工具 schema（Tool Search 模式下每轮可能变化）。
func toolsSentToLLM(tsMode bool, allDefs []openai.Tool, discovered map[string]bool) []openai.Tool {
	if tsMode {
		return buildToolSearchDefs(allDefs, discovered)
	}
	return allDefs
}
