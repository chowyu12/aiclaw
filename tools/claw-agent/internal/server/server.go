// Package server 是面向桌面宿主的 stdio JSON-RPC 服务端。
//
// 它同时扮演两个角色：接宿主的请求（开会话、发轮次），也向宿主发请求
// （审批）。后者需要宿主回应，所以这里维护一张「等宿主回」的表。
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/pluginhost"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/providers"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/searchengines"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
)

const (
	codeParseError     = -32700
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeInternal       = -32603
)

type Options struct {
	Version string
	// DataHome 是会话文件目录。
	DataHome string
	// APIKey 从环境变量来，不经协议帧。会话没指定模型服务时用它。
	APIKey string
	// AppDB 是应用库（AIClaw 沿用的 SQLite：模型服务、插件、通道授权）的路径。
	// 空表示不开：那时 provider/* 与 plugin/* 都报错，会话只能走环境变量的 Key。
	// 插件文件放在它所在目录下的 plugins/。
	AppDB string
	// ProtectedPaths 是宿主追加的敏感路径，给通道会话用（桌面会话由宿主按会话传）。
	ProtectedPaths []string
	Logf           func(format string, args ...any)
}

type Server struct {
	options  Options
	out      io.Writer
	writeMu  sync.Mutex
	sessions map[string]*agent.Session
	sessMu   sync.Mutex
	// db 是会话库。整个进程共用一个连接池。
	db *store.Store
	// appDB 是应用库；Options.AppDB 为空时下面三个都是 nil。
	appDB     *gormstore.GormStore
	providers *providers.Store
	plugins   *pluginhost.Service
	search    *searchengines.Store

	// 向宿主发出的请求，等它回。
	outboundID      atomic.Int64
	outboundPending map[int64]chan json.RawMessage
	outboundMu      sync.Mutex

	shutdown chan struct{}
}

// New 建服务端并打开会话库。
//
// 旧版的 sessions/*.json 会在这里一次性搬进库；原文件保留不删，
// 万一搬错了还能翻回去看。
func New(options Options, out io.Writer) (*Server, error) {
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	db, err := store.Open(options.DataHome)
	if err != nil {
		return nil, err
	}
	if imported, err := store.ImportLegacy(context.Background(), db, options.DataHome); err != nil {
		options.Logf("导入旧会话失败：%v", err)
	} else if imported > 0 {
		options.Logf("已从旧的 JSON 文件导入 %d 个会话", imported)
	}
	server := &Server{
		options:         options,
		out:             out,
		sessions:        map[string]*agent.Session{},
		outboundPending: map[int64]chan json.RawMessage{},
		shutdown:        make(chan struct{}),
		db:              db,
	}
	if options.AppDB != "" {
		server.appDB, err = appdb.Open(options.AppDB)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		server.providers = providers.New(server.appDB)
		server.search = searchengines.New(server.appDB)
		// 插件系统与通道。通道收到消息经 channelGateway 变成一轮，见 channel.go。
		server.plugins, err = pluginhost.New(context.Background(), server.appDB,
			filepath.Dir(options.AppDB), &channelGateway{server: server}, options.Logf)
		if err != nil {
			_ = server.appDB.Close()
			_ = db.Close()
			return nil, err
		}
	}
	return server, nil
}

// keyFor 是交给会话的 Key 解析器：指定了模型服务就到库里查，否则用环境变量。
func (s *Server) keyFor(model *protocol.ModelConfig) (string, error) {
	if model.ProviderID == 0 {
		return s.options.APIKey, nil
	}
	if s.providers == nil {
		return "", errors.New("没有打开模型配置库，无法按模型服务取 Key")
	}
	return s.providers.Resolve(context.Background(), model)
}

type frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve 读 stdin 直到 EOF 或收到 shutdown。
func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if len(line) > 0 {
				lines <- line
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			s.closeAll()
			return nil
		case <-s.shutdown:
			s.closeAll()
			return nil
		case line, ok := <-lines:
			if !ok {
				s.closeAll()
				return scanner.Err()
			}
			var f frame
			if err := json.Unmarshal(line, &f); err != nil {
				s.writeError(nil, codeParseError, "invalid JSON")
				continue
			}
			// 有 id 且带 result/error 的是宿主对我们请求（审批）的回应。
			if f.Method == "" && len(f.ID) > 0 {
				s.resolveOutbound(f)
				continue
			}
			go s.dispatch(ctx, f)
		}
	}
}

func (s *Server) dispatch(ctx context.Context, f frame) {
	switch f.Method {
	case protocol.MethodInitialize:
		s.writeResult(f.ID, protocol.InitializeResult{
			Version:  s.options.Version,
			DataHome: s.options.DataHome,
			Tools:    []string{"read_file", "write_file", "edit_file", "list_dir", "search_files", "run_command"},
		})
	case protocol.MethodSessionStart:
		s.handleSessionStart(ctx, f)
	case protocol.MethodSessionResume:
		s.handleSessionResume(ctx, f)
	case protocol.MethodMCPProbe:
		s.handleMCPProbe(ctx, f)
	case protocol.MethodProviderList, protocol.MethodProviderCreate, protocol.MethodProviderUpdate,
		protocol.MethodProviderDelete, protocol.MethodProviderModels, protocol.MethodProviderAutoMark:
		s.handleProvider(ctx, f)
	case protocol.MethodPluginList, protocol.MethodPluginInstall, protocol.MethodPluginToggle,
		protocol.MethodPluginDelete, protocol.MethodPluginConfig, protocol.MethodPluginSetConfig,
		protocol.MethodPluginContrib, protocol.MethodChannelStatus, protocol.MethodChannelBindings,
		protocol.MethodChannelAuthorize, protocol.MethodChannelRevoke,
		protocol.MethodWeChatLoginStart, protocol.MethodWeChatLoginPoll:
		s.handlePlugin(ctx, f)
	case protocol.MethodSearchList, protocol.MethodSearchCreate, protocol.MethodSearchUpdate,
		protocol.MethodSearchDelete, protocol.MethodSearchTest:
		s.handleSearch(ctx, f)
	case protocol.MethodSessionSearch:
		var params protocol.SessionSearchParams
		// 参数解不出来就当空关键词：搜索框里打字很快，宁可回全部也不要报错。
		_ = json.Unmarshal(f.Params, &params)
		found, err := agent.Search(ctx, s.db, params.Keyword)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, map[string]any{"sessions": found})
	case protocol.MethodSessionList:
		summaries, err := agent.List(ctx, s.db)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		if summaries == nil {
			summaries = []agent.Summary{}
		}
		s.writeResult(f.ID, map[string]any{"sessions": summaries})
	case protocol.MethodSessionDelete:
		var params protocol.SessionIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		s.sessMu.Lock()
		if session, ok := s.sessions[params.SessionID]; ok {
			session.Close()
			delete(s.sessions, params.SessionID)
		}
		s.sessMu.Unlock()
		if err := agent.Delete(ctx, s.db, params.SessionID); err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodSessionConfigure:
		var params protocol.SessionConfigureParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		session := s.session(params.SessionID)
		if session == nil {
			s.writeError(f.ID, codeInvalidParams, "会话不存在或尚未恢复："+params.SessionID)
			return
		}
		if params.Workspace != nil {
			if err := session.SetWorkspace(*params.Workspace); err != nil {
				s.writeError(f.ID, codeInvalidParams, err.Error())
				return
			}
		}
		// 模型名为空表示这次只改工作区。
		if strings.TrimSpace(params.Model.Model) != "" {
			if err := session.Configure(params.Model); err != nil {
				s.writeError(f.ID, codeInvalidParams, err.Error())
				return
			}
		}
		// 立刻落盘：换完模型没发消息就退出应用的话，下次恢复要能记得。
		if err := session.Save(ctx, s.db); err != nil {
			s.options.Logf("保存会话失败：%v", err)
		}
		s.writeResult(f.ID, map[string]any{"model": session.Model()})
	case protocol.MethodSessionHistory:
		var params protocol.SessionIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		session := s.session(params.SessionID)
		if session == nil {
			s.writeError(f.ID, codeInvalidParams, "会话不存在或尚未恢复："+params.SessionID)
			return
		}
		s.writeResult(f.ID, protocol.SessionHistoryResult{Items: session.History()})
	case protocol.MethodTurnStart:
		s.handleTurnStart(ctx, f)
	case protocol.MethodTurnInterrupt:
		var params protocol.SessionIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if session := s.session(params.SessionID); session != nil {
			session.Interrupt()
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodShutdown:
		s.writeResult(f.ID, map[string]any{})
		close(s.shutdown)
	default:
		if len(f.ID) == 0 {
			return // 未知通知，忽略
		}
		s.writeError(f.ID, codeMethodNotFound, fmt.Sprintf("method %q not supported", f.Method))
	}
}

func (s *Server) handleSessionStart(ctx context.Context, f frame) {
	var params protocol.SessionStartParams
	if err := json.Unmarshal(f.Params, &params); err != nil {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	id := fmt.Sprintf("s_%d", time.Now().UnixNano())
	started := time.Now()
	session, err := agent.New(ctx, id, params, s.keyFor)
	if err != nil {
		s.writeError(f.ID, codeInternal, err.Error())
		return
	}
	// 开会话慢的时候，日志里以前只有「运行时 ready」，没有任何线索说慢在哪一段。
	s.options.Logf("开会话 %s：挂 MCP %dms（%d 个）+ 其余 %dms = 共 %dms",
		id, session.MountMS(), len(params.MCPServers),
		time.Since(started).Milliseconds()-session.MountMS(), time.Since(started).Milliseconds())
	s.guard(session)
	s.sessMu.Lock()
	s.sessions[id] = session
	s.sessMu.Unlock()
	if err := session.Save(ctx, s.db); err != nil {
		s.options.Logf("保存会话失败：%v", err)
	}
	s.writeResult(f.ID, protocol.SessionStartResult{
		SessionID:   id,
		Tools:       session.Tools(),
		MCPStatus:   session.MCPStatus(),
		Model:       session.Model().Model,
		ProviderID:  session.Model().ProviderID,
		Skills:      session.Skills(),
		FoldedTools: session.FoldedTools(),
		Workspace:   session.Workspace(),
	})
}

func (s *Server) handleSessionResume(ctx context.Context, f frame) {
	var params protocol.SessionResumeParams
	if err := json.Unmarshal(f.Params, &params); err != nil {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	pinChannelPolicy(params.SessionID, params.Refresh)
	if existing := s.session(params.SessionID); existing != nil {
		// 已经在内存里的会话，配置没变就直接回它现在的样子。
		//
		// 变了就卸掉重来（跑着的时候不动它：把会话卸了，那一轮的事件就没有
		// 出口了）。重来之前先存一次——内核只在每轮结束后写库，不存的话
		// 最后那一轮的历史会跟着被卸掉的会话一起没。
		fresh := params.Refresh != nil && !existing.MountsMatch(*params.Refresh)
		if !fresh || existing.Busy() {
			s.writeResult(f.ID, protocol.SessionStartResult{
				SessionID: existing.ID, Tools: existing.Tools(),
				MCPStatus: existing.MCPStatus(), Model: existing.Model().Model,
				ProviderID: existing.Model().ProviderID,
				Skills:     existing.Skills(), FoldedTools: existing.FoldedTools(), Workspace: existing.Workspace(),
			})
			return
		}
		if err := existing.Save(ctx, s.db); err != nil {
			s.options.Logf("重挂前保存会话失败：%v", err)
		}
		s.dropSession(params.SessionID)
	}
	loadStarted := time.Now()
	session, err := agent.Load(ctx, s.db, params.SessionID, s.keyFor, params.Refresh)
	if err != nil {
		s.writeError(f.ID, codeInternal, err.Error())
		return
	}
	s.options.Logf("恢复会话 %s：挂 MCP %dms（%d 个）+ 其余 %dms = 共 %dms",
		params.SessionID, session.MountMS(), len(refreshServers(params.Refresh)),
		time.Since(loadStarted).Milliseconds()-session.MountMS(), time.Since(loadStarted).Milliseconds())
	s.guard(session)
	s.sessMu.Lock()
	s.sessions[session.ID] = session
	s.sessMu.Unlock()
	s.writeResult(f.ID, protocol.SessionStartResult{
		SessionID: session.ID, Tools: session.Tools(),
		MCPStatus: session.MCPStatus(), Model: session.Model().Model,
		ProviderID: session.Model().ProviderID,
		Skills:     session.Skills(), FoldedTools: session.FoldedTools(), Workspace: session.Workspace(),
	})
}

func (s *Server) handleTurnStart(ctx context.Context, f frame) {
	var params protocol.TurnStartParams
	if err := json.Unmarshal(f.Params, &params); err != nil {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	session := s.session(params.SessionID)
	if session == nil {
		s.writeError(f.ID, codeInvalidParams, "会话不存在或尚未恢复："+params.SessionID)
		return
	}
	// 有轮次在跑就把输入排进去，不另起一轮：宿主拿到的是那一轮的 id，
	// 也不会再收到一次 turn/started。见 Session.pending 的说明。
	if running, queued := session.Enqueue(params.Text, params.Images); queued {
		s.writeResult(f.ID, protocol.TurnStartResult{TurnID: running, Queued: true})
		return
	}

	turnID := fmt.Sprintf("t_%d", time.Now().UnixNano())
	// 先回应，再异步跑：宿主拿到 turnId 之后靠通知跟进度。
	s.writeResult(f.ID, protocol.TurnStartResult{TurnID: turnID})

	go func() {
		session.RunTurn(ctx, turnID, params.Text, params.Images, params.AudioPaths, &emitter{server: s})
		if err := session.Save(ctx, s.db); err != nil {
			s.options.Logf("保存会话失败：%v", err)
		}
	}()
}

// dropSession 把会话从内存里摘掉并关掉它的 MCP 连接。存档不动。
func (s *Server) dropSession(id string) {
	s.sessMu.Lock()
	session := s.sessions[id]
	delete(s.sessions, id)
	s.sessMu.Unlock()
	if session != nil {
		session.Close()
	}
}

// handleMCPProbe 试连一个 MCP server 并列出工具。
//
// 连不上不是协议错误，是那个 server 的事：结果里带 ok=false 和原因，
// 让宿主显示在那一条上。回 JSON-RPC error 的话，宿主只能弹个全局错误条，
// 而用户正盯着配置页上某一行想知道它通不通。
// mcpProbeTimeout 是试连的时限。宿主那边用户正等着一个「通/不通」，
// 比开会话时的挂载更该早点给结论。
const mcpProbeTimeout = 20 * time.Second

func (s *Server) handleMCPProbe(ctx context.Context, f frame) {
	var params protocol.MCPProbeParams
	if err := json.Unmarshal(f.Params, &params); err != nil {
		s.writeError(f.ID, codeInvalidParams, "invalid params")
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()

	client, err := mcpclient.Start(probeCtx, mcpclient.Config{
		Command:        params.Server.Command,
		Args:           params.Server.Args,
		Env:            params.Server.Env,
		URL:            params.Server.URL,
		Headers:        params.Server.Headers,
		StartupTimeout: mcpProbeTimeout,
	})
	if err != nil {
		s.writeResult(f.ID, protocol.MCPProbeResult{OK: false, Error: err.Error()})
		return
	}
	defer client.Close()

	tools := make([]protocol.MCPToolInfo, 0, len(client.Tools()))
	for _, tool := range client.Tools() {
		tools = append(tools, protocol.MCPToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			ReadOnly:    tool.ReadOnly(),
		})
	}
	s.writeResult(f.ID, protocol.MCPProbeResult{OK: true, Tools: tools})
}

func (s *Server) session(id string) *agent.Session {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	return s.sessions[id]
}

func (s *Server) closeAll() {
	s.sessMu.Lock()
	for id, session := range s.sessions {
		session.Close()
		delete(s.sessions, id)
	}
	s.sessMu.Unlock()
	// 库放在最后关：上面那些 Close 不写库，但顺序反了以后加的代码就会踩坑。
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.options.Logf("关闭会话库失败：%v", err)
		}
	}
	// 通道先停：它们还可能往库里写授权记录。
	if s.plugins != nil {
		s.plugins.Close()
	}
	if s.appDB != nil {
		if err := s.appDB.Close(); err != nil {
			s.options.Logf("关闭应用库失败：%v", err)
		}
	}
}

// handleSearch 处理 search/* 五个方法。搜索引擎的增删改查与试搜。
func (s *Server) handleSearch(ctx context.Context, f frame) {
	if s.search == nil {
		s.writeError(f.ID, codeInternal, "没有打开应用库（启动时未传 --app-db）")
		return
	}
	switch f.Method {
	case protocol.MethodSearchList:
		list, err := s.search.List(ctx)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, map[string]any{"engines": list})
	case protocol.MethodSearchCreate:
		var params protocol.SearchEngineCreateParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		created, err := s.search.Create(ctx, params)
		if err != nil {
			s.writeError(f.ID, codeInvalidParams, err.Error())
			return
		}
		s.writeResult(f.ID, created)
	case protocol.MethodSearchUpdate:
		var params protocol.SearchEngineUpdateParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		updated, err := s.search.Update(ctx, params)
		if err != nil {
			s.writeError(f.ID, codeInvalidParams, err.Error())
			return
		}
		s.writeResult(f.ID, updated)
	case protocol.MethodSearchDelete:
		var params protocol.SearchEngineIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.search.Delete(ctx, params.ID); err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodSearchTest:
		var params protocol.SearchEngineTestParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		result, err := s.search.Test(ctx, params)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		if result.Results == nil {
			result.Results = []protocol.SearchHit{}
		}
		s.writeResult(f.ID, result)
	}
}

// handlePlugin 处理 plugin/*、channel/*、wechat/* 方法。都是配置页上的操作。
func (s *Server) handlePlugin(ctx context.Context, f frame) {
	if s.plugins == nil {
		s.writeError(f.ID, codeInternal, "没有打开应用库（启动时未传 --app-db）")
		return
	}
	fail := func(code int, err error) { s.writeError(f.ID, code, err.Error()) }
	switch f.Method {
	case protocol.MethodPluginList:
		list, err := s.plugins.List(ctx)
		if err != nil {
			fail(codeInternal, err)
			return
		}
		s.writeResult(f.ID, map[string]any{"plugins": list})
	case protocol.MethodPluginInstall:
		var params protocol.PluginInstallParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		installed, err := s.plugins.Install(ctx, params.Path)
		if err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, installed)
	case protocol.MethodPluginToggle:
		var params protocol.PluginToggleParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.plugins.Toggle(ctx, params.UUID, params.Enabled); err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodPluginDelete:
		var params protocol.PluginUUIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.plugins.Delete(ctx, params.UUID); err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodPluginConfig:
		var params protocol.PluginUUIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		fields, err := s.plugins.ConfigFields(ctx, params.UUID)
		if err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, protocol.PluginConfigResult{Fields: fields})
	case protocol.MethodPluginSetConfig:
		var params protocol.PluginSetConfigParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.plugins.SetConfig(ctx, params.UUID, params.Key, params.Value); err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodPluginContrib:
		contributions, err := s.plugins.Contributions(ctx)
		if err != nil {
			fail(codeInternal, err)
			return
		}
		s.writeResult(f.ID, contributions)
	case protocol.MethodChannelStatus:
		s.writeResult(f.ID, map[string]any{"channels": s.plugins.ChannelStatus()})
	case protocol.MethodChannelBindings:
		bindings, err := s.plugins.Bindings(ctx)
		if err != nil {
			fail(codeInternal, err)
			return
		}
		s.writeResult(f.ID, map[string]any{"bindings": bindings})
	case protocol.MethodChannelAuthorize:
		var params protocol.ChannelAuthorizeParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.plugins.Authorize(ctx, params); err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodChannelRevoke:
		var params protocol.ChannelBindingKey
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.plugins.Revoke(ctx, params); err != nil {
			fail(codeInvalidParams, err)
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodWeChatLoginStart:
		result, err := s.plugins.WeChatLoginStart(ctx)
		if err != nil {
			fail(codeInternal, err)
			return
		}
		s.writeResult(f.ID, result)
	case protocol.MethodWeChatLoginPoll:
		var params protocol.WeChatLoginPollParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		result, err := s.plugins.WeChatLoginPoll(ctx, params)
		if err != nil {
			fail(codeInternal, err)
			return
		}
		s.writeResult(f.ID, result)
	}
}

// handleProvider 处理 provider/* 五个方法。都是配置页上的同步操作，没有会话上下文。
func (s *Server) handleProvider(ctx context.Context, f frame) {
	if s.providers == nil {
		s.writeError(f.ID, codeInternal, "没有打开模型配置库（启动时未传 --app-db）")
		return
	}
	switch f.Method {
	case protocol.MethodProviderList:
		list, err := s.providers.List(ctx)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, protocol.ProviderListResult{Providers: list})
	case protocol.MethodProviderCreate:
		var params protocol.ProviderCreateParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		created, err := s.providers.Create(ctx, params)
		if err != nil {
			s.writeError(f.ID, codeInvalidParams, err.Error())
			return
		}
		s.writeResult(f.ID, created)
	case protocol.MethodProviderUpdate:
		var params protocol.ProviderUpdateParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		updated, err := s.providers.Update(ctx, params)
		if err != nil {
			s.writeError(f.ID, codeInvalidParams, err.Error())
			return
		}
		s.writeResult(f.ID, updated)
	case protocol.MethodProviderDelete:
		var params protocol.ProviderIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		if err := s.providers.Delete(ctx, params.ID); err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, map[string]any{})
	case protocol.MethodProviderAutoMark:
		var params protocol.ProviderIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		result, err := s.providers.AutoMark(ctx, params.ID)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, result)
	case protocol.MethodProviderModels:
		var params protocol.ProviderIDParams
		if err := json.Unmarshal(f.Params, &params); err != nil || params.ID == 0 {
			s.writeError(f.ID, codeInvalidParams, "invalid params")
			return
		}
		names, err := s.providers.FetchModels(ctx, params.ID)
		if err != nil {
			s.writeError(f.ID, codeInternal, err.Error())
			return
		}
		s.writeResult(f.ID, protocol.ProviderModelsResult{Models: names})
	}
}

// ---------- 向宿主发请求（审批） ----------

func (s *Server) requestHost(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.outboundID.Add(1)
	ch := make(chan json.RawMessage, 1)
	s.outboundMu.Lock()
	s.outboundPending[id] = ch
	s.outboundMu.Unlock()

	idRaw, _ := json.Marshal(id)
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	s.write(frame{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw})

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		s.outboundMu.Lock()
		delete(s.outboundPending, id)
		s.outboundMu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Server) resolveOutbound(f frame) {
	var id int64
	if err := json.Unmarshal(f.ID, &id); err != nil {
		return
	}
	s.outboundMu.Lock()
	ch, ok := s.outboundPending[id]
	if ok {
		delete(s.outboundPending, id)
	}
	s.outboundMu.Unlock()
	if !ok {
		return
	}
	if f.Error != nil {
		// 宿主回了错误：当作拒绝，别让轮次卡死。
		ch <- json.RawMessage(`{"approved":false}`)
		return
	}
	ch <- f.Result
}

// emitter 把 agent 的事件翻成协议帧。
type emitter struct {
	server *Server
}

func (e *emitter) Notify(method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		e.server.options.Logf("编码通知失败：%v", err)
		return
	}
	e.server.write(frame{JSONRPC: "2.0", Method: method, Params: raw})
}

func (e *emitter) RequestApproval(
	ctx context.Context,
	params protocol.ApprovalRequestParams,
) (protocol.ApprovalResponse, error) {
	// 审批不能无限等：用户离开了、宿主崩了，轮次都得能收尾。
	approvalCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	raw, err := e.server.requestHost(approvalCtx, protocol.RequestApproval, params)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return protocol.ApprovalResponse{}, errors.New("等待确认超时，已按拒绝处理")
		}
		return protocol.ApprovalResponse{}, err
	}
	var response protocol.ApprovalResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return protocol.ApprovalResponse{}, fmt.Errorf("审批回应格式不对：%w", err)
	}
	return response, nil
}

// RequestComputer 请宿主代做一次屏幕操作。
//
// 超时比审批短得多：截屏和点击都是毫秒级的动作，卡住多半是宿主那边出了问题，
// 不该让整轮跟着挂。审批的三十分钟是在等人，这里没有人可等。
func (e *emitter) RequestComputer(
	ctx context.Context,
	params protocol.ComputerRequestParams,
) (protocol.ComputerResult, error) {
	actionCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := e.server.requestHost(actionCtx, protocol.RequestComputer, params)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return protocol.ComputerResult{}, errors.New("屏幕操作超时（60 秒）")
		}
		return protocol.ComputerResult{}, err
	}
	var result protocol.ComputerResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.ComputerResult{}, fmt.Errorf("屏幕操作回应格式不对：%w", err)
	}
	return result, nil
}

// RequestBrowser 请宿主在浏览器窗口里做一步。
//
// 超时比屏幕操作长：打开一个网页要等它加载完，慢的站点十几秒是常态。
func (e *emitter) RequestBrowser(
	ctx context.Context,
	params protocol.BrowserRequestParams,
) (protocol.BrowserResult, error) {
	actionCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	raw, err := e.server.requestHost(actionCtx, protocol.RequestBrowser, params)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return protocol.BrowserResult{}, errors.New("浏览器操作超时（90 秒）")
		}
		return protocol.BrowserResult{}, err
	}
	var result protocol.BrowserResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.BrowserResult{}, fmt.Errorf("浏览器操作回应格式不对：%w", err)
	}
	return result, nil
}

// refreshServers 取 refresh 里的 MCP server，refresh 为空时给空表。只用于记日志。
func refreshServers(refresh *protocol.SessionRefresh) map[string]protocol.MCPServerConfig {
	if refresh == nil {
		return nil
	}
	return refresh.MCPServers
}

// ---------- 写出 ----------

func (s *Server) writeResult(id json.RawMessage, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		s.writeError(id, codeInternal, err.Error())
		return
	}
	s.write(frame{JSONRPC: "2.0", ID: id, Result: raw})
}

func (s *Server) writeError(id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	s.write(frame{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *Server) write(f frame) {
	encoded, err := json.Marshal(f)
	if err != nil {
		s.options.Logf("编码帧失败：%v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.out.Write(append(encoded, '\n')); err != nil {
		s.options.Logf("写出失败：%v", err)
	}
}

// DefaultAppDB 是默认的模型配置库：AIClaw 旧版就放在 ~/.aiclaw/aiclaw.db，
// 沿用这个位置，升级上来的用户配过的模型服务原样可用。
func DefaultAppDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aiclaw", "aiclaw.db")
}

// DefaultDataHome 是默认的数据目录。宿主通常会显式传，这里只做兜底。
func DefaultDataHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claw-agent"
	}
	return filepath.Join(home, ".claw-agent")
}
