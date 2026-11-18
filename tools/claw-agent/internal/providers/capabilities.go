package providers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// Each nonempty field has its own authority. Explicit unsupported values never
// turn back into unknown just because a higher-priority source omits a field.
func mergeCapabilities(dst *model.ModelCapabilities, evidence map[string]protocol.CapabilitySource, src model.ModelCapabilities, source protocol.CapabilitySource) {
	target, input := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src)
	for i := 0; i < input.NumField(); i++ {
		if input.Field(i).IsZero() {
			continue
		}
		target.Field(i).Set(input.Field(i))
		name := strings.Split(input.Type().Field(i).Tag.Get("json"), ",")[0]
		evidence[name] = source
	}
}
func capabilityOverrides(p *model.Provider) map[string]model.ModelCapabilities {
	result := map[string]model.ModelCapabilities{}
	_ = json.Unmarshal(p.CapabilityOverrides, &result)
	return result
}
func validateCapabilityOverrides(values map[string]model.ModelCapabilities) (model.JSON, error) {
	for name, cap := range values {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
			return nil, fmt.Errorf("invalid capability model name")
		}
		for _, flag := range []string{cap.Tools, cap.Vision, cap.AudioInput, cap.Reasoning} {
			if flag != "" && flag != "supported" && flag != "unsupported" {
				return nil, fmt.Errorf("capabilities must be supported, unsupported or unknown")
			}
		}
		if cap.APIProtocol != "" && !cap.APIProtocol.Valid() || cap.ContextWindow < 0 || cap.ContextWindow > 100_000_000 {
			return nil, fmt.Errorf("invalid protocol or context window")
		}
		for _, effort := range strings.Split(cap.ReasoningEfforts, ",") {
			switch effort {
			case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
			default:
				return nil, fmt.Errorf("invalid reasoning effort: %s", effort)
			}
		}
	}
	raw, err := json.Marshal(values)
	return model.JSON(raw), err
}
