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
		log.WithField("endpoint", c.CDPEndpoint).Info("browser tool: connecting to existing browser via CDP")
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
