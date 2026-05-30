package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	openai "github.com/chowyu12/go-openai"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/tools"
	"github.com/chowyu12/aiclaw/internal/tools/result"
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
func (e *Executor) runOneToolCall(ctx context.Context, ec *execContext, tc openai.ToolCall, st *agentRunState) (toolMsg openai.ChatCompletionMessage, tr ToolResult, fileParts []openai.ChatMessagePart, toolFiles []*model.File) {
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
	if toolName == "sub_agent" {
		toolCtx = withSubAgentCallID(toolCtx, tc.ID)
	}

	// 工具调用前：快照 sandbox 文件列表，用于事后检测新增文件。
	sandboxDir := workspace.AgentSandboxFromCtx(toolCtx)
	preSandbox := snapshotDir(sandboxDir)

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

	// PostToolUse hook（异步，不阻塞工具链路）
	e.hooks.FireAsync(ctx, HookPostToolUse, &HookPayload{ToolName: toolName, ToolArgs: toolArgs, Result: output, Error: callErr})

	toolMsg, fileParts = e.buildToolResponseParts(ctx, tc.ID, toolName, toolResult, callErr == nil, ec.l)

	if callErr == nil {
		if toolFile := e.persistToolFile(ctx, ec, toolResult); toolFile != nil {
			toolFiles = append(toolFiles, toolFile)
		}
		if toolName == "sub_agent" {
			toolFiles = append(toolFiles, filesFromSubAgentOutput(toolResult)...)
		}
		// 若工具未返回 FileResult（如 codeinterp、shellexec），则扫描 sandbox
		// 目录查找本轮新建的文件，并将其持久化为工具输出附件。
		if len(toolFiles) == 0 && sandboxDir != "" {
			toolFiles = append(toolFiles, e.persistNewSandboxFiles(ctx, ec, sandboxDir, preSandbox)...)
		}
	}

	return toolMsg, ToolResult{ToolCallID: tc.ID, ToolName: toolName, Content: toolMsg.Content}, fileParts, dedupeFiles(toolFiles)
}

func (e *Executor) persistToolFile(ctx context.Context, ec *execContext, toolResult string) *model.File {
	fr := tools.ParseFileResult(toolResult)
	if fr == nil {
		return nil
	}

	ec.l.WithFields(log.Fields{"path": fr.Path, "mime": fr.MimeType}).Debug("[Tool] detected file result, persisting...")

	data, err := os.ReadFile(fr.Path)
	if err != nil {
		ec.l.WithError(err).WithField("path", fr.Path).Warn("[Tool] read tool file for persist failed")
		return nil
	}

	ws := workspace.FromContext(ec.ctx)
	if ws == nil {
		ec.l.Warn("[Tool] workspace not available, cannot persist tool file")
		return nil
	}
	uploadsDir := ws.Uploads()

	fileUUID := uuid.New().String()
	ext := filepath.Ext(fr.Path)
	storagePath := filepath.Join(uploadsDir, fileUUID+ext)
	if err := os.WriteFile(storagePath, data, 0o644); err != nil {
		ec.l.WithError(err).WithField("storage_path", storagePath).Warn("[Tool] persist tool file to uploads failed")
		return nil
	}

	f := &model.File{
		UUID:           fileUUID,
		ConversationID: ec.conv.ID,
		Filename:       filepath.Base(fr.Path),
		ContentType:    fr.MimeType,
		FileSize:       int64(len(data)),
		FileType:       model.ClassifyFileType(fr.MimeType, filepath.Base(fr.Path)),
		StoragePath:    storagePath,
	}
	if err := e.store.CreateFile(ctx, f); err != nil {
		ec.l.WithError(err).WithField("filename", f.Filename).Warn("[Tool] create file record failed")
		return nil
	}
	ec.l.WithFields(log.Fields{
		"file_uuid": fileUUID,
		"filename":  f.Filename,
		"path":      storagePath,
		"type":      f.FileType,
	}).Info("[Tool] persisted tool output as file")
	return f
}

// snapshotDir 返回目录中所有文件的名称→修改时间戳映射；目录为空或不可读时返回 nil。
func snapshotDir(dir string) map[string]int64 {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	snap := make(map[string]int64, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			snap[e.Name()] = info.ModTime().UnixNano()
		}
	}
	return snap
}

// persistNewSandboxFiles 在 sandbox 目录中查找相比快照新增或修改的文件，
// 并持久化为工具输出附件。
// pre 为 nil 时（快照失败）直接返回 nil，避免误判历史文件。
func (e *Executor) persistNewSandboxFiles(ctx context.Context, ec *execContext, sandboxDir string, pre map[string]int64) []*model.File {
	if pre == nil {
		return nil
	}
	entries, err := os.ReadDir(sandboxDir)
	if err != nil {
		return nil
	}
	var files []*model.File
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		prevMtime, existed := pre[name]
		// 跳过执行脚本本身（codeinterp 写入的临时代码文件）
		if existed && info.ModTime().UnixNano() == prevMtime {
			continue
		}
		// 跳过 .py / .js / .sh 等代码文件（这些是执行载体，不是产出物）
		switch filepath.Ext(name) {
		case ".py", ".js", ".sh", ".rb", ".ts":
			continue
		}
		fullPath := filepath.Join(sandboxDir, name)
		mimeStr := result.MimeFromExt(filepath.Ext(name))
		fr := result.NewFileResult(fullPath, mimeStr, name)
		if tf := e.persistToolFile(ctx, ec, fr); tf != nil {
			ec.l.WithFields(log.Fields{"file": name, "sandbox": sandboxDir}).Info("[Tool] persisted new sandbox file as output")
			files = append(files, tf)
		}
	}
	return dedupeFiles(files)
}

func filesFromSubAgentOutput(output string) []*model.File {
	var batch subAgentBatchResult
	if err := json.Unmarshal([]byte(output), &batch); err != nil {
		return nil
	}
	var files []*model.File
	for _, r := range batch.Results {
		files = append(files, r.Files...)
	}
	return dedupeFiles(files)
}

func dedupeFiles(files []*model.File) []*model.File {
	seen := make(map[string]bool, len(files))
	out := make([]*model.File, 0, len(files))
	for _, f := range files {
		if f == nil {
			continue
		}
		key := f.UUID
		if key == "" {
			key = fmt.Sprintf("%d:%s", f.ID, f.Filename)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// appendAssistantToolRound 执行一轮工具调用：并发安全的工具并行执行，其余串行执行。
func (e *Executor) appendAssistantToolRound(ctx context.Context, ec *execContext, st *agentRunState, assistant openai.ChatCompletionMessage) {
	st.Messages = append(st.Messages, assistant)
	tcs := assistant.ToolCalls
	n := len(tcs)

	// 注入父执行信息，供 sub_agent handler 获取父 tracker 和会话 ID
	ctx = withParentExecInfo(ctx, ec.tracker, ec.conv.ID)

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
		toolFiles []*model.File
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
		if len(r.toolFiles) > 0 {
			ec.toolFiles = append(ec.toolFiles, r.toolFiles...)
		}
	}
	ec.toolFiles = dedupeFiles(ec.toolFiles)

	if !ec.ephemeral {
		if err := e.memory.SaveToolCallRound(ctx, ec.conv.ID, assistant.Content, assistant.ToolCalls, toolResults); err != nil {
			ec.l.WithError(err).Warn("[Memory] save tool call round failed")
		}
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
