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

// wechatProtectedConfigKeys 仅由扫码登录链路写入；普通 PUT /channels 必须保留 DB 现值，
// 避免编辑弹窗提交时把刚扫码下发的凭据冲掉，导致长轮询用旧 token 起来后被 iLink 服务端拒绝。
var wechatProtectedConfigKeys = []string{"bot_token", "ilink_bot_id", "base_url", "ilink_user_id"}

// SanitizeUpdateConfig 在普通更新链路上对 channel.config 做受保护字段合并。
//
// 当前仅对微信渠道生效：传入 config 中的 iLink 凭据键会被忽略，统一回填为 existing 中的现有值
// （existing 中没有则从结果里删除）。其他渠道与未识别类型不做处理。
func SanitizeUpdateConfig(channelType model.ChannelType, incoming, existing []byte) ([]byte, error) {
	if channelType != model.ChannelWeChat || len(incoming) == 0 {
		return incoming, nil
	}
	inMap := map[string]any{}
	if err := json.Unmarshal(incoming, &inMap); err != nil {
		return nil, err
	}
	var existingMap map[string]any
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &existingMap)
	}
	for _, k := range wechatProtectedConfigKeys {
		if v, ok := existingMap[k]; ok && v != nil {
			inMap[k] = v
		} else {
			delete(inMap, k)
		}
	}
	return json.Marshal(inMap)
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
