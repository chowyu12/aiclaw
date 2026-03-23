package model

import "time"

type HandlerType string

const (
	HandlerBuiltin HandlerType = "builtin"
	HandlerHTTP    HandlerType = "http"
	HandlerScript  HandlerType = "script"
	HandlerCommand HandlerType = "command"
)

type Tool struct {
	ID            int64       `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID          string      `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name          string      `json:"name" gorm:"size:200;not null"`
	Description   string      `json:"description" gorm:"type:text"`
	FunctionDef   JSON        `json:"function_def,omitzero" gorm:"type:text"`
	HandlerType   HandlerType `json:"handler_type" gorm:"size:50;not null"`
	HandlerConfig JSON        `json:"handler_config,omitzero" gorm:"type:text"`
	Enabled       bool        `json:"enabled" gorm:"not null;default:true"`
	Timeout       int         `json:"timeout" gorm:"default:30"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

const DefaultToolTimeout = 30

func (t *Tool) TimeoutSeconds() int {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return DefaultToolTimeout
}

type CreateToolReq struct {
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	FunctionDef   JSON        `json:"function_def,omitzero"`
	HandlerType   HandlerType `json:"handler_type"`
	HandlerConfig JSON        `json:"handler_config,omitzero"`
	Enabled       *bool       `json:"enabled,omitzero"`
	Timeout       int         `json:"timeout"`
}

type UpdateToolReq struct {
	Name          *string      `json:"name,omitzero"`
	Description   *string      `json:"description,omitzero"`
	FunctionDef   JSON         `json:"function_def,omitzero"`
	HandlerType   *HandlerType `json:"handler_type,omitzero"`
	HandlerConfig JSON         `json:"handler_config,omitzero"`
	Enabled       *bool        `json:"enabled,omitzero"`
	Timeout       *int         `json:"timeout,omitzero"`
}

type HTTPHandlerConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitzero"`
	Body    string            `json:"body,omitzero"`
}

type CommandHandlerConfig struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitzero"`
	Timeout    int    `json:"timeout,omitzero"`
	Shell      string `json:"shell,omitzero"`
}
