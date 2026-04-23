package server

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/config"
	"github.com/chowyu12/aiclaw/internal/tools/browser"
)

// ApplyBrowserToolConfig 根据配置初始化浏览器类内置工具（chromedp）。
func ApplyBrowserToolConfig(c config.BrowserConfig) {
	if c.Visible {
		browser.SetVisible(true)
		log.Info("browser tool: visible mode enabled")
	}
	if c.Width > 0 && c.Height > 0 {
		browser.SetViewport(c.Width, c.Height)
	}
	if c.UserAgent != "" {
		browser.SetUserAgent(c.UserAgent)
	}
	if c.Proxy != "" {
		browser.SetProxy(c.Proxy)
	}
	if c.CDPEndpoint != "" {
		browser.SetCDPEndpoint(c.CDPEndpoint)
		// 立刻探测一次端点：让用户在启动日志里就知道 CDP 是否可达，
		// 而不是等第一次 browser 工具调用失败时才发现配置问题。
		if name, ver, err := browser.ProbeCDPEndpoint(c.CDPEndpoint); err != nil {
			log.WithError(err).WithField("endpoint", c.CDPEndpoint).
				Warn("browser tool: CDP endpoint configured but probe failed (会保留配置；首次使用时再尝试连接)")
		} else {
			log.WithFields(log.Fields{
				"endpoint": c.CDPEndpoint,
				"browser":  name,
				"protocol": ver,
			}).Info("browser tool: CDP attach mode (sessions/cookies will be reused from existing browser)")
		}
	}
	if c.IdleTimeout > 0 {
		browser.SetIdleTimeout(time.Duration(c.IdleTimeout) * time.Second)
	}
	if c.MaxTabs > 0 {
		browser.SetMaxTabs(c.MaxTabs)
	} else {
		browser.SetMaxTabs(50)
	}
}
