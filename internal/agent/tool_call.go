package agent

import (
	"context"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"
)

// ToolResult 表示单个工具调用的结果。
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
}

const toolExecutionWall = 5 * time.Minute

func toolCallContext(parent context.Context) (ctx context.Context, done func()) {
	toolCtx, toolCancel := context.WithTimeout(context.Background(), toolExecutionWall)
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

func (e *Executor) runOneToolCall(ctx context.Context, ec *execContext, tc openai.ToolCall, st *agentRunState) (toolMsg openai.ChatCompletionMessage, tr ToolResult, fileParts []openai.ChatMessagePart) {
	toolName := tc.Function.Name
	toolArgs := tc.Function.Arguments

	if st.TSMode && toolName == toolSearchName {
		if blocked, guardMsg := st.loopDet.check(toolName, toolArgs); blocked {
			ec.l.WithField("tool", toolName).Warn("[LoopGuard] blocked tool_search")
			return toolResultMsg(tc.ID, toolName, guardMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil
		}
		st.loopDet.record(toolName, toolArgs)
		toolMsg = e.handleToolSearch(ctx, ec, tc, st)
		return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, nil
	}

	tool, ok := st.ToolMap[toolName]
	if !ok {
		errMsg := fmt.Sprintf("tool %q not found", toolName)
		ec.l.WithField("tool", toolName).Warn("[Tool] tool not registered, skipping")
		return toolResultMsg(tc.ID, toolName, errMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: errMsg}, nil
	}

	if blocked, guardMsg := st.loopDet.check(toolName, toolArgs); blocked {
		ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 120)}).Warn("[LoopGuard] blocked")
		return toolResultMsg(tc.ID, toolName, guardMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil
	}
	st.loopDet.record(toolName, toolArgs)

	ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 200)}).Info("[Tool] >> invoke")
	st.calledTools[toolName] = true
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

func (e *Executor) appendAssistantToolRound(ctx context.Context, ec *execContext, st *agentRunState, assistant openai.ChatCompletionMessage) {
	st.Messages = append(st.Messages, assistant)
	var toolResults []ToolResult
	var pendingParts []openai.ChatMessagePart
	for _, tc := range assistant.ToolCalls {
		toolMsg, tr, fps := e.runOneToolCall(ctx, ec, tc, st)
		st.Messages = append(st.Messages, toolMsg)
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
		st.Messages = append(st.Messages, openai.ChatCompletionMessage{
			Role:         openai.ChatMessageRoleUser,
			MultiContent: parts,
		})
	}
}

func toolResultMsg(toolCallID, toolName, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleTool, Content: content,
		ToolCallID: toolCallID, Name: toolName,
	}
}
