package agent

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/tools"
)

type Tool interface {
	Name() string
	Description() string
	Call(ctx context.Context, input string) (string, error)
	IsConcurrencySafe() bool
}

type BuiltinHandler func(ctx context.Context, args string) (string, error)

type ToolRegistry struct {
	builtins    map[string]BuiltinHandler
	builtinDefs []model.Tool
}

func NewToolRegistry() *ToolRegistry {
	r := &ToolRegistry{builtins: make(map[string]BuiltinHandler)}
	for name, handler := range tools.DefaultBuiltins() {
		r.builtins[name] = handler
	}
	r.builtinDefs = tools.DefaultBuiltinDefs()
	return r
}

func (r *ToolRegistry) RegisterBuiltin(name string, handler BuiltinHandler) {
	r.builtins[name] = handler
}

// BuiltinDefs 返回所有内置工具的元数据定义，始终 Enabled。
func (r *ToolRegistry) BuiltinDefs() []model.Tool {
	return r.builtinDefs
}

func (r *ToolRegistry) BuildTrackedTools(toolDefs []model.Tool, tracker *StepTracker, toolSkillMap map[string]string) []Tool {
	var result []Tool
	for _, td := range toolDefs {
		if !td.Enabled {
			continue
		}
		baseTool := r.buildTool(td)
		if baseTool == nil {
			log.WithField("tool", td.Name).Warn("no handler found for tool, skipping")
			continue
		}
		result = append(result, &trackedTool{
			baseTool:  baseTool,
			name:      td.Name,
			skillName: toolSkillMap[td.Name],
			tracker:   tracker,
		})
	}
	return result
}

func (r *ToolRegistry) buildTool(td model.Tool) Tool {
	switch td.HandlerType {
	case model.HandlerBuiltin:
		handler, ok := r.builtins[td.Name]
		if !ok {
			return nil
		}
		return &dynamicTool{toolName: td.Name, toolDesc: td.Description, concurrencySafe: concurrencySafeBuiltins[td.Name], handler: handler}
	case model.HandlerHTTP:
		var cfg model.HTTPHandlerConfig
		if err := json.Unmarshal(td.HandlerConfig, &cfg); err != nil {
			log.WithError(err).WithField("tool", td.Name).Warn("[Tool] invalid http handler config, skipping")
			return nil
		}
		return &dynamicTool{
			toolName: td.Name,
			toolDesc: td.Description,
			handler:  tools.NewHTTPHandler(cfg, td.TimeoutSeconds()),
		}
	case model.HandlerCommand:
		var cfg model.CommandHandlerConfig
		if err := json.Unmarshal(td.HandlerConfig, &cfg); err != nil {
			log.WithError(err).WithField("tool", td.Name).Warn("[Tool] invalid command handler config, skipping")
			return nil
		}
		return &dynamicTool{
			toolName: td.Name,
			toolDesc: td.Description,
			handler:  tools.NewCommandHandler(cfg, td.TimeoutSeconds()),
		}
	case model.HandlerScript:
		log.WithField("tool", td.Name).Warn("handler_type script is not implemented; use builtin/command/http")
		return nil
	default:
		log.WithFields(log.Fields{"tool": td.Name, "handler_type": td.HandlerType}).Warn("unsupported handler type")
		return nil
	}
}

type trackedTool struct {
	baseTool  Tool
	name      string
	skillName string
	tracker   *StepTracker
}

func (t *trackedTool) Name() string            { return t.baseTool.Name() }
func (t *trackedTool) Description() string     { return t.baseTool.Description() }
func (t *trackedTool) IsConcurrencySafe() bool { return t.baseTool.IsConcurrencySafe() }
func (t *trackedTool) Call(ctx context.Context, input string) (string, error) {
	l := log.WithField("tool", t.name)
	if t.skillName != "" {
		l = l.WithField("skill", t.skillName)
	}
	l.WithField("input", truncateLog(input, 200)).Debug("[Tool]    invoke args")

	start := time.Now()
	output, err := t.baseTool.Call(ctx, input)
	duration := time.Since(start)

	status := model.StepSuccess
	errMsg := ""
	if err != nil {
		status = model.StepError
		errMsg = err.Error()
	}

	meta := &model.StepMetadata{
		ToolName:  t.name,
		SkillName: t.skillName,
	}
	if t.name == "sub_agent" {
		meta.SubAgentDepth = subAgentDepth(ctx) + 1
	}
	t.tracker.RecordStep(ctx, model.StepToolCall, t.name, input, output, status, errMsg, duration, 0, meta)
	return output, err
}

var _ Tool = (*trackedTool)(nil)

// concurrencySafeBuiltins 列出可并行执行的内置工具（只读/无副作用）。
var concurrencySafeBuiltins = map[string]bool{
	"current_time": true,
	"read":         true,
	"grep":         true,
	"find":         true,
	"ls":           true,
	"web_fetch":    true,
	"sub_agent":    true,
}

type dynamicTool struct {
	toolName        string
	toolDesc        string
	params          any
	concurrencySafe bool
	handler         func(ctx context.Context, input string) (string, error)
}

func (t *dynamicTool) Name() string        { return t.toolName }
func (t *dynamicTool) Description() string { return t.toolDesc }
func (t *dynamicTool) Call(ctx context.Context, input string) (string, error) {
	return t.handler(ctx, input)
}
func (t *dynamicTool) IsConcurrencySafe() bool { return t.concurrencySafe }

var _ Tool = (*dynamicTool)(nil)
