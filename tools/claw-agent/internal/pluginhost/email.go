package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mail"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// emailProvider 是邮件插件声明的工具提供者。工具本身在内核里（agent 包的 email_*），
// 插件只负责「开没开」和「邮箱怎么连」。
const emailProvider = "builtin:email"

// providesEmail 报一个插件是不是邮件插件。
func providesEmail(item model.Plugin) bool {
	var manifest pluginpkg.Manifest
	if err := json.Unmarshal(item.Manifest, &manifest); err != nil {
		return false
	}
	for _, tool := range manifest.Contributes.Tools {
		if strings.TrimSpace(tool.Provider) == emailProvider {
			return true
		}
	}
	return false
}

// EmailAccount 取启用中的邮件插件配的邮箱。没启用时返回错误——会话只在启用时才挂工具，
// 这里再挡一次是为了用户在会话中途停用插件之后，已经挂上的工具也立刻停手。
func (s *Service) EmailAccount(ctx context.Context) (mail.Account, error) {
	plugins, err := s.db.ListPlugins(ctx)
	if err != nil {
		return mail.Account{}, err
	}
	for _, item := range plugins {
		if item.Enabled && providesEmail(item) {
			return s.emailAccountOf(ctx, item)
		}
	}
	return mail.Account{}, errors.New("邮件插件已停用：要用的话请用户到「插件」页启用「邮件」")
}

func (s *Service) emailAccountOf(ctx context.Context, item model.Plugin) (mail.Account, error) {
	values, err := s.config.Load(ctx, item.UUID, "")
	if err != nil {
		return mail.Account{}, err
	}
	port := func(key string) int {
		value, _ := strconv.Atoi(strings.TrimSpace(values[key]))
		return value
	}
	account := mail.Account{
		Address:  values["address"],
		Name:     values["name"],
		Username: values["username"],
		Password: values["password"],
		IMAPHost: values["imap_host"],
		IMAPPort: port("imap_port"),
		SMTPHost: values["smtp_host"],
		SMTPPort: port("smtp_port"),
	}
	if strings.TrimSpace(account.Address) == "" {
		return account, errors.New("还没填邮箱地址")
	}
	return account.Normalize()
}

// TestEmail 用插件里已经存下的配置试着登录收信、发信两台服务器。启用前也能测。
func (s *Service) TestEmail(ctx context.Context, uuid string) protocol.EmailTestResult {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return protocol.EmailTestResult{Error: err.Error()}
	}
	if !providesEmail(item) {
		return protocol.EmailTestResult{Error: "这个插件不是邮件插件"}
	}
	account, err := s.emailAccountOf(ctx, item)
	if err != nil {
		return protocol.EmailTestResult{Error: err.Error()}
	}
	result := protocol.EmailTestResult{
		IMAPHost: account.IMAPHost, IMAPPort: account.IMAPPort,
		SMTPHost: account.SMTPHost, SMTPPort: account.SMTPPort,
	}
	if account.Password == "" {
		result.Error = "还没填授权码"
		return result
	}
	if err := mail.Test(ctx, account); err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	return result
}
