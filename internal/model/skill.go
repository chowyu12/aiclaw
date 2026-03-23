package model

import "time"

type SkillSource string

const (
	SkillSourceClawHub SkillSource = "clawhub"
	SkillSourceLocal   SkillSource = "local"
	SkillSourceCustom  SkillSource = "custom"
)

type Skill struct {
	ID          int64       `json:"id" gorm:"primaryKey;autoIncrement"`
	UUID        string      `json:"uuid" gorm:"uniqueIndex;size:36;not null"`
	Name        string      `json:"name" gorm:"size:200;not null"`
	Description string      `json:"description" gorm:"type:text"`
	Instruction string      `json:"instruction" gorm:"type:text"`
	Source      SkillSource `json:"source" gorm:"size:50;not null;default:custom"`
	Slug        string      `json:"slug,omitzero" gorm:"size:200"`
	Version     string      `json:"version,omitzero" gorm:"size:50"`
	Author      string      `json:"author,omitzero" gorm:"size:100"`
	DirName     string      `json:"dir_name,omitzero" gorm:"size:200;index"`
	MainFile    string      `json:"main_file,omitzero" gorm:"size:200"`
	Config      JSON        `json:"config,omitzero" gorm:"type:text"`
	Permissions JSON        `json:"permissions,omitzero" gorm:"type:text"`
	ToolDefs    JSON        `json:"tool_defs,omitzero" gorm:"type:text"`
	Enabled     bool        `json:"enabled" gorm:"not null;default:true"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`

	Tools []Tool `json:"tools,omitzero" gorm:"-"`
}

type CreateSkillReq struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Instruction string      `json:"instruction"`
	Source      SkillSource `json:"source,omitzero"`
	Slug        string      `json:"slug,omitzero"`
	Version     string      `json:"version,omitzero"`
	Author      string      `json:"author,omitzero"`
	DirName     string      `json:"dir_name,omitzero"`
	MainFile    string      `json:"main_file,omitzero"`
	Config      JSON        `json:"config,omitzero"`
	Permissions JSON        `json:"permissions,omitzero"`
	ToolDefs    JSON        `json:"tool_defs,omitzero"`
	Enabled     *bool       `json:"enabled,omitzero"`
	ToolIDs     []int64     `json:"tool_ids,omitzero"`
}

type UpdateSkillReq struct {
	Name        *string      `json:"name,omitzero"`
	Description *string      `json:"description,omitzero"`
	Instruction *string      `json:"instruction,omitzero"`
	Source      *SkillSource `json:"source,omitzero"`
	Slug        *string      `json:"slug,omitzero"`
	Version     *string      `json:"version,omitzero"`
	Author      *string      `json:"author,omitzero"`
	DirName     *string      `json:"dir_name,omitzero"`
	MainFile    *string      `json:"main_file,omitzero"`
	Config      JSON         `json:"config,omitzero"`
	Permissions JSON         `json:"permissions,omitzero"`
	ToolDefs    JSON         `json:"tool_defs,omitzero"`
	Enabled     *bool        `json:"enabled,omitzero"`
	ToolIDs     []int64      `json:"tool_ids,omitzero"`
}

type SkillManifestTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitzero"`
}

type SkillManifest struct {
	Name        string                       `json:"name"`
	Version     string                       `json:"version"`
	Description string                       `json:"description"`
	Author      string                       `json:"author"`
	Main        string                       `json:"main,omitzero"`
	Permissions []string                     `json:"permissions,omitzero"`
	Config      map[string]SkillConfigField  `json:"config,omitzero"`
	Tools       []SkillManifestTool          `json:"tools,omitzero"`
}

type SkillConfigField struct {
	Type        string `json:"type"`
	Required    bool   `json:"required,omitzero"`
	Description string `json:"description,omitzero"`
}
