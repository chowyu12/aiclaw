package appservice

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
)

type SearchEngineInput struct{ Provider, Name, BaseURL, APIKey string }
type DesktopSearchEngine struct {
	ID        int64  `json:"id"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Enabled   bool   `json:"enabled"`
	APIKeySet bool   `json:"api_key_set"`
}
type MCPServerInput struct{ Name, Description, Transport, Endpoint, Args, Env, Headers string }
type DesktopMCPServer struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Transport   string `json:"transport"`
	Endpoint    string `json:"endpoint"`
	Enabled     bool   `json:"enabled"`
	PluginUUID  string `json:"plugin_uuid"`
}
type DesktopPlugin struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	// Source is "builtin" for plugins that ship with the application. Those
	// can be disabled but not removed, so the UI must not offer a delete.
	Source       string   `json:"source"`
	Enabled      bool     `json:"enabled"`
	SkillCount   int      `json:"skill_count"`
	MCPCount     int      `json:"mcp_count"`
	ToolCount    int      `json:"tool_count"`
	ChannelCount int      `json:"channel_count"`
	Permissions  []string `json:"permissions"`
	// MissingConfig lists declared required keys that are still unset. A
	// plugin with any of these can be installed but not enabled.
	MissingConfig []string `json:"missing_config,omitzero"`
}

type DesktopMemorySettings struct {
	UseMemories      bool `json:"use_memories"`
	GenerateMemories bool `json:"generate_memories"`
}

type DesktopModelSelection struct {
	ProviderID int64  `json:"provider_id"`
	ModelName  string `json:"model_name"`
}

type DesktopMemory struct {
	UUID        string  `json:"uuid"`
	Kind        string  `json:"kind"`
	MemoryKey   string  `json:"memory_key"`
	Content     string  `json:"content"`
	Status      string  `json:"status"`
	Importance  int     `json:"importance"`
	Confidence  float64 `json:"confidence"`
	Sensitivity string  `json:"sensitivity"`
	Pinned      bool    `json:"pinned"`
	UpdatedAt   string  `json:"updated_at"`
}

const lastModelSelectionSettingKey = "chat.last_model"

func (s *Service) LastModelSelection() (DesktopModelSelection, error) {
	if err := s.ready(); err != nil {
		return DesktopModelSelection{}, err
	}
	value, err := s.store.GetAppSetting(s.ctx, lastModelSelectionSettingKey, "")
	if err != nil || value == "" {
		return DesktopModelSelection{}, err
	}
	var selection DesktopModelSelection
	if json.Unmarshal([]byte(value), &selection) != nil || selection.ProviderID <= 0 || strings.TrimSpace(selection.ModelName) == "" {
		return DesktopModelSelection{}, nil
	}
	provider, err := s.store.GetProvider(s.ctx, selection.ProviderID)
	if err != nil {
		// A deleted Provider makes the preference stale, not the application
		// unusable. The frontend will select and persist the next available model.
		if errors.Is(err, sql.ErrNoRows) {
			return DesktopModelSelection{}, nil
		}
		return DesktopModelSelection{}, err
	}
	var names []string
	if json.Unmarshal(provider.Models, &names) != nil {
		return DesktopModelSelection{}, nil
	}
	for _, name := range names {
		if name == selection.ModelName {
			return selection, nil
		}
	}
	return DesktopModelSelection{}, nil
}

func (s *Service) SetLastModelSelection(providerID int64, modelName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	modelName = strings.TrimSpace(modelName)
	if providerID <= 0 || modelName == "" {
		return fmt.Errorf("provider and model are required")
	}
	provider, err := s.store.GetProvider(s.ctx, providerID)
	if err != nil {
		return err
	}
	var names []string
	if err := json.Unmarshal(provider.Models, &names); err != nil {
		return fmt.Errorf("decode Provider models: %w", err)
	}
	for _, name := range names {
		if name != modelName {
			continue
		}
		value, err := json.Marshal(DesktopModelSelection{ProviderID: providerID, ModelName: modelName})
		if err != nil {
			return err
		}
		return s.store.SetAppSetting(s.ctx, lastModelSelectionSettingKey, string(value))
	}
	return fmt.Errorf("model %q is not configured for Provider %d", modelName, providerID)
}

func (s *Service) MemorySettings() (DesktopMemorySettings, error) {
	if err := s.ready(); err != nil {
		return DesktopMemorySettings{}, err
	}
	use, err := s.store.GetAppSetting(s.ctx, "memory.use", "true")
	if err != nil {
		return DesktopMemorySettings{}, err
	}
	generate, err := s.store.GetAppSetting(s.ctx, "memory.generate", "true")
	if err != nil {
		return DesktopMemorySettings{}, err
	}
	return DesktopMemorySettings{UseMemories: use != "false", GenerateMemories: generate != "false"}, nil
}

func (s *Service) SetMemorySettings(useMemories, generateMemories bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.store.SetAppSetting(s.ctx, "memory.use", fmt.Sprintf("%t", useMemories)); err != nil {
		return err
	}
	return s.store.SetAppSetting(s.ctx, "memory.generate", fmt.Sprintf("%t", generateMemories))
}

func (s *Service) Memories() ([]DesktopMemory, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, _, err := s.store.ListMemories(s.ctx, model.MemoryListQuery{UserID: "local", IncludeAll: false, Page: 1, PageSize: 200})
	if err != nil {
		return nil, err
	}
	result := make([]DesktopMemory, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopMemory{
			UUID: item.UUID, Kind: string(item.Kind), MemoryKey: item.MemoryKey, Content: item.Content,
			Status: string(item.Status), Importance: item.Importance, Confidence: item.Confidence,
			Sensitivity: string(item.Sensitivity), Pinned: item.Pinned, UpdatedAt: item.UpdatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

func (s *Service) ApproveMemory(memoryUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	active := model.MemoryStatusActive
	item, err := s.memory.Update(s.ctx, "local", memoryUUID, model.UpdateMemoryRequest{Status: &active}, "desktop_user")
	if err != nil {
		return err
	}
	if item.Status != model.MemoryStatusActive {
		return fmt.Errorf("sensitive memory must be edited before approval")
	}
	return nil
}

func (s *Service) ForgetMemory(memoryUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.memory.Forget(s.ctx, "local", memoryUUID, "desktop_user")
}

func (s *Service) DeleteProvider(id int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	count, err := s.store.CountThreadsUsingProvider(s.ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("provider is used by %d conversation(s) and cannot be deleted", count)
	}
	return s.store.DeleteProvider(s.ctx, id)
}

func (s *Service) SearchEngines() ([]DesktopSearchEngine, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, _, err := s.store.ListSearchEngineConfigs(s.ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	result := make([]DesktopSearchEngine, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopSearchEngine{ID: item.ID, Provider: string(item.Provider), Name: item.Name, BaseURL: item.BaseURL, Enabled: item.Enabled, APIKeySet: item.APIKey != ""})
	}
	return result, nil
}
func (s *Service) AddSearchEngine(input SearchEngineInput) (DesktopSearchEngine, error) {
	if err := s.ready(); err != nil {
		return DesktopSearchEngine{}, err
	}
	input.Name, input.Provider, input.BaseURL = strings.TrimSpace(input.Name), strings.TrimSpace(input.Provider), strings.TrimSpace(input.BaseURL)
	if input.Name == "" || input.Provider == "" || input.BaseURL == "" {
		return DesktopSearchEngine{}, fmt.Errorf("search provider, name, and base URL are required")
	}
	cfg := &model.SearchEngineConfig{Provider: model.SearchEngineProvider(input.Provider), Name: input.Name, BaseURL: input.BaseURL, APIKey: strings.TrimSpace(input.APIKey), Enabled: true}
	if err := s.store.CreateSearchEngineConfig(s.ctx, cfg); err != nil {
		return DesktopSearchEngine{}, err
	}
	return DesktopSearchEngine{ID: cfg.ID, Provider: input.Provider, Name: cfg.Name, BaseURL: cfg.BaseURL, Enabled: true, APIKeySet: cfg.APIKey != ""}, nil
}
func (s *Service) ToggleSearchEngine(id int64, enabled bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	cfg, err := s.store.GetSearchEngineConfig(s.ctx, id)
	if err != nil {
		return err
	}
	cfg.Enabled = enabled
	return s.store.UpdateSearchEngineConfig(s.ctx, id, cfg)
}
func (s *Service) DeleteSearchEngine(id int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	count, err := s.store.CountThreadsUsingSearchEngine(s.ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("search engine is used by %d conversation(s) and cannot be deleted", count)
	}
	return s.store.DeleteSearchEngineConfig(s.ctx, id)
}

func (s *Service) MCPServers() ([]DesktopMCPServer, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, err := s.store.ListMCPServers(s.ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopMCPServer, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopMCPServer{UUID: item.UUID, Name: item.Name, Description: item.Description, Transport: string(item.Transport), Endpoint: item.Endpoint, Enabled: item.Enabled, PluginUUID: item.PluginUUID})
	}
	return result, nil
}
func (s *Service) AddMCPServer(input MCPServerInput) (DesktopMCPServer, error) {
	if err := s.ready(); err != nil {
		return DesktopMCPServer{}, err
	}
	input.Name, input.Transport, input.Endpoint = strings.TrimSpace(input.Name), strings.TrimSpace(input.Transport), strings.TrimSpace(input.Endpoint)
	if input.Name == "" || input.Endpoint == "" {
		return DesktopMCPServer{}, fmt.Errorf("MCP name and endpoint are required")
	}
	if input.Transport == "" {
		input.Transport = string(model.MCPTransportStdio)
	}
	switch model.MCPTransport(input.Transport) {
	case model.MCPTransportStdio, model.MCPTransportSSE, model.MCPTransportStreamableHTTP:
	default:
		return DesktopMCPServer{}, fmt.Errorf("unsupported MCP transport %q", input.Transport)
	}
	args, err := validatedJSON(input.Args, `[]`, "args")
	if err != nil {
		return DesktopMCPServer{}, err
	}
	env, err := validatedJSON(input.Env, `{}`, "env")
	if err != nil {
		return DesktopMCPServer{}, err
	}
	headers, err := validatedJSON(input.Headers, `{}`, "headers")
	if err != nil {
		return DesktopMCPServer{}, err
	}
	srv := &model.MCPServer{Name: input.Name, Description: input.Description, Transport: model.MCPTransport(input.Transport), Endpoint: input.Endpoint, Args: args, Env: env, Headers: headers, Enabled: true}
	if err := s.store.UpsertMCPServer(s.ctx, srv); err != nil {
		return DesktopMCPServer{}, err
	}
	s.tools.Reload()
	return DesktopMCPServer{UUID: srv.UUID, Name: srv.Name, Description: srv.Description, Transport: string(srv.Transport), Endpoint: srv.Endpoint, Enabled: true}, nil
}
func (s *Service) DeleteMCPServer(serverUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.store.DeleteMCPServer(s.ctx, strings.TrimSpace(serverUUID)); err != nil {
		return err
	}
	s.tools.Reload()
	return nil
}
func (s *Service) ToggleMCPServer(serverUUID string, enabled bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.store.SetMCPServerEnabled(s.ctx, serverUUID, enabled); err != nil {
		return err
	}
	s.tools.Reload()
	return nil
}

func (s *Service) Plugins() ([]DesktopPlugin, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	plugins, err := s.store.ListPlugins(s.ctx)
	if err != nil {
		return nil, err
	}
	skillItems, _ := s.store.ListSkills(s.ctx)
	mcpItems, _ := s.store.ListMCPServers(s.ctx)
	result := make([]DesktopPlugin, 0, len(plugins))
	for _, item := range plugins {
		entry := DesktopPlugin{
			UUID: item.UUID, Name: item.Name, Description: item.Description,
			Version: item.Version, Source: string(item.Source), Enabled: item.Enabled,
		}
		toolCount, channelCount, contributionErr := pluginpkg.NativeContributions(item)
		if contributionErr != nil {
			return nil, contributionErr
		}
		entry.ToolCount, entry.ChannelCount = toolCount, channelCount
		for _, sk := range skillItems {
			if sk.PluginUUID == item.UUID {
				entry.SkillCount++
			}
		}
		for _, srv := range mcpItems {
			if srv.PluginUUID == item.UUID {
				entry.MCPCount++
			}
		}
		permissions, permissionErr := pluginpkg.InstalledPermissions(item, skillItems, mcpItems)
		if permissionErr != nil {
			return nil, permissionErr
		}
		entry.Permissions = permissions
		missing, missingErr := s.pluginConfig().MissingRequired(s.ctx, item)
		if missingErr != nil {
			return nil, missingErr
		}
		entry.MissingConfig = missing
		result = append(result, entry)
	}
	return result, nil
}
func (s *Service) DeletePlugin(pluginUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	plugin, err := s.findPlugin(pluginUUID)
	if err != nil {
		return err
	}
	if err := s.installer.Uninstall(s.ctx, plugin); err != nil {
		return err
	}
	s.tools.Reload()
	return nil
}
func (s *Service) TogglePlugin(pluginUUID string, enabled bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if enabled {
		plugin, err := s.findPlugin(pluginUUID)
		if err != nil {
			return err
		}
		// Enabling is what grants a plugin its declared permissions, so it is
		// also where the declared configuration has to be complete.
		if err := s.pluginConfig().ValidateEnable(s.ctx, plugin); err != nil {
			return err
		}
	}
	if err := s.store.SetPluginEnabled(s.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	if err := s.store.SetPluginSkillsEnabled(s.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	if err := s.store.SetPluginMCPEnabled(s.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	s.tools.Reload()
	// Tools are rebuilt every turn, but a channel holds a connection: it has
	// to be started or stopped now.
	return s.syncChannels()
}

// DesktopChannelBinding is one external conversation known to a connector.
type DesktopChannelBinding struct {
	PluginUUID  string `json:"plugin_uuid"`
	ChannelID   string `json:"channel_id"`
	ExternalKey string `json:"external_key"`
	DisplayName string `json:"display_name"`
	ThreadUUID  string `json:"thread_uuid,omitzero"`
	ProviderID  int64  `json:"provider_id,omitzero"`
	ModelName   string `json:"model_name,omitzero"`
	Allowed     bool   `json:"allowed"`
	// AllowedTools are the acting tools this conversation may use beyond the
	// read-only default set.
	AllowedTools []string `json:"allowed_tools,omitzero"`
	LastMessage  string   `json:"last_message,omitzero"`
}

// ChannelBindings lists every external conversation a connector has seen,
// including the ones waiting for authorization.
func (s *Service) ChannelBindings() ([]DesktopChannelBinding, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, err := s.store.ListChannelBindings(s.ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopChannelBinding, 0, len(items))
	for _, item := range items {
		entry := DesktopChannelBinding{
			PluginUUID: item.PluginUUID, ChannelID: item.ChannelID, ExternalKey: item.ExternalKey,
			DisplayName: item.DisplayName, ThreadUUID: item.ThreadUUID,
			ProviderID: item.ProviderID, ModelName: item.ModelName, Allowed: item.Allowed,
		}
		if len(item.AllowedTools) > 0 {
			_ = json.Unmarshal(item.AllowedTools, &entry.AllowedTools)
		}
		if !item.LastMessage.IsZero() {
			entry.LastMessage = item.LastMessage.Format(time.RFC3339)
		}
		result = append(result, entry)
	}
	return result, nil
}

// AuthorizeChannelBinding lets one external conversation reach the agent.
//
// This is the consent step for inbound messages: until it happens, a message
// from that conversation is recorded and refused. The model and the acting
// tools it may use are chosen here rather than inherited from the desktop
// session, because the sender is not the local user.
func (s *Service) AuthorizeChannelBinding(pluginUUID, channelID, externalKey string, providerID int64, modelName string, allowedTools []string) error {
	if err := s.ready(); err != nil {
		return err
	}
	binding, err := s.store.GetChannelBinding(s.ctx, pluginUUID, channelID, externalKey)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("conversation %q is not known to this connector", externalKey)
	}
	if providerID == 0 || strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("a provider and model are required to authorize a conversation")
	}
	tools, err := json.Marshal(allowedTools)
	if err != nil {
		return err
	}
	binding.Allowed, binding.ProviderID, binding.ModelName = true, providerID, strings.TrimSpace(modelName)
	binding.AllowedTools = model.JSON(tools)
	return s.store.SaveChannelBinding(s.ctx, binding)
}

// RevokeChannelBinding stops an external conversation from reaching the agent.
// The binding and its thread are kept so the history stays readable.
func (s *Service) RevokeChannelBinding(pluginUUID, channelID, externalKey string) error {
	if err := s.ready(); err != nil {
		return err
	}
	binding, err := s.store.GetChannelBinding(s.ctx, pluginUUID, channelID, externalKey)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("conversation %q is not known to this connector", externalKey)
	}
	binding.Allowed = false
	return s.store.SaveChannelBinding(s.ctx, binding)
}

// PluginConfigFields returns a plugin's declared configuration for the
// settings form. A secret that is already stored is reported as set and its
// value is never returned.
func (s *Service) PluginConfigFields(pluginUUID string) ([]pluginpkg.Field, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	plugin, err := s.findPlugin(pluginUUID)
	if err != nil {
		return nil, err
	}
	return s.pluginConfig().Fields(s.ctx, plugin)
}

// SetPluginConfig stores one declared configuration value. An empty value
// clears the key.
func (s *Service) SetPluginConfig(pluginUUID, key, value string) error {
	if err := s.ready(); err != nil {
		return err
	}
	plugin, err := s.findPlugin(pluginUUID)
	if err != nil {
		return err
	}
	return s.pluginConfig().Set(s.ctx, plugin, key, value)
}

func (s *Service) pluginConfig() *pluginpkg.ConfigService {
	return pluginpkg.NewConfigService(s.store)
}

func (s *Service) findPlugin(pluginUUID string) (model.Plugin, error) {
	pluginUUID = strings.TrimSpace(pluginUUID)
	plugins, err := s.store.ListPlugins(s.ctx)
	if err != nil {
		return model.Plugin{}, err
	}
	for _, plugin := range plugins {
		if plugin.UUID == pluginUUID {
			return plugin, nil
		}
	}
	return model.Plugin{}, fmt.Errorf("plugin %q not found", pluginUUID)
}

func (s *Service) installPlugin(source string) (DesktopPlugin, error) {
	plugin, counts, err := s.installer.Install(s.ctx, source)
	if err != nil {
		return DesktopPlugin{}, err
	}
	s.tools.Reload()
	return DesktopPlugin{
		UUID: plugin.UUID, Name: plugin.Name, Description: plugin.Description,
		Version: plugin.Version, Source: string(plugin.Source), Enabled: plugin.Enabled,
		SkillCount: counts.Skills, MCPCount: counts.MCP,
		ToolCount: counts.Tools, ChannelCount: counts.Channels,
	}, nil
}

func validatedJSON(value, fallback, field string) (model.JSON, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if !json.Valid([]byte(value)) {
		return nil, fmt.Errorf("MCP %s must be valid JSON", field)
	}
	return model.JSON(value), nil
}
