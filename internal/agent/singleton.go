package agent

import (
	"cmp"
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/model"
)

var (
	testAgentMu     sync.Mutex
	testAgentConfig *model.Agent

	runtimeAgentMu sync.RWMutex
	runtimeAgent   *model.Agent
)

// SetTestAgent 仅单测使用：覆盖内存中的 Agent 配置。
func SetTestAgent(a *model.Agent) {
	testAgentMu.Lock()
	defer testAgentMu.Unlock()
	if a == nil {
		testAgentConfig = nil
		return
	}
	cp := *a
	testAgentConfig = &cp
}

// ClearTestAgent 清除单测覆盖。
func ClearTestAgent() {
	SetTestAgent(nil)
}

// InitSingletonAgent 从 config.RT.Cfg.Agent 初始化内存单例；必要时生成 uuid/token 并写回 config.yaml。
func InitSingletonAgent(ctx context.Context, pl ProviderLister) error {
	testAgentMu.Lock()
	if testAgentConfig != nil {
		testAgentMu.Unlock()
		return nil
	}
	testAgentMu.Unlock()

	config.RT.Mu.RLock()
	if config.RT.Cfg == nil {
		config.RT.Mu.RUnlock()
		return errors.New("config runtime not initialized")
	}
	ac := &config.RT.Cfg.Agent
	config.RT.Mu.RUnlock()

	a := agentFromConfig(ac)
	normalizeLoadedAgent(a)
	needSave := false
	if a.ProviderID == 0 && pl != nil {
		providers, _, err := pl.ListProviders(ctx, model.ListQuery{Page: 1, PageSize: 1})
		if err == nil && len(providers) > 0 {
			a.ProviderID = providers[0].ID
		}
	}
	if a.UUID == "" {
		a.UUID = uuid.New().String()
		needSave = true
	}
	if a.Token == "" {
		a.Token = "ag-" + strings.ReplaceAll(uuid.New().String(), "-", "")
		needSave = true
	}

	cp := *a
	runtimeAgentMu.Lock()
	runtimeAgent = &cp
	runtimeAgentMu.Unlock()

	config.RT.Mu.Lock()
	config.SyncAgentConfigFromModel(&config.RT.Cfg.Agent, &cp)
	config.RT.Mu.Unlock()

	if needSave {
		return config.SaveRuntime()
	}
	return nil
}

// ReloadSingletonFromConfig 热加载：用 yaml 中的 agent 段覆盖内存单例（不自动绑定 provider）。
func ReloadSingletonFromConfig(ac *config.AgentConfig) error {
	testAgentMu.Lock()
	if testAgentConfig != nil {
		testAgentMu.Unlock()
		return nil
	}
	testAgentMu.Unlock()

	a := agentFromConfig(ac)
	normalizeLoadedAgent(a)
	if a.UUID == "" || a.Token == "" {
		return errors.New("agent uuid/token missing in config")
	}
	cp := *a
	runtimeAgentMu.Lock()
	runtimeAgent = &cp
	runtimeAgentMu.Unlock()
	return nil
}

func agentFromConfig(ac *config.AgentConfig) *model.Agent {
	if ac == nil {
		ac = &config.AgentConfig{}
	}
	a := &model.Agent{
		UUID:              ac.UUID,
		Name:              cmp.Or(ac.Name, "Assistant"),
		Description:       cmp.Or(ac.Description, "默认 Agent，可在控制台修改"),
		SystemPrompt:      ac.SystemPrompt,
		ProviderID:        ac.ProviderID,
		ModelName:         cmp.Or(ac.ModelName, "gpt-4o"),
		Temperature:       ac.Temperature,
		MaxTokens:         ac.MaxTokens,
		Timeout:           ac.Timeout,
		MaxHistory:        ac.MaxHistory,
		MaxIterations:     ac.MaxIterations,
		Token:             strings.TrimSpace(ac.Token),
		ToolSearchEnabled: ac.ToolSearchEnabled,
		MemOSEnabled:      ac.MemOSEnabled,
		MemOSCfg:          ac.MemOSCfg,
		ToolIDs:           append([]int64(nil), ac.ToolIDs...),
	}
	if a.Temperature == 0 {
		a.Temperature = 0.7
	}
	return a
}

// TryLoadAgent 返回内存中的 Agent；若 provider_id 仍为 0 且库中已有 Provider，则尝试绑定首个并落盘。
func TryLoadAgent(ctx context.Context, pl ProviderLister) (*model.Agent, error) {
	a, err := LoadAgent(ctx)
	if err != nil {
		return nil, err
	}
	if a.ProviderID == 0 && pl != nil {
		_ = tryBindFirstProvider(ctx, pl)
		return LoadAgent(ctx)
	}
	return a, nil
}

// LoadAgent 返回内存中单例 Agent 的副本；测试环境下可被 SetTestAgent 覆盖。
func LoadAgent(_ context.Context) (*model.Agent, error) {
	testAgentMu.Lock()
	ta := testAgentConfig
	testAgentMu.Unlock()
	if ta != nil {
		cp := *ta
		normalizeLoadedAgent(&cp)
		return &cp, nil
	}

	runtimeAgentMu.RLock()
	ra := runtimeAgent
	runtimeAgentMu.RUnlock()
	if ra == nil {
		return nil, errors.New("agent not initialized")
	}
	cp := *ra
	normalizeLoadedAgent(&cp)
	return &cp, nil
}

func normalizeLoadedAgent(a *model.Agent) {
	if a.MaxTokens == 0 {
		a.MaxTokens = 4096
	}
	if a.MaxHistory == 0 {
		a.MaxHistory = model.DefaultAgentMaxHistory
	}
	if a.MaxIterations == 0 {
		a.MaxIterations = model.DefaultAgentMaxIterations
	}
}

// SaveAgent 更新内存中的单例并写回 config.yaml（不含 Tools 等瞬时字段）。
func SaveAgent(a *model.Agent) error {
	testAgentMu.Lock()
	if testAgentConfig != nil {
		cp := *a
		cp.Tools = nil
		cp.ID = 0
		normalizeLoadedAgent(&cp)
		testAgentConfig = &cp
		testAgentMu.Unlock()
		return nil
	}
	testAgentMu.Unlock()

	runtimeAgentMu.Lock()
	defer runtimeAgentMu.Unlock()
	if runtimeAgent == nil {
		return errors.New("agent not initialized")
	}
	out := *a
	out.Tools = nil
	out.ID = 0
	normalizeLoadedAgent(&out)
	cp := out
	runtimeAgent = &cp

	config.RT.Mu.Lock()
	if config.RT.Cfg != nil {
		config.SyncAgentConfigFromModel(&config.RT.Cfg.Agent, &cp)
	}
	config.RT.Mu.Unlock()

	return config.SaveRuntime()
}

// GetAgentByToken 校验 token 是否与当前单例一致；pl 非空时可在 provider_id 为 0 时尝试绑定首个 Provider。
func GetAgentByToken(ctx context.Context, token string, pl ProviderLister) (*model.Agent, error) {
	if token == "" {
		return nil, errors.New("empty token")
	}
	a, err := TryLoadAgent(ctx, pl)
	if err != nil {
		return nil, err
	}
	if a.Token == "" || subtle.ConstantTimeCompare([]byte(a.Token), []byte(token)) != 1 {
		return nil, errors.New("invalid agent token")
	}
	return a, nil
}

// UpdateAgentToken 生成新 token 并保存到内存与 config.yaml。
func UpdateAgentToken() (string, error) {
	testAgentMu.Lock()
	if testAgentConfig != nil {
		newToken := "ag-" + strings.ReplaceAll(uuid.New().String(), "-", "")
		testAgentConfig.Token = newToken
		testAgentMu.Unlock()
		return newToken, nil
	}
	testAgentMu.Unlock()

	runtimeAgentMu.Lock()
	defer runtimeAgentMu.Unlock()
	if runtimeAgent == nil {
		return "", errors.New("agent not initialized")
	}
	newToken := "ag-" + strings.ReplaceAll(uuid.New().String(), "-", "")
	runtimeAgent.Token = newToken
	cp := *runtimeAgent

	config.RT.Mu.Lock()
	if config.RT.Cfg != nil {
		config.SyncAgentConfigFromModel(&config.RT.Cfg.Agent, &cp)
	}
	config.RT.Mu.Unlock()

	return newToken, config.SaveRuntime()
}

// ProviderLister 用于在 provider_id 未设置时绑定首个 Provider。
type ProviderLister interface {
	ListProviders(ctx context.Context, q model.ListQuery) ([]*model.Provider, int64, error)
}

func tryBindFirstProvider(ctx context.Context, pl ProviderLister) error {
	runtimeAgentMu.Lock()
	defer runtimeAgentMu.Unlock()
	if runtimeAgent == nil || runtimeAgent.ProviderID != 0 {
		return nil
	}
	providers, _, err := pl.ListProviders(ctx, model.ListQuery{Page: 1, PageSize: 1})
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return errors.New("no providers")
	}
	runtimeAgent.ProviderID = providers[0].ID
	cp := *runtimeAgent

	config.RT.Mu.Lock()
	if config.RT.Cfg != nil {
		config.SyncAgentConfigFromModel(&config.RT.Cfg.Agent, &cp)
	}
	config.RT.Mu.Unlock()

	return config.SaveRuntime()
}

// OnProviderCreated 在新增 Provider 后调用：若单例尚未绑定 provider_id，则绑定为首个可用 Provider。
func OnProviderCreated(ctx context.Context, pl ProviderLister) error {
	return tryBindFirstProvider(ctx, pl)
}
