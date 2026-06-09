package model

import "time"

type SearchEngineProvider string

const (
	SearchEngineTavily    SearchEngineProvider = "tavily"
	SearchEngineSerpAPI   SearchEngineProvider = "serpapi"
	SearchEngineAliyunIQS SearchEngineProvider = "aliyun-iqs"
)

type SearchEngineConfig struct {
	ID        int64                `json:"id" gorm:"primaryKey;autoIncrement"`
	Provider  SearchEngineProvider `json:"provider" gorm:"size:50;not null"`
	Name      string               `json:"name" gorm:"size:100;not null"`
	BaseURL   string               `json:"base_url" gorm:"size:500;not null"`
	APIKey    string               `json:"api_key,omitempty" gorm:"size:500"`
	Enabled   bool                 `json:"enabled" gorm:"not null;default:false"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type SearchEngineConfigResp struct {
	ID        int64                `json:"id"`
	Provider  SearchEngineProvider `json:"provider"`
	Name      string               `json:"name"`
	BaseURL   string               `json:"base_url"`
	APIKeySet bool                 `json:"api_key_set"`
	Enabled   bool                 `json:"enabled"`
	CreatedAt time.Time            `json:"created_at,omitzero"`
	UpdatedAt time.Time            `json:"updated_at,omitzero"`
}

type UpdateSearchEngineConfigReq struct {
	Provider *SearchEngineProvider `json:"provider,omitzero"`
	Name     *string               `json:"name,omitzero"`
	BaseURL  *string               `json:"base_url,omitzero"`
	APIKey   *string               `json:"api_key,omitzero"`
	Enabled  *bool                 `json:"enabled,omitzero"`
}

type CreateSearchEngineConfigReq struct {
	Provider SearchEngineProvider `json:"provider"`
	Name     string               `json:"name"`
	BaseURL  string               `json:"base_url"`
	APIKey   string               `json:"api_key"`
	Enabled  bool                 `json:"enabled"`
}

type TestSearchEngineReq struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitzero"`
}

type TestSearchEngineConfigReq struct {
	ID       int64                `json:"id,omitzero"`
	Query    string               `json:"query"`
	Limit    int                  `json:"limit,omitzero"`
	Provider SearchEngineProvider `json:"provider,omitzero"`
	Name     string               `json:"name,omitzero"`
	BaseURL  string               `json:"base_url,omitzero"`
	APIKey   string               `json:"api_key,omitzero"`
}
