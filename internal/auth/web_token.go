package auth

import (
	"strings"
	"sync"
)

var webTokenMu sync.RWMutex
var webTokenVal string

// SetWebToken 更新 Web 控制台鉴权令牌（启动与 config 热加载时调用）。
func SetWebToken(s string) {
	webTokenMu.Lock()
	defer webTokenMu.Unlock()
	webTokenVal = strings.TrimSpace(s)
}

// CurrentWebToken 返回当前生效的 web_token。
func CurrentWebToken() string {
	webTokenMu.RLock()
	defer webTokenMu.RUnlock()
	return webTokenVal
}
