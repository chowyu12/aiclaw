package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
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
	Enabled     bool   `json:"enabled"`
	SkillCount  int    `json:"skill_count"`
	MCPCount    int    `json:"mcp_count"`
}

type DesktopMemorySettings struct {
	UseMemories      bool `json:"use_memories"`
	GenerateMemories bool `json:"generate_memories"`
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

func (a *App) MemorySettings() (DesktopMemorySettings, error) {
	if err := a.ready(); err != nil {
		return DesktopMemorySettings{}, err
	}
	use, err := a.store.GetAppSetting(a.ctx, "memory.use", "true")
	if err != nil {
		return DesktopMemorySettings{}, err
	}
	generate, err := a.store.GetAppSetting(a.ctx, "memory.generate", "true")
	if err != nil {
		return DesktopMemorySettings{}, err
	}
	return DesktopMemorySettings{UseMemories: use != "false", GenerateMemories: generate != "false"}, nil
}

func (a *App) SetMemorySettings(useMemories, generateMemories bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.store.SetAppSetting(a.ctx, "memory.use", fmt.Sprintf("%t", useMemories)); err != nil {
		return err
	}
	return a.store.SetAppSetting(a.ctx, "memory.generate", fmt.Sprintf("%t", generateMemories))
}

func (a *App) Memories() ([]DesktopMemory, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, _, err := a.store.ListMemories(a.ctx, model.MemoryListQuery{UserID: "local", IncludeAll: false, Page: 1, PageSize: 200})
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

func (a *App) ApproveMemory(memoryUUID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	active := model.MemoryStatusActive
	item, err := a.memory.Update(a.ctx, "local", memoryUUID, model.UpdateMemoryRequest{Status: &active}, "desktop_user")
	if err != nil {
		return err
	}
	if item.Status != model.MemoryStatusActive {
		return fmt.Errorf("sensitive memory must be edited before approval")
	}
	return nil
}

func (a *App) ForgetMemory(memoryUUID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.memory.Forget(a.ctx, "local", memoryUUID, "desktop_user")
}

func (a *App) DeleteProvider(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	count, err := a.store.CountThreadsUsingProvider(a.ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("provider is used by %d conversation(s) and cannot be deleted", count)
	}
	return a.store.DeleteProvider(a.ctx, id)
}

func (a *App) SearchEngines() ([]DesktopSearchEngine, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, _, err := a.store.ListSearchEngineConfigs(a.ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	result := make([]DesktopSearchEngine, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopSearchEngine{ID: item.ID, Provider: string(item.Provider), Name: item.Name, BaseURL: item.BaseURL, Enabled: item.Enabled, APIKeySet: item.APIKey != ""})
	}
	return result, nil
}
func (a *App) AddSearchEngine(input SearchEngineInput) (DesktopSearchEngine, error) {
	if err := a.ready(); err != nil {
		return DesktopSearchEngine{}, err
	}
	input.Name, input.Provider, input.BaseURL = strings.TrimSpace(input.Name), strings.TrimSpace(input.Provider), strings.TrimSpace(input.BaseURL)
	if input.Name == "" || input.Provider == "" || input.BaseURL == "" {
		return DesktopSearchEngine{}, fmt.Errorf("search provider, name, and base URL are required")
	}
	cfg := &model.SearchEngineConfig{Provider: model.SearchEngineProvider(input.Provider), Name: input.Name, BaseURL: input.BaseURL, APIKey: strings.TrimSpace(input.APIKey), Enabled: true}
	if err := a.store.CreateSearchEngineConfig(a.ctx, cfg); err != nil {
		return DesktopSearchEngine{}, err
	}
	return DesktopSearchEngine{ID: cfg.ID, Provider: input.Provider, Name: cfg.Name, BaseURL: cfg.BaseURL, Enabled: true, APIKeySet: cfg.APIKey != ""}, nil
}
func (a *App) ToggleSearchEngine(id int64, enabled bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	cfg, err := a.store.GetSearchEngineConfig(a.ctx, id)
	if err != nil {
		return err
	}
	cfg.Enabled = enabled
	return a.store.UpdateSearchEngineConfig(a.ctx, id, cfg)
}
func (a *App) DeleteSearchEngine(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	count, err := a.store.CountThreadsUsingSearchEngine(a.ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("search engine is used by %d conversation(s) and cannot be deleted", count)
	}
	return a.store.DeleteSearchEngineConfig(a.ctx, id)
}

func (a *App) MCPServers() ([]DesktopMCPServer, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, err := a.store.ListMCPServers(a.ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopMCPServer, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopMCPServer{UUID: item.UUID, Name: item.Name, Description: item.Description, Transport: string(item.Transport), Endpoint: item.Endpoint, Enabled: item.Enabled, PluginUUID: item.PluginUUID})
	}
	return result, nil
}
func (a *App) AddMCPServer(input MCPServerInput) (DesktopMCPServer, error) {
	if err := a.ready(); err != nil {
		return DesktopMCPServer{}, err
	}
	input.Name, input.Transport, input.Endpoint = strings.TrimSpace(input.Name), strings.TrimSpace(input.Transport), strings.TrimSpace(input.Endpoint)
	if input.Name == "" || input.Endpoint == "" {
		return DesktopMCPServer{}, fmt.Errorf("MCP name and endpoint are required")
	}
	if input.Transport == "" {
		input.Transport = string(model.MCPTransportStdio)
	}
	srv := &model.MCPServer{Name: input.Name, Description: input.Description, Transport: model.MCPTransport(input.Transport), Endpoint: input.Endpoint, Args: normalizedJSON(input.Args, `[]`), Env: normalizedJSON(input.Env, `{}`), Headers: normalizedJSON(input.Headers, `{}`), Enabled: true}
	if err := a.store.UpsertMCPServer(a.ctx, srv); err != nil {
		return DesktopMCPServer{}, err
	}
	a.tools.Reload()
	return DesktopMCPServer{UUID: srv.UUID, Name: srv.Name, Description: srv.Description, Transport: string(srv.Transport), Endpoint: srv.Endpoint, Enabled: true}, nil
}
func (a *App) ToggleMCPServer(serverUUID string, enabled bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.store.SetMCPServerEnabled(a.ctx, serverUUID, enabled); err != nil {
		return err
	}
	a.tools.Reload()
	return nil
}

func (a *App) Plugins() ([]DesktopPlugin, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	plugins, err := a.store.ListPlugins(a.ctx)
	if err != nil {
		return nil, err
	}
	skillItems, _ := a.store.ListSkills(a.ctx)
	mcpItems, _ := a.store.ListMCPServers(a.ctx)
	result := make([]DesktopPlugin, 0, len(plugins))
	for _, plugin := range plugins {
		item := DesktopPlugin{UUID: plugin.UUID, Name: plugin.Name, Description: plugin.Description, Version: plugin.Version, Enabled: plugin.Enabled}
		for _, sk := range skillItems {
			if sk.PluginUUID == plugin.UUID {
				item.SkillCount++
			}
		}
		for _, srv := range mcpItems {
			if srv.PluginUUID == plugin.UUID {
				item.MCPCount++
			}
		}
		result = append(result, item)
	}
	return result, nil
}
func (a *App) TogglePlugin(pluginUUID string, enabled bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.store.SetPluginEnabled(a.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	if err := a.store.SetPluginSkillsEnabled(a.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	if err := a.store.SetPluginMCPEnabled(a.ctx, pluginUUID, enabled); err != nil {
		return err
	}
	a.tools.Reload()
	return nil
}

func (a *App) installPlugin(source string) (DesktopPlugin, error) {
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return DesktopPlugin{}, fmt.Errorf("invalid plugin directory")
	}
	manifest := struct{ Name, Description, Version string }{Name: filepath.Base(source)}
	manifestBytes := []byte(`{}`)
	for _, candidate := range []string{filepath.Join(source, ".codex-plugin", "plugin.json"), filepath.Join(source, "plugin.json")} {
		if data, readErr := os.ReadFile(candidate); readErr == nil {
			manifestBytes = data
			_ = json.Unmarshal(data, &manifest)
			break
		}
	}
	if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = filepath.Base(source)
	}
	pluginUUID := uuid.NewString()
	dest := filepath.Join(a.root, "plugins", safeName(manifest.Name)+"-"+pluginUUID[:8])
	if err := copyPluginDir(source, dest); err != nil {
		return DesktopPlugin{}, err
	}
	plugin := &model.Plugin{UUID: pluginUUID, Name: manifest.Name, Description: manifest.Description, Version: manifest.Version, InstallDir: dest, Manifest: model.JSON(manifestBytes), Enabled: true}
	if err := a.store.CreatePlugin(a.ctx, plugin); err != nil {
		return DesktopPlugin{}, err
	}
	skillCount, err := a.installPluginSkills(plugin)
	if err != nil {
		return DesktopPlugin{}, err
	}
	mcpCount, err := a.installPluginMCP(plugin)
	if err != nil {
		return DesktopPlugin{}, err
	}
	a.tools.Reload()
	return DesktopPlugin{UUID: plugin.UUID, Name: plugin.Name, Description: plugin.Description, Version: plugin.Version, Enabled: true, SkillCount: skillCount, MCPCount: mcpCount}, nil
}

func (a *App) installPluginSkills(plugin *model.Plugin) (int, error) {
	var infos []skills.SkillInfo
	if info, err := skills.ParseSkillDir(plugin.InstallDir); err == nil {
		infos = append(infos, *info)
	}
	if found, err := skills.ScanAll(filepath.Join(plugin.InstallDir, "skills")); err == nil {
		infos = append(infos, found...)
	}
	for _, info := range infos {
		skill := skills.InfoToSkill(info, model.SkillSourceLocal, info.Slug)
		skill.UUID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(plugin.UUID+"/"+info.DirName)).String()
		skill.PluginUUID = plugin.UUID
		skill.InstallDir = filepath.Join(plugin.InstallDir, "skills", info.DirName)
		if _, err := os.Stat(skill.InstallDir); err != nil {
			skill.InstallDir = plugin.InstallDir
		}
		if err := a.store.UpsertSkill(a.ctx, skill); err != nil {
			return 0, err
		}
	}
	return len(infos), nil
}
func (a *App) installPluginMCP(plugin *model.Plugin) (int, error) {
	var raw struct {
		MCPServers map[string]struct {
			Command, URL string
			Args         []string
			Env          map[string]string
			Headers      map[string]string
		} `json:"mcpServers"`
	}
	found := false
	for _, name := range []string{".mcp.json", "mcp.json", filepath.Join(".codex-plugin", "mcp.json")} {
		data, err := os.ReadFile(filepath.Join(plugin.InstallDir, name))
		if err == nil {
			if err := json.Unmarshal(data, &raw); err != nil {
				return 0, fmt.Errorf("parse %s: %w", name, err)
			}
			found = true
			break
		}
	}
	if !found {
		return 0, nil
	}
	count := 0
	for name, cfg := range raw.MCPServers {
		transport, endpoint := model.MCPTransportStdio, cfg.Command
		if cfg.URL != "" {
			transport, endpoint = model.MCPTransportSSE, cfg.URL
		}
		args, _ := json.Marshal(cfg.Args)
		env, _ := json.Marshal(cfg.Env)
		headers, _ := json.Marshal(cfg.Headers)
		srv := &model.MCPServer{UUID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(plugin.UUID+"/mcp/"+name)).String(), PluginUUID: plugin.UUID, Name: name, Transport: transport, Endpoint: endpoint, Args: model.JSON(args), Env: model.JSON(env), Headers: model.JSON(headers), Enabled: true}
		if endpoint == "" {
			continue
		}
		if err := a.store.UpsertMCPServer(a.ctx, srv); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func normalizedJSON(value, fallback string) model.JSON {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if !json.Valid([]byte(value)) {
		value = fallback
	}
	return model.JSON(value)
}
func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "plugin"
	}
	return strings.Trim(b.String(), "-")
}
func copyPluginDir(source, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inCloseErr := in.Close()
		outCloseErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inCloseErr != nil {
			return inCloseErr
		}
		return outCloseErr
	})
}
