// Package providers 是模型服务（Provider）的配置库：多个 OpenAI 兼容端点，
// 各带自己的 Key 与模型清单，会话按 providerId 选其中一个。
//
// 数据落在 AIClaw 沿用至今的 SQLite 库（gorm 的 providers 表）里，表结构不动——
// 用户在旧版里配好的模型服务升级后原样可用。这也是 Key 唯一存放的地方：
// 它**不经协议帧**，宿主只拿到 apiKeySet 这一位；打模型时由内核按 providerId
// 在这里查出来。
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 各类型的默认端点。用户留空 baseUrl 时用它。
//
// claude / gemini 给的是它们的 **OpenAI 兼容**入口：内核的模型客户端只会说
// chat/completions 这一种话，原生的 Messages / generateContent 接口不支持。
var defaultBaseURLs = map[string]string{
	string(model.ProviderOpenAI):     "https://api.openai.com/v1",
	string(model.ProviderQwen):       "https://dashscope.aliyuncs.com/compatible-mode/v1",
	string(model.ProviderKimi):       "https://api.moonshot.cn/v1",
	string(model.ProviderOpenRouter): "https://openrouter.ai/api/v1",
	string(model.ProviderClaude):     "https://api.anthropic.com/v1",
	string(model.ProviderGemini):     "https://generativelanguage.googleapis.com/v1beta/openai",
}

// DefaultBaseURL 返回某类型的默认端点；没有就是空串（openai-compatible 必须自己填）。
func DefaultBaseURL(providerType string) string {
	return defaultBaseURLs[providerType]
}

// Store 是模型服务的配置库。
type Store struct {
	db *gormstore.GormStore
}

// New 在一个已打开的应用库上建 Store。库由调用方打开与关闭：插件系统
// 用的是同一个库。
func New(db *gormstore.GormStore) *Store {
	return &Store{db: db}
}

// List 列出全部模型服务，按 id 排序。Key 不出来，只给「配了没有」。
func (s *Store) List(ctx context.Context) ([]protocol.ProviderView, error) {
	items, _, err := s.db.ListProviders(ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	views := make([]protocol.ProviderView, 0, len(items))
	for _, item := range items {
		views = append(views, view(item))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}

// Create 新建一个模型服务。
func (s *Store) Create(ctx context.Context, params protocol.ProviderCreateParams) (protocol.ProviderView, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return protocol.ProviderView{}, errors.New("名字不能为空")
	}
	providerType := strings.TrimSpace(params.Type)
	if providerType == "" {
		providerType = string(model.ProviderOpenAICompat)
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}
	item := &model.Provider{
		Name:    name,
		Type:    model.ProviderType(providerType),
		BaseURL: strings.TrimRight(strings.TrimSpace(params.BaseURL), "/"),
		APIKey:  strings.TrimSpace(params.APIKey),
		Models:  encodeModels(params.Models),
		Enabled: enabled,
	}
	if err := s.db.CreateProvider(ctx, item); err != nil {
		return protocol.ProviderView{}, err
	}
	return view(item), nil
}

// Update 改一个模型服务。nil 的字段不动；apiKey 传空串表示清掉。
func (s *Store) Update(ctx context.Context, params protocol.ProviderUpdateParams) (protocol.ProviderView, error) {
	req := model.UpdateProviderReq{Enabled: params.Enabled}
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return protocol.ProviderView{}, errors.New("名字不能为空")
		}
		req.Name = &name
	}
	if params.Type != nil {
		t := model.ProviderType(strings.TrimSpace(*params.Type))
		req.Type = &t
	}
	if params.BaseURL != nil {
		url := strings.TrimRight(strings.TrimSpace(*params.BaseURL), "/")
		req.BaseURL = &url
	}
	if params.APIKey != nil {
		key := strings.TrimSpace(*params.APIKey)
		req.APIKey = &key
	}
	if params.Models != nil {
		req.Models = encodeModels(params.Models)
	}
	if err := s.db.UpdateProvider(ctx, params.ID, req); err != nil {
		return protocol.ProviderView{}, err
	}
	item, err := s.db.GetProvider(ctx, params.ID)
	if err != nil {
		return protocol.ProviderView{}, err
	}
	return view(item), nil
}

// Delete 删一个模型服务。引用它的会话下次恢复时会报「模型服务不存在」，
// 用户在对话页顶部换一个即可——不级联删会话，那是用户的记录。
func (s *Store) Delete(ctx context.Context, id int64) error {
	return s.db.DeleteProvider(ctx, id)
}

// Resolve 按 model.ProviderID 查出 Key，并把 BaseURL 换成该服务当前的端点。
//
// 端点**总是**以库里的为准，不看调用方传来的：用户改了端点之后，恢复旧会话
// 也该打新地址——端点是基础设施，不是那个会话的选择。
func (s *Store) Resolve(ctx context.Context, m *protocol.ModelConfig) (string, error) {
	item, err := s.db.GetProvider(ctx, m.ProviderID)
	if err != nil {
		return "", fmt.Errorf("模型服务不存在（id=%d）：%w", m.ProviderID, err)
	}
	if !item.Enabled {
		return "", fmt.Errorf("模型服务「%s」已停用", item.Name)
	}
	baseURL := item.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURLs[string(item.Type)]
	}
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("模型服务「%s」没有填端点", item.Name)
	}
	m.BaseURL = baseURL
	if strings.TrimSpace(item.APIKey) == "" {
		return "", fmt.Errorf("模型服务「%s」没有配置 Key", item.Name)
	}
	return item.APIKey, nil
}

// FetchModels 到端点的 /models 拉一遍模型名。给「从端点拉取」按钮用，
// 结果由用户决定要不要写进模型清单，这里不落库。
func (s *Store) FetchModels(ctx context.Context, id int64) ([]string, error) {
	m := protocol.ModelConfig{ProviderID: id}
	key, err := s.Resolve(ctx, &m)
	if err != nil {
		return nil, err
	}
	// 拉列表不该无限等：端点不通时用户面前是一个一直转的按钮。
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接模型服务失败：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		// 把上游的话带出来：401/403 最常见，都是 Key 的问题，直接说比让人猜强。
		return nil, fmt.Errorf("模型服务返回 %d：%s", resp.StatusCode, upstreamMessage(body))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("模型列表不是预期的格式：%w", err)
	}
	names := make([]string, 0, len(parsed.Data))
	for _, entry := range parsed.Data {
		if entry.ID != "" {
			names = append(names, entry.ID)
		}
	}
	sort.Strings(names)
	return names, nil
}

func view(item *model.Provider) protocol.ProviderView {
	return protocol.ProviderView{
		ID:        item.ID,
		Name:      item.Name,
		Type:      string(item.Type),
		BaseURL:   item.BaseURL,
		APIKeySet: strings.TrimSpace(item.APIKey) != "",
		Models:    decodeModels(item.Models),
		Enabled:   item.Enabled,
	}
}

// 模型清单在库里是一个 JSON 字符串数组——旧版就是这么存的。
func encodeModels(names []string) model.JSON {
	clean := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		clean = append(clean, name)
	}
	encoded, _ := json.Marshal(clean)
	return model.JSON(encoded)
}

func decodeModels(raw model.JSON) []string {
	var names []string
	_ = json.Unmarshal(raw, &names)
	if names == nil {
		names = []string{}
	}
	return names
}

func upstreamMessage(body []byte) string {
	var parsed struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return parsed.Error.Message
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}
