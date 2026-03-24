package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/skill"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/tool/mcp"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

// ============================================================
//  类型定义
// ============================================================

type ExecuteResult struct {
	ConversationID string
	Content        string
	TokensUsed     int
	Steps          []model.ExecutionStep
}

type ProviderFactory func(p *model.Provider, modelName string) (provider.LLMProvider, error)

type ExecutorOption func(*Executor)

func WithProviderFactory(f ProviderFactory) ExecutorOption {
	return func(e *Executor) { e.providerFactory = f }
}

type Executor struct {
	store           store.Store
	registry        *ToolRegistry
	memory          *MemoryManager
	providerFactory ProviderFactory
}

func NewExecutor(s store.Store, registry *ToolRegistry, opts ...ExecutorOption) *Executor {
	e := &Executor{
		store:           s,
		registry:        registry,
		memory:          NewMemoryManager(s, s),
		providerFactory: provider.NewFromProvider,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// execContext 聚合单次执行所需的全部上下文。
type execContext struct {
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
}

func (ec *execContext) hasTools() bool {
	return len(ec.agentTools) > 0 || len(ec.mcpTools) > 0 || len(ec.skillTools) > 0
}

func (ec *execContext) closeMCP() {
	if ec.mcpManager != nil {
		ec.mcpManager.Close()
	}
}

func (ec *execContext) stepMeta() *model.StepMetadata {
	return &model.StepMetadata{
		Provider:    ec.prov.Name,
		Model:       ec.ag.ModelName,
		Temperature: ec.ag.Temperature,
	}
}

// ============================================================
//  对外入口
// ============================================================

func (e *Executor) Execute(ctx context.Context, req model.ChatRequest) (*ExecuteResult, error) {
	ec, err := e.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	defer ec.closeMCP()

	ctx = workspace.WithWorkdirScope(ctx, ec.ag.UUID)
	ec.l.WithField("user", req.UserID).Info("[Execute] >> start")

	return e.run(ctx, ec, blockingCaller(ec.llmProv), false)
}

func (e *Executor) ExecuteStream(ctx context.Context, req model.ChatRequest, chunkHandler func(chunk model.StreamChunk) error) error {
	ec, err := e.prepare(ctx, req)
	if err != nil {
		return err
	}
	defer ec.closeMCP()

	ctx = workspace.WithWorkdirScope(ctx, ec.ag.UUID)
	ec.l.WithField("user", req.UserID).Info("[Execute] >> start (stream)")

	ec.tracker.SetOnStep(func(step model.ExecutionStep) {
		_ = chunkHandler(model.StreamChunk{ConversationID: ec.conv.UUID, Step: &step})
	})

	if _, err := e.run(ctx, ec, streamingCaller(ec.llmProv, ec.conv.UUID, chunkHandler), true); err != nil {
		return err
	}
	return chunkHandler(model.StreamChunk{ConversationID: ec.conv.UUID, Done: true})
}

// ============================================================
//  准备阶段：构建 execContext
// ============================================================

func (e *Executor) prepare(ctx context.Context, req model.ChatRequest) (*execContext, error) {
	ag, err := TryLoadAgent(ctx, e.store)
	if err != nil {
		log.WithError(err).Error("[Execute] load agent config failed")
		return nil, fmt.Errorf("agent not found: %w", err)
	}
	prov, err := e.store.GetProvider(ctx, ag.ProviderID)
	if err != nil {
		log.WithFields(log.Fields{"agent": ag.Name, "provider_id": ag.ProviderID}).WithError(err).Error("[Execute] provider not found")
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	l := log.WithFields(log.Fields{"agent": ag.Name, "provider": prov.Name, "model": ag.ModelName})

	llmProv, err := e.providerFactory(prov, ag.ModelName)
	if err != nil {
		l.WithError(err).Error("[Execute] create llm provider failed")
		return nil, fmt.Errorf("create llm provider: %w", err)
	}

	agentTools, toolSkillMap, err := e.collectTools(ctx, ag)
	if err != nil {
		l.WithError(err).Error("[Execute] collect tools failed")
		return nil, err
	}

	skills, err := loadWorkspaceSkills()
	if err != nil {
		l.WithError(err).Warn("[Execute] load workspace skills failed, continuing without skills")
		skills = nil
	}

	isNewConv := req.ConversationID == ""
	conv, err := e.memory.GetOrCreateConversation(ctx, req.ConversationID, req.UserID)
	if err != nil {
		l.WithError(err).Error("[Execute] get/create conversation failed")
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if isNewConv {
		e.memory.AutoSetTitle(ctx, conv.ID, req.Message)
	}

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

func loadWorkspaceSkills() ([]model.Skill, error) {
	dir := workspace.Skills()
	if dir == "" {
		return nil, nil
	}
	infos, err := skill.ScanAll(dir)
	if err != nil {
		return nil, err
	}
	out := make([]model.Skill, 0, len(infos))
	for i := range infos {
		s := skill.InfoToSkill(infos[i], model.SkillSourceLocal, "")
		s.Enabled = true
		out = append(out, *s)
	}
	return out, nil
}

func (e *Executor) connectMCPServers(ctx context.Context, servers []model.MCPServer, tracker *StepTracker, toolSkillMap map[string]string) (*mcp.Manager, []Tool) {
	if len(servers) == 0 {
		return nil, nil
	}

	mgr := mcp.NewManager()
	if err := mgr.Connect(ctx, servers); err != nil {
		log.WithError(err).Warn("[MCP] connect failed")
		return nil, nil
	}
	if !mgr.HasTools() {
		mgr.Close()
		return nil, nil
	}

	infos := mgr.Tools()
	mcpTools := make([]Tool, 0, len(infos))
	for _, info := range infos {
		info := info
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
	log.WithField("count", len(mcpTools)).Info("[MCP] tools loaded")
	return mgr, mcpTools
}

func (e *Executor) buildSkillManifestTools(skills []model.Skill, tracker *StepTracker, toolSkillMap map[string]string) []Tool {
	var result []Tool
	for _, sk := range skills {
		if !sk.Enabled || len(sk.ToolDefs) == 0 {
			continue
		}
		var toolDefs []model.SkillManifestTool
		if err := json.Unmarshal(sk.ToolDefs, &toolDefs); err != nil {
			log.WithError(err).WithField("skill", sk.Name).Warn("[Skill] parse tool_defs failed")
			continue
		}
		for _, td := range toolDefs {
			td := td
			toolSkillMap[td.Name] = sk.Name
			var handler func(ctx context.Context, input string) (string, error)

			if sk.MainFile != "" {
				skillDir := workspace.SkillDir(sk.DirName)
				if skillDir != "" {
					mainFile := sk.MainFile
					handler = func(ctx context.Context, input string) (string, error) {
						return skill.RunTool(ctx, skillDir, mainFile, td.Name, input, nil, 0)
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
	seen := make(map[int64]bool)

	if ag.ToolSearchEnabled {
		items, _, err := e.store.ListTools(ctx, model.ListQuery{Page: 1, PageSize: 10000})
		if err != nil {
			return nil, nil, fmt.Errorf("list all tools: %w", err)
		}
		for _, t := range items {
			if t.Enabled {
				agentTools = append(agentTools, *t)
				seen[t.ID] = true
			}
		}
		log.WithField("count", len(agentTools)).Info("[Execute]    tool_search: loaded all enabled tools")
	} else {
		for _, tid := range ag.ToolIDs {
			t, err := e.store.GetTool(ctx, tid)
			if err != nil || t == nil || !t.Enabled {
				continue
			}
			if !seen[t.ID] {
				agentTools = append(agentTools, *t)
				seen[t.ID] = true
			}
		}
	}

	toolSkillMap := make(map[string]string)
	return agentTools, toolSkillMap, nil
}

// ============================================================
//  结果持久化
// ============================================================

func (e *Executor) saveResult(ctx context.Context, ec *execContext, content string, tokensUsed int, duration time.Duration) (*ExecuteResult, error) {
	msgID, err := e.memory.SaveAssistantMessage(ctx, ec.conv.ID, content, tokensUsed)
	if err != nil {
		ec.l.WithError(err).Error("[Execute] save assistant message failed")
		return nil, err
	}

	ec.tracker.SetMessageID(msgID)
	ec.tracker.RecordStep(ctx, model.StepLLMCall, ec.ag.ModelName, ec.userMsg, content, model.StepSuccess, "", duration, tokensUsed, ec.stepMeta())

	ec.l.WithFields(log.Fields{"msg_id": msgID, "duration": duration, "tokens": tokensUsed}).Info("[Execute] << done")

	e.memory.StoreMemories(ec.userMsg, content, ec.ag)

	return &ExecuteResult{
		ConversationID: ec.conv.UUID,
		Content:        content,
		TokensUsed:     tokensUsed,
		Steps:          ec.tracker.Steps(),
	}, nil
}
