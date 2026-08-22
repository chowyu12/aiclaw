// Package app provides AIClaw's local, terminal-native application.
//
// It deliberately talks to the Agent executor directly instead of starting the
// HTTP server.  Provider, conversation, MCP and search configuration remain in
// the local SQLite database, just as Codex keeps its local state separate from
// its interaction surface.
package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/appserver"
	"github.com/chowyu12/aiclaw/internal/config"
	corepkg "github.com/chowyu12/aiclaw/internal/core"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/protocol"
	"github.com/chowyu12/aiclaw/internal/runtimeclient"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

const localUserID = "local"

// Options controls a local application instance. Input and Output make the
// command loop embeddable and testable without a terminal.
type Options struct {
	ConfigFlag string
	Input      io.Reader
	Output     io.Writer
}

// Run starts the native AIClaw application. No HTTP listener is created.
func Run(opts Options) error {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}

	cfgPath := config.ConfigPath(opts.ConfigFlag)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := configureLocalDatabase(cfg, cfgPath); err != nil {
		return err
	}
	ws, err := workspace.New(cfg.Workspace)
	if err != nil {
		return fmt.Errorf("initialize workspace: %w", err)
	}
	if cfg.Upload.Dir == "" || cfg.Upload.Dir == "./uploads" {
		cfg.Upload.Dir = ws.Uploads()
	}
	store, err := gormstore.New(cfg.Database)
	if err != nil {
		return fmt.Errorf("open local database: %w", err)
	}
	defer store.Close()
	store.InitFTS5()
	if err := store.MigrateLegacyConversations(context.Background(), localUserID); err != nil {
		return fmt.Errorf("migrate local conversations: %w", err)
	}
	localRuntime, err := runtimeclient.EnsureBuiltinRuntime(context.Background(), store, "local")
	if err != nil {
		return fmt.Errorf("initialize local runtime: %w", err)
	}
	if err := store.EnsureRuntimeAgentConfigs(context.Background(), localRuntime.ID, []string(localRuntime.DetectedAgents)); err != nil {
		return fmt.Errorf("initialize local runtime agents: %w", err)
	}

	application := &application{
		ctx:   workspace.WithWorkspace(context.Background(), ws),
		out:   out,
		store: store,
	}
	application.server = appserver.New(store, corepkg.ProviderSampler{Resolver: store}, corepkg.NewLocalToolDispatcher(store))
	return application.loop(in)
}

func configureLocalDatabase(cfg *config.Config, path string) error {
	if cfg.Workspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home directory: %w", err)
		}
		cfg.Workspace = filepath.Join(home, ".aiclaw")
	}
	wanted := filepath.Join(cfg.Workspace, "aiclaw.db")
	changed := cfg.Database.Driver != "sqlite" || cfg.Database.DSN == ""
	cfg.Database.Driver = "sqlite"
	if cfg.Database.DSN == "" || cfg.Database.DSN != wanted {
		cfg.Database.DSN = wanted
		changed = true
	}
	if changed {
		if err := cfg.Save(path); err != nil {
			return fmt.Errorf("save local database configuration: %w", err)
		}
	}
	return nil
}

type application struct {
	ctx            context.Context
	out            io.Writer
	store          *gormstore.GormStore
	server         *appserver.Service
	agent          *model.Agent
	conversationID string
	threadID       string
}

func (a *application) loop(in io.Reader) error {
	fmt.Fprintln(a.out, "AIClaw local app — /help for commands")
	a.printStatus()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for {
		fmt.Fprint(a.out, "\naiclaw> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			quit, err := a.command(line)
			if err != nil {
				fmt.Fprintf(a.out, "error: %v\n", err)
			}
			if quit {
				return nil
			}
			continue
		}
		if err := a.chat(line); err != nil {
			fmt.Fprintf(a.out, "error: %v\n", err)
		}
	}
}

func (a *application) command(line string) (bool, error) {
	parts := strings.Fields(line)
	switch parts[0] {
	case "/exit", "/quit":
		return true, nil
	case "/help":
		fmt.Fprintln(a.out, "Commands: /provider list | /provider add NAME TYPE BASE_URL API_KEY MODEL")
		fmt.Fprintln(a.out, "          /agent list | /agent add NAME PROVIDER_ID MODEL [WORKDIR] | /agent use ID")
		fmt.Fprintln(a.out, "          /thread list | /conversation list | /resume UUID | /new | /mcp list | /mcp add NAME stdio COMMAND [ARGS...] | /search list | /status | /exit")
		return false, nil
	case "/status":
		a.printStatus()
		return false, nil
	case "/new":
		a.conversationID = ""
		a.threadID = ""
		fmt.Fprintln(a.out, "Started a new local conversation.")
		return false, nil
	case "/resume":
		if len(parts) != 2 {
			return false, fmt.Errorf("usage: /resume THREAD_OR_CONVERSATION_UUID")
		}
		if thread, err := a.store.GetThreadByUUID(a.ctx, parts[1], false); err == nil {
			a.threadID = thread.UUID
			a.conversationID = thread.LegacyConversationUUID
			a.agent, _ = a.store.GetAgentByUUID(a.ctx, thread.AgentUUID)
			fmt.Fprintf(a.out, "Resumed thread: %s\n", thread.Title)
			return false, nil
		}
		conversation, err := a.store.GetConversationByUUID(a.ctx, parts[1])
		if err != nil {
			return false, err
		}
		thread, err := a.store.MigrateConversation(a.ctx, conversation.UUID)
		if err != nil {
			return false, err
		}
		a.conversationID = conversation.UUID
		a.threadID = thread.UUID
		a.agent, _ = a.store.GetAgentByUUID(a.ctx, thread.AgentUUID)
		fmt.Fprintf(a.out, "Resumed: %s\n", conversation.Title)
		return false, nil
	case "/thread":
		return false, a.threads(parts)
	case "/conversation":
		return false, a.conversations(parts)
	case "/provider":
		return false, a.providers(parts)
	case "/agent":
		return false, a.agents(parts)
	case "/mcp":
		return false, a.mcp(parts)
	case "/search":
		return false, a.search(parts)
	default:
		return false, fmt.Errorf("unknown command %q; use /help", parts[0])
	}
}

func (a *application) printStatus() {
	if a.agent == nil {
		agent, err := a.store.GetDefaultAgent(a.ctx)
		if err == nil {
			a.agent = agent
		}
	}
	if a.agent == nil {
		fmt.Fprintln(a.out, "No agent configured. Add a provider, then create an agent.")
		return
	}
	fmt.Fprintf(a.out, "Agent: %s | model: %s | thread: %s\n", a.agent.Name, a.agent.ModelName, valueOrNew(a.threadID))
}

func (a *application) providers(parts []string) error {
	if len(parts) < 2 {
		return fmt.Errorf("usage: /provider list | /provider add NAME TYPE BASE_URL API_KEY MODEL")
	}
	switch parts[1] {
	case "list":
		items, _, err := a.store.ListProviders(a.ctx, model.ListQuery{Page: 1, PageSize: 1000})
		if err != nil {
			return err
		}
		for _, item := range items {
			fmt.Fprintf(a.out, "%d  %s  %s  %s\n", item.ID, item.Name, item.Type, item.BaseURL)
		}
		return nil
	case "add":
		if len(parts) != 7 {
			return fmt.Errorf("usage: /provider add NAME TYPE BASE_URL API_KEY MODEL")
		}
		enabled := true
		return a.store.CreateProvider(a.ctx, &model.Provider{Name: parts[2], Type: model.ProviderType(parts[3]), BaseURL: parts[4], APIKey: parts[5], Models: model.JSON(fmt.Sprintf("[%q]", parts[6])), Enabled: enabled})
	default:
		return fmt.Errorf("unknown provider command %q", parts[1])
	}
}

func (a *application) agents(parts []string) error {
	if len(parts) < 2 {
		return fmt.Errorf("usage: /agent list | /agent add NAME PROVIDER_ID MODEL [WORKDIR] | /agent use ID")
	}
	switch parts[1] {
	case "list":
		items, _, err := a.store.ListAgents(a.ctx, model.ListQuery{Page: 1, PageSize: 1000})
		if err != nil {
			return err
		}
		for _, item := range items {
			fmt.Fprintf(a.out, "%d  %s  provider=%d  model=%s%s\n", item.ID, item.Name, item.ProviderID, item.ModelName, defaultSuffix(item.IsDefault))
		}
		return nil
	case "use":
		if len(parts) != 3 {
			return fmt.Errorf("usage: /agent use ID")
		}
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return err
		}
		a.agent, err = a.store.GetAgent(a.ctx, id)
		if err == nil {
			a.conversationID = ""
			a.printStatus()
		}
		return err
	case "add":
		if len(parts) < 5 || len(parts) > 6 {
			return fmt.Errorf("usage: /agent add NAME PROVIDER_ID MODEL [WORKDIR]")
		}
		providerID, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return err
		}
		workingDir := ""
		if len(parts) == 6 {
			workingDir = parts[5]
		}
		item := &model.Agent{Name: parts[2], ProviderID: providerID, ModelName: parts[4], WorkingDir: workingDir, IsDefault: a.agent == nil, EnableThinking: true, EnableWebSearch: true, WebSearchMode: model.WebSearchModeBuiltin}
		if err := a.store.CreateAgent(a.ctx, item); err != nil {
			return err
		}
		a.agent = item
		a.printStatus()
		return nil
	default:
		return fmt.Errorf("unknown agent command %q", parts[1])
	}
}

func (a *application) conversations(parts []string) error {
	if len(parts) != 2 || parts[1] != "list" {
		return fmt.Errorf("usage: /conversation list")
	}
	items, _, err := a.store.ListConversations(a.ctx, localUserID, model.ListQuery{Page: 1, PageSize: 100})
	if err != nil {
		return err
	}
	for _, item := range items {
		fmt.Fprintf(a.out, "%s  %s\n", item.UUID, item.Title)
	}
	return nil
}

func (a *application) threads(parts []string) error {
	if len(parts) != 2 || parts[1] != "list" {
		return fmt.Errorf("usage: /thread list")
	}
	items, _, err := a.store.ListThreads(a.ctx, localUserID, false, 1, 100)
	if err != nil {
		return err
	}
	for _, item := range items {
		fmt.Fprintf(a.out, "%s  %s\n", item.UUID, item.Title)
	}
	return nil
}

func (a *application) mcp(parts []string) error {
	if len(parts) < 2 {
		return fmt.Errorf("usage: /mcp list | /mcp add NAME stdio COMMAND [ARGS...]")
	}
	switch parts[1] {
	case "list":
		if len(parts) != 2 {
			return fmt.Errorf("usage: /mcp list")
		}
		items, err := a.store.ListMCPServers(a.ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			fmt.Fprintf(a.out, "%s  %s  %s%s\n", item.Name, item.Transport, item.Endpoint, defaultSuffix(item.Enabled))
		}
		return nil
	case "add":
		if len(parts) < 5 || parts[3] != string(model.MCPTransportStdio) {
			return fmt.Errorf("usage: /mcp add NAME stdio COMMAND [ARGS...]")
		}
		items, err := a.store.ListMCPServers(a.ctx)
		if err != nil {
			return err
		}
		args := model.JSON("[]")
		if len(parts) > 5 {
			args = model.JSON("[")
			for i, arg := range parts[5:] {
				if i > 0 {
					args = append(args, ',')
				}
				args = append(args, fmt.Sprintf("%q", arg)...)
			}
			args = append(args, ']')
		}
		items = append(items, model.MCPServer{Name: parts[2], Transport: model.MCPTransportStdio, Endpoint: parts[4], Args: args, Enabled: true})
		return a.store.ReplaceMCPServers(a.ctx, items)
	default:
		return fmt.Errorf("unknown MCP command %q", parts[1])
	}
}

func (a *application) search(parts []string) error {
	if len(parts) != 2 || parts[1] != "list" {
		return fmt.Errorf("usage: /search list")
	}
	items, _, err := a.store.ListSearchEngineConfigs(a.ctx, model.ListQuery{Page: 1, PageSize: 100})
	if err != nil {
		return err
	}
	for _, item := range items {
		fmt.Fprintf(a.out, "%d  %s  %s%s\n", item.ID, item.Name, item.Provider, defaultSuffix(item.Enabled))
	}
	return nil
}

func (a *application) chat(message string) error {
	if a.agent == nil {
		return fmt.Errorf("configure an agent first with /agent add")
	}
	fmt.Fprint(a.out, "\nassistant> ")
	if a.threadID == "" {
		err := a.server.Handle(a.ctx, protocol.Command{
			Kind: protocol.CommandCreateThread, UserID: localUserID, Input: message,
			ProviderID: a.agent.ProviderID, ModelName: a.agent.ModelName,
			SearchEngineID: a.agent.SearchEngineID, WorkingDir: a.agent.WorkingDir,
		}, func(event protocol.Event) error {
			a.threadID = event.ThreadID
			return nil
		})
		if err != nil {
			return err
		}
	}
	err := a.server.Handle(a.ctx, protocol.Command{Kind: protocol.CommandStartTurn, ThreadID: a.threadID, Input: message}, func(event protocol.Event) error {
		if event.Kind == protocol.EventAssistantDelta {
			_, err := fmt.Fprint(a.out, event.Delta)
			return err
		}
		return nil
	})
	fmt.Fprintln(a.out)
	return err
}

func valueOrNew(value string) string {
	if value == "" {
		return "new"
	}
	return value
}
func defaultSuffix(enabled bool) string {
	if enabled {
		return "  *"
	}
	return ""
}
