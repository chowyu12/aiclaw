package agent

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

// Window-scoped subset of Cua Driver 0.38.0's tool contract. The host also
// enforces this boundary: no arbitrary driver tools, desktop scope or file output.
//
//go:embed computer-tools.json
var computerToolsJSON []byte

type computerToolSpec struct {
	Name           string                  `json:"name"`
	Action         protocol.ComputerAction `json:"action"`
	ReadOnly       bool                    `json:"readOnly"`
	Description    string                  `json:"description"`
	Schema         json.RawMessage         `json:"schema"`
	PlatformFields map[string][]string     `json:"platformFields,omitempty"`
}

// Project optional native fields before exposing the schema to the model.
// GOOS uses "windows", matching the platform names in the pinned contract.
func computerToolSpecs(platform string) ([]computerToolSpec, error) {
	var specs []computerToolSpec
	if err := json.Unmarshal(computerToolsJSON, &specs); err != nil {
		return nil, err
	}
	for i := range specs {
		spec := &specs[i]
		if len(spec.PlatformFields) == 0 {
			continue
		}
		var schema struct {
			Type                 string                     `json:"type"`
			Properties           map[string]json.RawMessage `json:"properties"`
			AdditionalProperties bool                       `json:"additionalProperties"`
			Required             []string                   `json:"required,omitempty"`
		}
		if err := json.Unmarshal(spec.Schema, &schema); err != nil {
			return nil, err
		}
		for field, platforms := range spec.PlatformFields {
			supported := false
			for _, candidate := range platforms {
				if candidate == platform {
					supported = true
				}
			}
			if !supported {
				delete(schema.Properties, field)
			}
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			return nil, err
		}
		spec.Schema = raw
	}
	return specs, nil
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
					"Operate the explicitly selected application window, outside the workspace.",
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
// Detailed action semantics and platform support remain the driver's contract.
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
			var n int64
			if json.Unmarshal(value, &n) != nil || n <= 0 || n > 9007199254740991 {
				return fmt.Errorf("%s must be a positive safe integer", key)
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
