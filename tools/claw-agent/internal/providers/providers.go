// Package providers 保存模型服务：厂商、API 协议、基础地址、Key 与模型清单。
// 会话按 providerId 选择服务，协议与厂商类型独立。
//
// 数据沿用 SQLite 的 providers 表，新增协议列默认 Chat Completions，保留旧配置。
// 这也是 Key 唯一存放的地方：
// 它**不经协议帧**，宿主只拿到 apiKeySet 这一位；打模型时由内核按 providerId
// 在这里查出来。
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 各类型的默认端点。用户留空 baseUrl 时用它。
//
// 这是旧版 Chat Completions 地址预设；原生协议有独立的默认地址。
// 对话协议由模型服务显式配置，厂商类型不决定协议。
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

func defaultAPIBaseURL(providerType string, api model.APIProtocol) string {
	switch api.Normalized() {
	case model.APIAnthropic:
		return "https://api.anthropic.com/v1"
	case model.APIGemini:
		return "https://generativelanguage.googleapis.com/v1beta"
	default:
		return DefaultBaseURL(providerType)
	}
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
	if params.RoutingMode != "" && params.RoutingMode != "auto" && params.RoutingMode != "manual" {
		return protocol.ProviderView{}, fmt.Errorf("invalid routing mode")
	}
	if !params.APIProtocol.Valid() {
		return protocol.ProviderView{}, fmt.Errorf("unsupported model API protocol: %s", params.APIProtocol)
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return protocol.ProviderView{}, i18n.E("名字不能为空")
	}
	providerType := strings.TrimSpace(params.Type)
	if providerType == "" {
		providerType = string(model.ProviderOpenAICompat)
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}
	mode := params.RoutingMode
	if mode == "" {
		mode = "auto"
		if params.APIProtocol != "" {
			mode = "manual"
		}
	}
	overrides, err := validateOverrides(params.ModelOverrides)
	if err != nil {
		return protocol.ProviderView{}, err
	}
	item := &model.Provider{
		RoutingMode: mode, ModelOverrides: overrides,
		Name:        name,
		Type:        model.ProviderType(providerType),
		APIProtocol: params.APIProtocol.Normalized(),
		BaseURL:     strings.TrimRight(strings.TrimSpace(params.BaseURL), "/"),
		APIKey:      strings.TrimSpace(params.APIKey),
		Models:      encodeModels(params.Models),
		Enabled:     enabled,
	}
	if err := s.db.CreateProvider(ctx, item); err != nil {
		return protocol.ProviderView{}, err
	}
	return view(item), nil
}

// Update 改一个模型服务。nil 的字段不动；apiKey 传空串表示清掉。
func (s *Store) Update(ctx context.Context, params protocol.ProviderUpdateParams) (protocol.ProviderView, error) {
	req := model.UpdateProviderReq{Enabled: params.Enabled}
	if params.ModelOverrides != nil {
		raw, err := validateOverrides(params.ModelOverrides)
		if err != nil {
			return protocol.ProviderView{}, err
		}
		req.ModelOverrides = raw
	}
	if params.RoutingMode != nil {
		if *params.RoutingMode != "auto" && *params.RoutingMode != "manual" {
			return protocol.ProviderView{}, fmt.Errorf("invalid routing mode")
		}
		req.RoutingMode = params.RoutingMode
	}

	if params.APIProtocol != nil {
		if !params.APIProtocol.Valid() {
			return protocol.ProviderView{}, fmt.Errorf("unsupported model API protocol: %s", *params.APIProtocol)
		}
		req.APIProtocol = params.APIProtocol
		if params.RoutingMode == nil {
			manual := "manual"
			req.RoutingMode = &manual
		}
	}
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return protocol.ProviderView{}, i18n.E("名字不能为空")
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
		return "", fmt.Errorf("%s%w", i18n.D("模型服务不存在（id={id}）：", "id", m.ProviderID), err)
	}
	return resolveProvider(item, m)
}

func resolveProvider(item *model.Provider, m *protocol.ModelConfig) (string, error) {
	if !item.Enabled {
		return "", i18n.E("模型服务「{name}」已停用", "name", item.Name)
	}
	route := resolveRoute(item, m.Model)
	baseURL := item.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultAPIBaseURL(serviceProfile(item), route.APIProtocol)
	}
	if strings.TrimSpace(baseURL) == "" {
		return "", i18n.E("模型服务「{name}」没有填端点", "name", item.Name)
	}
	m.BaseURL = baseURL
	m.APIProtocol = route.APIProtocol
	m.ProtocolSource = route.Source
	m.ServiceProfile = serviceProfile(item)
	m.Capabilities = route.Capabilities
	if strings.TrimSpace(item.APIKey) == "" {
		return "", i18n.E("模型服务「{name}」没有配置 Key", "name", item.Name)
	}
	return item.APIKey, nil
}

// FetchModels 到端点的 /models 拉一遍模型名。给「从端点拉取」按钮用，
// 结果由用户决定要不要写进模型清单，这里不落库。
func (s *Store) FetchModels(ctx context.Context, id int64) ([]string, error) {
	item, err := s.db.GetProvider(ctx, id)
	if err != nil {
		return nil, err
	}
	m := protocol.ModelConfig{ProviderID: id}
	key, err := resolveProvider(item, &m)
	if err != nil {
		return nil, err
	}
	// 拉列表不该无限等：端点不通时用户面前是一个一直转的按钮。
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, err := llm.NewConfigured(llm.Config{ProviderID: m.ProviderID, BaseURL: m.BaseURL, APIKey: key, Protocol: m.APIProtocol})
	if err != nil {
		return nil, err
	}
	if serviceProfile(item) != "openrouter" {
		return client.ListModels(ctx)
	}
	names, caps, err := client.ListModelsAndMetadata(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(catalogMetadata{Profile: "openrouter", BaseURL: item.BaseURL, Models: caps})
	if err != nil {
		return nil, err
	}
	cached, err := s.db.CacheProviderModelMetadata(ctx, id, item.Revision, model.JSON(raw))
	if err != nil {
		return nil, err
	}
	if !cached {
		return nil, fmt.Errorf("model service configuration changed while fetching its catalog; refresh the model list again")
	}
	return names, nil
}

func view(item *model.Provider) protocol.ProviderView {
	return protocol.ProviderView{
		DefaultBaseURL: defaultAPIBaseURL(serviceProfile(item), resolveRoute(item, "").APIProtocol), RoutingMode: pRoutingMode(item), ServiceProfile: serviceProfile(item), ModelOverrides: modelOverrides(item), ModelRoutes: modelRoutes(item),
		ID:          item.ID,
		Name:        item.Name,
		Type:        string(item.Type),
		APIProtocol: item.APIProtocol.Normalized(),
		BaseURL:     item.BaseURL,
		APIKeySet:   strings.TrimSpace(item.APIKey) != "",
		Models:      decodeModels(item.Models),
		Enabled:     item.Enabled,
	}
}

// 模型清单在库里是一个 JSON 字符串数组——旧版就是这么存的。
//
// 能力标记写成 `名字#vision,image` 附在名字后面，而不是换成对象数组：
// 旧版写下的纯字符串照样读得出来（没有 `#` 就是只做对话），也不用动表结构。
// encodeModels 把清单写成落库的形式：**按模型名去重**，同名的合并标记。
//
// 早先按整条字符串去重，`qwen-image-3.0#vision,image` 与从端点拉回来的
// `qwen-image-3.0` 被当成两条，界面上同一个模型出现两次、勾选改到哪一行都说不清。
// 合并规则与能力表相同：能力取并集、窗口取大、位置留在第一次出现的地方。
func encodeModels(names []string) model.JSON {
	encoded, _ := json.Marshal(MergeModelEntries(names))
	return model.JSON(encoded)
}

// MergeModelEntries 按模型名合并清单项。
func MergeModelEntries(entries []string) []string {
	order := make([]string, 0, len(entries))
	merged := map[string]protocol.ParsedModel{}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parsed := protocol.ParseModelMark(entry)
		key := strings.ToLower(parsed.Name)
		if key == "" {
			continue
		}
		existing, seen := merged[key]
		if !seen {
			order = append(order, key)
			merged[key] = parsed
			continue
		}
		for _, role := range parsed.Roles {
			if !hasRole(existing.Roles, role) {
				existing.Roles = append(existing.Roles, role)
			}
		}
		if parsed.Context > existing.Context {
			existing.Context = parsed.Context
		}
		merged[key] = existing
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, protocol.FormatModelMark(merged[key]))
	}
	return out
}

// GuessRolesByName 按模型名猜能力，只在两份能力表都没收这个模型时用。
//
// 只认最不会错的几个词：名字里带 tts/speech 的是朗读，asr/whisper/transcribe 是听写，
// image/imagen/dall-e/flux/万相 是画图，-vl-/vision 是看图。「omni」这类什么都能的
// 不猜——猜多了等于乱标。
func GuessRolesByName(name string) []protocol.ModelRole {
	lower := strings.ToLower(name)
	if index := strings.LastIndex(lower, "/"); index >= 0 {
		lower = lower[index+1:]
	}
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}
	switch {
	case has("-tts", "tts-", "speech-0", "text-to-speech"):
		return []protocol.ModelRole{protocol.RoleTTS}
	case has("-asr", "asr-", "whisper", "transcribe", "speech-to-text"):
		return []protocol.ModelRole{protocol.RoleSTT}
	case has("-image", "image-", "imagen", "dall-e", "flux", "wan2", "stable-diffusion", "z-image"):
		return []protocol.ModelRole{protocol.RoleImage}
	case has("-vl-", "-vl", "vision"):
		return []protocol.ModelRole{protocol.RoleVision}
	}
	return nil
}

func decodeModels(raw model.JSON) []string {
	var names []string
	_ = json.Unmarshal(raw, &names)
	if names == nil {
		names = []string{}
	}
	return names
}

func pRoutingMode(p *model.Provider) string {
	if p.RoutingMode == "manual" {
		return "manual"
	}
	return "auto"
}
