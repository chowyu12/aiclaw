// Package pluginhost 把 AIClaw 的插件系统（internal/plugin）接进内核。
//
// 插件是一个带 plugin.json 的目录，声明权限、配置项与贡献：技能目录、MCP
// server、宿主能力（computer use）、通道（微信、企业微信）。记录、配置与通道
// 授权都在应用库里。这里负责三件事：
//
//   - 装 / 启停 / 删，以及配置项的读写（秘密只进不出）；
//   - 把启用中的插件贡献汇总成一份 PluginContributions，宿主开会话时并进去；
//   - 托管通道：启用了微信 / 企业微信插件就把连接拉起来，收到的消息经 Gateway
//     变成一轮受限的对话（见 server 包的 channelGateway）。
package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	pluginpkg "github.com/chowyu12/aiclaw/internal/plugin"
	"github.com/chowyu12/aiclaw/internal/plugins/bundled"
	"github.com/chowyu12/aiclaw/internal/plugins/wechat"
	"github.com/chowyu12/aiclaw/internal/plugins/wecom"
	"github.com/chowyu12/aiclaw/internal/store/gormstore"
	"github.com/chowyu12/aiclaw/pkg/wechatlink"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// computerUseProvider 是 computer-use 插件声明的宿主能力提供者。启用了它的
// 插件就等于打开会话的 computer use——实现在宿主（截屏与输入合成只有宿主做得了）。
const computerUseProvider = "builtin:computer_use"

// wechatPluginID 是随应用分发的微信插件的 manifest id；扫码登录只对它有意义。
const wechatPluginID = "aiclaw.wechat"

// Service 是插件系统对内核的门面。
type Service struct {
	db        *gormstore.GormStore
	installer *pluginpkg.Installer
	config    *pluginpkg.ConfigService
	host      *pluginpkg.Host
	// hostCtx 是通道的生命周期；Close 取消它。
	hostCtx    context.Context
	hostCancel context.CancelFunc
	logf       func(format string, args ...any)
}

// New 建服务：把随应用分发的插件同步进库与 root/plugins，再按启用状态拉起通道。
//
// root 是应用数据目录（~/.aiclaw），插件文件放在它下面的 plugins/。
func New(ctx context.Context, db *gormstore.GormStore, root string, gateway pluginpkg.Gateway, logf func(string, ...any)) (*Service, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	installer := pluginpkg.NewInstaller(db, root)
	if err := installer.EnsureBuiltins(ctx, bundled.FS()); err != nil {
		return nil, fmt.Errorf("同步内置插件失败：%w", err)
	}
	config := pluginpkg.NewConfigService(db)
	host, err := pluginpkg.NewHost(gateway, config, map[string]pluginpkg.ChannelFactory{
		wecom.ProviderName:  wecom.New,
		wechat.ProviderName: wechat.New,
	}, pluginpkg.WithHostLogger(func(format string, args ...any) {
		logf("[plugin] "+format, args...)
	}))
	if err != nil {
		return nil, err
	}
	hostCtx, cancel := context.WithCancel(context.Background())
	s := &Service{
		db: db, installer: installer, config: config, host: host,
		hostCtx: hostCtx, hostCancel: cancel, logf: logf,
	}
	if err := s.syncChannels(ctx); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

// Close 停掉全部通道。库由调用方关。
func (s *Service) Close() {
	s.hostCancel()
	s.host.Stop()
}

// syncChannels 让运行中的通道与启用中的插件对齐。
func (s *Service) syncChannels(ctx context.Context) error {
	plugins, err := s.db.ListPlugins(ctx)
	if err != nil {
		return err
	}
	// 通道用的是 hostCtx（进程级），不是这次请求的 ctx——请求结束通道还得活着。
	return s.host.Sync(s.hostCtx, plugins)
}

// ---------- 插件 ----------

// List 列出全部插件，带贡献计数、权限与缺失的必填配置。
func (s *Service) List(ctx context.Context) ([]protocol.PluginView, error) {
	plugins, err := s.db.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	skillItems, _ := s.db.ListSkills(ctx)
	mcpItems, _ := s.db.ListMCPServers(ctx)
	views := make([]protocol.PluginView, 0, len(plugins))
	for _, item := range plugins {
		view, err := s.view(ctx, item, skillItems, mcpItems)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	// 内置的排前面，其余按名字：列表顺序不该随安装顺序漂。
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Source != views[j].Source {
			return views[i].Source == string(model.PluginSourceBuiltin)
		}
		return views[i].Name < views[j].Name
	})
	return views, nil
}

func (s *Service) view(ctx context.Context, item model.Plugin, skillItems []model.Skill, mcpItems []model.MCPServer) (protocol.PluginView, error) {
	view := protocol.PluginView{
		UUID: item.UUID, PluginID: item.PluginID, Name: item.Name, Description: item.Description,
		Version: item.Version, Source: string(item.Source), Enabled: item.Enabled,
		Permissions: []string{}, MissingConfig: []string{},
	}
	tools, channels, err := pluginpkg.NativeContributions(item)
	if err != nil {
		return view, err
	}
	view.Tools, view.Channels = tools, channels
	for _, sk := range skillItems {
		if sk.PluginUUID == item.UUID {
			view.Skills++
		}
	}
	for _, srv := range mcpItems {
		if srv.PluginUUID == item.UUID {
			view.MCP++
		}
	}
	if permissions, err := pluginpkg.InstalledPermissions(item, skillItems, mcpItems); err != nil {
		return view, err
	} else if permissions != nil {
		view.Permissions = permissions
	}
	if missing, err := s.config.MissingRequired(ctx, item); err != nil {
		return view, err
	} else if missing != nil {
		view.MissingConfig = missing
	}
	return view, nil
}

// Install 从一个目录装插件。装好是停用的：启用是另一个明确的动作，
// 因为那一步才真正授予声明的权限。
func (s *Service) Install(ctx context.Context, path string) (protocol.PluginView, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return protocol.PluginView{}, fmt.Errorf("没有给插件目录")
	}
	installed, _, err := s.installer.Install(ctx, path)
	if err != nil {
		return protocol.PluginView{}, err
	}
	skillItems, _ := s.db.ListSkills(ctx)
	mcpItems, _ := s.db.ListMCPServers(ctx)
	return s.view(ctx, *installed, skillItems, mcpItems)
}

// Toggle 启停一个插件。启用时先检查必填配置齐了没有——启用即授权，
// 授权那一刻配置就得是完整的。通道要立刻拉起或停掉，技能与 MCP 下次开会话生效。
func (s *Service) Toggle(ctx context.Context, uuid string, enabled bool) error {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return err
	}
	if enabled {
		if err := s.config.ValidateEnable(ctx, item); err != nil {
			return err
		}
	}
	if err := s.db.SetPluginEnabled(ctx, item.UUID, enabled); err != nil {
		return err
	}
	if err := s.db.SetPluginSkillsEnabled(ctx, item.UUID, enabled); err != nil {
		return err
	}
	if err := s.db.SetPluginMCPEnabled(ctx, item.UUID, enabled); err != nil {
		return err
	}
	return s.syncChannels(ctx)
}

// Delete 卸载一个用户装的插件。内置的只能停用。
func (s *Service) Delete(ctx context.Context, uuid string) error {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return err
	}
	if err := s.installer.Uninstall(ctx, item); err != nil {
		return err
	}
	return s.syncChannels(ctx)
}

// ConfigFields 给配置表单用。秘密只报 isSet。
func (s *Service) ConfigFields(ctx context.Context, uuid string) ([]protocol.PluginConfigField, error) {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return nil, err
	}
	fields, err := s.config.Fields(ctx, item)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.PluginConfigField, 0, len(fields))
	for _, f := range fields {
		out = append(out, protocol.PluginConfigField{
			Key: f.Key, Type: f.Type, Description: f.Description,
			Required: f.Required, Secret: f.Secret, IsSet: f.IsSet, Value: f.Value,
		})
	}
	return out, nil
}

// SetConfig 写一个配置项；空串表示清掉。改完通道重连时会读到新值。
func (s *Service) SetConfig(ctx context.Context, uuid, key, value string) error {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return err
	}
	return s.config.Set(ctx, item, key, value)
}

// Contributions 汇总启用中的插件贡献给会话的东西。
func (s *Service) Contributions(ctx context.Context) (protocol.PluginContributions, error) {
	result := protocol.PluginContributions{
		MCPServers: map[string]protocol.MCPServerConfig{},
		Skills:     []protocol.PluginSkill{},
	}
	plugins, err := s.db.ListPlugins(ctx)
	if err != nil {
		return result, err
	}
	enabled := map[string]model.Plugin{}
	for _, item := range plugins {
		if !item.Enabled {
			continue
		}
		enabled[item.UUID] = item
		var manifest pluginpkg.Manifest
		if err := json.Unmarshal(item.Manifest, &manifest); err != nil {
			continue
		}
		for _, tool := range manifest.Contributes.Tools {
			if strings.TrimSpace(tool.Provider) == computerUseProvider {
				result.ComputerUse = true
			}
		}
	}
	skillItems, err := s.db.ListSkills(ctx)
	if err != nil {
		return result, err
	}
	for _, sk := range skillItems {
		owner, ok := enabled[sk.PluginUUID]
		if !ok || !sk.Enabled || strings.TrimSpace(sk.InstallDir) == "" {
			continue
		}
		result.Skills = append(result.Skills, protocol.PluginSkill{
			Dir: sk.InstallDir, PluginName: owner.Name, PluginUUID: owner.UUID,
		})
	}
	mcpItems, err := s.db.ListMCPServers(ctx)
	if err != nil {
		return result, err
	}
	for _, srv := range mcpItems {
		if _, ok := enabled[srv.PluginUUID]; !ok || !srv.Enabled {
			continue
		}
		config := protocol.MCPServerConfig{Args: srv.GetArgs(), Env: srv.GetEnv(), Headers: srv.GetHeaders()}
		if srv.Transport == model.MCPTransportStdio {
			config.Command = srv.Endpoint
		} else {
			config.URL = srv.Endpoint
		}
		// 名字就是工具名前缀。与用户自己配的撞名时内核会在挂载阶段报重复，
		// 比这里静默改名好查。
		result.MCPServers[srv.Name] = config
	}
	return result, nil
}

func (s *Service) find(ctx context.Context, uuid string) (model.Plugin, error) {
	uuid = strings.TrimSpace(uuid)
	plugins, err := s.db.ListPlugins(ctx)
	if err != nil {
		return model.Plugin{}, err
	}
	for _, item := range plugins {
		if item.UUID == uuid {
			return item, nil
		}
	}
	return model.Plugin{}, fmt.Errorf("插件不存在：%s", uuid)
}

// ---------- 通道 ----------

// ChannelStatus 报每个托管中的通道的状态。
func (s *Service) ChannelStatus() []protocol.ChannelStatusView {
	statuses := s.host.Status()
	out := make([]protocol.ChannelStatusView, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, protocol.ChannelStatusView{
			PluginUUID: st.PluginUUID, PluginName: st.PluginName, ChannelID: st.ChannelID,
			DisplayName: st.DisplayName, State: string(st.State), Attempts: st.Attempts,
			LastError: st.LastError,
		})
	}
	return out
}

// Bindings 列出通道见过的全部外部会话，包括等着授权的。
func (s *Service) Bindings(ctx context.Context) ([]protocol.ChannelBindingView, error) {
	items, err := s.db.ListChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.ChannelBindingView, 0, len(items))
	for _, item := range items {
		view := protocol.ChannelBindingView{
			PluginUUID: item.PluginUUID, ChannelID: item.ChannelID, ExternalKey: item.ExternalKey,
			DisplayName: item.DisplayName, SessionID: item.ThreadUUID,
			ProviderID: item.ProviderID, Model: item.ModelName, Allowed: item.Allowed,
			AllowedTools: []string{},
		}
		if len(item.AllowedTools) > 0 {
			_ = json.Unmarshal(item.AllowedTools, &view.AllowedTools)
		}
		if !item.LastMessage.IsZero() {
			view.LastMessage = item.LastMessage.Format(time.RFC3339)
		}
		out = append(out, view)
	}
	return out, nil
}

// Authorize 放行一个外部会话。
//
// 这是入站消息的同意环节：在此之前那边发来的消息只被记下、不被服务。
// 模型与放开的工具在这里选，而不是沿用桌面会话的——发消息的人不是本机用户。
func (s *Service) Authorize(ctx context.Context, params protocol.ChannelAuthorizeParams) error {
	binding, err := s.binding(ctx, params.ChannelBindingKey)
	if err != nil {
		return err
	}
	if params.ProviderID == 0 || strings.TrimSpace(params.Model) == "" {
		return fmt.Errorf("放行前要先给这个会话选模型")
	}
	tools, err := json.Marshal(params.AllowedTools)
	if err != nil {
		return err
	}
	binding.Allowed, binding.ProviderID, binding.ModelName = true, params.ProviderID, strings.TrimSpace(params.Model)
	binding.AllowedTools = model.JSON(tools)
	return s.db.SaveChannelBinding(ctx, binding)
}

// Revoke 收回放行。记录与会话都留着，历史还能看。
func (s *Service) Revoke(ctx context.Context, key protocol.ChannelBindingKey) error {
	binding, err := s.binding(ctx, key)
	if err != nil {
		return err
	}
	binding.Allowed = false
	return s.db.SaveChannelBinding(ctx, binding)
}

func (s *Service) binding(ctx context.Context, key protocol.ChannelBindingKey) (*model.ChannelBinding, error) {
	binding, err := s.db.GetChannelBinding(ctx, key.PluginUUID, key.ChannelID, key.ExternalKey)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, fmt.Errorf("通道里没有这个会话：%s", key.ExternalKey)
	}
	return binding, nil
}

// ---------- 微信扫码登录 ----------

// WeChatLoginStart 向中继要一张登录二维码。
func (s *Service) WeChatLoginStart(ctx context.Context) (protocol.WeChatLoginStartResult, error) {
	qr, err := wechat.FetchLoginQR(ctx)
	if err != nil {
		return protocol.WeChatLoginStartResult{}, err
	}
	return protocol.WeChatLoginStartResult{Token: qr.Token, Image: qr.Image}, nil
}

// WeChatLoginPoll 查一次扫码进度；确认之后把凭据写进插件配置。
// 凭据不回传：调用方只知道登录完成了，与秘密只报「已配置」是一回事。
func (s *Service) WeChatLoginPoll(ctx context.Context, params protocol.WeChatLoginPollParams) (protocol.WeChatLoginPollResult, error) {
	item, err := s.find(ctx, params.UUID)
	if err != nil {
		return protocol.WeChatLoginPollResult{}, err
	}
	if item.PluginID != wechatPluginID {
		return protocol.WeChatLoginPollResult{}, fmt.Errorf("「%s」不是微信插件", item.Name)
	}
	result, err := wechatlink.PollQRStatus(ctx, params.Token)
	if err != nil {
		return protocol.WeChatLoginPollResult{}, err
	}
	status := protocol.WeChatLoginPollResult{Status: result.Status}
	if !strings.EqualFold(result.Status, "confirmed") || strings.TrimSpace(result.BotToken) == "" {
		return status, nil
	}
	for key, value := range map[string]string{
		wechat.ConfigBotToken:  result.BotToken,
		wechat.ConfigBotID:     result.ILinkBotID,
		wechat.ConfigBaseURL:   result.BaseURL,
		wechat.ConfigILinkUser: result.ILinkUserID,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := s.config.Set(ctx, item, key, value); err != nil {
			return status, err
		}
	}
	status.Saved = true
	return status, nil
}
