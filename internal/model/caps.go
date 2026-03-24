package model

import "strings"

type ModelCaps struct {
	NoTemperature bool
	NoTopP        bool
	NoStreaming   bool
}

type modelCapRule struct {
	Prefix string
	Caps   ModelCaps
}

var modelCapRules = []modelCapRule{
	{Prefix: "gpt-5.", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "openai/gpt-5.", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{"o1-", ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "openai/o1-", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "o3-", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "openai/o3-", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "o4-mini", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{Prefix: "openai/o4-mini", Caps: ModelCaps{NoTemperature: true, NoTopP: true}},
	{"deepseek-reasoner", ModelCaps{NoTemperature: true, NoTopP: true}},
}

var modelCapExact = map[string]ModelCaps{
	"o1": {NoTemperature: true, NoTopP: true},
	"o3": {NoTemperature: true, NoTopP: true},
}

func GetModelCaps(modelName string) ModelCaps {
	if caps, ok := modelCapExact[modelName]; ok {
		return caps
	}
	for _, r := range modelCapRules {
		if strings.HasPrefix(modelName, r.Prefix) {
			return r.Caps
		}
	}
	return ModelCaps{}
}
