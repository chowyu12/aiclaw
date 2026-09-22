// Package agent 是自研的 Agent 循环：模型 → 工具调用 → 执行 → 回填 → 再问模型。
//
// 循环的设计读了 Codex 的 codex-rs/core 之后对齐过一遍，逐条取舍记在
// docs/agent-loop.md，包括几条**故意没有照搬**的。事件模型沿用它的
// 会话 / 轮次 / 条目三层。
//
// **本版本不做 OS 级沙箱**：工具直接在用户本机执行，路径约束与审批是
// 仅剩的防护，见 tools 包的说明。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/memory"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/skills"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/store"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// maxIterations 是一轮里模型最多连续调用工具的次数。
// 防止模型陷入「调工具 → 看结果 → 再调」的死循环把额度烧光。
const maxIterations = 40

// Emitter 把事件推给宿主。
type Emitter interface {
	Notify(method string, params any)
	// RequestApproval 向宿主要一次审批，阻塞直到用户决定。
	//
	// 返回完整回应而不只是 bool：用户可能选的是「本次会话这个目录都允许」。
	RequestApproval(
		ctx context.Context,
		params protocol.ApprovalRequestParams,
	) (protocol.ApprovalResponse, error)
	// RequestComputer 请宿主代做一次屏幕操作。截屏与输入都只有宿主做得了。
	RequestComputer(ctx context.Context, params protocol.ComputerRequestParams) (protocol.ComputerResult, error)
}

// userInput 是一次用户输入：文字，可能还带着图片。
type userInput struct {
	text   string
	images [][]byte
}

// Session 是一个会话：一份配置 + 一段对话历史 + 一套已挂载的工具。
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Title     string

	config   protocol.SessionStartParams
	messages []llm.Message
	registry *tools.Registry
	// mcpKeys 是这个会话从连接池里借走的那些连接。Close 时归还，
	// 最后一个用完的会话负责真正关掉它——见 mcppool.go。
	mcpKeys []string
	llm     *llm.Client
	// keyFor 按模型配置给出 Key，换模型时重建客户端要它。只在内存里，不进存档。
	keyFor KeyResolver

	mu        sync.Mutex
	turnCount int
	// cancelTurn 是当前轮次的取消函数；没有进行中的轮次时为 nil。
	cancelTurn context.CancelFunc
	// currentTurn 是进行中那一轮的 id，用于把排队的输入归到它名下。
	currentTurn string
	// emitter 是当轮的事件出口。computer use 的工具要靠它回调宿主，
	// 而工具是建会话时注册的、emitter 是每轮传进来的，所以存在这儿。
	emitter Emitter
	// pending 是轮次进行中用户又发来的输入（转向）。
	//
	// 不拒绝、不另起一轮，而是在下一次打模型之前插进历史——这是 Codex 的
	// input_queue 同款处理。拒绝的话用户得先等完一轮才能纠正方向，而他想
	// 纠正往往正是因为看见这一轮跑偏了。
	pending []userInput
	// backoff 给出第 n 次重试前的等待时长；测试把它换成零等待。
	backoff func(attempt int) time.Duration
	// lastInputTokens 是最近一次采样上游报的输入 token 数，压缩阈值据此判断。
	// 用上游报的而不是本地估算：估算只在上游不给 usage 时才有意义，而那种
	// 情况下不压缩也比按错误的估算乱压强。
	lastInputTokens int
	// 最近一次 MCP 挂载结果，起会话时回给宿主。
	mcpStatus map[string]string
	// skills 是本次会话可用的技能。只把名字与说明放进提示词，
	// 正文等模型调 load_skill 时才给——见 skills 包的说明。
	skills []skills.Skill
	// memory 是跨会话的长期记忆正文，起会话时读进来。
	memory string
	// shells 是这个会话的常驻 shell。会话结束时一并关掉——
	// 留着的话，用户关了对话，几个 shell 还在后台跑着。
	shells *tools.ShellPool
	// codeModeTokens 是代码模式换掉工具清单前后的 token 估算，给状态栏用。
	codeModeTokens [2]int
	// mcpMounted 记每个 server 挂了几个工具，好在代码模式装完之后
	// 把那几行状态重写一遍——见 foldStatusIntoExec。
	mcpMounted map[string]int
	// grants 是用户在本次会话里批准过的可写目录。
	//
	// **只活在内存里，不进存档**：下次打开这个会话应当重新问一次。
	// 一个批准过的目录跨天跨会话一直有效，等于一次点击换来一条永久的口子，
	// 而用户当时想的只是「这次让它写完」。
	grants []string
	// applied 是本次挂载用的那几项「跟着配置走」的东西。恢复会话时宿主会带上
	// 当前配置，与这份一比就知道要不要重挂——不比的话，已经在内核内存里的
	// 会话会一直用着挂载那一刻的配置。
	applied protocol.SessionRefresh
}

// refreshOf 取出配置里「跟着宿主配置走」的那几项。
func refreshOf(config protocol.SessionStartParams) protocol.SessionRefresh {
	return protocol.SessionRefresh{
		MCPServers:        config.MCPServers,
		SkillDirs:         config.SkillDirs,
		MemoryFile:        config.MemoryFile,
		EnableComputerUse: config.EnableComputerUse,
		DisableSandbox:    config.DisableSandbox,
		CodeMode:          config.CodeMode,
		ApprovalPolicy:    config.ApprovalPolicy,
	}
}

// KeyResolver 按模型配置给出 API Key。
//
// 它还可以**改写** model.BaseURL：填了 ProviderID 时端点以模型配置库里的为准，
// 传进来的值被盖掉。Key 只在这一步经手，不进存档、不进事件。
type KeyResolver func(model *protocol.ModelConfig) (string, error)

// StaticKey 是最简单的解析器：不管什么模型都用这一把 Key。给测试与
// 只走环境变量的场景用。
func StaticKey(key string) KeyResolver {
	return func(*protocol.ModelConfig) (string, error) { return key, nil }
}

// New 按配置创建会话：建模型客户端、装内置工具、挂 MCP server。
//
// MCP server 挂载失败不让整个会话起不来——那个 server 的工具缺席，
// 其余照常，失败原因放进 mcpStatus 让宿主展示。
func New(ctx context.Context, id string, config protocol.SessionStartParams, keyFor KeyResolver) (*Session, error) {
	// 工作区可以没有：那时相对路径按主目录解析，写之前一律问一句。
	// 早先这里是「没配工作目录就起不来」，而用户刚打开应用还没想好在哪儿干活，
	// 却被一个配置项挡在门外。
	if workspace := strings.TrimSpace(config.Workdir); workspace != "" {
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return nil, fmt.Errorf("创建工作区失败：%w", err)
		}
	}
	if config.ApprovalPolicy == "" {
		config.ApprovalPolicy = protocol.ApprovalOnWrite
	}

	if keyFor == nil {
		keyFor = StaticKey("")
	}
	apiKey, err := keyFor(&config.Model)
	if err != nil {
		return nil, err
	}
	client, err := llm.New(config.Model.BaseURL, apiKey, 0)
	if err != nil {
		return nil, err
	}

	registry := tools.NewRegistry()
	if err := registerBuiltins(registry, config.EnabledBuiltins); err != nil {
		return nil, err
	}

	session := &Session{
		ID:         id,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		config:     config,
		registry:   registry,
		llm:        client,
		keyFor:     keyFor,
		backoff:    backoffDelay,
		mcpStatus:  map[string]string{},
		mcpMounted: map[string]int{},
		shells:     tools.NewShellPool(),
	}

	// 按名字排序挂载，让工具在模型面前的顺序稳定可复现。
	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		session.mountMCP(ctx, name, config.MCPServers[name])
	}

	// 代码模式在技能与 computer use **之前**装：它要把注册表里已有的工具
	// 全部收进 exec，而 load_skill / computer_* 是之后才注册的——那几个
	// 留在外面是对的，模型读技能、点屏幕不需要写脚本。
	if config.CodeMode {
		if count := session.installCodeMode(); count > 0 {
			session.mcpStatus["代码模式"] = fmt.Sprintf(
				"已把 %d 个工具收进 exec（工具清单 %s → %s）",
				count,
				formatTokens(session.codeModeTokens[0]),
				formatTokens(session.codeModeTokens[1]),
			)
			session.foldStatusIntoExec()
		}
	}

	// 技能、记忆、computer use 都在系统提示词之前装好：提示词要列出它们。
	session.loadSkills(config.SkillDirs)
	session.loadMemory(config.MemoryFile)
	if err := session.registerComputerTools(); err != nil {
		return nil, err
	}

	// 系统提示词在工具挂载完之后再生成：它要列出实际可用的工具名与技能。
	session.messages = append(session.messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: buildSystemPrompt(config, registry, session.skills, session.memory),
	})
	session.applied = refreshOf(config)
	return session, nil
}

// loadSkills 扫描技能目录并注册 load_skill 工具。
//
// 一个技能都没有时不注册这个工具：给模型一个「永远返回没有技能」的工具，
// 只会让它反复去试。
func (s *Session) loadSkills(dirs []string) {
	if len(dirs) == 0 {
		return
	}
	loaded, err := skills.Load(dirs)
	if err != nil {
		s.mcpStatus["技能"] = "加载失败：" + err.Error()
		return
	}
	for _, skill := range loaded {
		if !skill.Disabled {
			s.skills = append(s.skills, skill)
		}
	}
	if len(s.skills) == 0 {
		return
	}
	// 与 MCP 工具同理：技能名与说明每轮都进系统提示词，说一声占了多少。
	// 自动发现之后技能会突然变多（Claude Code、Codex 那边本来就装了一堆），
	// 不说的话用户不会知道上下文是被这个吃掉的。
	promptBytes := 0
	for _, skill := range s.skills {
		promptBytes += len(skill.Name) + len([]rune(skill.Description)) + 8
	}
	s.mcpStatus["技能"] = fmt.Sprintf(
		"已加载 %d 个（提示词约占 %s）", len(s.skills), formatTokens(promptBytes*10/32),
	)
	if err := s.registry.Register(tools.Tool{
		Name: "load_skill",
		Description: "读取一个技能的完整说明。系统提示词里列出了可用技能的名字与用途，" +
			"判断某个技能适用时用这个工具把它的正文取出来，再照着做。",
		// 只读本机已有文件，不需要审批。
		Effect: tools.EffectRead,
		Schema: skillToolSchema(s.skills),
		Handler: func(_ context.Context, args json.RawMessage, _ *tools.Env) (string, error) {
			var input struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", errors.New("参数不是合法 JSON 对象")
			}
			for _, skill := range s.skills {
				if skill.Name == input.Name {
					// 带上目录：技能正文里常引用同目录下的脚本或模板，
					// 模型需要知道去哪儿找它们。
					return fmt.Sprintf("技能「%s」的说明（所在目录 %s）：\n\n%s",
						skill.Name, skill.Dir, skill.Body), nil
				}
			}
			return "", fmt.Errorf("没有名为 %q 的技能；可用技能：%s", input.Name, s.skillNames())
		},
	}); err != nil {
		s.mcpStatus["技能"] = "注册失败：" + err.Error()
	}
}

// loadMemory 读长期记忆并注册 remember 工具。
//
// 记忆文件在工作目录之外（它跨项目），所以不能走内置文件工具——
// 那些工具的路径一律收敛在工作目录内，这是没有沙箱之后仅剩的防护之一，
// 不能为了记忆开个口子。于是给一个只能写这一个文件的专用工具。
func (s *Session) loadMemory(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	text, err := memory.Load(path)
	if err != nil {
		s.mcpStatus["长期记忆"] = err.Error()
	}
	s.memory = strings.TrimSpace(text)

	if err := s.registry.Register(tools.Tool{
		Name: "remember",
		Description: "把一条需要**跨会话**记住的事实写进长期记忆：用户的偏好、" +
			"项目的约定、踩过的坑。只写结论，一句话；这里放的不是日志也不是原始内容。" +
			"当前记忆已经在系统提示词里，重复的不用再写。",
		// 按 external 而不是 write。
		//
		// EffectWrite 在 on-write 档位下**不弹审批**，因为内置文件工具的路径
		// 一律收敛在工作目录内，那种写是用户已经默许的。记忆文件不一样：
		// 它在工作目录之外，而且会进入**以后每一个会话**的系统提示词——
		// 一条写歪的记忆会长期影响模型的行为。这种代价该让用户看一眼。
		Effect: tools.EffectExternal,
		Schema: rememberSchema(),
		Handler: func(ctx context.Context, args json.RawMessage, env *tools.Env) (string, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", errors.New("参数不是合法 JSON 对象")
			}
			// 这里传的 Effect 才是决定档位的那个；Tool.Effect 只是元信息。
			if err := env.RequestApproval(
				ctx, tools.EffectExternal, protocol.ApprovalWrite,
				"写入长期记忆", input.Text, "这条会在以后每个会话里都带上",
			); err != nil {
				return "", err
			}
			updated, err := memory.Append(path, input.Text)
			if err != nil {
				return "", err
			}
			s.mu.Lock()
			s.memory = strings.TrimSpace(updated)
			s.mu.Unlock()
			return "已记住。", nil
		},
	}); err != nil {
		s.mcpStatus["长期记忆"] = "注册失败：" + err.Error()
	}
}

func rememberSchema() json.RawMessage {
	encoded, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "要记住的一句话。写结论，不写过程。",
			},
		},
		"required":             []string{"text"},
		"additionalProperties": false,
	})
	if err != nil {
		panic(fmt.Sprintf("构造 remember schema 失败：%v", err))
	}
	return encoded
}

// Memory 返回当前的长期记忆正文，起会话时回给宿主展示。
func (s *Session) Memory() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory
}

func (s *Session) skillNames() string {
	names := make([]string, 0, len(s.skills))
	for _, skill := range s.skills {
		names = append(names, skill.Name)
	}
	return strings.Join(names, "、")
}

// Skills 返回本次会话挂上的技能名，起会话时回给宿主展示。
func (s *Session) Skills() []string {
	names := make([]string, 0, len(s.skills))
	for _, skill := range s.skills {
		names = append(names, skill.Name)
	}
	return names
}

// skillToolSchema 把技能名做成枚举。
//
// 枚举而不是自由字符串：模型照着提示词里的名字抄，偶尔会抄错大小写或多个空格，
// 枚举能让它在生成阶段就对齐。
func skillToolSchema(list []skills.Skill) json.RawMessage {
	names := make([]string, 0, len(list))
	for _, skill := range list {
		names = append(names, skill.Name)
	}
	encoded, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string", "enum": names,
				"description": "技能名，取自系统提示词里的可用技能清单",
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	})
	if err != nil {
		panic(fmt.Sprintf("构造 load_skill schema 失败：%v", err))
	}
	return encoded
}

// ReadOnlyBuiltins 是不改任何东西的那几个内置工具名。
//
// 通道（微信、企业微信）来的消息不是本机用户说的，默认只给这些；
// 要放开写与执行由用户按会话逐个授权。列表从注册表算出来而不是写死，
// 免得新加一个只读工具时这里漏了。
func ReadOnlyBuiltins() []string {
	registry := tools.NewRegistry()
	if err := registerBuiltins(registry, nil); err != nil {
		return nil
	}
	var names []string
	for _, tool := range registry.List() {
		if tool.Effect == tools.EffectRead {
			names = append(names, tool.Name)
		}
	}
	return names
}

func registerBuiltins(registry *tools.Registry, enabled []string) error {
	if err := tools.RegisterFileTools(registry); err != nil {
		return err
	}
	if err := tools.RegisterExecTool(registry); err != nil {
		return err
	}
	if len(enabled) == 0 {
		return nil
	}
	// 有白名单时重建一遍只留白名单里的。
	allow := map[string]bool{}
	for _, name := range enabled {
		allow[name] = true
	}
	filtered := tools.NewRegistry()
	for _, tool := range registry.List() {
		if allow[tool.Name] {
			if err := filtered.Register(tool); err != nil {
				return err
			}
		}
	}
	*registry = *filtered
	return nil
}

func (s *Session) mountMCP(ctx context.Context, name string, config protocol.MCPServerConfig) {
	// 走连接池：同一份配置的 server 在会话之间共用，不然每开一个会话都要
	// 把「库里有哪些 API、契约长什么样」向内部平台重问一遍（实测每个 server
	// 十几次串行 HTTPS），切会话就卡在这儿。
	client, key, err := mcpShared.acquire(ctx, name, config)
	if err != nil {
		s.mcpStatus[name] = "挂载失败：" + err.Error()
		return
	}

	allow := map[string]bool{}
	for _, toolName := range config.EnabledTools {
		allow[toolName] = true
	}

	mounted := 0
	mountedTokens := 0
	var mountedDefs []mcpclient.ToolDef
	for _, def := range client.Tools() {
		if len(allow) > 0 && !allow[def.Name] {
			continue
		}
		// 工具名带上 server 前缀，两个 server 暴露同名工具时不会撞。
		qualified := name + "__" + def.Name
		toolName := def.Name
		mcp := client
		effect := mcpToolEffect(config.Trusted, def)
		err := s.registry.Register(tools.Tool{
			Name:        qualified,
			Description: def.Description,
			Schema:      def.InputSchema,
			Effect:      effect,
			Handler: func(ctx context.Context, args json.RawMessage, env *tools.Env) (string, error) {
				if err := env.RequestApproval(
					ctx, effect, protocol.ApprovalTool,
					"调用工具 "+toolName, string(args), def.Description,
				); err != nil {
					return "", err
				}
				return mcp.CallTool(ctx, toolName, args)
			},
		})
		if err != nil {
			s.mcpStatus[name] = "部分工具重名被跳过：" + err.Error()
			continue
		}
		mounted++
		mountedDefs = append(mountedDefs, def)
	}
	mountedTokens = estimateToolTokens(mountedDefs)
	s.mcpMounted[name] = mounted
	s.mcpKeys = append(s.mcpKeys, key)
	if _, failed := s.mcpStatus[name]; !failed {
		s.mcpStatus[name] = fmt.Sprintf(
			"已挂载 %d 个工具（约占 %s 上下文）", mounted, formatTokens(mountedTokens),
		)
	}
}

// estimateToolTokens 估一组工具在请求里占多少 token。
//
// 为什么要显示这个数：工具清单每次请求都整份重发，而压缩只动消息历史、
// **碰不到它**——它是开局就占掉的固定地板。挂多了不是「聊久了会满」，
// 是第一句话就发不出去，而且压缩救不了。早先这件事只有等上游报错才发现。
//
// 3.2 字符 ≈ 1 token 是中英混排 JSON 的粗估。这里要的是数量级，
// 不是精确值：4K 和 40K 的区别才是用户需要知道的。
func truncateRunes(text string, max int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

func estimateToolTokens(defs []mcpclient.ToolDef) int {
	bytes := 0
	for _, def := range defs {
		bytes += len(def.Name) + len(def.Description) + len(def.InputSchema)
		// 线格式的固定开销：{"type":"function","function":{...}} 那一圈。
		bytes += 48
	}
	return bytes * 10 / 32
}

func formatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK token", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d token", tokens)
}

// mcpToolEffect 决定一个 MCP 工具要不要走审批。
//
// 两个条件任一满足就不问：
//
//   - server 被标成可信（内部平台能力）：那些工具是内部平台按当前员工的权限授出来的，
//     准入已经在内部平台侧判过，再问一遍只是噪音——而噪音会把用户训练成闭眼点
//     「允许」，那比少问一次危险；
//   - 工具自己声明了 readOnlyHint（MCP 2025-03-26 的注解）：它不改任何东西。
//
// 两者是**事实与策略**的分工，不要合并：readOnlyHint 说的是「这个工具改不改
// 东西」，trusted 说的是「我们信不信这个来源」。靠把写操作标成只读来免审批，
// 会让 readOnlyHint 从此不可信，而判断第三方 server 还要靠它。
//
// 第三方 server 两个都不满足时落到 external——副作用未知就按有副作用处理，
// 这正是想要的默认。
func mcpToolEffect(trusted bool, def mcpclient.ToolDef) tools.Effect {
	if trusted || def.ReadOnly() {
		return tools.EffectRead
	}
	return tools.EffectExternal
}

func buildSystemPrompt(
	config protocol.SessionStartParams,
	registry *tools.Registry,
	available []skills.Skill,
	remembered string,
) string {
	var builder strings.Builder
	// 本地运行事实放最前面：模型不知道自己在谁的机器上、能碰哪个目录，
	// 就容易提出它其实做不到的操作。
	builder.WriteString("你运行在用户的本机电脑上，可以直接读写文件和执行命令。\n")
	// 当前时间写进提示词。模型只知道自己训练到什么时候，「最近三十期」「上个月」
	// 这类要求会算错，而且错得很自信、没有任何报错。这一行管住大多数情况；
	// 跨午夜或隔天再打开的会话由 current_time 工具兜住。
	fmt.Fprintf(&builder, "当前时间：%s。\n", tools.DescribeNow(time.Now()))
	// 说清楚「基准在哪、什么会被问」。模型不知道这两件事时，
	// 要么不敢碰工作区外面的文件，要么写一堆会被拦下的路径。
	if workspace := strings.TrimSpace(config.Workdir); workspace != "" {
		fmt.Fprintf(&builder,
			"会话工作区：%s。相对路径按它解析。**读文件不限于工作区**，"+
				"工作区之外的路径也能读（涉及凭据的目录除外）；写到工作区之外会先请用户确认。\n",
			workspace,
		)
	} else {
		// **必须把主目录的真实路径写出来。** 只说「按用户主目录解析」的话，
		// 模型会自己编一个——实测它编出了 /Users/bytedance，然后命令在一个
		// 不存在的目录里执行，报错还指向别处。
		fmt.Fprintf(&builder,
			"这个会话**没有设置工作区**，相对路径按用户主目录（%s）解析。"+
				"读文件不受限制（涉及凭据的目录除外）；任何写入都会先请用户确认。\n",
			userHome(),
		)
		if !config.DisableSandbox && tools.SandboxAvailable() {
			// 没有工作区时命令几乎什么都建不了，这件事要提前说，
			// 否则模型会一遍遍重试同一个 mkdir（实测就是这样）。
			builder.WriteString(
				"注意：没有工作区时，**命令无法在主目录里创建文件**（沙箱只放开临时目录与" +
					"工具链缓存）。需要新建东西时，先请用户在对话页顶部指定一个工作区，" +
					"不要改用临时目录绕过去。\n",
			)
		}
	}
	if !config.DisableSandbox && tools.SandboxAvailable() {
		builder.WriteString(
			"没有经过确认的命令跑在系统沙箱里：只能写工作区与临时目录，读不到凭据目录。" +
				"被拦下时不要反复重试同一条命令，换个落点或者告诉用户。\n",
		)
	}
	switch config.ApprovalPolicy {
	case protocol.ApprovalNever:
		builder.WriteString("当前为无人值守模式，没有人会回答确认请求；需要确认的操作会直接失败。\n")
	case protocol.ApprovalAlways:
		builder.WriteString("每个有副作用的操作都会先请用户确认。\n")
	default:
		builder.WriteString(
			"普通命令不需要确认；删除、提权、改系统设置这类命令，以及调用外部工具，会先请用户确认。\n",
		)
	}
	if registry.Len() > 0 {
		fmt.Fprintf(&builder, "可用工具：%s。\n", strings.Join(registry.Names(), "、"))
	}
	// 技能只列名字与用途，正文等 load_skill 取——十几个技能的正文加起来
	// 能有几万 token，每轮都带着走会把上下文挤没。
	if len(available) > 0 {
		builder.WriteString("\n可用技能（判断适用时先用 load_skill 取出完整说明再照做）：\n")
		for _, skill := range available {
			// 说明截断。技能现在是从 Claude Code、Codex、npm 等处一并发现的，
			// 一台机器上二十来个很正常，而有些技能的 description 写了七八百字——
			// 原样铺进提示词就是几千 token，每一轮都在付。判断「用不用得上」
			// 不需要那么多字，真要用的时候 load_skill 会给出全文。
			fmt.Fprintf(&builder, "- %s：%s\n", skill.Name, truncateRunes(skill.Description, 200))
		}
	}

	// 长期记忆原样进提示词。它是用户攒下来的事实，不做摘要也不做裁剪——
	// 会话上下文撑满时压缩的是对话历史，不动这一段。
	if remembered != "" {
		builder.WriteString("\n关于用户与当前工作的长期记忆（以前的会话里记下来的）：\n")
		builder.WriteString(remembered)
		builder.WriteString("\n")
	}

	if strings.TrimSpace(config.Instructions) != "" {
		builder.WriteString("\n")
		builder.WriteString(strings.TrimSpace(config.Instructions))
	}
	return builder.String()
}

// Configure 换掉会话正在用的模型。
//
// 在轮次进行中调用是安全的：改的是配置，下一次采样才会读到。改在半路上生效
// 反而更糟——同一轮里前半段用一个模型、后半段用另一个，出了问题没法复现。
//
// BaseURL 为空时沿用原来的：宿主只想换模型名时不必把端点再带一遍。
// SetWorkspace 改这个会话的工作区。空串表示清掉。
//
// 只影响之后的工具调用：已经发生的读写按当时的工作区判过了，改这里
// 不会、也不该回头去动它们。
func (s *Session) SetWorkspace(workspace string) error {
	trimmed := strings.TrimSpace(workspace)
	if trimmed != "" {
		info, err := os.Stat(trimmed)
		if err != nil {
			return fmt.Errorf("工作区不可用：%w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("工作区必须是一个目录：%s", trimmed)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Workdir = trimmed
	s.applied = refreshOf(s.config)
	// **提示词要跟着改。** 它是建会话那一刻生成的，里面写着「这个会话没有设置
	// 工作区、相对路径按主目录解析」——用户后来指了工作区，这段话就成了假的。
	// 实测里模型照着它说「在主目录里建目录会被沙箱拦住」，然后发现东西其实
	// 建到了工作区里，自己都愣了一下。恢复会话时已经会重新生成（见 Load），
	// 中途改工作区漏了这一步。
	s.refreshSystemPromptLocked()
	s.UpdatedAt = time.Now()
	return nil
}

// refreshSystemPromptLocked 用当前配置重新生成第一条系统提示词。调用方持锁。
//
// 只换第一条，历史一个字不动：那段话是「环境说明」，不是对话内容。
func (s *Session) refreshSystemPromptLocked() {
	if len(s.messages) == 0 || s.messages[0].Role != llm.RoleSystem {
		return
	}
	s.messages[0].Content = buildSystemPrompt(s.config, s.registry, s.skills, s.memory)
}

// Workspace 返回会话当前的工作区，空串表示没设置。
func (s *Session) Workspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Workdir
}

func (s *Session) Configure(model protocol.ModelConfig) error {
	if strings.TrimSpace(model.Model) == "" {
		return errors.New("模型名为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 没说换哪个服务就沿用现在这个：顶部切模型只给了模型名。
	if model.ProviderID == 0 && strings.TrimSpace(model.BaseURL) == "" {
		model.ProviderID = s.config.Model.ProviderID
		model.BaseURL = s.config.Model.BaseURL
	}
	// 每次都重建客户端：Key 与端点都可能已经变了，而 llm.New 只是构造，不连网。
	apiKey, err := s.keyFor(&model)
	if err != nil {
		return err
	}
	client, err := llm.New(model.BaseURL, apiKey, 0)
	if err != nil {
		return err
	}
	s.llm = client
	s.config.Model = model
	// 换了模型窗口就不一样了，旧的用量读数不能再拿来判断要不要压缩。
	s.lastInputTokens = 0
	s.UpdatedAt = time.Now()
	return nil
}

// Model 返回会话当前的模型配置。
func (s *Session) Model() protocol.ModelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Model
}

func (s *Session) currentEmitter() Emitter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emitter
}

// Tools 返回当前会话的工具名。
func (s *Session) Tools() []string { return s.registry.Names() }

// MCPStatus 返回各 MCP server 的挂载结果。
func (s *Session) MCPStatus() map[string]string { return s.mcpStatus }

// MountsMatch 报告这个会话挂载时用的配置是不是就是 want。
//
// 不一样就该卸掉重挂——内核会把会话留在内存里，而用户可能在这期间加了
// 一个 MCP server 或者同步到了新的内部平台能力。
func (s *Session) MountsMatch(want protocol.SessionRefresh) bool {
	return reflect.DeepEqual(s.applied, want)
}

// Grants 返回本次会话里批准过的可写目录。
func (s *Session) Grants() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.grants...)
}

// Grant 记下一条会话级授权。已经被覆盖的目录不重复记。
func (s *Session) Grant(dir string) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.grants {
		if existing == trimmed {
			return
		}
	}
	s.grants = append(s.grants, trimmed)
}

// Busy 报告有没有轮次正在跑。重挂之前要看它：跑到一半把会话卸了，
// 那一轮的事件就再也没有出口。
func (s *Session) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelTurn != nil
}

// Close 关闭所有 MCP 子进程。
func (s *Session) Close() {
	s.mu.Lock()
	cancel := s.cancelTurn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if s.shells != nil {
		s.shells.Close()
	}
	// 归还而不是直接关：同一条连接可能还有别的会话在用，
	// 最后一个归还的才真正关掉它。
	for _, key := range s.mcpKeys {
		mcpShared.release(key)
	}
	s.mcpKeys = nil
}

// Interrupt 取消进行中的轮次。没有进行中的轮次时无副作用。
func (s *Session) Interrupt() {
	s.mu.Lock()
	cancel := s.cancelTurn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---------- 持久化 ----------

type persisted struct {
	ID        string                      `json:"id"`
	Title     string                      `json:"title"`
	CreatedAt time.Time                   `json:"createdAt"`
	UpdatedAt time.Time                   `json:"updatedAt"`
	Config    protocol.SessionStartParams `json:"config"`
	Messages  []llm.Message               `json:"messages"`
	TurnCount int                         `json:"turnCount"`
}

// Save 把会话写进 SQLite 库。每轮结束都调一次。
//
// 存的是给模型看的原始消息，不是给人看的条目——恢复会话需要的是前者，
// 后者由 History() 从前者还原。
func (s *Session) Save(ctx context.Context, db *store.Store) error {
	s.mu.Lock()
	snapshot := persisted{
		ID:        s.ID,
		Title:     s.Title,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Config:    s.config,
		Messages:  s.messages,
		TurnCount: s.turnCount,
	}
	s.mu.Unlock()

	config, err := json.Marshal(snapshot.Config)
	if err != nil {
		return err
	}
	messages, err := json.Marshal(snapshot.Messages)
	if err != nil {
		return err
	}
	return db.Save(ctx, store.Session{
		ID:        snapshot.ID,
		Title:     snapshot.Title,
		CreatedAt: snapshot.CreatedAt,
		UpdatedAt: snapshot.UpdatedAt,
		// workdir 与 model 单独成列，让列表不必解析 config 就能显示和筛选。
		Workdir:   snapshot.Config.Workdir,
		Model:     snapshot.Config.Model.Model,
		TurnCount: snapshot.TurnCount,
		Config:    config,
		Messages:  messages,
	})
}

// Load 从库里恢复一个会话并重新挂载工具。
func Load(
	ctx context.Context,
	db *store.Store,
	id string,
	keyFor KeyResolver,
	refresh *protocol.SessionRefresh,
) (*Session, error) {
	record, err := db.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	var config protocol.SessionStartParams
	if err := json.Unmarshal(record.Config, &config); err != nil {
		return nil, fmt.Errorf("会话配置损坏：%w", err)
	}
	// 存档里的 MCP server、技能、记忆、computer use 开关是**建会话那一刻**的。
	// 而应用一启动就接着上次的会话：不拿当前配置盖掉的话，用户后来加的 MCP
	// server 和新装的技能永远挂不上，表现就是「配了没用」，
	// 而且他没有任何线索知道要去开个新会话。实际踩过。
	// 工作目录与模型不在这里面：换工作目录会让历史里的文件路径对不上；
	// 模型是会话自己的选择（端点由宿主用 session/configure 另行对齐）。
	if refresh != nil {
		config.MCPServers = refresh.MCPServers
		config.SkillDirs = refresh.SkillDirs
		config.MemoryFile = refresh.MemoryFile
		config.EnableComputerUse = refresh.EnableComputerUse
		config.DisableSandbox = refresh.DisableSandbox
		config.CodeMode = refresh.CodeMode
		if refresh.ApprovalPolicy != "" {
			config.ApprovalPolicy = refresh.ApprovalPolicy
		}
	}
	var messages []llm.Message
	if len(record.Messages) > 0 {
		if err := json.Unmarshal(record.Messages, &messages); err != nil {
			return nil, fmt.Errorf("会话历史损坏：%w", err)
		}
	}

	session, err := New(ctx, record.ID, config, keyFor)
	if err != nil {
		return nil, err
	}
	session.Title = record.Title
	session.CreatedAt = record.CreatedAt
	session.UpdatedAt = record.UpdatedAt
	session.turnCount = record.TurnCount
	if len(messages) > 0 {
		// 历史以存档为准，**但第一条系统提示词换成刚生成的那份**：
		// 上面按当前配置重挂了 MCP 与技能，而提示词里列的正是「你有哪些工具、
		// 哪些技能」。留着存档那份，模型会不知道刚挂上的工具能用，
		// 或者去调一个已经不在的技能。
		fresh := session.messages[0]
		session.messages = messages
		if session.messages[0].Role == llm.RoleSystem {
			session.messages[0] = fresh
		} else {
			session.messages = append([]llm.Message{fresh}, session.messages...)
		}
	}
	return session, nil
}

// Summary 是会话列表里的一行。
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Workdir   string    `json:"workdir"`
	TurnCount int       `json:"turnCount"`
	Model     string    `json:"model"`
	/** 搜索命中的那一小段正文。只有 Search 会填。 */
	Snippet string `json:"snippet,omitempty"`
}

// List 列出全部会话，按更新时间倒序。
func List(ctx context.Context, db *store.Store) ([]Summary, error) {
	return summarize(db.List(ctx))
}

// Search 按关键词找会话：标题与正文都找，命中的那一段放在 Snippet 里。
func Search(ctx context.Context, db *store.Store, keyword string) ([]Summary, error) {
	return summarize(db.Search(ctx, keyword, 0))
}

func summarize(records []store.Summary, err error) ([]Summary, error) {
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, Summary{
			ID:        record.ID,
			Title:     record.Title,
			CreatedAt: record.CreatedAt,
			UpdatedAt: record.UpdatedAt,
			Workdir:   record.Workdir,
			TurnCount: record.TurnCount,
			Model:     record.Model,
			Snippet:   record.Snippet,
		})
	}
	return summaries, nil
}

// Delete 删除会话。
func Delete(ctx context.Context, db *store.Store, id string) error {
	return db.Delete(ctx, id)
}
