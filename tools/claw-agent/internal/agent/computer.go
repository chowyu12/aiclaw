package agent

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// Allowlisted Cua Driver tools. The host owns approvals, exact background
// window targets and turn-scoped MCP leases; no scripts or foreground fallback.
//
//go:embed computer-tools.json
var computerToolsJSON []byte

// Explicit per-turn restrictions override the default foreground fallback. This
// intentionally recognizes explicit prohibitions, not a mere preference for background.
type computerBackgroundOnlyKey struct{}
type computerActivityKey struct{}
type computerRememberAllowedKey struct{}

func computerRememberAllowed(text string) bool {
	t := strings.ToLower(text)
	for _, v := range []string{"记住", "记下来", "记到长期记忆", "remember this", "save to memory"} {
		if strings.Contains(t, v) {
			return true
		}
	}
	return false
}

func computerBackgroundOnly(text string) bool {
	t := strings.ToLower(text)
	for _, phrase := range []string{"不要自动切到前台", "不要切到前台", "不要切换前台", "不要切换我的前台", "不能切换前台", "禁止切换前台", "禁止前台", "严格后台", "仅后台", "只在后台", "后台执行，不要切", "background only", "background-only", "do not switch to the foreground", "do not bring", "never foreground"} {
		if strings.Contains(t, phrase) {
			return true
		}
	}
	return false
}

type computerToolSpec struct {
	Name        string                  `json:"name"`
	Action      protocol.ComputerAction `json:"action"`
	ReadOnly    bool                    `json:"readOnly"`
	Description string                  `json:"description"`
	Schema      json.RawMessage         `json:"schema"`
	Platforms   []string                `json:"platforms,omitempty"`
}

func computerToolSpecs(platform string) ([]computerToolSpec, error) {
	var specs []computerToolSpec
	if err := json.Unmarshal(computerToolsJSON, &specs); err != nil {
		return nil, err
	}
	result := make([]computerToolSpec, 0, len(specs))
	for _, spec := range specs {
		if len(spec.Platforms) > 0 && !slices.Contains(spec.Platforms, platform) {
			continue
		}

		result = append(result, spec)
	}
	return result, nil
}

func (s *Session) registerComputerTools() error {
	if !s.config.EnableComputerUse {
		return nil
	}
	specs, err := computerToolSpecs(runtime.GOOS)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		effect := tools.EffectExec
		if spec.ReadOnly {
			effect = tools.EffectRead
		}
		if err := s.registry.Register(tools.Tool{
			Name: spec.Name, Description: spec.Description, Schema: spec.Schema, Effect: effect,
			Handler: func(ctx context.Context, raw json.RawMessage, env *tools.Env) (string, error) {
				if err := validateComputerArgs(raw, spec.Schema); err != nil {
					return "", err
				}
				emitter := s.currentEmitter()
				if emitter == nil {
					return "", i18n.E("没有进行中的轮次，无法执行屏幕操作")
				}
				if active, ok := ctx.Value(computerActivityKey{}).(*atomic.Bool); ok {
					active.Store(true)
				}
				request := protocol.ComputerRequestParams{SessionID: env.SessionID, TurnID: env.TurnID, Action: spec.Action, Arguments: raw, BackgroundOnly: ctx.Value(computerBackgroundOnlyKey{}) == true}
				if err := env.RequestApproval(ctx, effect, protocol.ApprovalExec,
					i18n.D("屏幕操作 {action}", "action", string(spec.Action)), string(raw),
					"Operate the explicitly selected application through AIClaw Computer Use, outside the workspace.",
				); err != nil {
					return "", err
				}
				return s.runComputerAction(ctx, env, request, emitter)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Reject missing targets and unknown routing fields before requesting approval.
// Detailed action semantics and platform support remain the service's contract.
func validateComputerArgs(raw, schema json.RawMessage) error {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return i18n.E("参数不是合法 JSON 对象")
	}
	var contract struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &contract); err != nil {
		return err
	}
	for key := range args {
		if _, ok := contract.Properties[key]; !ok {
			return fmt.Errorf("unsupported computer argument: %s", key)
		}
	}
	for _, key := range contract.Required {
		if value, ok := args[key]; !ok || string(value) == "null" {
			return fmt.Errorf("missing computer argument: %s", key)
		}
	}
	for _, key := range []string{"pid", "window_id"} {
		if value, ok := args[key]; ok {
			var number int64
			if json.Unmarshal(value, &number) != nil || number <= 0 {
				return fmt.Errorf("%s must be a positive integer", key)
			}
		}
	}
	return nil
}

func (s *Session) runComputerAction(ctx context.Context, env *tools.Env, request protocol.ComputerRequestParams, emitter Emitter) (string, error) {
	result, err := emitter.RequestComputer(ctx, request)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Text) == "" {
		return "", fmt.Errorf("computer host returned an empty result; action outcome is unknown")
	}
	if request.Action == protocol.ComputerScreenshot && result.ImageBase64 == "" {
		// Tree-only observations are intentional when include_screenshot=false.
		var args struct {
			IncludeScreenshot *bool `json:"include_screenshot"`
		}
		_ = json.Unmarshal(request.Arguments, &args)
		if args.IncludeScreenshot == nil || *args.IncludeScreenshot {
			return "", fmt.Errorf("computer host returned no window screenshot")
		}
	}
	if result.ImageBase64 != "" {
		image, err := base64.StdEncoding.DecodeString(result.ImageBase64)
		if err != nil || len(image) == 0 {
			return "", fmt.Errorf("computer host returned invalid screenshot data")
		}
		env.Attach(image)
	}
	if result.VerificationFailed {
		return "", fmt.Errorf("%s", result.Text)
	}
	return result.Text, nil
}

func emptySchema() json.RawMessage { return schemaOf(map[string]any{}) }

func schemaOf(properties map[string]any, required ...string) json.RawMessage {
	body := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		body["required"] = required
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("构造 computer schema 失败：%v", err))
	}
	return encoded
}
