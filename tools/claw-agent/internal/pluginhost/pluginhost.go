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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chowyu12/aiclaw/internal/i18n"
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
		return nil, i18n.E("同步内置插件失败：{error}", "error", err)
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
	view.Connections = []protocol.ChannelConnectionView{}
	if !pluginpkg.HasChannels(item) {
		if missing, err := s.config.MissingRequired(ctx, item, ""); err != nil {
			return view, err
		} else if missing != nil {
			view.MissingConfig = missing
		}
		return view, nil
	}
	// 渠道插件：配置按连接存。至少一个连接配齐了才能启用。
	connections, err := s.db.ListChannelConnections(ctx, item.UUID)
	if err != nil {
		return view, err
	}
	ready := 0
	for _, connection := range connections {
		missing, err := s.config.MissingRequired(ctx, item, connection.UUID)
		if err != nil {
			return view, err
		}
		if missing == nil {
			missing = []string{}
		}
		if len(missing) == 0 {
			ready++
		}
		view.Connections = append(view.Connections, protocol.ChannelConnectionView{
			UUID: connection.UUID, Name: connection.Name, MissingConfig: missing,
		})
	}
	if ready == 0 {
		view.MissingConfig = []string{i18n.D("至少一个可用的连接")}
	}
	return view, nil
}

// Install 从一个目录装插件。装好是停用的：启用是另一个明确的动作，
// 因为那一步才真正授予声明的权限。
func (s *Service) Install(ctx context.Context, path string) (protocol.PluginView, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return protocol.PluginView{}, i18n.E("没有给插件目录")
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

// ConfigFields 给配置表单用。秘密只报 isSet。connectionID 为空是插件本身的配置。
func (s *Service) ConfigFields(ctx context.Context, uuid, connectionID string) ([]protocol.PluginConfigField, error) {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if err := s.checkConnection(ctx, item, connectionID); err != nil {
		return nil, err
	}
	fields, err := s.config.Fields(ctx, item, connectionID)
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

// SetConfig 写一个配置项；空串表示清掉。
//
// 连接的配置改完立刻重启那一个连接（凭据换了，旧的长连接还连着旧账号），别的连接
// 不受影响；刚配齐的连接由 Sync 拉起来。
func (s *Service) SetConfig(ctx context.Context, uuid, connectionID, key, value string) error {
	item, err := s.find(ctx, uuid)
	if err != nil {
		return err
	}
	if err := s.checkConnection(ctx, item, connectionID); err != nil {
		return err
	}
	if err := s.config.Set(ctx, item, connectionID, key, value); err != nil {
		return err
	}
	if connectionID == "" {
		return nil
	}
	s.host.RestartConnection(connectionID)
	return s.syncChannels(ctx)
}

// checkConnection 核对连接属于这个插件；渠道插件的配置必须指明连接。
func (s *Service) checkConnection(ctx context.Context, item model.Plugin, connectionID string) error {
	if connectionID == "" {
		if pluginpkg.HasChannels(item) {
			return i18n.E("「{name}」的配置是按连接存的：先选一个连接", "name", item.Name)
		}
		return nil
	}
	connection, err := s.connection(ctx, connectionID)
	if err != nil {
		return err
	}
	if connection.PluginUUID != item.UUID {
		return i18n.E("连接 {connection} 不属于「{name}」", "connection", connectionID, "name", item.Name)
	}
	return nil
}

// ---------- 连接 ----------

func (s *Service) connection(ctx context.Context, uuid string) (model.ChannelConnection, error) {
	connections, err := s.db.ListChannelConnections(ctx, "")
	if err != nil {
		return model.ChannelConnection{}, err
	}
	for _, connection := range connections {
		if connection.UUID == strings.TrimSpace(uuid) {
			return connection, nil
		}
	}
	return model.ChannelConnection{}, i18n.E("连接不存在：{id}", "id", uuid)
}

// connectionNames 连接 id → 名字，给状态与放行记录显示。
func (s *Service) connectionNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	connections, err := s.db.ListChannelConnections(ctx, "")
	if err != nil {
		return names
	}
	for _, connection := range connections {
		names[connection.UUID] = connection.Name
	}
	return names
}

// CreateConnection 给渠道插件加一个连接（一个企微机器人、一个微信号）。刚建出来没有
// 凭据，不会启动；填好配置（或扫码登录）之后才拉起来。
func (s *Service) CreateConnection(ctx context.Context, pluginUUID, name string) (protocol.ChannelConnectionView, error) {
	item, err := s.find(ctx, pluginUUID)
	if err != nil {
		return protocol.ChannelConnectionView{}, err
	}
	if !pluginpkg.HasChannels(item) {
		return protocol.ChannelConnectionView{}, i18n.E("「{name}」不是渠道插件，没有连接", "name", item.Name)
	}
	connection, err := s.newConnection(ctx, item, name)
	if err != nil {
		return protocol.ChannelConnectionView{}, err
	}
	missing, _ := s.config.MissingRequired(ctx, item, connection.UUID)
	if missing == nil {
		missing = []string{}
	}
	return protocol.ChannelConnectionView{UUID: connection.UUID, Name: connection.Name, MissingConfig: missing}, nil
}

func (s *Service) newConnection(ctx context.Context, item model.Plugin, name string) (model.ChannelConnection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		existing, err := s.db.ListChannelConnections(ctx, item.UUID)
		if err != nil {
			return model.ChannelConnection{}, err
		}
		// 「连接器」是插件清单里名字的后缀，按原文去掉，不是界面文字。
		name = fmt.Sprintf("%s %d", strings.TrimSuffix(item.Name, "连接器"), len(existing)+1)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return model.ChannelConnection{}, err
	}
	connection := model.ChannelConnection{UUID: "k" + hex.EncodeToString(suffix), PluginUUID: item.UUID, Name: name}
	return connection, s.db.CreateChannelConnection(ctx, &connection)
}

// RenameConnection 改连接的名字。会话标题里带着它，新来的消息就用新名字。
func (s *Service) RenameConnection(ctx context.Context, uuid, name string) error {
	if _, err := s.connection(ctx, uuid); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return i18n.E("连接名不能为空")
	}
	return s.db.RenameChannelConnection(ctx, uuid, name)
}

// DeleteConnection 删掉一个连接：停掉它，删掉它的凭据与放行记录。会话本身留着，
// 历史还能在侧边栏里翻。
func (s *Service) DeleteConnection(ctx context.Context, uuid string) error {
	connection, err := s.connection(ctx, uuid)
	if err != nil {
		return err
	}
	s.host.RestartConnection(connection.UUID)
	if err := s.db.DeleteConnectionConfig(ctx, connection.PluginUUID, connection.UUID); err != nil {
		return err
	}
	if err := s.db.DeleteConnectionBindings(ctx, connection.PluginUUID, connection.UUID); err != nil {
		return err
	}
	if err := s.db.DeleteChannelConnection(ctx, connection.UUID); err != nil {
		return err
	}
	return s.syncChannels(ctx)
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
			if strings.TrimSpace(tool.Provider) == emailProvider {
				result.Email = true
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
	return model.Plugin{}, i18n.E("插件不存在：{id}", "id", uuid)
}

// ---------- 通道 ----------

// ChannelStatus 报每个托管中的通道的状态。
func (s *Service) ChannelStatus() []protocol.ChannelStatusView {
	statuses := s.host.Status()
	names := s.connectionNames(context.Background())
	out := make([]protocol.ChannelStatusView, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, protocol.ChannelStatusView{
			PluginUUID: st.PluginUUID, PluginName: st.PluginName, ChannelID: st.ChannelID,
			DisplayName: st.DisplayName, State: string(st.State), Attempts: st.Attempts,
			LastError: st.LastError, ConnectionID: st.ConnectionID, ConnectionName: names[st.ConnectionID],
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
	names := s.connectionNames(ctx)
	out := make([]protocol.ChannelBindingView, 0, len(items))
	for _, item := range items {
		view := protocol.ChannelBindingView{
			PluginUUID: item.PluginUUID, ChannelID: item.ChannelID, ExternalKey: item.ExternalKey,
			ConnectionID: item.ConnectionID, ConnectionName: names[item.ConnectionID],
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
		return i18n.E("放行前要先给这个会话选模型")
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
	binding, err := s.db.GetChannelBinding(ctx, key.PluginUUID, key.ChannelID, key.ConnectionID, key.ExternalKey)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, i18n.E("通道里没有这个会话：{key}", "key", key.ExternalKey)
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

// WeChatLoginPoll 查一次扫码进度；确认之后把凭据写进一个连接。
// 凭据不回传：调用方只知道登录完成了，与秘密只报「已配置」是一回事。
//
// 没指明连接就是「添加一个微信号」：同一个微信号以前连过（ilink_bot_id 相同），
// 写回它原来的连接，不重复建；否则新建一个。
func (s *Service) WeChatLoginPoll(ctx context.Context, params protocol.WeChatLoginPollParams) (protocol.WeChatLoginPollResult, error) {
	item, err := s.find(ctx, params.UUID)
	if err != nil {
		return protocol.WeChatLoginPollResult{}, err
	}
	if item.PluginID != wechatPluginID {
		return protocol.WeChatLoginPollResult{}, i18n.E("「{name}」不是微信插件", "name", item.Name)
	}
	result, err := wechatlink.PollQRStatus(ctx, params.Token)
	if err != nil {
		return protocol.WeChatLoginPollResult{}, err
	}
	status := protocol.WeChatLoginPollResult{Status: result.Status}
	if !strings.EqualFold(result.Status, "confirmed") || strings.TrimSpace(result.BotToken) == "" {
		return status, nil
	}
	connectionID := strings.TrimSpace(params.ConnectionID)
	if connectionID != "" {
		if err := s.checkConnection(ctx, item, connectionID); err != nil {
			return status, err
		}
	} else {
		connections, err := s.db.ListChannelConnections(ctx, item.UUID)
		if err != nil {
			return status, err
		}
		for _, connection := range connections {
			values, err := s.config.Load(ctx, item.UUID, connection.UUID)
			if err == nil && strings.TrimSpace(result.ILinkBotID) != "" && values.String(wechat.ConfigBotID) == result.ILinkBotID {
				connectionID = connection.UUID
				break
			}
		}
		if connectionID == "" {
			connection, err := s.newConnection(ctx, item, "")
			if err != nil {
				return status, err
			}
			connectionID = connection.UUID
		}
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
		if err := s.config.Set(ctx, item, connectionID, key, value); err != nil {
			return status, err
		}
	}
	status.Saved, status.ConnectionID = true, connectionID
	// 重新登录的连接用新凭据重连；新连接（插件已启用时）马上拉起来。
	s.host.RestartConnection(connectionID)
	return status, s.syncChannels(ctx)
}
