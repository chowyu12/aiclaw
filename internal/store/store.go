package store

import (
	"context"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

type Store interface {
	AgentStore
	RuntimeStore
	RuntimeAgentConfigStore
	ProviderStore
	ToolStore
	ChannelStore
	ChannelThreadStore
	ConversationStore
	AgentRunStore
	PlanStore
	MemoryStore
	FileStore
	MCPServerStore
	SearchEngineStore
	Close() error
}

type AgentRunStore interface {
	CreateAgentRun(ctx context.Context, run *model.AgentRun) error
	UpdateAgentRun(ctx context.Context, id int64, updates map[string]any) error
	GetAgentRunByUUID(ctx context.Context, runUUID string) (*model.AgentRun, error)
	ListAgentRuns(ctx context.Context, q model.AgentRunListQuery) ([]*model.AgentRun, int64, error)
	ListExecutionStepsByRun(ctx context.Context, runUUID string) ([]model.ExecutionStep, error)
	ClaimQueuedAgentRun(ctx context.Context, runtimeID int64) (*model.AgentRun, error)
}

type RuntimeStore interface {
	CreateRuntime(ctx context.Context, runtime *model.Runtime) error
	GetRuntime(ctx context.Context, id int64) (*model.Runtime, error)
	GetRuntimeByToken(ctx context.Context, token string) (*model.Runtime, error)
	ListRuntimes(ctx context.Context, q model.ListQuery) ([]*model.Runtime, int64, error)
	UpdateRuntime(ctx context.Context, id int64, req model.UpdateRuntimeReq) error
	TouchRuntime(ctx context.Context, id int64, version string, detectedAgents []string, seenAt time.Time) error
	ResetRuntimeToken(ctx context.Context, id int64) (string, error)
	DeleteRuntime(ctx context.Context, id int64) error
}

type RuntimeAgentConfigStore interface {
	EnsureRuntimeAgentConfigs(ctx context.Context, runtimeID int64, agentTypes []string) error
	ListRuntimeAgentConfigs(ctx context.Context, runtimeID int64) ([]model.RuntimeAgentConfig, error)
	GetRuntimeAgentConfig(ctx context.Context, runtimeID int64, agentType string) (*model.RuntimeAgentConfig, error)
	UpdateRuntimeAgentConfig(ctx context.Context, runtimeID int64, agentType string, req model.UpdateRuntimeAgentConfigReq) error
}

type AgentStore interface {
	CreateAgent(ctx context.Context, a *model.Agent) error
	GetAgent(ctx context.Context, id int64) (*model.Agent, error)
	GetAgentByUUID(ctx context.Context, uuid string) (*model.Agent, error)
	GetAgentByToken(ctx context.Context, token string) (*model.Agent, error)
	GetDefaultAgent(ctx context.Context) (*model.Agent, error)
	ListAgents(ctx context.Context, q model.ListQuery) ([]*model.Agent, int64, error)
	UpdateAgent(ctx context.Context, id int64, req *model.UpdateAgentReq) error
	ResetAgentToken(ctx context.Context, id int64) (string, error)
	DeleteAgent(ctx context.Context, id int64) error
}

type ChannelStore interface {
	CreateChannel(ctx context.Context, c *model.Channel) error
	GetChannel(ctx context.Context, id int64) (*model.Channel, error)
	GetChannelByUUID(ctx context.Context, uuid string) (*model.Channel, error)
	ListChannels(ctx context.Context, q model.ListQuery) ([]*model.Channel, int64, error)
	UpdateChannel(ctx context.Context, id int64, req model.UpdateChannelReq) error
	DeleteChannel(ctx context.Context, id int64) error
}

// ChannelThreadStore 渠道侧线程与会话 UUID 映射。
type ChannelThreadStore interface {
	GetChannelThread(ctx context.Context, channelID int64, threadKey string) (*model.ChannelThread, error)
	UpsertChannelThread(ctx context.Context, channelID int64, threadKey, conversationUUID string) error
	ListChannelThreads(ctx context.Context, channelID int64) ([]model.ChannelThread, error)
	DeleteChannelThreadsByConversation(ctx context.Context, channelID int64, conversationUUID string) error
}

// MCPServerStore 运行时 MCP 服务列表（与设置页「MCP」同步）。
type MCPServerStore interface {
	ListMCPServers(ctx context.Context) ([]model.MCPServer, error)
	ReplaceMCPServers(ctx context.Context, servers []model.MCPServer) error
}

type SearchEngineStore interface {
	ListSearchEngineConfigs(ctx context.Context, q model.ListQuery) ([]*model.SearchEngineConfig, int64, error)
	GetSearchEngineConfig(ctx context.Context, id int64) (*model.SearchEngineConfig, error)
	CreateSearchEngineConfig(ctx context.Context, cfg *model.SearchEngineConfig) error
	UpdateSearchEngineConfig(ctx context.Context, id int64, cfg *model.SearchEngineConfig) error
	DeleteSearchEngineConfig(ctx context.Context, id int64) error
}

type ProviderStore interface {
	CreateProvider(ctx context.Context, p *model.Provider) error
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	ListProviders(ctx context.Context, q model.ListQuery) ([]*model.Provider, int64, error)
	UpdateProvider(ctx context.Context, id int64, req model.UpdateProviderReq) error
	DeleteProvider(ctx context.Context, id int64) error
}

type ToolStore interface {
	CreateTool(ctx context.Context, t *model.Tool) error
	GetTool(ctx context.Context, id int64) (*model.Tool, error)
	ListTools(ctx context.Context, q model.ListQuery) ([]*model.Tool, int64, error)
	UpdateTool(ctx context.Context, id int64, req model.UpdateToolReq) error
	DeleteTool(ctx context.Context, id int64) error
}

type ConversationStore interface {
	CreateConversation(ctx context.Context, c *model.Conversation) error
	GetConversation(ctx context.Context, id int64) (*model.Conversation, error)
	GetConversationByUUID(ctx context.Context, uuid string) (*model.Conversation, error)
	ListConversations(ctx context.Context, userID string, q model.ListQuery) ([]*model.Conversation, int64, error)
	ListConversationsByUserPrefix(ctx context.Context, prefix string, q model.ListQuery) ([]*model.Conversation, int64, error)
	UpdateConversationTitle(ctx context.Context, id int64, title string) error
	DeleteConversation(ctx context.Context, id int64) error

	CreateMessage(ctx context.Context, m *model.Message) error
	CreateMessages(ctx context.Context, msgs []*model.Message) error
	ListMessages(ctx context.Context, conversationID int64, limit int) ([]model.Message, error)
	CountMessages(ctx context.Context, conversationID int64) (int64, error)
	GetMessage(ctx context.Context, id int64) (*model.Message, error)
	DeleteMessagesFrom(ctx context.Context, conversationID, fromMessageID int64) error

	CreateExecutionStep(ctx context.Context, step *model.ExecutionStep) error
	UpdateExecutionStep(ctx context.Context, step *model.ExecutionStep) error
	UpdateStepsMessageID(ctx context.Context, conversationID, messageID int64) error
	UpdateStepsMessageIDByRun(ctx context.Context, conversationID int64, runUUID string, messageID int64) error
	ListExecutionSteps(ctx context.Context, messageID int64) ([]model.ExecutionStep, error)
	ListExecutionStepsByConversation(ctx context.Context, conversationID int64) ([]model.ExecutionStep, error)
}

type PlanStore interface {
	CreatePlanRun(ctx context.Context, run *model.PlanRun) error
	UpdatePlanRun(ctx context.Context, run *model.PlanRun) error
	GetActivePlanRun(ctx context.Context, conversationID int64) (*model.PlanRun, error)
	GetPlanRunByMessage(ctx context.Context, messageID int64) (*model.PlanRun, error)
	ListPlanItems(ctx context.Context, planRunID int64) ([]model.PlanItem, error)
	ReplacePlanItems(ctx context.Context, planRunID int64, items []model.PlanItem) error
	UpdatePlanItemsMessageID(ctx context.Context, conversationID, messageID int64) error
	DeletePlansByConversation(ctx context.Context, conversationID int64) error
	DeletePlansFromMessage(ctx context.Context, conversationID, fromMessageID int64) error
}

// MemoryStore persists durable, user-scoped memory and its audit trail.
type MemoryStore interface {
	UpsertMemory(ctx context.Context, item *model.MemoryItem, evidence *model.MemoryEvidence, actor string) (*model.MemoryItem, error)
	GetMemoryByUUID(ctx context.Context, userID, uuid string) (*model.MemoryItem, error)
	ListMemories(ctx context.Context, q model.MemoryListQuery) ([]model.MemoryItem, int64, error)
	RetrieveMemories(ctx context.Context, q model.MemoryRetrieveQuery) ([]model.MemoryItem, error)
	UpdateMemory(ctx context.Context, item *model.MemoryItem, action model.MemoryRevisionAction, actor string) error
	DeleteMemory(ctx context.Context, userID, uuid, actor string) error
	ListMemoryRevisions(ctx context.Context, memoryID int64) ([]model.MemoryRevision, error)
	ListMemoryEvidence(ctx context.Context, memoryID int64) ([]model.MemoryEvidence, error)
	ListMemoryUsageByMessage(ctx context.Context, messageID int64) ([]model.MemoryItem, error)
	RecordMemoryUsage(ctx context.Context, memoryIDs []int64, conversationID, messageID int64, runUUID string) error
	UpdateMemoryEvidenceMessageIDByRun(ctx context.Context, runUUID string, messageID int64) error
}

type FileStore interface {
	CreateFile(ctx context.Context, f *model.File) error
	GetFileByUUID(ctx context.Context, uuid string) (*model.File, error)
	ListFilesByConversation(ctx context.Context, conversationID int64) ([]*model.File, error)
	ListFilesByMessage(ctx context.Context, messageID int64) ([]*model.File, error)
	UpdateFileMessageID(ctx context.Context, fileID, messageID int64) error
	LinkFileToMessage(ctx context.Context, fileID, conversationID, messageID int64) error
	DeleteFile(ctx context.Context, id int64) error
}
