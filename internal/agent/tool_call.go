package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/tools"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/google/uuid"
)

// ToolResult 表示单个工具调用的结果。
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
}

const toolExecutionWall = 5 * time.Minute

func toolCallContext(parent context.Context) (ctx context.Context, done func()) {
	base := propagateAgentValues(parent)
	toolCtx, toolCancel := context.WithTimeout(base, toolExecutionWall)
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

// runOneToolCall 执行单个工具调用，返回消息、结果、文件附件、持久化文件。
// 方法内部使用 st.mu 保护共享状态（loopDet / calledTools），可安全并行调用。
func (e *Executor) runOneToolCall(ctx context.Context, ec *execContext, tc openai.ToolCall, st *agentRunState) (toolMsg openai.ChatCompletionMessage, tr ToolResult, fileParts []openai.ChatMessagePart, toolFile *model.File) {
	toolName := tc.Function.Name
	toolArgs := tc.Function.Arguments

	if st.TSMode && toolName == toolSearchName {
		st.mu.Lock()
		if blocked, guardMsg := st.loopDet.check(toolName, toolArgs); blocked {
			st.mu.Unlock()
			ec.l.WithField("tool", toolName).Warn("[LoopGuard] blocked tool_search")
			return toolResultMsg(tc.ID, toolName, guardMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil, nil
		}
		st.loopDet.record(toolName, toolArgs)
		st.mu.Unlock()
		toolMsg = e.handleToolSearch(ctx, ec, tc, st)
		return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, nil, nil
	}

	tool, ok := st.ToolMap[toolName]
	if !ok {
		errMsg := fmt.Sprintf("tool %q not found", toolName)
		ec.l.WithField("tool", toolName).Warn("[Tool] tool not registered, skipping")
		return toolResultMsg(tc.ID, toolName, errMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: errMsg}, nil, nil
	}

	st.mu.Lock()
	if blocked, guardMsg := st.loopDet.check(toolName, toolArgs); blocked {
		st.mu.Unlock()
		ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 120)}).Warn("[LoopGuard] blocked")
		return toolResultMsg(tc.ID, toolName, guardMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: guardMsg}, nil, nil
	}
	st.loopDet.record(toolName, toolArgs)
	st.calledTools[toolName] = true
	st.mu.Unlock()

	// PreToolUse hook
	if action := e.hooks.Fire(ctx, HookPreToolUse, &HookPayload{ToolName: toolName, ToolArgs: toolArgs}); action == HookSkip {
		ec.l.WithField("tool", toolName).Info("[Hook] tool call skipped by pre_tool_use hook")
		skipMsg := fmt.Sprintf("tool %q skipped by policy", toolName)
		return toolResultMsg(tc.ID, toolName, skipMsg), ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: skipMsg}, nil, nil
	}

	ec.l.WithFields(log.Fields{"tool": toolName, "args": truncateLog(toolArgs, 200)}).Info("[Tool] >> invoke")
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

	// PostToolUse hook
	e.hooks.Fire(ctx, HookPostToolUse, &HookPayload{ToolName: toolName, ToolArgs: toolArgs, Result: output, Error: callErr})

	toolMsg, fileParts = e.buildToolResponseParts(ctx, tc.ID, toolName, toolResult, callErr == nil, ec.l)

	if callErr == nil {
		toolFile = e.persistToolFile(ctx, ec, toolResult)
	}

	return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, fileParts, toolFile
}

func (e *Executor) persistToolFile(ctx context.Context, ec *execContext, toolResult string) *model.File {
	fr := tools.ParseFileResult(toolResult)
	if fr == nil || !strings.HasPrefix(fr.MimeType, "image/") {
		return nil
	}

	data, err := os.ReadFile(fr.Path)
	if err != nil {
		ec.l.WithError(err).WithField("path", fr.Path).Warn("[Tool] read tool file for persist failed")
		return nil
	}

	ws := workspace.FromContext(ec.ctx)
	if ws == nil {
		return nil
	}
	uploadsDir := ws.Uploads()

	fileUUID := uuid.New().String()
	ext := filepath.Ext(fr.Path)
	storagePath := filepath.Join(uploadsDir, fileUUID+ext)
	if err := os.WriteFile(storagePath, data, 0o644); err != nil {
		ec.l.WithError(err).Warn("[Tool] persist tool file to uploads failed")
		return nil
	}

	f := &model.File{
		UUID:           fileUUID,
		ConversationID: ec.conv.ID,
		Filename:       filepath.Base(fr.Path),
		ContentType:    fr.MimeType,
		FileSize:       int64(len(data)),
		FileType:       model.FileTypeImage,
		StoragePath:    storagePath,
	}
	if err := e.store.CreateFile(ctx, f); err != nil {
		ec.l.WithError(err).Warn("[Tool] create file record failed")
		return nil
	}
	ec.l.WithFields(log.Fields{"file_uuid": fileUUID, "path": storagePath}).Info("[Tool] persisted tool screenshot as file")
	return f
}

// appendAssistantToolRound 执行一轮工具调用：并发安全的工具并行执行，其余串行执行。
func (e *Executor) appendAssistantToolRound(ctx context.Context, ec *execContext, st *agentRunState, assistant openai.ChatCompletionMessage) {
	st.Messages = append(st.Messages, assistant)
	tcs := assistant.ToolCalls
	n := len(tcs)

	// 统计本轮 sub_agent 调用数量，注入 context 供 handler 决定走完整/轻量路径
	subAgentCnt := 0
	for _, tc := range tcs {
		if tc.Function.Name == "sub_agent" {
			subAgentCnt++
		}
	}
	if subAgentCnt > 0 {
		ctx = withSubAgentRoundCount(ctx, subAgentCnt)
	}

	type callResult struct {
		toolMsg   openai.ChatCompletionMessage
		tr        ToolResult
		fileParts []openai.ChatMessagePart
		toolFile  *model.File
	}
	results := make([]callResult, n)

	// 分类：并发安全 vs 需要串行执行
	var safeIdx, seqIdx []int
	for i, tc := range tcs {
		if st.TSMode && tc.Function.Name == toolSearchName {
			seqIdx = append(seqIdx, i)
			continue
		}
		tool, ok := st.ToolMap[tc.Function.Name]
		if ok && tool.IsConcurrencySafe() {
			safeIdx = append(safeIdx, i)
		} else {
			seqIdx = append(seqIdx, i)
		}
	}

	// 并发安全工具：≥2 个时并行执行
	if len(safeIdx) > 1 {
		ec.l.WithField("count", len(safeIdx)).Debug("[Tool] running concurrency-safe tools in parallel")
		var wg sync.WaitGroup
		for _, idx := range safeIdx {
			wg.Go(func() {
				msg, tr, fps, tf := e.runOneToolCall(ctx, ec, tcs[idx], st)
				results[idx] = callResult{msg, tr, fps, tf}
			})
		}
		wg.Wait()
	} else if len(safeIdx) == 1 {
		idx := safeIdx[0]
		msg, tr, fps, tf := e.runOneToolCall(ctx, ec, tcs[idx], st)
		results[idx] = callResult{msg, tr, fps, tf}
	}

	// 非安全工具：顺序执行
	for _, idx := range seqIdx {
		msg, tr, fps, tf := e.runOneToolCall(ctx, ec, tcs[idx], st)
		results[idx] = callResult{msg, tr, fps, tf}
	}

	// 按原始顺序收集结果
	var toolResults []ToolResult
	var pendingParts []openai.ChatMessagePart
	for _, r := range results {
		st.Messages = append(st.Messages, r.toolMsg)
		toolResults = append(toolResults, r.tr)
		pendingParts = append(pendingParts, r.fileParts...)
		if r.toolFile != nil {
			ec.toolFiles = append(ec.toolFiles, r.toolFile)
		}
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
