package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/appserver"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/core"
	memorypkg "github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
	providerpkg "github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx    context.Context
	store  *gormstore.GormStore
	server *appserver.Service
	tools  *core.LocalToolDispatcher
	memory *memorypkg.Service
	root   string
	err    string
}

// ProviderInput is the configuration collected by the desktop settings screen.
// Credentials remain in the local SQLite database under ~/.aiclaw.
type ProviderInput struct {
	Name    string
	Type    string
	BaseURL string
	APIKey  string
	Model   string
}

type ChatResult struct {
	ThreadID string `json:"threadId"`
	Content  string `json:"content"`
	Error    string `json:"error,omitempty"`
}

type ChatProfile struct {
	ProviderID            int64
	ModelName             string
	SearchEnabled         bool
	ProjectUUID           string
	RequestID             string
	MemoryUseEnabled      bool
	MemoryGenerateEnabled bool
}

type ChatDelta struct {
	RequestID string `json:"request_id"`
	ThreadID  string `json:"thread_id"`
	Delta     string `json:"delta"`
}

type ChatProgress struct {
	RequestID string `json:"request_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	Kind      string `json:"kind"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DesktopProvider and DesktopThread deliberately avoid exposing
// database models to the Wails binding generator. In particular, time.Time is
// not a frontend binding type in older Wails releases used for SDK compatibility.
type DesktopProvider struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	BaseURL string   `json:"base_url"`
	Models  []string `json:"models"`
}

type DesktopThread struct {
	UUID        string `json:"uuid"`
	Title       string `json:"title"`
	ProjectUUID string `json:"project_uuid"`
	ProviderID  int64  `json:"provider_id"`
	ModelName   string `json:"model_name"`
	UpdatedAt   string `json:"updated_at"`
}

type DesktopProject struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type DesktopMessage struct {
	Role        string              `json:"role"`
	Content     string              `json:"content"`
	Attachments []DesktopAttachment `json:"attachments,omitempty"`
	Execution   []DesktopExecution  `json:"execution,omitempty"`
}

type DesktopExecution struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func NewApp() *App { return &App{} }
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	home, err := os.UserHomeDir()
	if err != nil {
		a.err = err.Error()
		return
	}
	root := filepath.Join(home, ".aiclaw")
	a.root = root
	cfg, err := config.Load(filepath.Join(root, "config.yaml"))
	if err != nil {
		a.err = err.Error()
		return
	}
	cfg.Workspace, cfg.Database.Driver, cfg.Database.DSN = root, "sqlite", filepath.Join(root, "aiclaw.db")
	if err := os.MkdirAll(root, 0o755); err != nil {
		a.err = err.Error()
		return
	}
	a.store, err = gormstore.New(cfg.Database)
	if err != nil {
		a.err = err.Error()
		return
	}
	a.store.InitFTS5()
	if err := a.store.MigrateLegacyConversations(ctx, "local"); err != nil {
		a.err = err.Error()
		return
	}
	sampler := core.ProviderSampler{Resolver: a.store}
	a.tools = core.NewLocalToolDispatcher(a.store, core.WithDispatcherRoot(root), core.WithSubAgentSampler(sampler))
	a.memory = memorypkg.NewService(a.store)
	a.server = appserver.New(a.store, sampler, a.tools)
	a.cleanupPendingAttachments()
}
func (a *App) shutdown(context.Context) {
	if a.store != nil {
		_ = a.store.Close()
	}
}
func (a *App) Status() string { return a.err }
func (a *App) Providers() ([]DesktopProvider, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, _, err := a.store.ListProviders(a.ctx, model.ListQuery{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	result := make([]DesktopProvider, 0, len(items))
	for _, item := range items {
		var models []string
		_ = json.Unmarshal(item.Models, &models)
		result = append(result, DesktopProvider{ID: item.ID, Name: item.Name, Type: string(item.Type), BaseURL: item.BaseURL, Models: models})
	}
	return result, nil
}

func (a *App) Threads() ([]DesktopThread, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, _, err := a.store.ListThreads(a.ctx, "local", false, 1, 100)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopThread, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopThread{UUID: item.UUID, Title: item.Title, ProjectUUID: item.ProjectUUID, ProviderID: item.ProviderID, ModelName: item.ModelName, UpdatedAt: item.UpdatedAt.Format(time.RFC3339)})
	}
	return result, nil
}

func (a *App) Projects() ([]DesktopProject, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	items, err := a.store.ListProjects(a.ctx, "local")
	if err != nil {
		return nil, err
	}
	result := make([]DesktopProject, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopProject{UUID: item.UUID, Name: item.Name})
	}
	return result, nil
}

func (a *App) CreateProject(name string) (DesktopProject, error) {
	if err := a.ready(); err != nil {
		return DesktopProject{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProject{}, fmt.Errorf("project name is required")
	}
	project := &model.Project{UserID: "local", Name: name}
	if err := a.store.CreateProject(a.ctx, project); err != nil {
		return DesktopProject{}, err
	}
	return DesktopProject{UUID: project.UUID, Name: project.Name}, nil
}

func (a *App) ThreadMessages(threadUUID string) ([]DesktopMessage, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	thread, err := a.store.GetThreadByUUID(a.ctx, threadUUID, false)
	if err != nil {
		return nil, err
	}
	items, err := a.store.LoadRollout(a.ctx, thread.ID)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopMessage, 0)
	executionByTurn := make(map[string][]DesktopExecution)
	for _, item := range items {
		if item.Kind == model.RolloutToolRequested {
			var payload struct {
				ToolCalls []core.ToolCall `json:"tool_calls"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			for _, call := range payload.ToolCalls {
				executionByTurn[item.TurnID] = append(executionByTurn[item.TurnID], DesktopExecution{
					ID: call.ID, Name: call.Name, Status: string(model.StepPending), Message: "等待执行工具",
				})
			}
			continue
		}
		if item.Kind == model.RolloutToolCompleted {
			var payload struct {
				CallID string           `json:"call_id"`
				Name   string           `json:"name"`
				Status model.StepStatus `json:"status"`
				Error  string           `json:"error"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			status := payload.Status
			if status == "" {
				status = model.StepSuccess
			}
			message := "工具执行完成"
			if status == model.StepError || payload.Error != "" {
				status, message = model.StepError, "工具执行失败"
			}
			steps := executionByTurn[item.TurnID]
			matched := false
			for index := range steps {
				if steps[index].ID == payload.CallID {
					steps[index].Name, steps[index].Status, steps[index].Message = payload.Name, string(status), message
					matched = true
					break
				}
			}
			if !matched {
				steps = append(steps, DesktopExecution{ID: payload.CallID, Name: payload.Name, Status: string(status), Message: message})
			}
			executionByTurn[item.TurnID] = steps
			continue
		}
		if item.Kind != model.RolloutUserMessage && item.Kind != model.RolloutAssistantFinal {
			continue
		}
		var payload struct {
			Content     string   `json:"content"`
			Attachments []string `json:"attachments"`
		}
		_ = json.Unmarshal(item.Payload, &payload)
		if payload.Content == "" {
			continue
		}
		role := "AIClaw"
		if item.Kind == model.RolloutUserMessage {
			role = "你"
		}
		result = append(result, DesktopMessage{
			Role: role, Content: payload.Content, Attachments: a.attachmentsByUUIDs(payload.Attachments),
			Execution: append([]DesktopExecution(nil), executionByTurn[item.TurnID]...),
		})
	}
	return result, nil
}

func (a *App) AddProvider(input ProviderInput) (DesktopProvider, error) {
	if err := a.ready(); err != nil {
		return DesktopProvider{}, err
	}
	input.Name, input.Type, input.BaseURL, input.Model = strings.TrimSpace(input.Name), strings.TrimSpace(input.Type), strings.TrimSpace(input.BaseURL), strings.TrimSpace(input.Model)
	if input.Name == "" || input.Type == "" || input.BaseURL == "" {
		return DesktopProvider{}, fmt.Errorf("provider name, type, and base URL are required")
	}
	modelNames := make([]string, 0, 1)
	if input.Model != "" {
		modelNames = append(modelNames, input.Model)
	}
	models, err := json.Marshal(modelNames)
	if err != nil {
		return DesktopProvider{}, err
	}
	provider := &model.Provider{Name: input.Name, Type: model.ProviderType(input.Type), BaseURL: input.BaseURL, APIKey: input.APIKey, Models: model.JSON(models), Enabled: true}
	if err := a.store.CreateProvider(a.ctx, provider); err != nil {
		return DesktopProvider{}, err
	}
	return DesktopProvider{ID: provider.ID, Name: provider.Name, Type: string(provider.Type), BaseURL: provider.BaseURL, Models: modelNames}, nil
}

// SyncProviderModels queries the Provider's model API and returns searchable
// candidates. It does not overwrite the user's local selection; models are
// persisted only after AddProviderModel is called.
func (a *App) SyncProviderModels(id int64) ([]string, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	item, err := a.store.GetProvider(a.ctx, id)
	if err != nil {
		return nil, err
	}
	return providerpkg.FetchRemoteModels(a.ctx, item)
}

func (a *App) AddProviderModel(id int64, name string) (DesktopProvider, error) {
	if err := a.ready(); err != nil {
		return DesktopProvider{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProvider{}, fmt.Errorf("model name is required")
	}
	item, err := a.store.GetProvider(a.ctx, id)
	if err != nil {
		return DesktopProvider{}, err
	}
	var names []string
	_ = json.Unmarshal(item.Models, &names)
	for _, existing := range names {
		if existing == name {
			return desktopProvider(item, names), nil
		}
	}
	names = append(names, name)
	encoded, err := json.Marshal(names)
	if err != nil {
		return DesktopProvider{}, err
	}
	if err := a.store.UpdateProvider(a.ctx, id, model.UpdateProviderReq{Models: model.JSON(encoded)}); err != nil {
		return DesktopProvider{}, err
	}
	item.Models = model.JSON(encoded)
	return desktopProvider(item, names), nil
}

func (a *App) RemoveProviderModel(id int64, name string) (DesktopProvider, error) {
	if err := a.ready(); err != nil {
		return DesktopProvider{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProvider{}, fmt.Errorf("model name is required")
	}
	item, err := a.store.GetProvider(a.ctx, id)
	if err != nil {
		return DesktopProvider{}, err
	}
	var names []string
	_ = json.Unmarshal(item.Models, &names)
	filtered := make([]string, 0, len(names))
	found := false
	for _, existing := range names {
		if existing == name {
			found = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !found {
		return DesktopProvider{}, fmt.Errorf("model %q was not found", name)
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return DesktopProvider{}, err
	}
	if err := a.store.UpdateProvider(a.ctx, id, model.UpdateProviderReq{Models: model.JSON(encoded)}); err != nil {
		return DesktopProvider{}, err
	}
	item.Models = model.JSON(encoded)
	return desktopProvider(item, filtered), nil
}

func desktopProvider(item *model.Provider, models []string) DesktopProvider {
	return DesktopProvider{ID: item.ID, Name: item.Name, Type: string(item.Type), BaseURL: item.BaseURL, Models: models}
}

func (a *App) Chat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatResult, error) {
	if err := a.ready(); err != nil {
		return ChatResult{}, err
	}
	return a.runChat(profile, threadID, input, attachmentUUIDs)
}

func (a *App) Retry(profile ChatProfile, threadID string) (ChatResult, error) {
	if err := a.ready(); err != nil {
		return ChatResult{}, err
	}
	if strings.TrimSpace(threadID) == "" {
		return ChatResult{}, fmt.Errorf("choose a conversation to retry")
	}
	thread, err := a.store.GetThreadByUUID(a.ctx, threadID, false)
	if err != nil {
		return ChatResult{}, err
	}
	input, attachments, err := a.store.RewindLastTurnWithAttachments(a.ctx, thread.ID)
	if err != nil {
		return ChatResult{}, err
	}
	return a.runChat(profile, threadID, input, attachments)
}

func (a *App) runChat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatResult, error) {
	input = strings.TrimSpace(input)
	attachmentUUIDs = normalizeAttachmentIDs(attachmentUUIDs)
	if len(attachmentUUIDs) > maxDesktopAttachments {
		return ChatResult{}, fmt.Errorf("一次最多添加 %d 个附件", maxDesktopAttachments)
	}
	if input == "" && len(attachmentUUIDs) == 0 {
		return ChatResult{}, fmt.Errorf("message cannot be empty")
	}
	if input == "" {
		input = "请分析这些附件。"
	}
	if profile.ProviderID == 0 || strings.TrimSpace(profile.ModelName) == "" {
		return ChatResult{}, fmt.Errorf("choose a provider and model")
	}
	searchID, err := a.selectedSearchEngine(profile.SearchEnabled)
	if err != nil {
		return ChatResult{}, err
	}
	var thread *model.Thread
	if threadID == "" {
		projectUUID := strings.TrimSpace(profile.ProjectUUID)
		if err := a.validateProject(projectUUID); err != nil {
			return ChatResult{}, err
		}
		thread = &model.Thread{UserID: "local", ProjectUUID: projectUUID, ProviderID: profile.ProviderID, ModelName: strings.TrimSpace(profile.ModelName), SearchEngineID: searchID, Title: input}
		if err := a.store.CreateThread(a.ctx, thread); err != nil {
			return ChatResult{}, err
		}
		threadID = thread.UUID
	} else {
		if err := a.store.UpdateThreadProfile(a.ctx, threadID, profile.ProviderID, strings.TrimSpace(profile.ModelName), searchID); err != nil {
			return ChatResult{}, err
		}
		thread, err = a.store.GetThreadByUUID(a.ctx, threadID, false)
		if err != nil {
			return ChatResult{}, err
		}
	}
	if err := a.store.LinkFilesToThread(a.ctx, thread.ID, attachmentUUIDs); err != nil {
		return ChatResult{}, err
	}
	var answer strings.Builder
	runCtx := memorypkg.WithTurnPolicy(a.ctx, memorypkg.TurnPolicy{
		UseMemories: profile.MemoryUseEnabled, GenerateMemories: profile.MemoryGenerateEnabled,
	})
	err = a.server.Handle(runCtx, protocol.Command{Kind: protocol.CommandStartTurn, ThreadID: threadID, Input: input, Attachments: attachmentUUIDs}, func(e protocol.Event) error {
		if e.Kind == protocol.EventAssistantDelta {
			answer.WriteString(e.Delta)
			if profile.RequestID != "" {
				runtime.EventsEmit(a.ctx, "chat:delta", ChatDelta{RequestID: profile.RequestID, ThreadID: threadID, Delta: e.Delta})
			}
		} else if profile.RequestID != "" {
			runtime.EventsEmit(a.ctx, "chat:progress", ChatProgress{
				RequestID: profile.RequestID, ThreadID: threadID, TurnID: e.TurnID, Kind: string(e.Kind),
				CallID: e.CallID, Name: e.Name, Status: e.Status, Message: e.Message, Error: e.Error,
			})
		}
		return nil
	})
	result := ChatResult{ThreadID: threadID, Content: answer.String()}
	if err != nil {
		// Wails rejects a Promise when a bound Go method returns an error and
		// discards every other return value. Preserve the newly-created thread ID
		// so the frontend can retry a failed first turn instead of rendering a
		// retry button whose click is ignored because it has no conversation ID.
		result.Error = err.Error()
		return result, nil
	}
	return result, nil
}

func (a *App) ArchiveThread(threadUUID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	thread, err := a.store.GetThreadByUUID(a.ctx, threadUUID, false)
	if err != nil {
		return err
	}
	return a.store.ArchiveThread(a.ctx, thread.ID)
}

// MoveThreadToProject changes only the conversation grouping. An empty
// project UUID deliberately makes the conversation independent of any project.
func (a *App) MoveThreadToProject(threadUUID, projectUUID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	threadUUID, projectUUID = strings.TrimSpace(threadUUID), strings.TrimSpace(projectUUID)
	thread, err := a.store.GetThreadByUUID(a.ctx, threadUUID, false)
	if err != nil {
		return err
	}
	if thread.UserID != "local" {
		return fmt.Errorf("conversation does not belong to the local user")
	}
	if err := a.validateProject(projectUUID); err != nil {
		return err
	}
	return a.store.UpdateThreadProject(a.ctx, threadUUID, projectUUID)
}

func (a *App) validateProject(projectUUID string) error {
	if projectUUID == "" {
		return nil
	}
	projects, err := a.store.ListProjects(a.ctx, "local")
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.UUID == projectUUID {
			return nil
		}
	}
	return fmt.Errorf("project %q was not found", projectUUID)
}

func (a *App) DeleteProject(projectUUID string) error {
	if err := a.ready(); err != nil {
		return err
	}
	projectUUID = strings.TrimSpace(projectUUID)
	if projectUUID == "" {
		return fmt.Errorf("project ID is required")
	}
	if err := a.validateProject(projectUUID); err != nil {
		return err
	}
	if err := a.store.DeleteProject(a.ctx, projectUUID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

func (a *App) ChoosePluginDirectory() (DesktopPlugin, error) {
	if err := a.ready(); err != nil {
		return DesktopPlugin{}, err
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 AIClaw 插件目录"})
	if err != nil || path == "" {
		return DesktopPlugin{}, err
	}
	return a.installPlugin(path)
}

func (a *App) selectedSearchEngine(enabled bool) (int64, error) {
	if !enabled {
		return 0, nil
	}
	items, _, err := a.store.ListSearchEngineConfigs(a.ctx, model.ListQuery{Page: 1, PageSize: 100})
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if item.Enabled {
			return item.ID, nil
		}
	}
	return 0, nil
}
func (a *App) ready() error {
	if a.err != "" {
		return fmt.Errorf("desktop initialization: %s", a.err)
	}
	if a.store == nil {
		return fmt.Errorf("desktop is starting")
	}
	return nil
}
