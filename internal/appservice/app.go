package appservice

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"log"

	"github.com/chowyu12/aiclaw/internal/appserver"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/core"
	memorypkg "github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/bundled"
	"github.com/chowyu12/aiclaw/internal/plugins/computeruse"
	"github.com/chowyu12/aiclaw/internal/plugins/wechat"
	"github.com/chowyu12/aiclaw/internal/plugins/wecom"
	"github.com/chowyu12/aiclaw/internal/protocol"
	providerpkg "github.com/chowyu12/aiclaw/internal/provider"
	"github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
)

type Service struct {
	ctx    context.Context
	store  *gormstore.GormStore
	server *appserver.Service
	tools  *core.LocalToolDispatcher
	memory *memorypkg.Service
	// installer owns plugin bundle installation, validation and removal.
	installer *pluginpkg.Installer
	// rootOverride lets a host place the data directory somewhere other than
	// the user's home, which is what the tests rely on.
	rootOverride string
	// dialogs are the host's native pickers.
	dialogs Dialogs
	// host supervises the long-running channels enabled plugins contribute.
	host *pluginpkg.Host
	root string
	err  string

	runMu     sync.Mutex
	runs      map[string]*backgroundChatState
	runWG     sync.WaitGroup
	bgContext context.Context
	bgCancel  context.CancelFunc
	emit      func(string, ...any)
}

type backgroundChatState struct {
	BackgroundChat
	cancel context.CancelFunc
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

type ChatStartResult struct {
	RequestID string `json:"request_id"`
	ThreadID  string `json:"thread_id"`
}

type BackgroundChat struct {
	RequestID string `json:"request_id"`
	ThreadID  string `json:"thread_id"`
	StartedAt string `json:"started_at"`
}

type ChatFinished struct {
	RequestID string `json:"request_id"`
	ThreadID  string `json:"thread_id"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
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
	RequestID  string `json:"request_id"`
	ThreadID   string `json:"thread_id"`
	TurnID     string `json:"turn_id"`
	Kind       string `json:"kind"`
	CallID     string `json:"call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status,omitempty"`
	Message    string `json:"message,omitempty"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
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
	Files       []DesktopOutputFile `json:"files,omitempty"`
	Execution   []DesktopExecution  `json:"execution,omitempty"`
}

type DesktopExecution struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

func (s *Service) startup(ctx context.Context) {
	s.ctx = ctx
	s.bgContext, s.bgCancel = context.WithCancel(ctx)
	s.runs = make(map[string]*backgroundChatState)
	if s.emit == nil {
		s.emit = func(string, ...any) {}
	}
	if s.dialogs == nil {
		s.dialogs = NoDialogs{}
	}
	root := s.rootOverride
	if root == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			s.err = homeErr.Error()
			return
		}
		root = filepath.Join(home, ".aiclaw")
	}
	s.root = root
	var err error
	cfg, err := config.Load(filepath.Join(root, "config.yaml"))
	if err != nil {
		s.err = err.Error()
		return
	}
	cfg.Workspace, cfg.Database.Driver, cfg.Database.DSN = root, "sqlite", filepath.Join(root, "aiclaw.db")
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.err = err.Error()
		return
	}
	s.store, err = gormstore.New(cfg.Database)
	if err != nil {
		s.err = err.Error()
		return
	}
	s.store.InitFTS5()
	if err := s.store.MigrateLegacyConversations(ctx, "local"); err != nil {
		s.err = err.Error()
		return
	}
	if err := skills.EnsureBuiltins(ctx, s.store, root); err != nil {
		s.err = err.Error()
		return
	}
	sampler := core.ProviderSampler{Resolver: s.store}
	s.installer = pluginpkg.NewInstaller(s.store, root)
	if err := s.installer.EnsureBuiltins(ctx, bundled.FS()); err != nil {
		s.err = err.Error()
		return
	}
	pluginRuntime, err := pluginpkg.NewRuntime(map[string]pluginpkg.ToolProvider{
		computeruse.ProviderName: computeruse.New(root),
	})
	if err != nil {
		s.err = err.Error()
		return
	}
	s.tools = core.NewLocalToolDispatcher(s.store,
		core.WithDispatcherRoot(root), core.WithSubAgentSampler(sampler),
		core.WithPluginRuntime(pluginRuntime))
	s.memory = memorypkg.NewService(s.store)
	s.server = appserver.New(s.store, sampler, s.tools)
	gateway := appserver.NewChannelGateway(s.server, s.store)
	s.host, err = pluginpkg.NewHost(gateway, pluginpkg.NewConfigService(s.store),
		map[string]pluginpkg.ChannelFactory{
			wecom.ProviderName:  wecom.New,
			wechat.ProviderName: wechat.New,
		},
		pluginpkg.WithHostLogger(func(format string, args ...any) {
			log.Printf("[plugin] "+format, args...)
		}))
	if err != nil {
		s.err = err.Error()
		return
	}
	if err := s.syncChannels(); err != nil {
		s.err = err.Error()
		return
	}
	s.cleanupPendingAttachments()
}

// syncChannels brings the running channels in line with the enabled plugins.
func (s *Service) syncChannels() error {
	if s.host == nil {
		return nil
	}
	plugins, err := s.store.ListPlugins(s.ctx)
	if err != nil {
		return err
	}
	return s.host.Sync(s.bgContext, plugins)
}

// ChannelStatus reports each supervised channel for the settings page.
func (s *Service) ChannelStatus() []pluginpkg.ChannelStatus {
	if s.host == nil {
		return nil
	}
	return s.host.Status()
}

func (s *Service) shutdown(context.Context) {
	if s.host != nil {
		s.host.Stop()
	}
	if s.bgCancel != nil {
		s.bgCancel()
	}
	s.runMu.Lock()
	for _, run := range s.runs {
		run.cancel()
	}
	s.runMu.Unlock()
	s.runWG.Wait()
	if s.store != nil {
		_ = s.store.Close()
	}
}
func (s *Service) Status() string { return s.err }
func (s *Service) Providers() ([]DesktopProvider, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, _, err := s.store.ListProviders(s.ctx, model.ListQuery{Page: 1, PageSize: 1000})
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

func (s *Service) Threads() ([]DesktopThread, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, _, err := s.store.ListThreads(s.ctx, "local", false, 1, 100)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopThread, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopThread{UUID: item.UUID, Title: item.Title, ProjectUUID: item.ProjectUUID, ProviderID: item.ProviderID, ModelName: item.ModelName, UpdatedAt: item.UpdatedAt.Format(time.RFC3339)})
	}
	return result, nil
}

func (s *Service) Projects() ([]DesktopProject, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	items, err := s.store.ListProjects(s.ctx, "local")
	if err != nil {
		return nil, err
	}
	result := make([]DesktopProject, 0, len(items))
	for _, item := range items {
		result = append(result, DesktopProject{UUID: item.UUID, Name: item.Name})
	}
	return result, nil
}

func (s *Service) CreateProject(name string) (DesktopProject, error) {
	if err := s.ready(); err != nil {
		return DesktopProject{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProject{}, fmt.Errorf("project name is required")
	}
	project := &model.Project{UserID: "local", Name: name}
	if err := s.store.CreateProject(s.ctx, project); err != nil {
		return DesktopProject{}, err
	}
	return DesktopProject{UUID: project.UUID, Name: project.Name}, nil
}

func (s *Service) ThreadMessages(threadUUID string) ([]DesktopMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	thread, err := s.store.GetThreadByUUID(s.ctx, threadUUID, false)
	if err != nil {
		return nil, err
	}
	items, err := s.store.LoadRollout(s.ctx, thread.ID)
	if err != nil {
		return nil, err
	}
	result := make([]DesktopMessage, 0)
	executionByTurn := make(map[string][]DesktopExecution)
	filesByTurn := make(map[string][]DesktopOutputFile)
	for _, item := range items {
		if item.Kind == model.RolloutToolRequested {
			var payload struct {
				ToolCalls []core.ToolCall `json:"tool_calls"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			for _, call := range payload.ToolCalls {
				executionByTurn[item.TurnID] = append(executionByTurn[item.TurnID], DesktopExecution{
					ID: call.ID, Name: call.Name, Status: string(model.StepPending), Message: "等待执行工具", Input: call.Arguments,
				})
			}
			continue
		}
		if item.Kind == model.RolloutToolCompleted {
			var payload struct {
				CallID     string           `json:"call_id"`
				Name       string           `json:"name"`
				Status     model.StepStatus `json:"status"`
				Input      string           `json:"input"`
				Output     string           `json:"output"`
				Error      string           `json:"error"`
				DurationMS int64            `json:"duration_ms"`
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
					if payload.Input != "" {
						steps[index].Input = payload.Input
					}
					steps[index].Output, steps[index].Error = payload.Output, payload.Error
					steps[index].DurationMS = payload.DurationMS
					matched = true
					break
				}
			}
			if !matched {
				steps = append(steps, DesktopExecution{
					ID: payload.CallID, Name: payload.Name, Status: string(status), Message: message,
					Input: payload.Input, Output: payload.Output, Error: payload.Error, DurationMS: payload.DurationMS,
				})
			}
			executionByTurn[item.TurnID] = steps
			for _, file := range desktopOutputFiles(payload.Output) {
				filesByTurn[item.TurnID] = appendOutputFile(filesByTurn[item.TurnID], file)
			}
			continue
		}
		if item.Kind == model.RolloutTurnFailed {
			var payload struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(item.Payload, &payload)
			message := strings.TrimSpace(payload.Error)
			if message == "" {
				message = "会话执行失败"
			}
			result = append(result, DesktopMessage{
				Role: "系统", Content: "生成失败：" + message,
				Files:     append([]DesktopOutputFile(nil), filesByTurn[item.TurnID]...),
				Execution: append([]DesktopExecution(nil), executionByTurn[item.TurnID]...),
			})
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
		files := append([]DesktopOutputFile(nil), filesByTurn[item.TurnID]...)
		if item.Kind == model.RolloutAssistantFinal {
			for _, file := range desktopOutputFiles(payload.Content) {
				files = appendOutputFile(files, file)
			}
		}
		result = append(result, DesktopMessage{
			Role: role, Content: payload.Content, Attachments: s.attachmentsByUUIDs(payload.Attachments),
			Files:     files,
			Execution: append([]DesktopExecution(nil), executionByTurn[item.TurnID]...),
		})
	}
	return result, nil
}

func (s *Service) AddProvider(input ProviderInput) (DesktopProvider, error) {
	if err := s.ready(); err != nil {
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
	if err := s.store.CreateProvider(s.ctx, provider); err != nil {
		return DesktopProvider{}, err
	}
	return DesktopProvider{ID: provider.ID, Name: provider.Name, Type: string(provider.Type), BaseURL: provider.BaseURL, Models: modelNames}, nil
}

// SyncProviderModels queries the Provider's model API and returns searchable
// candidates. It does not overwrite the user's local selection; models are
// persisted only after AddProviderModel is called.
func (s *Service) SyncProviderModels(id int64) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	item, err := s.store.GetProvider(s.ctx, id)
	if err != nil {
		return nil, err
	}
	return providerpkg.FetchRemoteModels(s.ctx, item)
}

func (s *Service) AddProviderModel(id int64, name string) (DesktopProvider, error) {
	if err := s.ready(); err != nil {
		return DesktopProvider{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProvider{}, fmt.Errorf("model name is required")
	}
	item, err := s.store.GetProvider(s.ctx, id)
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
	if err := s.store.UpdateProvider(s.ctx, id, model.UpdateProviderReq{Models: model.JSON(encoded)}); err != nil {
		return DesktopProvider{}, err
	}
	item.Models = model.JSON(encoded)
	return desktopProvider(item, names), nil
}

func (s *Service) RemoveProviderModel(id int64, name string) (DesktopProvider, error) {
	if err := s.ready(); err != nil {
		return DesktopProvider{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return DesktopProvider{}, fmt.Errorf("model name is required")
	}
	item, err := s.store.GetProvider(s.ctx, id)
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
	if err := s.store.UpdateProvider(s.ctx, id, model.UpdateProviderReq{Models: model.JSON(encoded)}); err != nil {
		return DesktopProvider{}, err
	}
	item.Models = model.JSON(encoded)
	return desktopProvider(item, filtered), nil
}

func desktopProvider(item *model.Provider, models []string) DesktopProvider {
	return DesktopProvider{ID: item.ID, Name: item.Name, Type: string(item.Type), BaseURL: item.BaseURL, Models: models}
}

func (s *Service) Chat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatResult, error) {
	if err := s.ready(); err != nil {
		return ChatResult{}, err
	}
	return s.runChat(profile, threadID, input, attachmentUUIDs)
}

// StartChat creates or updates the conversation synchronously, then runs the
// turn independently of the currently selected desktop view. Switching
// conversations or opening settings therefore cannot cancel or misroute it.
func (s *Service) StartChat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatStartResult, error) {
	return s.startBackgroundChat(profile, threadID, input, attachmentUUIDs)
}

func (s *Service) StartRetry(profile ChatProfile, threadID string) (ChatStartResult, error) {
	if err := s.ready(); err != nil {
		return ChatStartResult{}, err
	}
	if s.threadRunning(threadID) {
		return ChatStartResult{}, fmt.Errorf("conversation already has a background task")
	}
	thread, err := s.store.GetThreadByUUID(s.ctx, threadID, false)
	if err != nil {
		return ChatStartResult{}, err
	}
	input, attachments, err := s.store.RewindLastTurnWithAttachments(s.ctx, thread.ID)
	if err != nil {
		return ChatStartResult{}, err
	}
	return s.startBackgroundChat(profile, threadID, input, attachments)
}

func (s *Service) BackgroundChats() []BackgroundChat {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	items := make([]BackgroundChat, 0, len(s.runs))
	for _, run := range s.runs {
		items = append(items, run.BackgroundChat)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartedAt < items[j].StartedAt })
	return items
}

func (s *Service) threadRunning(threadID string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	for _, run := range s.runs {
		if run.ThreadID == threadID {
			return true
		}
	}
	return false
}

func (s *Service) startBackgroundChat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatStartResult, error) {
	if err := s.ready(); err != nil {
		return ChatStartResult{}, err
	}
	if threadID != "" && s.threadRunning(threadID) {
		return ChatStartResult{}, fmt.Errorf("conversation already has a background task")
	}
	preparedThreadID, preparedInput, preparedAttachments, err := s.prepareChat(profile, threadID, input, attachmentUUIDs)
	if err != nil {
		return ChatStartResult{}, err
	}
	requestID := strings.TrimSpace(profile.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("chat-%d", time.Now().UnixNano())
		profile.RequestID = requestID
	}
	runContext := s.bgContext
	if runContext == nil {
		runContext = s.ctx
	}
	runContext, cancel := context.WithCancel(runContext)
	state := &backgroundChatState{
		BackgroundChat: BackgroundChat{RequestID: requestID, ThreadID: preparedThreadID, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		cancel:         cancel,
	}
	s.runMu.Lock()
	if s.runs == nil {
		s.runs = make(map[string]*backgroundChatState)
	}
	for _, active := range s.runs {
		if active.ThreadID == preparedThreadID {
			s.runMu.Unlock()
			cancel()
			return ChatStartResult{}, fmt.Errorf("conversation already has a background task")
		}
	}
	s.runs[requestID] = state
	s.runWG.Add(1)
	s.runMu.Unlock()

	go func() {
		defer s.runWG.Done()
		content, runErr := s.executeChat(runContext, profile, preparedThreadID, preparedInput, preparedAttachments)
		finished := ChatFinished{RequestID: requestID, ThreadID: preparedThreadID, Content: content}
		if runErr != nil {
			finished.Error = runErr.Error()
		}
		s.emitDesktopEvent("chat:finished", finished)
		s.runMu.Lock()
		delete(s.runs, requestID)
		s.runMu.Unlock()
		cancel()
	}()

	return ChatStartResult{RequestID: requestID, ThreadID: preparedThreadID}, nil
}

func (s *Service) Retry(profile ChatProfile, threadID string) (ChatResult, error) {
	if err := s.ready(); err != nil {
		return ChatResult{}, err
	}
	if strings.TrimSpace(threadID) == "" {
		return ChatResult{}, fmt.Errorf("choose a conversation to retry")
	}
	thread, err := s.store.GetThreadByUUID(s.ctx, threadID, false)
	if err != nil {
		return ChatResult{}, err
	}
	input, attachments, err := s.store.RewindLastTurnWithAttachments(s.ctx, thread.ID)
	if err != nil {
		return ChatResult{}, err
	}
	return s.runChat(profile, threadID, input, attachments)
}

func (s *Service) runChat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (ChatResult, error) {
	preparedThreadID, preparedInput, preparedAttachments, err := s.prepareChat(profile, threadID, input, attachmentUUIDs)
	if err != nil {
		return ChatResult{}, err
	}
	content, runErr := s.executeChat(s.ctx, profile, preparedThreadID, preparedInput, preparedAttachments)
	result := ChatResult{ThreadID: preparedThreadID, Content: content}
	if runErr != nil {
		result.Error = runErr.Error()
		return result, nil
	}
	return result, nil
}

func (s *Service) prepareChat(profile ChatProfile, threadID, input string, attachmentUUIDs []string) (string, string, []string, error) {
	input = strings.TrimSpace(input)
	attachmentUUIDs = normalizeAttachmentIDs(attachmentUUIDs)
	if len(attachmentUUIDs) > maxDesktopAttachments {
		return "", "", nil, fmt.Errorf("一次最多添加 %d 个附件", maxDesktopAttachments)
	}
	if input == "" && len(attachmentUUIDs) == 0 {
		return "", "", nil, fmt.Errorf("message cannot be empty")
	}
	if input == "" {
		input = "请分析这些附件。"
	}
	if profile.ProviderID == 0 || strings.TrimSpace(profile.ModelName) == "" {
		return "", "", nil, fmt.Errorf("choose a provider and model")
	}
	searchID, err := s.selectedSearchEngine(profile.SearchEnabled)
	if err != nil {
		return "", "", nil, err
	}
	var thread *model.Thread
	if threadID == "" {
		projectUUID := strings.TrimSpace(profile.ProjectUUID)
		if err := s.validateProject(projectUUID); err != nil {
			return "", "", nil, err
		}
		thread = &model.Thread{UserID: "local", ProjectUUID: projectUUID, ProviderID: profile.ProviderID, ModelName: strings.TrimSpace(profile.ModelName), SearchEngineID: searchID, Title: input}
		if err := s.store.CreateThread(s.ctx, thread); err != nil {
			return "", "", nil, err
		}
		threadID = thread.UUID
	} else {
		if err := s.store.UpdateThreadProfile(s.ctx, threadID, profile.ProviderID, strings.TrimSpace(profile.ModelName), searchID); err != nil {
			return "", "", nil, err
		}
		thread, err = s.store.GetThreadByUUID(s.ctx, threadID, false)
		if err != nil {
			return "", "", nil, err
		}
	}
	if err := s.store.LinkFilesToThread(s.ctx, thread.ID, attachmentUUIDs); err != nil {
		return "", "", nil, err
	}
	return threadID, input, attachmentUUIDs, nil
}

func (s *Service) executeChat(ctx context.Context, profile ChatProfile, threadID, input string, attachmentUUIDs []string) (string, error) {
	var answer strings.Builder
	runCtx := memorypkg.WithTurnPolicy(ctx, memorypkg.TurnPolicy{
		UseMemories: profile.MemoryUseEnabled, GenerateMemories: profile.MemoryGenerateEnabled,
	})
	err := s.server.Handle(runCtx, protocol.Command{Kind: protocol.CommandStartTurn, ThreadID: threadID, Input: input, Attachments: attachmentUUIDs}, func(e protocol.Event) error {
		if e.Kind == protocol.EventAssistantDelta {
			answer.WriteString(e.Delta)
			if profile.RequestID != "" {
				s.emitDesktopEvent("chat:delta", ChatDelta{RequestID: profile.RequestID, ThreadID: threadID, Delta: e.Delta})
			}
		} else if profile.RequestID != "" {
			s.emitDesktopEvent("chat:progress", ChatProgress{
				RequestID: profile.RequestID, ThreadID: threadID, TurnID: e.TurnID, Kind: string(e.Kind),
				CallID: e.CallID, Name: e.Name, Status: e.Status, Message: e.Message,
				Input: e.Input, Output: e.Output, Error: e.Error, StartedAt: e.StartedAt, DurationMS: e.DurationMS,
			})
		}
		return nil
	})
	if err != nil {
		return answer.String(), err
	}
	return answer.String(), nil
}

func (s *Service) emitDesktopEvent(name string, data any) {
	if s.emit != nil {
		s.emit(name, data)
	}
}

func (s *Service) ArchiveThread(threadUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.threadRunning(threadUUID) {
		return fmt.Errorf("cannot archive a conversation while its background task is running")
	}
	thread, err := s.store.GetThreadByUUID(s.ctx, threadUUID, false)
	if err != nil {
		return err
	}
	return s.store.ArchiveThread(s.ctx, thread.ID)
}

// MoveThreadToProject changes only the conversation grouping. An empty
// project UUID deliberately makes the conversation independent of any project.
func (s *Service) MoveThreadToProject(threadUUID, projectUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	threadUUID, projectUUID = strings.TrimSpace(threadUUID), strings.TrimSpace(projectUUID)
	thread, err := s.store.GetThreadByUUID(s.ctx, threadUUID, false)
	if err != nil {
		return err
	}
	if thread.UserID != "local" {
		return fmt.Errorf("conversation does not belong to the local user")
	}
	if err := s.validateProject(projectUUID); err != nil {
		return err
	}
	return s.store.UpdateThreadProject(s.ctx, threadUUID, projectUUID)
}

func (s *Service) validateProject(projectUUID string) error {
	if projectUUID == "" {
		return nil
	}
	projects, err := s.store.ListProjects(s.ctx, "local")
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

func (s *Service) DeleteProject(projectUUID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	projectUUID = strings.TrimSpace(projectUUID)
	if projectUUID == "" {
		return fmt.Errorf("project ID is required")
	}
	if err := s.validateProject(projectUUID); err != nil {
		return err
	}
	threads, _, err := s.store.ListThreads(s.ctx, "local", false, 1, 1000)
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if thread.ProjectUUID == projectUUID && s.threadRunning(thread.UUID) {
			return fmt.Errorf("cannot delete a project while one of its conversations is running")
		}
	}
	if err := s.store.DeleteProject(s.ctx, projectUUID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

func (s *Service) ChoosePluginDirectory() (DesktopPlugin, error) {
	if err := s.ready(); err != nil {
		return DesktopPlugin{}, err
	}
	path, err := s.dialogs.PickDirectory(s.ctx, "选择 AIClaw 插件目录")
	if err != nil || path == "" {
		return DesktopPlugin{}, err
	}
	return s.installPlugin(path)
}

func (s *Service) selectedSearchEngine(enabled bool) (int64, error) {
	if !enabled {
		return 0, nil
	}
	items, _, err := s.store.ListSearchEngineConfigs(s.ctx, model.ListQuery{Page: 1, PageSize: 100})
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
func (s *Service) ready() error {
	if s.err != "" {
		return fmt.Errorf("desktop initialization: %s", s.err)
	}
	if s.store == nil {
		return fmt.Errorf("desktop is starting")
	}
	return nil
}
