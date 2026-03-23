package model

import (
	"encoding/json"
	"time"
)

type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
	MCPTransportSSE   MCPTransport = "sse"
)

type MCPServer struct {
	ID          int64        `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string       `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name        string       `json:"name" gorm:"size:200;not null"`
	Description string       `json:"description" gorm:"type:text"`
	Transport   MCPTransport `json:"transport" gorm:"size:50;not null"`
	Endpoint    string       `json:"endpoint" gorm:"size:500;not null"`
	Args        JSON         `json:"args,omitzero" gorm:"type:text"`
	Env         JSON         `json:"env,omitzero" gorm:"type:text"`
	Headers     JSON         `json:"headers,omitzero" gorm:"type:text"`
	Enabled     bool         `json:"enabled" gorm:"not null;default:true"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func (s *MCPServer) GetArgs() []string {
	var args []string
	if len(s.Args) > 0 {
		_ = json.Unmarshal(s.Args, &args)
	}
	return args
}

func (s *MCPServer) GetEnv() map[string]string {
	m := make(map[string]string)
	if len(s.Env) > 0 {
		_ = json.Unmarshal(s.Env, &m)
	}
	return m
}

func (s *MCPServer) GetHeaders() map[string]string {
	m := make(map[string]string)
	if len(s.Headers) > 0 {
		_ = json.Unmarshal(s.Headers, &m)
	}
	return m
}

type CreateMCPServerReq struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Transport   MCPTransport `json:"transport"`
	Endpoint    string       `json:"endpoint"`
	Args        JSON         `json:"args,omitzero"`
	Env         JSON         `json:"env,omitzero"`
	Headers     JSON         `json:"headers,omitzero"`
	Enabled     *bool        `json:"enabled,omitzero"`
}

type UpdateMCPServerReq struct {
	Name        *string       `json:"name,omitzero"`
	Description *string       `json:"description,omitzero"`
	Transport   *MCPTransport `json:"transport,omitzero"`
	Endpoint    *string       `json:"endpoint,omitzero"`
	Args        JSON          `json:"args,omitzero"`
	Env         JSON          `json:"env,omitzero"`
	Headers     JSON          `json:"headers,omitzero"`
	Enabled     *bool         `json:"enabled,omitzero"`
}
