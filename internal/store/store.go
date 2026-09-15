package store

import (
	"context"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
)

// These focused contracts are shared by the native desktop runtime. Legacy
// Agent, Runtime, Channel, Conversation, daemon, and CDP stores are
// intentionally not part of this package anymore.
type SkillStore interface {
	ListSkills(ctx context.Context) ([]model.Skill, error)
	UpsertSkill(ctx context.Context, skill *model.Skill) error
	SetSkillEnabled(ctx context.Context, uuid string, enabled bool) error
}

type PluginStore interface {
	ListPlugins(ctx context.Context) ([]model.Plugin, error)
	CreatePlugin(ctx context.Context, plugin *model.Plugin) error
	UpsertPlugin(ctx context.Context, plugin *model.Plugin) error
	SetPluginEnabled(ctx context.Context, uuid string, enabled bool) error
	DeletePlugin(ctx context.Context, uuid string) error
	ListPluginConfig(ctx context.Context, pluginUUID string) ([]model.PluginConfig, error)
	SetPluginConfig(ctx context.Context, item *model.PluginConfig) error
	DeletePluginConfig(ctx context.Context, pluginUUID, key string) error
	DeletePluginConfigs(ctx context.Context, pluginUUID string) error
}

// ChannelBindingStore owns the mapping between external conversations and
// local threads, including whether an external conversation is allowed to
// reach the agent at all.
type ChannelBindingStore interface {
	ListChannelBindings(ctx context.Context) ([]model.ChannelBinding, error)
	GetChannelBinding(ctx context.Context, pluginUUID, channelID, externalKey string) (*model.ChannelBinding, error)
	SaveChannelBinding(ctx context.Context, item *model.ChannelBinding) error
	DeleteChannelBindings(ctx context.Context, pluginUUID string) error
}

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
	CreateProvider(ctx context.Context, provider *model.Provider) error
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	ListProviders(ctx context.Context, q model.ListQuery) ([]*model.Provider, int64, error)
	UpdateProvider(ctx context.Context, id int64, req model.UpdateProviderReq) error
	DeleteProvider(ctx context.Context, id int64) error
}

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
	CreateFile(ctx context.Context, file *model.File) error
	GetFileByUUID(ctx context.Context, uuid string) (*model.File, error)
	ListFilesByThread(ctx context.Context, threadID int64) ([]*model.File, error)
	LinkFilesToThread(ctx context.Context, threadID int64, fileUUIDs []string) error
	ListPendingFilesBefore(ctx context.Context, before time.Time) ([]*model.File, error)
	DeleteFile(ctx context.Context, id int64) error
}
