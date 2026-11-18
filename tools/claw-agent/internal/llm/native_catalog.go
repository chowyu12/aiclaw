package llm

import (
	"encoding/json"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

func catalogSupport(supported *bool) string {
	if supported == nil {
		return ""
	}
	if *supported {
		return "supported"
	}
	return "unsupported"
}

func anthropicCatalogCapabilities(raw json.RawMessage, inputLimit int) model.ModelCapabilities {
	// Anthropic-compatible gateways may already return the common flat fields.
	var cap model.ModelCapabilities
	_ = json.Unmarshal(raw, &cap)
	type support struct {
		Supported *bool `json:"supported"`
	}
	var native struct {
		ImageInput support `json:"image_input"`
		Effort     struct {
			support
			Low    support `json:"low"`
			Medium support `json:"medium"`
			High   support `json:"high"`
			XHigh  support `json:"xhigh"`
			Max    support `json:"max"`
		} `json:"effort"`
	}
	_ = json.Unmarshal(raw, &native)
	if inputLimit > 0 {
		cap.ContextWindow = inputLimit
	}
	if native.ImageInput.Supported != nil {
		cap.Vision = catalogSupport(native.ImageInput.Supported)
	}
	if native.Effort.Supported != nil {
		cap.Reasoning = catalogSupport(native.Effort.Supported)
	}
	var levels []string
	for _, level := range []struct {
		name    string
		support support
	}{
		{"low", native.Effort.Low}, {"medium", native.Effort.Medium}, {"high", native.Effort.High},
		{"xhigh", native.Effort.XHigh}, {"max", native.Effort.Max},
	} {
		if catalogSupport(level.support.Supported) == "supported" {
			levels = append(levels, level.name)
		}
	}
	if len(levels) > 0 {
		cap.ReasoningEfforts = strings.Join(levels, ",")
	}
	return cap
}

func geminiCatalogCapabilities(raw []byte) (map[string]model.ModelCapabilities, error) {
	var catalog struct {
		Models []struct {
			Name            string `json:"name"`
			InputTokenLimit int    `json:"inputTokenLimit"`
			Thinking        *bool  `json:"thinking"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, err
	}
	caps := map[string]model.ModelCapabilities{}
	for _, entry := range catalog.Models {
		name := strings.TrimSpace(strings.TrimPrefix(entry.Name, "models/"))
		cap := model.ModelCapabilities{
			ContextWindow: entry.InputTokenLimit,
			Reasoning:     catalogSupport(entry.Thinking),
		}.Normalized()
		if name != "" && cap != (model.ModelCapabilities{}) {
			caps[name] = cap
		}
	}
	return caps, nil
}
