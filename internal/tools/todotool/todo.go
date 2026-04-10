package todotool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TodoItem 单个任务条目。
type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"` // pending, in_progress, completed, cancelled
}

type todoArgs struct {
	Action string     `json:"action"`
	Todos  []TodoItem `json:"todos,omitempty"`
	Merge  bool       `json:"merge,omitempty"`
}

type todoResult struct {
	Success bool       `json:"success"`
	Action  string     `json:"action"`
	Todos   []TodoItem `json:"todos"`
	Summary string     `json:"summary"`
	Error   string     `json:"error,omitempty"`
}

// convTodoKey 用于从 context 获取当前会话的 todo 存储。
type convTodoKey struct{}

// TodoStore per-conversation 的 todo 存储。
type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

func (s *TodoStore) Get() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]TodoItem, len(s.items))
	copy(cp, s.items)
	return cp
}

func (s *TodoStore) Set(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make([]TodoItem, len(items))
	copy(s.items, items)
}

func (s *TodoStore) Merge(updates []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := make(map[string]int, len(s.items))
	for i, item := range s.items {
		idx[item.ID] = i
	}

	for _, u := range updates {
		if pos, ok := idx[u.ID]; ok {
			if u.Content != "" {
				s.items[pos].Content = u.Content
			}
			if u.Status != "" {
				s.items[pos].Status = u.Status
			}
		} else {
			s.items = append(s.items, u)
			idx[u.ID] = len(s.items) - 1
		}
	}
}

// ── 全局注册表 ──

var (
	registryMu sync.RWMutex
	registry   = map[string]*TodoStore{}
)

// GetOrCreateStore 获取或创建 per-conversation 的 todo 存储。
func GetOrCreateStore(convUUID string) *TodoStore {
	registryMu.RLock()
	if s, ok := registry[convUUID]; ok {
		registryMu.RUnlock()
		return s
	}
	registryMu.RUnlock()

	registryMu.Lock()
	defer registryMu.Unlock()
	if s, ok := registry[convUUID]; ok {
		return s
	}
	s := &TodoStore{}
	registry[convUUID] = s
	return s
}

// FormatTodoBlock 将 todo 列表格式化为 system prompt 片段。
func FormatTodoBlock(items []TodoItem) string {
	if len(items) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 当前任务\n\n")

	statusIcon := map[string]string{
		"pending":     "[ ]",
		"in_progress": "[>]",
		"completed":   "[x]",
		"cancelled":   "[-]",
	}

	pending, inProg, done := 0, 0, 0
	for _, item := range items {
		icon := statusIcon[item.Status]
		if icon == "" {
			icon = "[ ]"
		}
		sb.WriteString(fmt.Sprintf("- %s %s (id: %s)\n", icon, item.Content, item.ID))
		switch item.Status {
		case "pending":
			pending++
		case "in_progress":
			inProg++
		case "completed", "cancelled":
			done++
		}
	}

	sb.WriteString(fmt.Sprintf("\n进度: %d/%d 完成", done, len(items)))
	if inProg > 0 {
		sb.WriteString(fmt.Sprintf(", %d 进行中", inProg))
	}
	if pending > 0 {
		sb.WriteString(fmt.Sprintf(", %d 待处理", pending))
	}
	return sb.String()
}

// WithTodoStore 将 TodoStore 注入 context。
func WithTodoStore(ctx context.Context, store *TodoStore) context.Context {
	return context.WithValue(ctx, convTodoKey{}, store)
}

func storeFromCtx(ctx context.Context) *TodoStore {
	if s, ok := ctx.Value(convTodoKey{}).(*TodoStore); ok {
		return s
	}
	return nil
}

// Handler 是 todo 工具入口。
func Handler(ctx context.Context, args string) (string, error) {
	var p todoArgs
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	store := storeFromCtx(ctx)
	if store == nil {
		return errJSON("create", "todo store not available in this context"), nil
	}

	switch p.Action {
	case "create":
		return handleCreate(store, p)
	case "update":
		return handleUpdate(store, p)
	case "read":
		return handleRead(store)
	case "clear":
		return handleClear(store)
	default:
		return errJSON(p.Action, fmt.Sprintf("unknown action %q, use: create, update, read, clear", p.Action)), nil
	}
}

func handleCreate(store *TodoStore, p todoArgs) (string, error) {
	if len(p.Todos) == 0 {
		return errJSON("create", "todos array is required"), nil
	}

	for i := range p.Todos {
		if p.Todos[i].Status == "" {
			p.Todos[i].Status = "pending"
		}
		if !validStatus(p.Todos[i].Status) {
			return errJSON("create", fmt.Sprintf("invalid status %q for todo %q", p.Todos[i].Status, p.Todos[i].ID)), nil
		}
	}

	if p.Merge {
		store.Merge(p.Todos)
	} else {
		store.Set(p.Todos)
	}

	items := store.Get()
	return okJSON("create", items, fmt.Sprintf("已创建 %d 个任务。", len(p.Todos))), nil
}

func handleUpdate(store *TodoStore, p todoArgs) (string, error) {
	if len(p.Todos) == 0 {
		return errJSON("update", "todos array is required"), nil
	}

	for _, t := range p.Todos {
		if t.Status != "" && !validStatus(t.Status) {
			return errJSON("update", fmt.Sprintf("invalid status %q for todo %q", t.Status, t.ID)), nil
		}
	}

	store.Merge(p.Todos)
	items := store.Get()
	return okJSON("update", items, fmt.Sprintf("已更新 %d 个任务。", len(p.Todos))), nil
}

func handleRead(store *TodoStore) (string, error) {
	items := store.Get()
	if len(items) == 0 {
		return okJSON("read", nil, "暂无任务。"), nil
	}
	return okJSON("read", items, fmt.Sprintf("共 %d 个任务。", len(items))), nil
}

func handleClear(store *TodoStore) (string, error) {
	store.Set(nil)
	return okJSON("clear", nil, "任务列表已清空。"), nil
}

func validStatus(s string) bool {
	switch s {
	case "pending", "in_progress", "completed", "cancelled":
		return true
	}
	return false
}

func okJSON(action string, items []TodoItem, summary string) string {
	if items == nil {
		items = []TodoItem{}
	}
	r := todoResult{Success: true, Action: action, Todos: items, Summary: summary}
	out, _ := json.Marshal(r)
	return string(out)
}

func errJSON(action, msg string) string {
	r := todoResult{Success: false, Action: action, Error: msg}
	out, _ := json.Marshal(r)
	return string(out)
}
