// Package searchengines 是联网搜索引擎的配置库：Tavily / SerpAPI / 阿里云 IQS，
// 各带自己的 Key。AIClaw 旧版的 search_engine_configs 表原样沿用。
//
// 搜索本身不在内核的工具注册表里，而是一个随内核二进制分发的 MCP server
// （`claw-agent mcp-search`，见 searchmcp 包）：宿主在有启用中的引擎时把它挂进
// 会话，换引擎、关引擎都只是改配置，不碰循环。
package searchengines

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/tools/websearch"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Store 是搜索引擎的配置库。
type Store struct {
	db *gormstore.GormStore
}

// New 在一个已打开的应用库上建 Store。
func New(db *gormstore.GormStore) *Store { return &Store{db: db} }

// List 列出全部引擎。Key 不出来，只给「配了没有」。
func (s *Store) List(ctx context.Context) ([]protocol.SearchEngineView, error) {
	items, _, err := s.db.ListSearchEngineConfigs(ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	views := make([]protocol.SearchEngineView, 0, len(items))
	for _, item := range items {
		views = append(views, view(item))
	}
	return views, nil
}

// AnyEnabled 报是否至少有一个启用且配了 Key 的引擎——宿主据此决定挂不挂搜索 server。
func (s *Store) AnyEnabled(ctx context.Context) (bool, error) {
	engine, err := s.Active(ctx)
	if err != nil {
		return false, err
	}
	return engine != nil, nil
}

// Active 返回当前生效的引擎：第一个启用且配了 Key 的。没有就是 nil。
//
// 一次只用一个：两个引擎同时挂给模型，它无从选择，而用户想换引擎时只会想
// 「把那个打开」而不是「把这个关掉再把那个打开」。
func (s *Store) Active(ctx context.Context) (*model.SearchEngineConfig, error) {
	items, _, err := s.db.ListSearchEngineConfigs(ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Enabled && strings.TrimSpace(item.APIKey) != "" {
			return item, nil
		}
	}
	return nil, nil
}

// Create 新建一个引擎。
func (s *Store) Create(ctx context.Context, params protocol.SearchEngineCreateParams) (protocol.SearchEngineView, error) {
	provider := model.SearchEngineProvider(strings.TrimSpace(params.Provider))
	if !known(provider) {
		return protocol.SearchEngineView{}, fmt.Errorf("不支持的搜索引擎类型：%q", params.Provider)
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = label(provider)
	}
	item := &model.SearchEngineConfig{
		Provider: provider, Name: name,
		BaseURL: strings.TrimSpace(params.BaseURL),
		APIKey:  strings.TrimSpace(params.APIKey),
		Enabled: params.Enabled,
	}
	if err := s.db.CreateSearchEngineConfig(ctx, item); err != nil {
		return protocol.SearchEngineView{}, err
	}
	return view(item), nil
}

// Update 改一个引擎。nil 的字段不动；apiKey 指向空串表示清掉。
func (s *Store) Update(ctx context.Context, params protocol.SearchEngineUpdateParams) (protocol.SearchEngineView, error) {
	item, err := s.db.GetSearchEngineConfig(ctx, params.ID)
	if err != nil {
		return protocol.SearchEngineView{}, fmt.Errorf("搜索引擎不存在（id=%d）：%w", params.ID, err)
	}
	if params.Provider != nil {
		provider := model.SearchEngineProvider(strings.TrimSpace(*params.Provider))
		if !known(provider) {
			return protocol.SearchEngineView{}, fmt.Errorf("不支持的搜索引擎类型：%q", *params.Provider)
		}
		item.Provider = provider
	}
	if params.Name != nil {
		if name := strings.TrimSpace(*params.Name); name != "" {
			item.Name = name
		}
	}
	if params.BaseURL != nil {
		item.BaseURL = strings.TrimSpace(*params.BaseURL)
	}
	if params.APIKey != nil {
		item.APIKey = strings.TrimSpace(*params.APIKey)
	}
	if params.Enabled != nil {
		item.Enabled = *params.Enabled
	}
	if err := s.db.UpdateSearchEngineConfig(ctx, params.ID, item); err != nil {
		return protocol.SearchEngineView{}, err
	}
	return view(item), nil
}

// Delete 删一个引擎。
func (s *Store) Delete(ctx context.Context, id int64) error {
	return s.db.DeleteSearchEngineConfig(ctx, id)
}

// Test 用一个引擎真搜一次。配置页的「试一下」用；结果不落库。
func (s *Store) Test(ctx context.Context, params protocol.SearchEngineTestParams) (protocol.SearchEngineTestResult, error) {
	item, err := s.db.GetSearchEngineConfig(ctx, params.ID)
	if err != nil {
		return protocol.SearchEngineTestResult{}, fmt.Errorf("搜索引擎不存在（id=%d）：%w", params.ID, err)
	}
	if strings.TrimSpace(item.APIKey) == "" {
		return protocol.SearchEngineTestResult{}, errors.New("这个引擎还没配 Key")
	}
	// 试搜不看启用状态：用户就是想在启用前确认 Key 对不对。
	probe := *item
	probe.Enabled = true
	resp, err := websearch.Search(ctx, &probe, params.Query, 3)
	if err != nil {
		return protocol.SearchEngineTestResult{}, err
	}
	result := protocol.SearchEngineTestResult{Provider: resp.Provider}
	for _, hit := range resp.Results {
		result.Results = append(result.Results, protocol.SearchHit{Title: hit.Title, URL: hit.URL, Snippet: hit.Snippet})
	}
	return result, nil
}

func known(provider model.SearchEngineProvider) bool {
	switch provider {
	case model.SearchEngineTavily, model.SearchEngineSerpAPI, model.SearchEngineAliyunIQS:
		return true
	}
	return false
}

func label(provider model.SearchEngineProvider) string {
	switch provider {
	case model.SearchEngineTavily:
		return "Tavily"
	case model.SearchEngineSerpAPI:
		return "SerpAPI"
	case model.SearchEngineAliyunIQS:
		return "阿里云 IQS"
	}
	return string(provider)
}

func view(item *model.SearchEngineConfig) protocol.SearchEngineView {
	return protocol.SearchEngineView{
		ID: item.ID, Provider: string(item.Provider), Name: item.Name, BaseURL: item.BaseURL,
		APIKeySet: strings.TrimSpace(item.APIKey) != "", Enabled: item.Enabled,
	}
}
