package channels

import (
	"encoding/json"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
)

// ConfigFromModel 将持久化渠道转为适配器输入。
func ConfigFromModel(ch *model.Channel) ChannelConfig {
	var raw []byte
	if len(ch.Config) > 0 {
		raw = []byte(ch.Config)
	}
	return ChannelConfig{
		ID:          ch.ID,
		UUID:        ch.UUID,
		ChannelType: string(ch.ChannelType),
		ConfigJSON:  raw,
	}
}

func cfgString(jsonRaw []byte, keys ...string) string {
	if len(jsonRaw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(jsonRaw, &m); err != nil {
		return ""
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}
