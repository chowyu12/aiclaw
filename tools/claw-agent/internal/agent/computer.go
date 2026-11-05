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

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// Allowlisted AIClaw native Computer Use IPC tools. The host owns approvals,
// per-turn observations and isolated connections; no arbitrary script or file output.
//
//go:embed computer-tools.json
var computerToolsJSON []byte

// Host policies and subscriptions are separate from the native app's action manifest.
//
//go:embed computer-host-tools.json
var computerHostToolsJSON []byte

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
	var hostSpecs []computerToolSpec
	if err := json.Unmarshal(computerHostToolsJSON, &hostSpecs); err != nil {
		return nil, err
	}
	specs = append(specs, hostSpecs...)
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
				request := protocol.ComputerRequestParams{SessionID: env.SessionID, TurnID: env.TurnID, Action: spec.Action, Arguments: raw}
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
	for _, key := range []string{"app", "observation_id"} {
		if value, ok := args[key]; ok {
			var text string
			if json.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
				return fmt.Errorf("%s must be a non-empty string", key)
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
