package agent

import (
	"context"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"
)

// toolExecutionWall 单次工具调用的 wall 时间上限，与「整轮 Agent ctx」解耦。
// 整轮 execute/stream 若对 ctx 使用了 WithTimeout（Agent 超时），LLM 多轮会提前耗尽 deadline；
// browser 等再通过 mergedActionContext 合并该 deadline 会得到「已过期」的 context，chromedp 立刻报 context canceled。
// 工具在独立子 ctx 上运行，仍通过 AfterFunc 在父 ctx 取消/超时时立刻终止。
const toolExecutionWall = 5 * time.Minute

func toolCallContext(parent context.Context) (ctx context.Context, done func()) {
	toolCtx, toolCancel := context.WithTimeout(context.Background(), toolExecutionWall)
	// 父 ctx 已结束（HTTP/SSE 断开、Agent 超时等）：不把独立 wall 传给工具，避免在客户端已断时仍执行 browser 等重操作。
	if parent.Err() != nil {
		toolCancel()
		return parent, func() {}
	}
	stop := context.AfterFunc(parent, toolCancel)
	return toolCtx, func() {
		stop()
		toolCancel()
	}
}

// runOneToolCall 执行单次工具调用（含 tool_search、循环检测、内置/自定义工具），供阻塞式与流式执行共用。
func (e *Executor) runOneToolCall(
	ctx context.Context,
	ec *execContext,
	tc openai.ToolCall,
	toolMap map[string]Tool,
	tsMode bool,
	allToolDefs []openai.Tool,
	discovered map[string]bool,
	loopDet *toolLoopDetector,
	calledTools map[string]bool,
) (toolMsg openai.ChatCompletionMessage, tr ToolResult, fileParts []openai.ChatMessagePart) {
	toolName := tc.Function.Name
	toolArgs := tc.Function.Arguments

	if tsMode && toolName == toolSearchName {
		if blocked, guardMsg := loopDet.check(toolName, toolArgs); blocked {
			ec.l.WithField("tool", toolName).Warn("[LoopGuard] blocked tool_search")
			toolMsg = openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    guardMsg,
				ToolCallID: tc.ID,
				Name:       toolName,
			}
			return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil
		}
		loopDet.record(toolName, toolArgs)
		toolMsg = e.handleToolSearch(ctx, ec, tc, allToolDefs, discovered)
		return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, nil
	}

	tool, ok := toolMap[toolName]
	if !ok {
		errMsg := fmt.Sprintf("tool %q not found", toolName)
		ec.l.WithField("tool", toolName).Warn("[Tool] tool not registered, skipping")
		toolMsg = openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    errMsg,
			ToolCallID: tc.ID,
			Name:       toolName,
		}
		return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: errMsg}, nil
	}

	if blocked, guardMsg := loopDet.check(toolName, toolArgs); blocked {
		ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 120)}).Warn("[LoopGuard] blocked")
		toolMsg = openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    guardMsg,
			ToolCallID: tc.ID,
			Name:       toolName,
		}
		return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil
	}
	loopDet.record(toolName, toolArgs)

	ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 200)}).Info("[Tool] >> invoke")
	calledTools[toolName] = true
	toolCtx, toolDone := toolCallContext(ctx)
	defer toolDone()
	callStart := time.Now()
	output, callErr := tool.Call(toolCtx, toolArgs)
	callDur := time.Since(callStart)
	toolResult := output
	if callErr != nil {
		toolResult = fmt.Sprintf("error: %s", callErr)
		ec.l.WithFields(log.Fields{"tool": toolName, "duration": callDur}).WithError(callErr).Error("[Tool] << failed")
	} else {
		ec.l.WithFields(log.Fields{"tool": toolName, "duration": callDur, "preview": truncateLog(output, 200)}).Info("[Tool] << ok")
	}

	toolMsg, fileParts = e.buildToolResponseParts(ctx, tc.ID, toolName, toolResult, callErr == nil, ec.l)
	return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, fileParts
}

// appendAssistantToolRound 追加助手 tool_calls 消息、执行工具、持久化该轮工具结果，并在有文件时追加用户侧文件提示消息。
func (e *Executor) appendAssistantToolRound(
	ctx context.Context,
	ec *execContext,
	messages []openai.ChatCompletionMessage,
	assistant openai.ChatCompletionMessage,
	toolMap map[string]Tool,
	tsMode bool,
	allToolDefs []openai.Tool,
	discovered map[string]bool,
	loopDet *toolLoopDetector,
	calledTools map[string]bool,
) []openai.ChatCompletionMessage {
	messages = append(messages, assistant)
	var toolResults []ToolResult
	var pendingParts []openai.ChatMessagePart
	for _, tc := range assistant.ToolCalls {
		toolMsg, tr, fps := e.runOneToolCall(ctx, ec, tc, toolMap, tsMode, allToolDefs, discovered, loopDet, calledTools)
		messages = append(messages, toolMsg)
		toolResults = append(toolResults, tr)
		pendingParts = append(pendingParts, fps...)
	}
	if err := e.memory.SaveToolCallRound(ctx, ec.conv.ID, assistant.Content, assistant.ToolCalls, toolResults); err != nil {
		ec.l.WithError(err).Warn("[Memory] save tool call round failed")
	}
	if len(pendingParts) > 0 {
		parts := append([]openai.ChatMessagePart{
			{Type: openai.ChatMessagePartTypeText, Text: "工具返回了以下文件:"},
		}, pendingParts...)
		messages = append(messages, openai.ChatCompletionMessage{
			Role:         openai.ChatMessageRoleUser,
			MultiContent: parts,
		})
	}
	return messages
}
