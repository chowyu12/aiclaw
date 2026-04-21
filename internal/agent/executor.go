package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tools/mcp"
	"github.com/chowyu12/aiclaw/internal/tools/sessionsearch"
	"github.com/chowyu12/aiclaw/internal/tools/todotool"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

type ExecuteResult struct {
	ConversationID string
	MessageID      int64
	Content        string
	TokensUsed     int
	Steps          []model.ExecutionStep
	ToolFiles      []*model.File
}

type ProviderFactory func(p *model.Provider) (provider.LLMProvider, error)

type ExecutorOption func(*Executor)

func WithProviderFactory(f ProviderFactory) ExecutorOption {
	return func(e *Executor) { e.providerFactory = f }
}

type skillCache struct {
	mu   sync.RWMutex
	data []model.Skill
	ts   time.Time
}

type toolsCache struct {
	mu   sync.RWMutex
	data []model.Tool
	ts   time.Time
}

type Executor struct {
	store           store.Store
	registry        *ToolRegistry
	memory          *MemoryManager
	providerFactory ProviderFactory
	hooks           *HookRegistry
	ws              *workspace.Workspace
	sc              *skillCache
	tc              *toolsCache
	schedCtxFn      func(context.Context) context.Context

	shutdownMu   sync.Mutex
	shutdownDone bool
	activeExecs  sync.WaitGroup

	// mcpMu 保护跨请求复用的 MCP Manager，避免每次 Execute 都重启 stdio 子进程。
	mcpMu          sync.Mutex
	mcpMgr         *mcp.Manager
	mcpFingerprint string
}

func WithHookRegistry(h *HookRegistry) ExecutorOption {
	return func(e *Executor) { e.hooks = h }
}

func NewExecutor(s store.Store, registry *ToolRegistry, ws *workspace.Workspace, opts ...ExecutorOption) *Executor {
	e := &Executor{
		store:           s,
		registry:        registry,
		memory:          NewMemoryManager(s),
		providerFactory: provider.NewFromProvider,
		hooks:           NewHookRegistry(),
		ws:              ws,
		sc:              &skillCache{},
		tc:              &toolsCache{},
	}
	for _, opt := range opts {
		opt(e)
	}
	registerDefaultHooks(e.hooks)
	registry.RegisterBuiltin("sub_agent", e.subAgentHandler)
	registry.RegisterBuiltin("session_search", sessionsearch.NewHandler(s))
	return e
}

// Hooks 返回执行器的 HookRegistry，允许外部注册生命周期钩子。
func (e *Executor) Hooks() *HookRegistry { return e.hooks }

// SetSchedulerContextFunc 设置调度器 context 注入函数。
func (e *Executor) SetSchedulerContextFunc(fn func(context.Context) context.Context) {
	e.schedCtxFn = fn
}

// execContext 聚合单次执行所需的全部上下文。
type execContext struct {
	ctx     context.Context
	ag      *model.Agent
	prov    *model.Provider
	llmProv provider.LLMProvider
	conv    *model.Conversation
	skills  []model.Skill
	tracker *StepTracker
	files   []*model.File
	userMsg string
	l       *log.Entry

	agentTools   []model.Tool
	mcpTools     []Tool
	skillTools   []Tool
	mcpManager   *mcp.Manager
	toolSkillMap map[string]string

	toolFiles []*model.File

	// ephemeral 为 true 时跳过消息持久化（sub_agent 模式），
	// 步骤仍通过 tracker 记录在父会话中。
	ephemeral bool
}

func (ec *execContext) hasTools() bool {
	return len(ec.agentTools) > 0 || len(ec.mcpTools) > 0 || len(ec.skillTools) > 0
}

// closeMCP 过去每次请求结束都关闭 MCP manager；现在改为在 Executor 层共享，
// 因此每次请求结束不再关闭，仅在 Executor.Shutdown 时统一释放。保留空实现，
// 以便调用点语义保持不变、未来可按需切换策略。
func (ec *execContext) closeMCP() {
	// intentionally empty; MCP manager lifecycle is managed by Executor.
}

func (ec *execContext) stepMeta() *model.StepMetadata {
	return &model.StepMetadata{
		Provider:    ec.prov.Name,
		Model:       ec.ag.ModelName,
		Temperature: ec.ag.Temperature,
	}
}

func (e *Executor) checkShutdown() error {
	e.shutdownMu.Lock()
	defer e.shutdownMu.Unlock()
	if e.shutdownDone {
		return errors.New("executor is shutting down")
	}
	e.activeExecs.Add(1)
	return nil
}

// Shutdown 通知 Executor 停止接受新请求并等待活跃执行完成。
func (e *Executor) Shutdown(timeout time.Duration) {
	e.shutdownMu.Lock()
	e.shutdownDone = true
	e.shutdownMu.Unlock()

	done := make(chan struct{})
	go func() {
		e.activeExecs.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Info("[Executor] all active executions completed gracefully")
	case <-time.After(timeout):
		log.WithField("timeout", timeout).Warn("[Executor] shutdown timeout, some executions may be interrupted")
	}

	e.mcpMu.Lock()
	if e.mcpMgr != nil {
		e.mcpMgr.Close()
		e.mcpMgr = nil
		e.mcpFingerprint = ""
	}
	e.mcpMu.Unlock()
}

func (e *Executor) Execute(ctx context.Context, req model.ChatRequest) (*ExecuteResult, error) {
	if err := e.checkShutdown(); err != nil {
		return nil, err
	}
	defer e.activeExecs.Done()

	ec, err := e.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	defer ec.closeMCP()

	ec.l.WithField("user", req.UserID).Debug("[Execute] >> start")

	res, err := e.run(ec.ctx, ec, blockingCaller(ec.llmProv), false)
	if err != nil {
		e.saveErrorMessage(ec, err)
		return nil, err
	}
	return res, nil
}

func (e *Executor) ExecuteStream(ctx context.Context, req model.ChatRequest, chunkHandler func(chunk model.StreamChunk) error) error {
	if err := e.checkShutdown(); err != nil {
		return err
	}
	defer e.activeExecs.Done()

	ec, err := e.prepare(ctx, req)
	if err != nil {
		return err
	}
	defer ec.closeMCP()

	ec.l.WithField("user", req.UserID).Debug("[Execute] >> start (stream)")

	ec.tracker.SetOnStep(func(step model.ExecutionStep) {
		_ = chunkHandler(model.StreamChunk{ConversationID: ec.conv.UUID, Step: &step})
	})

	res, err := e.run(ec.ctx, ec, streamingCaller(ec.llmProv, ec.conv.UUID, chunkHandler), true)
	if err != nil {
		e.saveErrorMessage(ec, err)
		return err
	}
	doneChunk := model.StreamChunk{
		ConversationID: ec.conv.UUID,
		MessageID:      res.MessageID,
		Done:           true,
		Content:        res.Content,
		TokensUsed:     res.TokensUsed,
		Steps:          res.Steps,
	}
	if len(ec.toolFiles) > 0 {
		doneChunk.Files = ec.toolFiles
	}
	return chunkHandler(doneChunk)
}

// saveErrorMessage 执行失败时保存一条错误 assistant 消息，确保刷新页面后能看到失败记录。
// ephemeral 模式（sub_agent）下跳过持久化，错误通过返回值传递。
func (e *Executor) saveErrorMessage(ec *execContext, execErr error) {
	if ec.ephemeral {
		return
	}
	content := fmt.Sprintf("[错误] %s", execErr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msgID, err := e.memory.SaveAssistantMessage(ctx, ec.conv.ID, content, 0)
	if err != nil {
		ec.l.WithError(err).Error("[Execute] save error message failed")
		return
	}
	ec.tracker.SetMessageID(msgID)
	ec.l.WithFields(log.Fields{"msg_id": msgID, "error": execErr}).Warn("[Execute] << error saved")
}

func (e *Executor) prepare(ctx context.Context, req model.ChatRequest) (*execContext, error) {
	ag, err := e.loadAgent(ctx, req.AgentUUID)
	if err != nil {
		log.WithError(err).Error("[Execute] load agent config failed")
		return nil, fmt.Errorf("agent not found: %w", err)
	}
	ctx = workspace.WithWorkdirScope(ctx, ag.UUID)
	if e.ws != nil {
		ctx = workspace.WithWorkspace(ctx, e.ws)
	}
	if e.schedCtxFn != nil {
		ctx = e.schedCtxFn(ctx)
	}

	prov, err := e.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		log.WithFields(log.Fields{"agent": ag.Name, "provider_id": ag.ProviderID}).WithError(err).Error("[Execute] provider not found")
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	l := log.WithFields(log.Fields{"agent": ag.Name, "provider": prov.Name, "model": ag.ModelName})
	if depth := subAgentDepth(ctx); depth > 0 {
		l = l.WithField("sub_agent", fmt.Sprintf("L%d", depth))
	}

	llmProv, err := e.providerFactory(prov)
	if err != nil {
		l.WithError(err).Error("[Execute] create llm provider failed")
		return nil, fmt.Errorf("create llm provider: %w", err)
	}

	agentTools, toolSkillMap, err := e.collectTools(ctx, ag)
	if err != nil {
		l.WithError(err).Error("[Execute] collect tools failed")
		return nil, err
	}

	skills, err := e.loadWorkspaceSkills()
	if err != nil {
		l.WithError(err).Warn("[Execute] load workspace skills failed, continuing without skills")
		skills = nil
	}

	isNewConv := req.ConversationID == ""
	conv, err := e.memory.GetOrCreateConversation(ctx, req.ConversationID, req.UserID, ag.UUID)
	if err != nil {
		l.WithError(err).Error("[Execute] get/create conversation failed")
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if isNewConv {
		e.memory.AutoSetTitle(ctx, conv.ID, req.Message)
	}

	ctx = todotool.WithTodoStore(ctx, todotool.GetOrCreateStore(conv.UUID))

	tracker := NewStepTracker(e.store, conv.ID)
	if req.ExecChannel != nil {
		tracker.SetChannelTrace(req.ExecChannel)
	}

	mcpServers, err := e.store.ListMCPServers(ctx)
	if err != nil {
		l.WithError(err).Warn("[Execute] list MCP servers failed, continuing without MCP")
		mcpServers = nil
	}
	mcpManager, mcpTools := e.connectMCPServers(ctx, mcpServers, tracker, toolSkillMap)
	skillTools := e.buildSkillManifestTools(skills, tracker, toolSkillMap)

	logResourceSummary(l, agentTools, skills)

	files := e.loadRequestFiles(ctx, req.Files, conv.ID)

	return &execContext{
		ctx:          ctx,
		ag:           ag,
		prov:         prov,
		llmProv:      llmProv,
		conv:         conv,
		skills:       skills,
		tracker:      tracker,
		files:        files,
		userMsg:      req.Message,
		l:            l.WithField("conv", conv.UUID),
		agentTools:   agentTools,
		mcpTools:     mcpTools,
		skillTools:   skillTools,
		mcpManager:   mcpManager,
		toolSkillMap: toolSkillMap,
	}, nil
}

// loadAgent 按 agentUUID 从 DB 加载 Agent；若 UUID 为空则取默认 Agent。
func (e *Executor) loadAgent(ctx context.Context, agentUUID string) (*model.Agent, error) {
	if agentUUID != "" {
		ag, err := e.store.GetAgentByUUID(ctx, agentUUID)
		if err != nil {
			return nil, fmt.Errorf("agent %q not found: %w", agentUUID, err)
		}
		normalizeAgent(ag)
		return ag, nil
	}
	ag, err := e.store.GetDefaultAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("no default agent configured: %w", err)
	}
	normalizeAgent(ag)
	return ag, nil
}

func normalizeAgent(a *model.Agent) {
	if a.MaxTokens == 0 {
		a.MaxTokens = 4096
	}
	if a.MaxHistory == 0 {
		a.MaxHistory = model.DefaultAgentMaxHistory
	}
	if a.MaxIterations == 0 {
		a.MaxIterations = model.DefaultAgentMaxIterations
	}
}

const (
	skillCacheTTL = 30 * time.Second
	toolsCacheTTL = 15 * time.Second
)

// listAllToolsCached 以 toolsCacheTTL 为窗口缓存「tool_search 模式」所需的完整工具清单，
// 避免每次 Execute 都做一次全表扫描（Agent 工具集通常在小时/天级别变动，短 TTL 足够）。
func (e *Executor) listAllToolsCached(ctx context.Context) ([]model.Tool, error) {
	e.tc.mu.RLock()
	if e.tc.data != nil && time.Since(e.tc.ts) < toolsCacheTTL {
		out := make([]model.Tool, len(e.tc.data))
		copy(out, e.tc.data)
		e.tc.mu.RUnlock()
		return out, nil
	}
	e.tc.mu.RUnlock()

	items, total, err := e.store.ListTools(ctx, model.ListQuery{Page: 1, PageSize: 10000})
	if err != nil {
		return nil, err
	}
	if int64(len(items)) < total {
		log.WithFields(log.Fields{"fetched": len(items), "total": total}).
			Warn("[Execute] tool count exceeds single-page limit, some tools may be unavailable")
	}

	cached := make([]model.Tool, 0, len(items))
	for _, t := range items {
		if t != nil {
			cached = append(cached, *t)
		}
	}

	e.tc.mu.Lock()
	e.tc.data = cached
	e.tc.ts = time.Now()
	e.tc.mu.Unlock()

	out := make([]model.Tool, len(cached))
	copy(out, cached)
	return out, nil
}

// InvalidateToolsCache 外部在工具增删改后调用，强制下一次 Execute 重新拉取。
func (e *Executor) InvalidateToolsCache() {
	e.tc.mu.Lock()
	e.tc.data = nil
	e.tc.ts = time.Time{}
	e.tc.mu.Unlock()
}

func (e *Executor) loadWorkspaceSkills() ([]model.Skill, error) {
	e.sc.mu.RLock()
	if e.sc.data != nil && time.Since(e.sc.ts) < skillCacheTTL {
		result := make([]model.Skill, len(e.sc.data))
		copy(result, e.sc.data)
		e.sc.mu.RUnlock()
		return result, nil
	}
	e.sc.mu.RUnlock()

	out := skills.BuiltinSkills()
	seen := make(map[string]bool, len(out))
	for _, s := range out {
		seen[s.DirName] = true
	}

	if e.ws != nil {
		dir := e.ws.Skills()
		infos, err := skills.ScanAll(dir)
		if err != nil {
			return out, err
		}
		for i := range infos {
			if seen[infos[i].DirName] {
				continue
			}
			s := skills.InfoToSkill(infos[i], model.SkillSourceLocal, "")
			s.Enabled = true
			out = append(out, *s)
		}
	}

	e.sc.mu.Lock()
	e.sc.data = make([]model.Skill, len(out))
	copy(e.sc.data, out)
	e.sc.ts = time.Now()
	e.sc.mu.Unlock()

	return out, nil
}

func (e *Executor) connectMCPServers(ctx context.Context, servers []model.MCPServer, tracker *StepTracker, toolSkillMap map[string]string) (*mcp.Manager, []Tool) {
	if len(servers) == 0 {
		return nil, nil
	}

	mgr, err := e.getOrConnectMCP(ctx, servers)
	if err != nil {
		log.WithError(err).Warn("[MCP] connect failed")
		return nil, nil
	}
	if mgr == nil || !mgr.HasTools() {
		return nil, nil
	}

	infos := mgr.Tools()
	mcpTools := make([]Tool, 0, len(infos))
	for _, info := range infos {
		toolSkillMap[info.Name] = "mcp:" + info.ServerName
		base := &dynamicTool{
			toolName: info.Name,
			toolDesc: info.Description,
			params:   info.Parameters,
			handler: func(ctx context.Context, input string) (string, error) {
				return mgr.CallTool(ctx, info.Name, input)
			},
		}
		mcpTools = append(mcpTools, &trackedTool{
			baseTool:  base,
			name:      info.Name,
			skillName: "mcp:" + info.ServerName,
			tracker:   tracker,
		})
	}
	log.WithField("count", len(mcpTools)).Debug("[MCP] tools loaded")
	return mgr, mcpTools
}

// getOrConnectMCP 返回 Executor 级共享的 mcp.Manager。
// 只有当 servers 的指纹（UUID/Transport/Endpoint/Enabled/UpdatedAt）发生变化时才会重连，
// 避免每次请求都启动/关闭 stdio 子进程。
func (e *Executor) getOrConnectMCP(ctx context.Context, servers []model.MCPServer) (*mcp.Manager, error) {
	fp := mcpServersFingerprint(servers)

	e.mcpMu.Lock()
	if e.mcpMgr != nil && e.mcpFingerprint == fp {
		mgr := e.mcpMgr
		e.mcpMu.Unlock()
		return mgr, nil
	}
	oldMgr := e.mcpMgr
	e.mcpMgr = nil
	e.mcpFingerprint = ""
	e.mcpMu.Unlock()

	if oldMgr != nil {
		oldMgr.Close()
	}

	mgr := mcp.NewManager()
	if err := mgr.Connect(ctx, servers); err != nil {
		mgr.Close()
		return nil, err
	}
	if !mgr.HasTools() {
		mgr.Close()
		return nil, nil
	}

	e.mcpMu.Lock()
	e.mcpMgr = mgr
	e.mcpFingerprint = fp
	e.mcpMu.Unlock()
	return mgr, nil
}

// mcpServersFingerprint 根据 server 关键字段生成指纹，用于判断缓存是否失效。
func mcpServersFingerprint(servers []model.MCPServer) string {
	if len(servers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(servers))
	for _, s := range servers {
		keys = append(keys, fmt.Sprintf("%s|%s|%s|%v|%d", s.UUID, s.Transport, s.Endpoint, s.Enabled, s.UpdatedAt.UnixNano()))
	}
	sort.Strings(keys)
	return strings.Join(keys, ";")
}

func (e *Executor) buildSkillManifestTools(skillList []model.Skill, tracker *StepTracker, toolSkillMap map[string]string) []Tool {
	var result []Tool
	for _, sk := range skillList {
		if !sk.Enabled || len(sk.ToolDefs) == 0 {
			continue
		}
		var toolDefs []model.SkillManifestTool
		if err := json.Unmarshal(sk.ToolDefs, &toolDefs); err != nil {
			log.WithError(err).WithField("skill", sk.Name).Warn("[Skill] parse tool_defs failed")
			continue
		}
		for _, td := range toolDefs {
			toolSkillMap[td.Name] = sk.Name
			var handler func(ctx context.Context, input string) (string, error)

			if sk.MainFile != "" && e.ws != nil {
				skillDir := e.ws.SkillDir(sk.DirName)
				if skillDir != "" {
					mainFile := sk.MainFile
					handler = func(ctx context.Context, input string) (string, error) {
						return skills.RunTool(ctx, skillDir, mainFile, td.Name, input, nil, 0)
					}
				}
			}
			if handler == nil {
				instruction := sk.Instruction
				handler = func(_ context.Context, input string) (string, error) {
					return fmt.Sprintf("[skill:%s] 请根据技能指令处理。输入: %s\n指令: %s", sk.Name, input, instruction), nil
				}
			}

			base := &dynamicTool{
				toolName: td.Name,
				toolDesc: td.Description,
				params:   td.Parameters,
				handler:  handler,
			}
			result = append(result, &trackedTool{
				baseTool:  base,
				name:      td.Name,
				skillName: sk.Name,
				tracker:   tracker,
			})
		}
		log.WithFields(log.Fields{"skill": sk.Name, "manifest_tools": len(toolDefs)}).Debug("[Execute]    skill manifest tools loaded")
	}
	return result
}

func (e *Executor) collectTools(ctx context.Context, ag *model.Agent) ([]model.Tool, map[string]string, error) {
	var agentTools []model.Tool
	seenName := make(map[string]bool)

	for _, bt := range e.registry.BuiltinDefs() {
		agentTools = append(agentTools, bt)
		seenName[bt.Name] = true
	}

	if ag.ToolSearchEnabled {
		items, err := e.listAllToolsCached(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("list all tools: %w", err)
		}
		for i := range items {
			t := &items[i]
			if t.Enabled && !seenName[t.Name] {
				agentTools = append(agentTools, *t)
				seenName[t.Name] = true
			}
		}
	} else {
		for _, tid := range ag.ToolIDs {
			t, err := e.store.GetTool(ctx, tid)
			if err != nil || t == nil || !t.Enabled {
				continue
			}
			if !seenName[t.Name] {
				agentTools = append(agentTools, *t)
				seenName[t.Name] = true
			}
		}
	}

	log.WithField("count", len(agentTools)).Debug("[Execute]    tools loaded (builtins always included)")

	toolSkillMap := make(map[string]string)
	return agentTools, toolSkillMap, nil
}

func (e *Executor) saveResult(ctx context.Context, ec *execContext, st *agentRunState, content string, tokensUsed int, duration time.Duration) (*ExecuteResult, error) {
	// ephemeral 模式（sub_agent）：只记录执行步骤，跳过消息持久化和 HookAgentDone
	if ec.ephemeral {
		ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, content, model.StepSuccess, "", duration, tokensUsed, ec.stepMeta())
		ec.l.WithFields(log.Fields{"duration": duration, "tokens": tokensUsed}).Info("[SubAgent] << saveResult (ephemeral)")
		return &ExecuteResult{
			Content:    content,
			TokensUsed: tokensUsed,
			Steps:      ec.tracker.Steps(),
			ToolFiles:  append([]*model.File(nil), ec.toolFiles...),
		}, nil
	}

	msgID, err := e.memory.SaveAssistantMessage(ctx, ec.conv.ID, content, tokensUsed)
	if err != nil {
		ec.l.WithError(err).Error("[Execute] save assistant message failed")
		return nil, err
	}

	if len(ec.toolFiles) > 0 {
		e.memory.LinkFilesToMessage(ctx, ec.toolFiles, ec.conv.ID, msgID)
	}

	ec.tracker.SetMessageID(msgID)
	ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, content, model.StepSuccess, "", duration, tokensUsed, ec.stepMeta())

	ec.l.WithFields(log.Fields{"msg_id": msgID, "duration": duration, "tokens": tokensUsed}).Info("[Execute] << done")

	e.hooks.Fire(ctx, HookAgentDone, &HookPayload{
		Model:       ec.ag.ModelName,
		ConvUUID:    ec.conv.UUID,
		UserMsg:     ec.userMsg,
		Content:     content,
		TotalTokens: tokensUsed,
		Duration:    duration,
		Agent:        ec.ag,
		Skills:       ec.skills,
		CalledTools:  st.calledTools,
		ToolSkillMap: ec.toolSkillMap,
		Tracker:      ec.tracker,
		WS:           e.ws,
	})

	return &ExecuteResult{
		ConversationID: ec.conv.UUID,
		MessageID:      msgID,
		Content:        content,
		TokensUsed:     tokensUsed,
		Steps:          ec.tracker.Steps(),
		ToolFiles:      append([]*model.File(nil), ec.toolFiles...),
	}, nil
}

// prepareSubAgent 为 sub_agent 构建 execContext：
//   - 复用父 tracker（步骤记录在父会话中）
//   - 不创建独立会话、不持久化消息
//   - 完整加载工具/MCP/skills
//   - 根据 blocklist 过滤子 agent 可用工具
func (e *Executor) prepareSubAgent(ctx context.Context, prompt, agentUUID string) (*execContext, error) {
	parentTracker, parentConvID := parentExecInfoFromCtx(ctx)
	if parentTracker == nil {
		return nil, errors.New("sub_agent: parent tracker not found in context")
	}

	ag, err := e.loadAgent(ctx, agentUUID)
	if err != nil {
		return nil, fmt.Errorf("sub_agent: agent not found: %w", err)
	}
	ctx = workspace.WithWorkdirScope(ctx, ag.UUID)
	if e.ws != nil {
		ctx = workspace.WithWorkspace(ctx, e.ws)
	}

	prov, err := e.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("sub_agent: provider not found: %w", err)
	}

	l := log.WithFields(log.Fields{
		"agent": ag.Name, "provider": prov.Name, "model": ag.ModelName,
		"sub_agent": fmt.Sprintf("L%d", subAgentDepth(ctx)),
	})

	llmProv, err := e.providerFactory(prov)
	if err != nil {
		return nil, fmt.Errorf("sub_agent: create provider: %w", err)
	}

	agentTools, toolSkillMap, err := e.collectTools(ctx, ag)
	if err != nil {
		return nil, err
	}

	// 应用 blocklist 过滤
	agentTools = filterBlockedTools(ctx, agentTools, l)

	skills, err := e.loadWorkspaceSkills()
	if err != nil {
		l.WithError(err).Warn("[SubAgent] load skills failed, continuing without skills")
		skills = nil
	}

	mcpServers, err := e.store.ListMCPServers(ctx)
	if err != nil {
		l.WithError(err).Warn("[SubAgent] list MCP servers failed, continuing without MCP")
		mcpServers = nil
	}
	mcpManager, mcpTools := e.connectMCPServers(ctx, mcpServers, parentTracker, toolSkillMap)
	skillTools := e.buildSkillManifestTools(skills, parentTracker, toolSkillMap)

	conv := &model.Conversation{ID: parentConvID}

	return &execContext{
		ctx:          ctx,
		ag:           ag,
		prov:         prov,
		llmProv:      llmProv,
		conv:         conv,
		skills:       skills,
		tracker:      parentTracker,
		userMsg:      prompt,
		l:            l,
		agentTools:   agentTools,
		mcpTools:     mcpTools,
		skillTools:   skillTools,
		mcpManager:   mcpManager,
		toolSkillMap: toolSkillMap,
		ephemeral:    true,
	}, nil
}

// filterBlockedTools 从工具列表中移除被 blocklist 禁止的工具。
func filterBlockedTools(ctx context.Context, tools []model.Tool, l *log.Entry) []model.Tool {
	var filtered []model.Tool
	var blocked []string
	for _, t := range tools {
		if IsToolBlocked(ctx, t.Name) {
			blocked = append(blocked, t.Name)
			continue
		}
		filtered = append(filtered, t)
	}
	if len(blocked) > 0 {
		l.WithField("blocked", blocked).Debug("[SubAgent] tools filtered by blocklist")
	}
	return filtered
}
