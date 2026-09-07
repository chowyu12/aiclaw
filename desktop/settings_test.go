package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chowyu12/aiclaw/internal/appserver"
	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/core"
	"github.com/chowyu12/aiclaw/internal/memory"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	toolresult "github.com/chowyu12/aiclaw/internal/tools/result"
)

type retryOnceSampler struct {
	calls int
}

type attachmentCaptureSampler struct {
	requests []core.SamplingRequest
}

type blockingSampler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingSampler) Sample(ctx context.Context, _ core.SamplingRequest, emit func(string) error) (core.SamplingResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return core.SamplingResult{}, ctx.Err()
	}
	if err := emit("background response"); err != nil {
		return core.SamplingResult{}, err
	}
	return core.SamplingResult{Text: "background response"}, nil
}

func (s *attachmentCaptureSampler) Sample(_ context.Context, request core.SamplingRequest, emit func(string) error) (core.SamplingResult, error) {
	s.requests = append(s.requests, request)
	if err := emit("附件已理解"); err != nil {
		return core.SamplingResult{}, err
	}
	return core.SamplingResult{Text: "附件已理解"}, nil
}

func (s *retryOnceSampler) Sample(_ context.Context, _ core.SamplingRequest, emit func(string) error) (core.SamplingResult, error) {
	s.calls++
	if s.calls == 1 {
		return core.SamplingResult{}, fmt.Errorf("temporary provider failure")
	}
	if err := emit("recovered response"); err != nil {
		return core.SamplingResult{}, err
	}
	return core.SamplingResult{Text: "recovered response"}, nil
}

func newTestDesktopApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	store, err := gormstore.New(config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(root, "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{ctx: context.Background(), store: store, tools: core.NewLocalToolDispatcher(store), memory: memory.NewService(store), root: root}
}

func TestFailedFirstChatKeepsThreadIDAndCanRetry(t *testing.T) {
	app := newTestDesktopApp(t)
	sampler := &retryOnceSampler{}
	app.server = appserver.New(app.store, sampler, app.tools)
	profile := ChatProfile{ProviderID: 1, ModelName: "test-model"}

	failed, err := app.Chat(profile, "", "please retry this", nil)
	if err != nil {
		t.Fatalf("chat failure should be returned as a retryable result: %v", err)
	}
	if failed.ThreadID == "" || failed.Error == "" {
		t.Fatalf("failed chat lost retry state: %+v", failed)
	}

	retried, err := app.Retry(profile, failed.ThreadID)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if retried.Error != "" || retried.ThreadID != failed.ThreadID || retried.Content != "recovered response" {
		t.Fatalf("unexpected retry result: %+v", retried)
	}
}

func TestBackgroundChatSurvivesConversationSwitch(t *testing.T) {
	app := newTestDesktopApp(t)
	sampler := &blockingSampler{started: make(chan struct{}), release: make(chan struct{})}
	app.server = appserver.New(app.store, sampler, app.tools)
	profile := ChatProfile{ProviderID: 1, ModelName: "background-model", RequestID: "request-background"}

	started, err := app.StartChat(profile, "", "keep running", nil)
	if err != nil || started.ThreadID == "" || started.RequestID != profile.RequestID {
		t.Fatalf("start background chat: result=%+v err=%v", started, err)
	}
	select {
	case <-sampler.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background sampler did not start")
	}

	other := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "other-model", Title: "other"}
	if err := app.store.CreateThread(app.ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ThreadMessages(other.UUID); err != nil {
		t.Fatalf("switching to another conversation failed: %v", err)
	}
	active := app.BackgroundChats()
	if len(active) != 1 || active[0].ThreadID != started.ThreadID {
		t.Fatalf("background task disappeared after switch: %+v", active)
	}
	if _, err := app.StartChat(profile, started.ThreadID, "overlap", nil); err == nil {
		t.Fatal("same conversation accepted an overlapping background task")
	}
	if err := app.ArchiveThread(started.ThreadID); err == nil {
		t.Fatal("running conversation was archived")
	}

	close(sampler.release)
	deadline := time.Now().Add(3 * time.Second)
	for len(app.BackgroundChats()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(app.BackgroundChats()) != 0 {
		t.Fatal("background task did not finish")
	}
	messages, err := app.ThreadMessages(started.ThreadID)
	if err != nil || len(messages) != 2 || messages[1].Content != "background response" {
		t.Fatalf("background result was not persisted: messages=%+v err=%v", messages, err)
	}
}

func TestDesktopAttachmentsPersistRenderAndRetry(t *testing.T) {
	app := newTestDesktopApp(t)
	source := t.TempDir()
	textPath := filepath.Join(source, "notes.md")
	imagePath := filepath.Join(source, "pixel.png")
	mustWrite(t, textPath, "# Evidence\nlocal attachment content")
	if err := os.WriteFile(imagePath, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	attachments, err := app.ImportAttachments([]string{textPath, imagePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 || attachments[0].FileType != "text" || attachments[1].FileType != "image" || attachments[1].PreviewURL == "" {
		t.Fatalf("unexpected imported attachments: %+v", attachments)
	}

	sampler := &attachmentCaptureSampler{}
	app.server = appserver.New(app.store, sampler, app.tools)
	profile := ChatProfile{ProviderID: 1, ModelName: "vision-model"}
	result, err := app.Chat(profile, "", "比较文件和图片", []string{attachments[0].UUID, attachments[1].UUID})
	if err != nil || result.Error != "" || result.ThreadID == "" {
		t.Fatalf("attachment chat failed: result=%+v err=%v", result, err)
	}
	if len(sampler.requests) != 1 {
		t.Fatalf("sampler calls = %d", len(sampler.requests))
	}
	last := sampler.requests[0].Messages[len(sampler.requests[0].Messages)-1]
	if len(last.Attachments) != 2 || last.Attachments[0].TextContent == "" {
		t.Fatalf("sampling request lost attachments: %+v", last.Attachments)
	}
	thread, err := app.store.GetThreadByUUID(app.ctx, result.ThreadID, false)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := app.store.ListFilesByThread(app.ctx, thread.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("thread attachment persistence failed: files=%+v err=%v", stored, err)
	}
	messages, err := app.ThreadMessages(result.ThreadID)
	if err != nil || len(messages) != 2 || len(messages[0].Attachments) != 2 || messages[0].Attachments[1].PreviewURL == "" {
		t.Fatalf("thread history lost previews: messages=%+v err=%v", messages, err)
	}
	if err := app.DiscardAttachment(attachments[0].UUID); err == nil {
		t.Fatal("sent attachment could be removed from conversation history")
	}

	retried, err := app.Retry(profile, result.ThreadID)
	if err != nil || retried.Error != "" || retried.Content != "附件已理解" {
		t.Fatalf("attachment retry failed: result=%+v err=%v", retried, err)
	}
	if len(sampler.requests) != 2 {
		t.Fatalf("retry sampler calls = %d", len(sampler.requests))
	}
	retryMessage := sampler.requests[1].Messages[len(sampler.requests[1].Messages)-1]
	if len(retryMessage.Attachments) != 2 {
		t.Fatalf("retry lost attachments: %+v", retryMessage)
	}
}

func TestDesktopAttachmentImportRollsBackPartialBatch(t *testing.T) {
	app := newTestDesktopApp(t)
	source := t.TempDir()
	valid := filepath.Join(source, "valid.txt")
	unsupported := filepath.Join(source, "archive.bin")
	mustWrite(t, valid, "valid")
	if err := os.WriteFile(unsupported, []byte{0, 1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportAttachments([]string{valid, unsupported}); err == nil {
		t.Fatal("unsupported batch unexpectedly succeeded")
	}
	pending, err := app.store.ListPendingFilesBefore(app.ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("partial batch left staged files: %+v", pending)
	}
}

func TestPluginInstallAndToggle(t *testing.T) {
	app := newTestDesktopApp(t)
	source := t.TempDir()
	mustMkdir(t, filepath.Join(source, ".codex-plugin"))
	mustWrite(t, filepath.Join(source, ".codex-plugin", "plugin.json"), `{"name":"Research Kit","description":"Local research plugin","version":"1.2.0"}`)
	mustMkdir(t, filepath.Join(source, "skills", "research"))
	mustWrite(t, filepath.Join(source, "skills", "research", "manifest.json"), `{"name":"Research","version":"1.0.0","main":"main.py","permissions":["process.execute"],"tools":[{"name":"research_echo","description":"Echo research input","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}]}`)
	mustWrite(t, filepath.Join(source, "skills", "research", "SKILL.md"), "---\nname: Research\ndescription: Research carefully\n---\nAlways cite local evidence.")
	mustWrite(t, filepath.Join(source, "skills", "research", "main.py"), "import json,sys\ndata=json.load(sys.stdin)\nprint(data['arguments']['query'])\n")
	mustWrite(t, filepath.Join(source, "mcp.json"), `{"mcpServers":{"echo":{"command":"/bin/echo","args":["ready"]}}}`)

	plugin, err := app.installPlugin(source)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.SkillCount != 1 || plugin.MCPCount != 1 || plugin.Enabled {
		t.Fatalf("unexpected plugin: %+v", plugin)
	}
	if err := app.TogglePlugin(plugin.UUID, true); err != nil {
		t.Fatal(err)
	}
	contextMessages, err := app.tools.ContextMessages(app.ctx, model.Thread{})
	if err != nil {
		t.Fatal(err)
	}
	if len(contextMessages) != 1 || contextMessages[0].Content == "" {
		t.Fatalf("skill was not injected: %+v", contextMessages)
	}
	definitions, err := app.tools.Definitions(app.ctx, model.Thread{})
	if err != nil {
		t.Fatal(err)
	}
	foundTool := false
	for _, definition := range definitions {
		if definition.Name == "research_echo" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatal("skill tool definition was not loaded")
	}
	toolResult, err := app.tools.Execute(app.ctx, model.Thread{}, core.ToolCall{Name: "research_echo", Arguments: `{"query":"verified"}`})
	if err != nil || toolResult.Content != "verified" {
		t.Fatalf("skill tool execution failed: result=%+v err=%v", toolResult, err)
	}
	if err := app.TogglePlugin(plugin.UUID, false); err != nil {
		t.Fatal(err)
	}
	skills, err := app.store.ListSkills(app.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Enabled {
		t.Fatalf("skill toggle not persisted: %+v", skills)
	}
	servers, err := app.store.ListMCPServers(app.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Enabled {
		t.Fatalf("MCP toggle not persisted: %+v", servers)
	}
	plugins, err := app.Plugins()
	if err != nil || len(plugins) != 1 || len(plugins[0].Permissions) != 1 || plugins[0].Permissions[0] != "process.execute" {
		t.Fatalf("plugin permissions were not exposed: plugins=%+v err=%v", plugins, err)
	}
	installed, err := app.store.ListPlugins(app.ctx)
	if err != nil || len(installed) != 1 {
		t.Fatalf("installed plugin lookup failed: %+v err=%v", installed, err)
	}
	installDir := installed[0].InstallDir
	if err := app.DeletePlugin(plugin.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installDir); !os.IsNotExist(err) {
		t.Fatalf("plugin files still exist after deletion: %v", err)
	}
	remainingSkills, _ := app.store.ListSkills(app.ctx)
	remainingServers, _ := app.store.ListMCPServers(app.ctx)
	remainingPlugins, _ := app.store.ListPlugins(app.ctx)
	if len(remainingSkills) != 0 || len(remainingServers) != 0 || len(remainingPlugins) != 0 {
		t.Fatalf("plugin records survived deletion: skills=%d mcp=%d plugins=%d", len(remainingSkills), len(remainingServers), len(remainingPlugins))
	}
}

func TestPluginInstallRollsBackInvalidExecutableSkill(t *testing.T) {
	app := newTestDesktopApp(t)
	source := t.TempDir()
	mustMkdir(t, filepath.Join(source, ".codex-plugin"))
	mustWrite(t, filepath.Join(source, ".codex-plugin", "plugin.json"), `{"name":"Broken Plugin"}`)
	mustMkdir(t, filepath.Join(source, "skills", "broken"))
	mustWrite(t, filepath.Join(source, "skills", "broken", "manifest.json"), `{"name":"Broken","main":"main.py","tools":[{"name":"broken_tool"}]}`)
	mustWrite(t, filepath.Join(source, "skills", "broken", "main.py"), "print('no permission')")
	if _, err := app.installPlugin(source); err == nil {
		t.Fatal("invalid executable skill was installed")
	}
	plugins, _ := app.store.ListPlugins(app.ctx)
	skillItems, _ := app.store.ListSkills(app.ctx)
	if len(plugins) != 0 || len(skillItems) != 0 {
		t.Fatalf("failed install left database records: plugins=%d skills=%d", len(plugins), len(skillItems))
	}
}

func TestPluginURLMCPUsesStreamableHTTP(t *testing.T) {
	app := newTestDesktopApp(t)
	source := t.TempDir()
	mustWrite(t, filepath.Join(source, "plugin.json"), `{"name":"Remote MCP"}`)
	mustWrite(t, filepath.Join(source, "mcp.json"), `{"mcpServers":{"remote":{"url":"https://example.invalid/mcp"}}}`)
	if _, err := app.installPlugin(source); err != nil {
		t.Fatal(err)
	}
	servers, err := app.store.ListMCPServers(app.ctx)
	if err != nil || len(servers) != 1 || servers[0].Transport != model.MCPTransportStreamableHTTP || servers[0].Enabled {
		t.Fatalf("URL MCP transport = %+v, err=%v", servers, err)
	}
	plugins, err := app.Plugins()
	if err != nil || len(plugins) != 1 || len(plugins[0].Permissions) != 1 || plugins[0].Permissions[0] != "network.access" {
		t.Fatalf("remote MCP permission was not inferred: plugins=%+v err=%v", plugins, err)
	}
}

func TestAddMCPRejectsInvalidJSONAndTransport(t *testing.T) {
	app := newTestDesktopApp(t)
	if _, err := app.AddMCPServer(MCPServerInput{Name: "bad", Transport: "unknown", Endpoint: "x"}); err == nil {
		t.Fatal("unsupported MCP transport was accepted")
	}
	if _, err := app.AddMCPServer(MCPServerInput{Name: "bad", Transport: "stdio", Endpoint: "/bin/echo", Args: "["}); err == nil {
		t.Fatal("invalid MCP JSON was accepted")
	}
	servers, _ := app.store.ListMCPServers(app.ctx)
	if len(servers) != 0 {
		t.Fatalf("invalid MCP input was persisted: %+v", servers)
	}
}

func TestProjectLifecycleArchivesThreads(t *testing.T) {
	app := newTestDesktopApp(t)
	project, err := app.CreateProject("Workspace")
	if err != nil {
		t.Fatal(err)
	}
	if project.UUID == "" {
		t.Fatal("missing project UUID")
	}
	thread := &model.Thread{UserID: "local", ProjectUUID: project.UUID, ProviderID: 1, ModelName: "model-a", Title: "Thread"}
	if err := app.store.CreateThread(app.ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateThreadProfile(app.ctx, thread.UUID, 2, "model-b", 3); err != nil {
		t.Fatal(err)
	}
	updated, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderID != 2 || updated.ModelName != "model-b" || updated.SearchEngineID != 3 {
		t.Fatalf("thread profile did not update: %+v", updated)
	}
	if err := app.DeleteProject(project.UUID); err != nil {
		t.Fatal(err)
	}
	projects, err := app.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("project still present: %+v", projects)
	}
	archived, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != model.ThreadStatusArchived {
		t.Fatalf("thread was not archived: %+v", archived)
	}
	if archived.ProjectUUID != "" {
		t.Fatalf("archived thread still references deleted project: %+v", archived)
	}
}

func TestDeleteEmptyProject(t *testing.T) {
	app := newTestDesktopApp(t)
	project, err := app.CreateProject("Disposable workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteProject(project.UUID); err != nil {
		t.Fatalf("delete empty project: %v", err)
	}
	projects, err := app.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("empty project still present: %+v", projects)
	}
}

func TestDeleteProjectWithAlreadyArchivedThreads(t *testing.T) {
	app := newTestDesktopApp(t)
	project, err := app.CreateProject("Archived workspace")
	if err != nil {
		t.Fatal(err)
	}
	thread := &model.Thread{
		UserID:      "local",
		ProjectUUID: project.UUID,
		ProviderID:  1,
		ModelName:   "model-a",
		Title:       "Archived conversation",
		Status:      model.ThreadStatusArchived,
	}
	if err := app.store.CreateThread(app.ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteProject(project.UUID); err != nil {
		t.Fatalf("delete project with archived conversation: %v", err)
	}
	archived, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ProjectUUID != "" || archived.Status != model.ThreadStatusArchived {
		t.Fatalf("archived conversation was not detached: %+v", archived)
	}
}

func TestDeleteProjectRejectsMissingID(t *testing.T) {
	app := newTestDesktopApp(t)
	if err := app.DeleteProject("  "); err == nil {
		t.Fatal("expected empty project ID to be rejected")
	}
	if err := app.DeleteProject("missing-project"); err == nil {
		t.Fatal("expected missing project to be rejected")
	}
}

func TestConversationCanMoveInAndOutOfProject(t *testing.T) {
	app := newTestDesktopApp(t)
	project, err := app.CreateProject("Optional workspace")
	if err != nil {
		t.Fatal(err)
	}
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "model-a", Title: "Independent conversation"}
	if err := app.store.CreateThread(app.ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := app.MoveThreadToProject(thread.UUID, project.UUID); err != nil {
		t.Fatal(err)
	}
	grouped, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, false)
	if err != nil || grouped.ProjectUUID != project.UUID {
		t.Fatalf("conversation was not assigned to project: thread=%+v err=%v", grouped, err)
	}
	if err := app.MoveThreadToProject(thread.UUID, ""); err != nil {
		t.Fatal(err)
	}
	independent, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, false)
	if err != nil || independent.ProjectUUID != "" {
		t.Fatalf("conversation was not removed from project: thread=%+v err=%v", independent, err)
	}
	if err := app.MoveThreadToProject(thread.UUID, "missing-project"); err == nil {
		t.Fatal("conversation was assigned to a missing project")
	}
}

func TestProviderModelSyncSearchSourceAndAdd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing API key", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta-model"},{"id":"alpha-model"}]}`))
	}))
	defer server.Close()

	app := newTestDesktopApp(t)
	item, err := app.AddProvider(ProviderInput{Name: "Local API", Type: "openai-compatible", BaseURL: server.URL, APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Models) != 0 {
		t.Fatalf("new provider unexpectedly requires a model: %+v", item)
	}
	candidates, err := app.SyncProviderModels(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0] != "alpha-model" || candidates[1] != "zeta-model" {
		t.Fatalf("unexpected synchronized models: %#v", candidates)
	}
	updated, err := app.AddProviderModel(item.ID, "alpha-model")
	if err != nil {
		t.Fatal(err)
	}
	updated, err = app.AddProviderModel(item.ID, "alpha-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Models) != 1 || updated.Models[0] != "alpha-model" {
		t.Fatalf("model add was not persisted or deduplicated: %+v", updated)
	}
	updated, err = app.AddProviderModel(item.ID, "unused-model")
	if err != nil {
		t.Fatal(err)
	}
	updated, err = app.RemoveProviderModel(item.ID, "unused-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Models) != 1 || updated.Models[0] != "alpha-model" {
		t.Fatalf("unused model was not deleted: %+v", updated)
	}
	thread := &model.Thread{UserID: "local", ProviderID: item.ID, ModelName: "alpha-model", Title: "kept conversation"}
	if err := app.store.CreateThread(app.ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteProvider(item.ID); err == nil {
		t.Fatal("provider used by a conversation was deleted")
	}
	updated, err = app.RemoveProviderModel(item.ID, "alpha-model")
	if err != nil {
		t.Fatalf("model configuration should remain removable when referenced by history: %v", err)
	}
	if len(updated.Models) != 0 {
		t.Fatalf("model configuration was not deleted: %+v", updated)
	}
	kept, err := app.store.GetThreadByUUID(app.ctx, thread.UUID, false)
	if err != nil {
		t.Fatal(err)
	}
	if kept.ModelName != "alpha-model" || kept.ProviderID != item.ID {
		t.Fatalf("deleting a model configuration changed conversation history: %+v", kept)
	}
}

func TestDesktopMemorySettingsAndReview(t *testing.T) {
	app := newTestDesktopApp(t)
	settings, err := app.MemorySettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.UseMemories || !settings.GenerateMemories {
		t.Fatalf("unexpected memory defaults: %+v", settings)
	}
	if err := app.SetMemorySettings(false, true); err != nil {
		t.Fatal(err)
	}
	settings, err = app.MemorySettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.UseMemories || !settings.GenerateMemories {
		t.Fatalf("memory settings were not persisted: %+v", settings)
	}
	item, err := app.memory.Upsert(app.ctx, memory.ExecutionContext{UserID: "local", AgentUUID: "aiclaw-desktop"}, model.CreateMemoryRequest{
		Scope: model.MemoryScopeUser, Kind: model.MemoryKindPreference, MemoryKey: "format",
		Content: "Use concise answers.", Status: model.MemoryStatusCandidate,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ApproveMemory(item.UUID); err != nil {
		t.Fatal(err)
	}
	items, err := app.Memories()
	if err != nil || len(items) != 1 || items[0].Status != string(model.MemoryStatusActive) {
		t.Fatalf("approved memory missing: items=%+v err=%v", items, err)
	}
	if err := app.ForgetMemory(item.UUID); err != nil {
		t.Fatal(err)
	}
	items, err = app.Memories()
	if err != nil || len(items) != 0 {
		t.Fatalf("forgotten memory is still visible: items=%+v err=%v", items, err)
	}
}

func TestLastModelSelectionPersistsAndRejectsStaleModels(t *testing.T) {
	app := newTestDesktopApp(t)
	provider, err := app.AddProvider(ProviderInput{
		Name: "Local", Type: string(model.ProviderOpenAICompat), BaseURL: "http://localhost/v1", Model: "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	selection, err := app.LastModelSelection()
	if err != nil || selection.ProviderID != 0 || selection.ModelName != "" {
		t.Fatalf("unexpected empty selection: selection=%+v err=%v", selection, err)
	}
	if err := app.SetLastModelSelection(provider.ID, "model-a"); err != nil {
		t.Fatal(err)
	}
	selection, err = app.LastModelSelection()
	if err != nil || selection.ProviderID != provider.ID || selection.ModelName != "model-a" {
		t.Fatalf("selection was not restored: selection=%+v err=%v", selection, err)
	}
	if err := app.SetLastModelSelection(provider.ID, "missing-model"); err == nil {
		t.Fatal("unconfigured model was accepted as the last selection")
	}
	if _, err := app.RemoveProviderModel(provider.ID, "model-a"); err != nil {
		t.Fatal(err)
	}
	selection, err = app.LastModelSelection()
	if err != nil || selection.ProviderID != 0 || selection.ModelName != "" {
		t.Fatalf("stale model selection was not ignored: selection=%+v err=%v", selection, err)
	}
}

func TestThreadMessagesRestoresToolExecutionTrace(t *testing.T) {
	app := newTestDesktopApp(t)
	generated := filepath.Join(t.TempDir(), "report.xlsx")
	mustWrite(t, generated, "generated workbook")
	thread := &model.Thread{UserID: "local", ProviderID: 1, ModelName: "test", Title: "trace"}
	if err := app.store.CreateThread(app.ctx, thread); err != nil {
		t.Fatal(err)
	}
	encode := func(value any) model.JSON {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return model.JSON(data)
	}
	_, err := app.store.AppendRollout(app.ctx, thread.ID, []model.RolloutItem{
		{TurnID: "turn-trace", Kind: model.RolloutUserMessage, ModelVisible: true, Payload: encode(map[string]any{"content": "查找资料"})},
		{TurnID: "turn-trace", Kind: model.RolloutToolRequested, ModelVisible: true, Payload: encode(map[string]any{"tool_calls": []core.ToolCall{{ID: "call-search", Name: "exec", Arguments: `{"command":"go test ./...","working_dir":"/workspace"}`}}})},
		{TurnID: "turn-trace", Kind: model.RolloutToolCompleted, ModelVisible: true, Payload: encode(map[string]any{"call_id": "call-search", "name": "exec", "status": model.StepSuccess, "output": toolresult.NewFileResult(generated, toolresult.MimeFromExt(filepath.Ext(generated)), "Generated workbook"), "duration_ms": 1250})},
		{TurnID: "turn-trace", Kind: model.RolloutAssistantFinal, ModelVisible: true, Payload: encode(map[string]any{"content": "文件已生成：`" + generated + "`"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := app.ThreadMessages(thread.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || len(messages[1].Execution) != 1 {
		t.Fatalf("execution history missing: %+v", messages)
	}
	step := messages[1].Execution[0]
	if step.ID != "call-search" || step.Name != "exec" || step.Status != string(model.StepSuccess) {
		t.Fatalf("unexpected restored execution step: %+v", step)
	}
	if step.Input != `{"command":"go test ./...","working_dir":"/workspace"}` || step.DurationMS != 1250 {
		t.Fatalf("execution details were not restored: %+v", step)
	}
	if len(messages[1].Files) != 1 || messages[1].Files[0].Path != generated || !messages[1].Files[0].Available {
		t.Fatalf("generated file was not restored or deduplicated: %+v", messages[1].Files)
	}
	if messages[1].Files[0].ContentType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("generated file MIME = %q", messages[1].Files[0].ContentType)
	}
	if err := app.OpenOutputFile(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("missing generated file was opened")
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
