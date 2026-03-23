package model

import "time"

type Conversation struct {
	ID        int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID      string    `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	UserID    string    `json:"user_id" gorm:"size:100;index;not null"`
	Title     string    `json:"title" gorm:"size:500"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ConversationID int64     `json:"conversation_id" gorm:"index;not null"`
	Role           string    `json:"role" gorm:"size:50;not null"`
	Content        string    `json:"content" gorm:"type:text"`
	ToolCalls      JSON      `json:"tool_calls,omitzero" gorm:"type:text"`
	ToolCallID     string    `json:"tool_call_id,omitzero" gorm:"size:100"`
	Name           string    `json:"name,omitzero" gorm:"size:100"`
	TokensUsed     int       `json:"tokens_used" gorm:"default:0"`
	ParentStepID   int64     `json:"parent_step_id,omitzero" gorm:"default:0"`
	CreatedAt      time.Time `json:"created_at"`

	Steps []ExecutionStep `json:"steps,omitzero" gorm:"-"`
	Files []*File         `json:"files,omitzero" gorm:"-"`
}

type StepType string

const (
	StepLLMCall    StepType = "llm_call"
	StepToolCall   StepType = "tool_call"
	StepSkillMatch StepType = "skill_match"
)

type StepStatus string

const (
	StepSuccess StepStatus = "success"
	StepError   StepStatus = "error"
	StepPending StepStatus = "pending"
)

type ExecutionStep struct {
	ID             int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	MessageID      int64      `json:"message_id" gorm:"index;default:0"`
	ConversationID int64      `json:"conversation_id" gorm:"index;not null"`
	StepOrder      int        `json:"step_order" gorm:"not null"`
	StepType       StepType   `json:"step_type" gorm:"size:50;not null"`
	Name           string     `json:"name" gorm:"size:200"`
	Input          string     `json:"input" gorm:"type:text"`
	Output         string     `json:"output" gorm:"type:text"`
	Status         StepStatus `json:"status" gorm:"size:50;not null;default:pending"`
	Error          string     `json:"error,omitzero" gorm:"type:text"`
	DurationMs     int        `json:"duration_ms" gorm:"default:0"`
	TokensUsed     int        `json:"tokens_used" gorm:"default:0"`
	Metadata       JSON       `json:"metadata,omitzero" gorm:"type:text"`
	CreatedAt      time.Time  `json:"created_at"`
}

type StepMetadata struct {
	Provider    string   `json:"provider,omitzero"`
	Model       string   `json:"model,omitzero"`
	Temperature float64  `json:"temperature,omitzero"`
	ToolName    string   `json:"tool_name,omitzero"`
	SkillName   string   `json:"skill_name,omitzero"`
	SkillTools  []string `json:"skill_tools,omitzero"`
	AgentUUID   string   `json:"agent_uuid,omitzero"`
	AgentName   string   `json:"agent_name,omitzero"`
}

type ChatFileType string

const (
	ChatFileDocument ChatFileType = "document"
	ChatFileImage    ChatFileType = "image"
	ChatFileAudio    ChatFileType = "audio"
	ChatFileVideo    ChatFileType = "video"
	ChatFileCustom   ChatFileType = "custom"
)

type TransferMethod string

const (
	TransferRemoteURL TransferMethod = "remote_url"
	TransferLocalFile TransferMethod = "local_file"
)

type ChatFile struct {
	Type           ChatFileType   `json:"type"`
	TransferMethod TransferMethod `json:"transfer_method"`
	URL            string         `json:"url,omitzero"`
	UploadFileID   string         `json:"upload_file_id,omitzero"`
}

type ChatRequest struct {
	AgentID        string     `json:"agent_id"`
	ConversationID string     `json:"conversation_id,omitzero"`
	UserID         string     `json:"user_id"`
	Message        string     `json:"message"`
	Stream         bool       `json:"stream"`
	Files          []ChatFile `json:"files,omitzero"`
}

type ChatResponse struct {
	ConversationID string          `json:"conversation_id"`
	Message        string          `json:"message"`
	TokensUsed     int             `json:"tokens_used"`
	Steps          []ExecutionStep `json:"steps,omitzero"`
}

type StreamChunk struct {
	ConversationID string          `json:"conversation_id,omitzero"`
	Delta          string          `json:"delta,omitzero"`
	Done           bool            `json:"done"`
	Step           *ExecutionStep  `json:"step,omitzero"`
	Steps          []ExecutionStep `json:"steps,omitzero"`
}

type ListQuery struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Keyword  string `json:"keyword,omitzero"`
}
