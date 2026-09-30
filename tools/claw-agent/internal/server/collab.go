package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// 多 agent 协作的控制面（agent.Collaboration 的实现）。工具那一侧见 agent/collab.go。
//
// 一棵协作树从用户的会话长出来：它是 /root，spawn_agent 开的子 agent 是 /root/<task>。
// 子 agent 就是一个普通会话（存档、事件、审批都照常），配置照抄父会话，多记一个
// parentId 与 agentPath——桌面端据此把它挂在父会话下面显示。
//
// 树只活在内存里：内核重启之后，旧的子会话还在、能打开能继续聊，但不再属于哪棵树
// （Codex 同样只对「活着的 agent」开放协作工具）。

const (
	// maxAgentDepth 是子 agent 最多嵌套几层：根是 0，它开的是 1，再开的是 2。
	maxAgentDepth = 2
	// maxLiveAgents 是一棵树上同时在跑的子 agent 上限。每个都在烧 token，
	// 而同时开太多，用户根本跟不上它们在干什么。
	maxLiveAgents = 6
)

type collabNode struct {
	sessionID string
	path      string
	parentID  string
	rootID    string
	depth     int
	status    string
}

type collabHub struct {
	server *Server
	mu     sync.Mutex
	ctx    context.Context
	nodes  map[string]*collabNode
}

func newCollabHub(server *Server) *collabHub {
	return &collabHub{server: server, ctx: context.Background(), nodes: map[string]*collabNode{}}
}

// setContext 换成服务的生命周期：子 agent 的轮次不跟着开它的那次工具调用一起结束。
func (h *collabHub) setContext(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
}

func (h *collabHub) context() context.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctx
}

// node 取会话在树上的节点；还不在任何树上的会话，就是一棵新树的根。
func (h *collabHub) node(session *agent.Session) *collabNode {
	h.mu.Lock()
	defer h.mu.Unlock()
	if node, ok := h.nodes[session.ID]; ok {
		return node
	}
	node := &collabNode{sessionID: session.ID, path: "/root", rootID: session.ID, status: agent.AgentIdle}
	h.nodes[session.ID] = node
	return node
}

// resolve 按名字找同一棵树上的 agent：/ 开头是规范名，否则是调用者的子任务名。
func (h *collabHub) resolve(caller *collabNode, target string) (*collabNode, error) {
	target = strings.TrimSpace(strings.TrimSuffix(target, "/"))
	if target == "" {
		return nil, i18n.E("target 不能为空：要给出 agent 的名字")
	}
	path := target
	if !strings.HasPrefix(target, "/") {
		path = caller.path + "/" + target
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, node := range h.nodes {
		if node.rootID == caller.rootID && node.path == path {
			return node, nil
		}
	}
	return nil, i18n.E("没有叫 {path} 的 agent（用 list_agents 看看有哪些）", "path", path)
}

func (h *collabHub) Spawn(ctx context.Context, parent *agent.Session, request agent.SpawnRequest) (agent.SpawnResult, error) {
	parentNode := h.node(parent)
	if parentNode.depth+1 > maxAgentDepth {
		return agent.SpawnResult{}, i18n.E("子 agent 最多嵌套 {depth} 层；这件事自己做，或者交回给开你的 agent", "depth", maxAgentDepth)
	}
	path := parentNode.path + "/" + request.TaskName
	h.mu.Lock()
	live := 0
	for _, node := range h.nodes {
		if node.rootID != parentNode.rootID {
			continue
		}
		if node.path == path {
			h.mu.Unlock()
			return agent.SpawnResult{}, i18n.E("已经有叫 {path} 的 agent 了：换个 task_name，或者用 followup_task 给它派新活", "path", path)
		}
		if node.depth > 0 && node.status == agent.AgentRunning {
			live++
		}
	}
	h.mu.Unlock()
	if live >= maxLiveAgents {
		return agent.SpawnResult{}, i18n.E("同时在跑的子 agent 已经有 {count} 个了，先等一些做完（wait_agent）", "count", live)
	}

	config := parent.Config()
	config.Title = "↳ " + request.TaskName
	config.ParentID = parent.ID
	config.AgentPath = path
	id := fmt.Sprintf("s_%d", time.Now().UnixNano())
	// 建会话用服务的 ctx：MCP 连接要活得比这次工具调用长。
	child, err := agent.New(h.context(), id, config, h.server.keyFor, h.server.sessionOptions(id)...)
	if err != nil {
		return agent.SpawnResult{}, i18n.E("开子 agent 失败：{error}", "error", err)
	}
	child.ForkFrom(parent, request.ForkTurns)
	h.server.guard(child)
	h.server.sessMu.Lock()
	h.server.sessions[id] = child
	h.server.sessMu.Unlock()

	node := &collabNode{
		sessionID: id, path: path, parentID: parent.ID, rootID: parentNode.rootID,
		depth: parentNode.depth + 1, status: agent.AgentRunning,
	}
	h.mu.Lock()
	h.nodes[id] = node
	h.mu.Unlock()
	// 开出来就存一次档：会话列表是从库里读的，不存的话它跑完之前侧边栏里看不到，
	// 用户没法点进去看它在干什么——跑得久的时候看上去就像没动静。
	if err := child.Save(h.context(), h.server.db); err != nil {
		h.server.options.Logf("保存子会话失败：%v", err)
	}

	task := fmt.Sprintf("(This task was assigned to you by %s. You are a sub-agent; your canonical name is %s. When you finish, your final answer is delivered to it automatically; "+
		"to report progress or ask questions along the way, use send_message to %s.)\n\n%s", parentNode.path, path, parentNode.path, request.Message)
	go h.runTurn(child, node, task)
	return agent.SpawnResult{TaskName: path, Nickname: request.TaskName}, nil
}

func (h *collabHub) Send(_ context.Context, from *agent.Session, target, message string, trigger bool) error {
	sender := h.node(from)
	node, err := h.resolve(sender, target)
	if err != nil {
		return err
	}
	if node.sessionID == from.ID {
		return i18n.E("不能给自己发消息")
	}
	if trigger && node.depth == 0 {
		return i18n.E("followup_task 只能发给子 agent；给根 agent 用 send_message")
	}
	session := h.server.session(node.sessionID)
	if session == nil {
		return i18n.E("{path} 已经不在了", "path", node.path)
	}
	text := fmt.Sprintf("<agent_message from=%q>\n%s\n</agent_message>", sender.path, message)
	notice := i18n.D("📨 收到 {path} 的消息", "path", sender.path)
	if trigger {
		notice = i18n.D("📨 {path} 派来新任务", "path", sender.path)
	}
	running := session.DeliverAgentMail(sender.path, text, notice)
	if trigger && !running {
		h.setStatus(node, agent.AgentRunning)
		go h.runTurn(session, node, "")
	}
	return nil
}

func (h *collabHub) List(self *agent.Session, prefix string) ([]agent.AgentInfo, error) {
	caller := h.node(self)
	prefix = strings.TrimSuffix(prefix, "/")
	h.mu.Lock()
	nodes := []*collabNode{}
	for _, node := range h.nodes {
		if node.rootID == caller.rootID && (prefix == "" || node.path == prefix || strings.HasPrefix(node.path, prefix+"/")) {
			nodes = append(nodes, node)
		}
	}
	h.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].path < nodes[j].path })
	result := make([]agent.AgentInfo, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, agent.AgentInfo{AgentName: node.path, AgentStatus: h.statusOf(node)})
	}
	return result, nil
}

func (h *collabHub) Interrupt(self *agent.Session, target string) (string, error) {
	node, err := h.resolve(h.node(self), target)
	if err != nil {
		return "", err
	}
	previous := h.statusOf(node)
	if session := h.server.session(node.sessionID); session != nil {
		session.Interrupt()
	}
	return previous, nil
}

// sessionTree 找出一个会话连同它开出的子 agent（和孙子），子的在前、自己在最后。
//
// 父子关系以存档里的 parentId 为准，不只看内存里的协作树：内核重启过之后树没了，
// 但「删了、归档了父会话，留下一串孤儿子会话」同样不该发生。归档了的也算在内。
func (s *Server) sessionTree(ctx context.Context, rootID string) ([]string, error) {
	summaries, err := agent.ListAll(ctx, s.db)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, summary := range summaries {
		if summary.ParentID != "" {
			children[summary.ParentID] = append(children[summary.ParentID], summary.ID)
		}
	}
	var order []string
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, child := range children[id] {
			walk(child)
		}
		order = append(order, id)
	}
	walk(rootID)
	return order, nil
}

// unload 把会话从内存里摘掉：停下正在跑的轮次、关掉 MCP、从协作树上摘下节点。
func (s *Server) unload(id string) {
	s.sessMu.Lock()
	session := s.sessions[id]
	delete(s.sessions, id)
	s.sessMu.Unlock()
	if session != nil {
		session.Close()
	}
	s.collab.forget(id)
}

// deleteSessionTree 删掉一个会话，连同它开出的子 agent。返回删掉的全部 id，子的在前。
func (s *Server) deleteSessionTree(ctx context.Context, rootID string) ([]string, error) {
	order, err := s.sessionTree(ctx, rootID)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		s.unload(id)
		if err := agent.Delete(ctx, s.db, id); err != nil {
			return order, err
		}
	}
	return order, nil
}

// archiveSessionTree 归档（或恢复）一个会话，连同它开出的子 agent。返回涉及的全部 id。
//
// 归档时把它们从内存里摘掉：还在跑的就停下——归档的意思是「这件事先放一边」，
// 放一边的会话不该还在后台花钱。恢复只是改回标记，点开时照常重新挂载。
func (s *Server) archiveSessionTree(ctx context.Context, rootID string, archived bool) ([]string, error) {
	order, err := s.sessionTree(ctx, rootID)
	if err != nil {
		return nil, err
	}
	at := time.Time{}
	if archived {
		at = time.Now()
		for _, id := range order {
			s.unload(id)
		}
	}
	return order, s.db.SetArchived(ctx, order, at)
}

// forget 把删掉的会话从协作树上摘下来：它的名字可以再用，list_agents 也不再列它。
func (h *collabHub) forget(sessionID string) {
	h.mu.Lock()
	delete(h.nodes, sessionID)
	h.mu.Unlock()
}

// InterruptTree 用户在界面上停下一个会话时，它开出去还在跑的子 agent 一并停下：
// 用户点「停止」的意思是「别再干了」，不是「你停下，让你的手下接着花钱」。
func (h *collabHub) InterruptTree(sessionID string) {
	h.mu.Lock()
	var targets []string
	var collect func(parent string)
	collect = func(parent string) {
		for _, node := range h.nodes {
			if node.parentID == parent {
				targets = append(targets, node.sessionID)
				collect(node.sessionID)
			}
		}
	}
	collect(sessionID)
	h.mu.Unlock()
	for _, id := range targets {
		if session := h.server.session(id); session != nil {
			session.Interrupt()
		}
	}
}

func (h *collabHub) statusOf(node *collabNode) string {
	if session := h.server.session(node.sessionID); session != nil && session.Busy() {
		return agent.AgentRunning
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if node.status == agent.AgentRunning {
		return agent.AgentIdle
	}
	return node.status
}

func (h *collabHub) setStatus(node *collabNode, status string) {
	h.mu.Lock()
	node.status = status
	h.mu.Unlock()
}

// runTurn 在后台替一个 agent 跑一轮；text 为空表示由邮箱里的消息开起来。
// 这一轮结束后，它是子 agent 的话就把结果投进父 agent 的邮箱。
func (h *collabHub) runTurn(session *agent.Session, node *collabNode, text string) {
	if text == "" && session.Busy() {
		// 已经在跑：邮箱里的东西会在它下一次采样前送到，不用另开一轮。
		return
	}
	ctx := h.context()
	turnID := fmt.Sprintf("t_%d", time.Now().UnixNano())
	watcher := &turnWatcher{emitter: emitter{server: h.server}, sessionID: session.ID}
	session.RunTurn(ctx, turnID, text, nil, nil, watcher)
	if err := session.Save(ctx, h.server.db); err != nil {
		h.server.options.Logf("保存会话失败：%v", err)
	}
	status := agent.AgentCompleted
	switch {
	// 「已中断」是 turn.go 写进 turn/completed 的标记；它可能跟着界面语言翻译，两种都认。
	case watcher.err == i18n.T(i18n.Chinese, "已中断") || watcher.err == i18n.T(i18n.English, "已中断"):
		status = agent.AgentInterrupted
	case watcher.err != "":
		status = agent.AgentErrored
	}
	h.setStatus(node, status)
	if node.parentID == "" {
		return
	}
	parent := h.server.session(node.parentID)
	if parent == nil {
		return
	}
	notification, _ := json.Marshal(map[string]string{"agent_path": node.path, "status": status})
	var body strings.Builder
	fmt.Fprintf(&body, "<subagent_notification>\n%s\n</subagent_notification>", notification)
	notice := ""
	switch status {
	case agent.AgentCompleted:
		answer := strings.TrimSpace(session.LastAnswer())
		if answer == "" {
			answer = "(It finished its turn without giving a text answer.)"
		}
		fmt.Fprintf(&body, "\nFinal answer from %s:\n%s", node.path, answer)
		notice = i18n.D("✅ 子 agent {path} 做完了", "path", node.path)
	case agent.AgentInterrupted:
		notice = i18n.D("⏹ 子 agent {path} 被打断了", "path", node.path)
	default:
		fmt.Fprintf(&body, "\nError: %s", watcher.err)
		notice = i18n.D("⚠️ 子 agent {path} 出错了", "path", node.path)
	}
	running := parent.DeliverAgentMail(node.path, body.String(), notice)
	// 父 agent 闲着：开一轮让它接收结果，不然结果就搁在邮箱里没人看。被打断的不叫醒——
	// 那多半是用户在停整棵树。
	if !running && status != agent.AgentInterrupted {
		parentNode := h.node(parent)
		if parentNode.depth > 0 {
			h.setStatus(parentNode, agent.AgentRunning)
		}
		go h.runTurn(parent, parentNode, "")
	}
}

// turnWatcher 照常把事件发给宿主，顺手记下这一轮的结局。
type turnWatcher struct {
	emitter
	sessionID string
	err       string
}

func (w *turnWatcher) Notify(method string, params any) {
	if method == protocol.NotifyTurnCompleted {
		if completed, ok := params.(protocol.TurnNotification); ok && completed.SessionID == w.sessionID {
			w.err = completed.Error
		}
	}
	w.emitter.Notify(method, params)
}
