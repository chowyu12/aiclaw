package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// maxModelRetries 是一次采样最多重试几回。
//
// 四次配上下面的退避大约覆盖 15 秒，够扛过限流和上游抖动；再多用户已经
// 在盯着一个没动静的界面了，不如早点把失败说出来。
const maxModelRetries = 4

// RunTurn 跑一轮：接收用户输入，循环调模型与工具直到模型给出最终回答。
//
// 事件顺序固定为：turn/started → 若干 item/* → turn/completed。
// 任何路径退出都会发 turn/completed（成功或带 error），宿主据此收尾，
// 不用另猜「这轮到底结束没有」。
func (s *Session) RunTurn(
	parent context.Context,
	turnID, text string,
	images [][]byte,
	audioPaths []string,
	emitter Emitter,
) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.cancelTurn != nil {
		// 上一轮还在跑：把输入排进队列而不是拒绝。见 pending 字段的说明。
		s.pending = append(s.pending, userInput{text: text, images: images})
		s.mu.Unlock()
		cancel()
		return
	}
	s.cancelTurn = cancel
	s.currentTurn = turnID
	s.emitter = emitter
	s.turnCount++
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.cancelTurn = nil
		s.currentTurn = ""
		s.emitter = nil
		s.UpdatedAt = time.Now()
		s.mu.Unlock()
	}()

	emitter.Notify(protocol.NotifyTurnStarted, protocol.TurnNotification{SessionID: s.ID, TurnID: turnID})

	// 音频先转成文字：模型读不了音频，而用户发过来就是希望它「听」到。
	// 转写接在正文后面，界面上的那条消息仍是用户原话。
	if transcript := s.transcribeAttached(ctx, audioPaths); transcript != "" {
		text += transcript
	}
	s.acceptUserInput(ctx, turnID, text, images, emitter)

	usage, err := s.loop(ctx, turnID, emitter)

	completed := protocol.TurnNotification{SessionID: s.ID, TurnID: turnID, Usage: &usage}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.recordInterruption()
			completed.Error = "已中断"
		} else {
			completed.Error = err.Error()
		}
	}
	emitter.Notify(protocol.NotifyTurnCompleted, completed)
}

// acceptUserInput 把一条用户输入同时交给宿主和模型历史。
//
// 对话模型不认图时，图先由视觉模型转成文字接在消息后面——时间线上仍然显示
// 原图（用户发的就是图），进模型历史的是转述。不转的话那几张图要么被上游
// 拒绝（一句看不懂的报错），要么被静默忽略（模型答非所问，而用户以为它看见了）。
func (s *Session) acceptUserInput(ctx context.Context, turnID, text string, images [][]byte, emitter Emitter) {
	images = limitImages(images)
	// 用户消息本身也是一个条目，让宿主的时间线和模型看到的历史一致。
	emitter.Notify(protocol.NotifyItemCompleted, protocol.ItemNotification{
		SessionID: s.ID, TurnID: turnID,
		Item: protocol.Item{
			ID: newID("user"), Kind: protocol.ItemUserMessage, Text: text, Images: images,
		},
	})
	if len(images) > 0 && !s.config.ModelSeesImages {
		if described := s.describeImages(ctx, images); described != "" {
			s.appendMessage(llm.Message{Role: llm.RoleUser, Content: text + described})
			if s.Title == "" {
				s.Title = firstLine(text, 40)
			}
			return
		}
	}
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: text, Images: images})
	if s.Title == "" {
		s.Title = firstLine(text, 40)
	}
}

// Enqueue 在有轮次进行中时把输入排队，返回 true 与那一轮的 id。
//
// 排队的输入不带音频：它们在下一次打模型之前被插进历史，而转写要打一次网络，
// 卡在那里会让正在跑的这一轮停住。宿主在有音频时不走排队（见 handleTurnStart）。
//
// 没有进行中的轮次时返回 false，调用方按常规起新一轮。
func (s *Session) Enqueue(text string, images [][]byte) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelTurn == nil {
		return "", false
	}
	s.pending = append(s.pending, userInput{text: text, images: images})
	return s.currentTurn, true
}

// limitImages 兜住图片的张数与大小。
//
// 缩放是宿主的活（它有 canvas），这里只防「宿主没缩、或者调用方不是宿主」：
// 一张没缩过的 4K 截图 base64 之后好几 MB，进了历史就每一轮都重发一遍，
// 还要原样写进会话库。超限的整张丢掉而不是截断——截断的图片解不出来，
// 上游只会回一个看不懂的 400。
func limitImages(images [][]byte) [][]byte {
	const (
		maxCount = 4
		maxBytes = 4 << 20
		maxTotal = 8 << 20
	)
	kept := make([][]byte, 0, len(images))
	total := 0
	for _, image := range images {
		if len(image) == 0 || len(image) > maxBytes || len(kept) >= maxCount {
			continue
		}
		if total+len(image) > maxTotal {
			break
		}
		total += len(image)
		kept = append(kept, image)
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// userHome 取用户主目录。取不到就空着——Env 那边会自己再试一次。
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// drainPending 把排队的输入插进历史，在下一次打模型之前调用。
func (s *Session) drainPending(ctx context.Context, turnID string, emitter Emitter) {
	s.mu.Lock()
	queued := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, input := range queued {
		s.acceptUserInput(ctx, turnID, input.text, input.images, emitter)
	}
}

func (s *Session) loop(ctx context.Context, turnID string, emitter Emitter) (protocol.Usage, error) {
	var total protocol.Usage
	// 步骤序号按轮次重置：用户看的是「这一轮做了几步」，不是自会话开始以来的累计。
	steps := &stepCounter{}
	env := &tools.Env{
		Workspace:      s.config.Workdir,
		Home:           userHome(),
		ProtectedPaths: s.config.ProtectedPaths,
		Sandbox:        !s.config.DisableSandbox,
		Grants:         s.Grants,
		Grant:          s.Grant,
		Shells:         s.shells,
		Policy:         s.config.ApprovalPolicy,
		Approve:        emitter.RequestApproval,
		SessionID:      s.ID,
		TurnID:         turnID,
	}

	for iteration := 0; iteration < maxIterations; iteration++ {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}

		// 转向输入必须在采样之前插进去，不然模型这一次看到的还是旧要求，
		// 用户会觉得「我都说了别改那个文件了它还在改」。
		s.drainPending(ctx, turnID, emitter)

		// 主动压缩：等上游报超窗再压要白花一次请求，而那一次请求恰恰是
		// 历史最长、最贵的一次。
		if s.needsCompaction() {
			if err := s.compact(ctx, turnID, emitter); err != nil && ctx.Err() != nil {
				return total, ctx.Err()
			}
		}

		response, err := s.sample(ctx, turnID, emitter, &total, steps, iteration+1)
		if err != nil {
			// 采样失败时历史里没留下半截 assistant 消息，原样重发即可。
			return total, err
		}

		s.appendMessage(llm.Message{
			Role:      llm.RoleAssistant,
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})

		// finish_reason=length 是「输出撞上长度上限」的权威信号，比看参数结尾准。
		// 没有工具调用时是回答被截断，说给用户听；有工具调用时最后那条的参数
		// 不完整，让它的结果说明原因——即便截断处恰好凑成了合法 JSON。
		cut := response.FinishReason == "length"
		if cut && len(response.ToolCalls) == 0 {
			s.notify(emitter, turnID, "回答在模型的输出长度上限处被截断了，后面的内容没有生成。可以让它接着说，或把问题拆小。")
		}

		if len(response.ToolCalls) == 0 {
			// 没有工具调用就是这一轮说完了——除非用户在期间又发了话，
			// 那就接着跑，别让他等下一次回车。
			if s.hasPending() {
				continue
			}
			return total, nil
		}

		toolCtx := ctx
		if cut {
			toolCtx = context.WithValue(ctx, truncatedCallKey{}, response.ToolCalls[len(response.ToolCalls)-1].ID)
		}
		// 每个 tool_call 都必须有一条对应的结果消息，中断也不例外——
		// 缺一条下一次请求就是 400。executeTools 因此保证返回等长的结果。
		for _, result := range s.executeTools(toolCtx, turnID, response.ToolCalls, env, emitter, steps) {
			s.appendMessage(result)
		}
		// 工具产出的图片（截屏）作为紧随其后的一条 user 消息送进去。
		// Chat Completions 的 tool 消息必须是纯字符串，图没地方放；
		// 而模型看不到截图的话，computer use 就只是在瞎点。
		if images := env.TakeAttachments(); len(images) > 0 {
			s.appendMessage(llm.Message{
				Role:    llm.RoleUser,
				Content: "（上一步截屏的画面）",
				Images:  images,
			})
		}
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
	return total, fmt.Errorf("单轮内工具调用超过 %d 次，已停止；请把任务拆小", maxIterations)
}

// sample 打一次模型，按失败的类型决定重试、压缩还是直接放弃。
//
// 三条分支对应三种不同的病因，混在一起处理会两头不讨好：超窗重试多少次
// 都是同一个错，而网络抖动压缩历史也救不了。
// stepCounter 发轮次内的步骤序号。并发安全：一批工具是并行执行的。
type stepCounter struct {
	mu sync.Mutex
	n  int
}

func (c *stepCounter) next() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (s *Session) sample(
	ctx context.Context,
	turnID string,
	emitter Emitter,
	total *protocol.Usage,
	steps *stepCounter,
	round int,
) (llm.Response, error) {
	compacted := false
	for attempt := 1; ; attempt++ {
		response, err := s.callModel(ctx, turnID, emitter, steps, round)
		total.InputTokens += response.Usage.InputTokens
		total.OutputTokens += response.Usage.OutputTokens
		total.TotalTokens += response.Usage.TotalTokens
		if err == nil {
			s.mu.Lock()
			s.lastInputTokens = response.Usage.InputTokens
			s.mu.Unlock()
			return response, nil
		}
		// 中断优先于一切：用户点了停止就别再重试了。
		if ctx.Err() != nil {
			return response, ctx.Err()
		}

		var apiErr *llm.Error
		isAPIErr := errors.As(err, &apiErr)
		switch {
		case isAPIErr && apiErr.ContextWindowExceeded():
			// 历史塞不下。先压一次；压过还超就从头丢，直到丢无可丢。
			if !compacted {
				s.notify(emitter, turnID, "历史超出模型上下文，正在压缩后重试。")
				if cerr := s.compact(ctx, turnID, emitter); cerr != nil {
					return response, err
				}
				compacted = true
				continue
			}
			if !s.dropOldest() {
				return response, err
			}
			continue
		case isAPIErr && !apiErr.Retryable():
			return response, err
		case attempt > maxModelRetries:
			return response, err
		}

		s.notify(emitter, turnID, fmt.Sprintf(
			"模型调用失败，正在重试（%d/%d）：%s", attempt, maxModelRetries, err.Error(),
		))
		if werr := sleepCtx(ctx, s.backoff(attempt)); werr != nil {
			return response, werr
		}
	}
}

// backoffDelay 是第 n 次重试前等待的时长：1s、2s、4s、8s，封顶 30s，带抖动。
//
// 抖动不是讲究：限流往往是多个会话同时撞上的，不加抖动它们会一起醒来
// 再一起被限，整齐地反复失败。
func backoffDelay(attempt int) time.Duration {
	const (
		base = time.Second
		max  = 30 * time.Second
	)
	delay := base << (attempt - 1)
	if delay > max || delay <= 0 {
		delay = max
	}
	jitter := time.Duration(rand.Int63n(int64(delay / 4)))
	return delay - delay/8 + jitter
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// callModel 打一次模型并把正文增量流给宿主。
func (s *Session) callModel(
	ctx context.Context,
	turnID string,
	emitter Emitter,
	steps *stepCounter,
	round int,
) (llm.Response, error) {
	itemID := newID("msg")
	started := false
	var streamed strings.Builder

	// 这次采样本身也是一个执行步骤。先把它标出来，用户才看得见「正在等模型」
	// 与「正在跑工具」的区别——两者的耗时经常差两个数量级。
	llmItemID := newID("llm")
	llmStart := time.Now()
	llmSeq := steps.next()
	var ttft, thinkStart time.Duration
	var thinking time.Duration
	var reasoning strings.Builder

	emitter.Notify(protocol.NotifyItemStarted, protocol.ItemNotification{
		SessionID: s.ID, TurnID: turnID,
		Item: protocol.Item{
			ID: llmItemID, Kind: protocol.ItemLLM,
			Summary: s.config.Model.Model, Model: s.config.Model.Model,
			Seq: llmSeq, Round: round, StartedAt: llmStart.UnixMilli(),
		},
	})

	ensureStarted := func() {
		if started {
			return
		}
		started = true
		emitter.Notify(protocol.NotifyItemStarted, protocol.ItemNotification{
			SessionID: s.ID, TurnID: turnID,
			Item: protocol.Item{ID: itemID, Kind: protocol.ItemAgentMessage},
		})
	}
	completeMessage := func(text string) {
		ensureStarted()
		emitter.Notify(protocol.NotifyItemCompleted, protocol.ItemNotification{
			SessionID: s.ID, TurnID: turnID,
			Item: protocol.Item{ID: itemID, Kind: protocol.ItemAgentMessage, Text: text},
		})
	}

	var temperature *float64
	if s.config.Model.Temperature > 0 {
		value := s.config.Model.Temperature
		temperature = &value
	}

	response, err := s.llm.Stream(ctx, llm.Request{
		Model:           s.config.Model.Model,
		Messages:        s.snapshotMessages(),
		Tools:           s.llmTools(),
		Temperature:     temperature,
		MaxTokens:       s.config.Model.MaxTokens,
		ReasoningEffort: s.config.Model.ReasoningEffort,
	}, func(delta llm.Delta) {
		// 首字节：正文与推理哪个先来都算。慢在等模型还是慢在生成，
		// 是两种完全不同的问题。
		if ttft == 0 && (delta.Content != "" || delta.Reasoning != "") {
			ttft = time.Since(llmStart)
		}
		if delta.Content != "" {
			// 正文一开始，推理就结束了。
			if thinkStart != 0 && thinking == 0 {
				thinking = time.Since(llmStart) - thinkStart
			}
			ensureStarted()
			streamed.WriteString(delta.Content)
			emitter.Notify(protocol.NotifyItemDelta, protocol.ItemDeltaNotification{
				SessionID: s.ID, TurnID: turnID, ItemID: itemID, Delta: delta.Content,
			})
		}
		if delta.Reasoning != "" {
			if thinkStart == 0 {
				thinkStart = time.Since(llmStart)
			}
			reasoning.WriteString(delta.Reasoning)
			// 推理增量挂在 llm 条目上：它是这次采样的一部分，不是独立一步。
			emitter.Notify(protocol.NotifyItemDelta, protocol.ItemDeltaNotification{
				SessionID: s.ID, TurnID: turnID, ItemID: llmItemID, Delta: delta.Reasoning,
			})
		}
	})

	if thinkStart != 0 && thinking == 0 {
		thinking = time.Since(llmStart) - thinkStart
	}
	usage := response.Usage
	emitter.Notify(protocol.NotifyItemCompleted, protocol.ItemNotification{
		SessionID: s.ID, TurnID: turnID,
		Item: protocol.Item{
			ID: llmItemID, Kind: protocol.ItemLLM,
			Summary: s.config.Model.Model, Model: s.config.Model.Model,
			Text:       strings.TrimSpace(reasoning.String()),
			Seq:        llmSeq,
			Round:      round,
			StartedAt:  llmStart.UnixMilli(),
			DurationMS: time.Since(llmStart).Milliseconds(),
			TTFTMS:     ttft.Milliseconds(),
			ThinkMS:    thinking.Milliseconds(),
			ToolCalls:  len(response.ToolCalls),
			Usage: &protocol.Usage{
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				TotalTokens:  usage.TotalTokens,
			},
			ToolFailed: err != nil,
		},
	})
	if err != nil {
		// 流断在半截时已经有文字发给宿主了。把那个条目收尾成它实际收到的
		// 内容，否则界面上会永远留一条转着圈的半截消息；重试产生的是
		// 一个新条目，用户看到的顺序是「半截 → 重试提示 → 完整回答」。
		if started {
			completeMessage(streamed.String())
		}
		return response, err
	}
	// 只有产生了正文才发 completed；纯工具调用的轮次没有正文条目。
	if started || response.Content != "" {
		completeMessage(response.Content)
	}
	return response, nil
}

// executeTools 执行一批工具调用，按模型给出的顺序返回等长的结果。
//
// 并发靠一把读写锁控制，这是 Codex 的做法：只读工具拿读锁，可以彼此并行；
// 有副作用的工具拿写锁，于是它既不会和别的写并行，也不会和正在跑的读并行。
// 用一把锁而不是「读的并行、写的串行」两条路，是因为后者挡不住
// 「read_file 和 write_file 同时落在一个文件上」。
//
// 顺序不能乱：Chat Completions 要求 tool 结果与 tool_calls 一一对应，
// 顺序错了部分上游会拒收。
func (s *Session) executeTools(
	ctx context.Context,
	turnID string,
	calls []llm.ToolCall,
	env *tools.Env,
	emitter Emitter,
	steps *stepCounter,
) []llm.Message {
	results := make([]llm.Message, len(calls))
	var gate sync.RWMutex
	var wg sync.WaitGroup
	for index, call := range calls {
		wg.Add(1)
		go func(index int, call llm.ToolCall) {
			defer wg.Done()
			parallel := s.callIsParallelSafe(call)
			if parallel {
				gate.RLock()
				defer gate.RUnlock()
			} else {
				gate.Lock()
				defer gate.Unlock()
			}
			results[index] = s.executeOne(ctx, turnID, call, env, emitter, steps)
		}(index, call)
	}
	wg.Wait()
	return results
}

// callIsParallelSafe 查这次调用的工具能不能并发跑。未知的工具按不能算——
// 它马上就会以「没有这个工具」失败，不值得为它放开闸门。
func (s *Session) callIsParallelSafe(call llm.ToolCall) bool {
	tool, ok := s.registry.Get(call.Name)
	return ok && tool.ParallelSafe()
}

func (s *Session) executeOne(
	ctx context.Context,
	turnID string,
	call llm.ToolCall,
	env *tools.Env,
	emitter Emitter,
	steps *stepCounter,
) llm.Message {
	itemID := newID("tool")
	// 序号在真正开跑之前取，而不是在拿到锁之后：用户看到的顺序应当是模型
	// 决定调用的顺序，不是它们碰巧抢到执行权的顺序。
	started := time.Now()
	item := protocol.Item{
		ID:        itemID,
		Kind:      protocol.ItemToolCall,
		ToolName:  call.Name,
		ToolArgs:  call.Arguments,
		Summary:   summarizeCall(call),
		Seq:       steps.next(),
		StartedAt: started.UnixMilli(),
	}
	emitter.Notify(protocol.NotifyItemStarted, protocol.ItemNotification{SessionID: s.ID, TurnID: turnID, Item: item})

	// 产出物按调用收集：一轮里的工具可能并发跑，挂在 Env 上会串。
	artifacts := &tools.ArtifactSink{}
	output, err := s.runTool(tools.WithArtifacts(ctx, artifacts), call, env)
	item.Artifacts = artifacts.Paths()
	if err != nil {
		item.ToolFailed = true
		item.ToolResult = err.Error()
		// 失败原因回给模型，它据此改参数重试；这和 MCP 的 isError 语义一致。
		output = "错误：" + err.Error()
	} else {
		item.ToolResult = output
	}
	item.DurationMS = time.Since(started).Milliseconds()
	emitter.Notify(protocol.NotifyItemCompleted, protocol.ItemNotification{SessionID: s.ID, TurnID: turnID, Item: item})

	// 截断只作用于进历史的副本：界面上留的是工具实际返回的内容。
	return llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: truncateForHistory(output)}
}

// truncatedCallKey 标记「这一批里哪条调用的参数被长度上限截断了」。
// 用 context 传而不是挂在 Session 上：一批工具可能并发跑，字段会串。
type truncatedCallKey struct{}

func (s *Session) runTool(ctx context.Context, call llm.ToolCall, env *tools.Env) (string, error) {
	// 取消之后仍然会走到这里（一批调用里排在后面的那些）。提前退出，
	// 但仍旧返回一条结果，让历史保持完整。
	if ctx.Err() != nil {
		return "", errors.New("用户中断，这次调用未执行")
	}
	if id, _ := ctx.Value(truncatedCallKey{}).(string); id != "" && id == call.ID {
		return "", fmt.Errorf(
			"这次输出在第 %d 个字符处撞上了模型的长度上限（finish_reason=length），这条调用的参数不完整，没有执行。"+
				"把这一步拆成几次调用：先产出一部分、用 store() 存着，或者让工具自己去读文件而不是把内容写进参数。",
			len([]rune(call.Arguments)),
		)
	}
	tool, ok := s.registry.Get(call.Name)
	if !ok {
		return "", fmt.Errorf("没有名为 %q 的工具；可用工具：%s", call.Name, strings.Join(s.registry.Names(), "、"))
	}
	var args json.RawMessage
	if strings.TrimSpace(call.Arguments) == "" {
		args = json.RawMessage(`{}`)
	} else {
		args = json.RawMessage(call.Arguments)
		if !json.Valid(args) {
			return "", errors.New(tools.ExplainBadArguments(call.Arguments, errors.New("json 解析失败")))
		}
	}
	// 单个工具的墙钟上限。exec 有自己更短的超时，这里防的是没有自身上限的工具
	//（比如一个卡住的 MCP server）把整轮拖死。
	toolCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return tool.Handler(toolCtx, args, env)
}

// recordInterruption 在用户中断之后把历史补成可以继续用的形状。
//
// 两件事，缺一不可：
//  1. 给没有结果的 tool_call 补上结果。Chat Completions 要求一一对应，
//     少一条下一轮开口就是 400，会话直接废掉；
//  2. 写一句中断说明，否则模型下一轮会把那些半截结果当成正常完成的操作。
func (s *Session) recordInterruption() {
	s.mu.Lock()
	defer s.mu.Unlock()

	answered := map[string]bool{}
	for _, message := range s.messages {
		if message.Role == llm.RoleTool {
			answered[message.ToolCallID] = true
		}
	}
	var missing []llm.Message
	for _, message := range s.messages {
		if message.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if answered[call.ID] {
				continue
			}
			answered[call.ID] = true
			missing = append(missing, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Content:    "错误：用户中断，这次调用未完成。",
			})
		}
	}
	s.messages = append(s.messages, missing...)
	s.messages = append(s.messages, llm.Message{Role: llm.RoleUser, Content: interruptMarker})
}

func (s *Session) hasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) > 0
}

func (s *Session) llmTools() []llm.Tool {
	list := s.registry.List()
	out := make([]llm.Tool, 0, len(list))
	for _, tool := range list {
		out = append(out, llm.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Schema,
		})
	}
	return out
}

func (s *Session) appendMessage(message llm.Message) {
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
}

func (s *Session) snapshotMessages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.Message(nil), s.messages...)
}

// summarizeCall 给工具调用一句人能看的摘要，宿主直接显示。
//
// 可信 server 的工具不弹审批之后，这里是用户唯一能看见「它到底调了什么」的地方，
// 所以认不出来的工具也要给出点东西：把参数压成一行，总比只显示一个
// api_operation_87 强。
func summarizeCall(call llm.ToolCall) string {
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	pick := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := args[key].(string); ok && value != "" {
				return value
			}
		}
		return ""
	}
	switch call.Name {
	case "run_command":
		return firstLine(pick("command"), 120)
	case "read_file", "write_file", "edit_file", "list_dir":
		return pick("path")
	case "search_files":
		return pick("pattern")
	case "exec":
		// 代码模式下参数是整段脚本。压成一行的话，步骤标题会变成一坨
		// 带着 \n 的代码；只取第一行有内容的，完整脚本在展开的详情里。
		return firstLine(strings.TrimSpace(pick("code")), 100)
	default:
		if detail := pick("query", "keyword", "path", "name", "text", "q"); detail != "" {
			return detail
		}
		return compactArgs(args)
	}
}

// compactArgs 把参数压成「键=值」一行。
//
// 键排序，让同一个工具每次显示的顺序一致——顺序乱跳会让人以为参数变了。
func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, args[key]))
	}
	return firstLine(strings.Join(parts, " "), 120)
}

func firstLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

var idCounter struct {
	sync.Mutex
	n int64
}

func newID(prefix string) string {
	idCounter.Lock()
	idCounter.n++
	n := idCounter.n
	idCounter.Unlock()
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixMilli(), n)
}
