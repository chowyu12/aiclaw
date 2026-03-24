package store

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/model"
)

type Store interface {
	ProviderStore
	ToolStore
	ChannelStore
	ChannelThreadStore
	ConversationStore
	FileStore
	MCPServerStore
	Close() error
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

	CreateExecutionStep(ctx context.Context, step *model.ExecutionStep) error
	UpdateStepsMessageID(ctx context.Context, conversationID, messageID int64) error
	ListExecutionSteps(ctx context.Context, messageID int64) ([]model.ExecutionStep, error)
	ListExecutionStepsByConversation(ctx context.Context, conversationID int64) ([]model.ExecutionStep, error)
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
