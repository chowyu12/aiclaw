package modelcaps

import (
	_ "embed"
	"path"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed model_caps.yaml
var modelCapsYAML []byte

// ModelCaps 表示某个模型的运行时能力集合，由 GetModelCaps 返回。
type ModelCaps struct {
	NoTemperature   bool `json:"no_temperature"`
	NoTopP          bool `json:"no_top_p"`
	NoStreaming     bool `json:"no_streaming"`
	AlwaysThinking  bool `json:"always_thinking"`
	Vision          bool `json:"vision"`
	FunctionCalling bool `json:"function_calling"`
	WebSearch       bool `json:"web_search"`
	ContextWindow   int  `json:"context_window"`
}

// ── YAML 配置结构 ──────────────────────────────────────

type capsConfig struct {
	Defaults capsDefaults `yaml:"defaults"`
	Models   []capsEntry  `yaml:"models"`
}

type capsDefaults struct {
	Temperature     bool   `yaml:"temperature"`
	TopP            bool   `yaml:"top_p"`
	Streaming       bool   `yaml:"streaming"`
	Thinking        string `yaml:"thinking"`
	Vision          bool   `yaml:"vision"`
	FunctionCalling bool   `yaml:"function_calling"`
	WebSearch       bool   `yaml:"web_search"`
	ContextWindow   int    `yaml:"context_window"`
}

type capsEntry struct {
	Patterns        []string `yaml:"patterns"`
	Temperature     *bool    `yaml:"temperature,omitempty"`
	TopP            *bool    `yaml:"top_p,omitempty"`
	Streaming       *bool    `yaml:"streaming,omitempty"`
	Thinking        string   `yaml:"thinking,omitempty"`
	Vision          *bool    `yaml:"vision,omitempty"`
	FunctionCalling *bool    `yaml:"function_calling,omitempty"`
	WebSearch       *bool    `yaml:"web_search,omitempty"`
	ContextWindow   *int     `yaml:"context_window,omitempty"`
}

// ── 加载 & 缓存 ──────────────────────────────────────

var (
	globalCapsConfig *capsConfig
	capsOnce         sync.Once
)

func loadCapsConfig() *capsConfig {
	capsOnce.Do(func() {
		var cfg capsConfig
		if err := yaml.Unmarshal(modelCapsYAML, &cfg); err != nil {
			globalCapsConfig = &capsConfig{
				Defaults: capsDefaults{
					Temperature: true, TopP: true, Streaming: true,
					Thinking: "optional", FunctionCalling: true, ContextWindow: 128000,
				},
			}
			return
		}
		globalCapsConfig = &cfg
	})
	return globalCapsConfig
}

// GetModelCaps 根据模型名称返回其能力配置，按 model_caps.yaml 中的顺序匹配。
func GetModelCaps(modelName string) ModelCaps {
	cfg := loadCapsConfig()

	for i := range cfg.Models {
		if matchesAnyPattern(modelName, cfg.Models[i].Patterns) {
			return buildCaps(&cfg.Defaults, &cfg.Models[i])
		}
	}
	return defaultCaps(&cfg.Defaults)
}

func defaultCaps(d *capsDefaults) ModelCaps {
	return ModelCaps{
		NoTemperature:   !d.Temperature,
		NoTopP:          !d.TopP,
		NoStreaming:     !d.Streaming,
		AlwaysThinking:  d.Thinking == "always",
		Vision:          d.Vision,
		FunctionCalling: d.FunctionCalling,
		WebSearch:       d.WebSearch,
		ContextWindow:   d.ContextWindow,
	}
}

func buildCaps(d *capsDefaults, e *capsEntry) ModelCaps {
	caps := defaultCaps(d)
	if e.Temperature != nil {
		caps.NoTemperature = !*e.Temperature
	}
	if e.TopP != nil {
		caps.NoTopP = !*e.TopP
	}
	if e.Streaming != nil {
		caps.NoStreaming = !*e.Streaming
	}
	if e.Thinking != "" {
		caps.AlwaysThinking = e.Thinking == "always"
	}
	if e.Vision != nil {
		caps.Vision = *e.Vision
	}
	if e.FunctionCalling != nil {
		caps.FunctionCalling = *e.FunctionCalling
	}
	if e.WebSearch != nil {
		caps.WebSearch = *e.WebSearch
	}
	if e.ContextWindow != nil {
		caps.ContextWindow = *e.ContextWindow
	}
	return caps
}

// ── 模式匹配 ──────────────────────────────────────

func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if matchPattern(name, p) {
			return true
		}
	}
	return false
}

// matchPattern 支持两种匹配：
//   - 尾部 * 作为前缀匹配（如 "gpt-4o*" 匹配 "gpt-4o-mini"）
//   - path.Match glob（如 "o1-*" 匹配 "o1-mini"）
func matchPattern(name, pattern string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}
