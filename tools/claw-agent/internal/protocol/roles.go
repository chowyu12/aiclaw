package protocol

// 模型角色：对话之外的那几件事各自交给一个模型。
//
// 为什么不是「换一个更强的对话模型」：看图、听写、朗读、画图是四种不同的模型，
// 没有哪个对话模型四样都好，而用户手上往往正好各有一个便宜的专用模型。所以
// 这里的形状是「主模型负责对话，需要时把这一件事外包出去」——工具调用与旁路
// 都由内核发起，模型不用知道背后是另一个服务。
//
// 角色模型与对话模型一样按 ProviderID 找端点与 Key，Key 不经协议帧。

// ModelRole 是一个模型能干的事。一个模型可以身兼数职。
type ModelRole string

const (
	// RoleVision 看得懂图。对话模型自己带这个标记时不需要旁路。
	RoleVision ModelRole = "vision"
	// RoleSTT 语音转文字。
	RoleSTT ModelRole = "stt"
	// RoleTTS 文字转语音。
	RoleTTS ModelRole = "tts"
	// RoleImage 文生图。
	RoleImage ModelRole = "image"
)

// KnownRoles 是全部角色，界面按它出勾选项。
func KnownRoles() []ModelRole { return []ModelRole{RoleVision, RoleSTT, RoleTTS, RoleImage} }

// RoleModel 是某个角色用哪个模型。ProviderID 为 0 表示这个角色没配。
type RoleModel struct {
	ProviderID int64  `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
}

// Configured 报这个角色配了没有。
func (r RoleModel) Configured() bool { return r.ProviderID > 0 && r.Model != "" }

// RoleModels 是一份角色配置，随会话下发。
//
// 为什么整份下发而不是内核自己去库里读：角色是**会话的配置**，与模型、审批档位
// 同级——恢复旧会话时要能按当前配置刷新，而内核不该替宿主决定「现在该用哪个」。
type RoleModels struct {
	Vision RoleModel `json:"vision,omitzero"`
	STT    RoleModel `json:"stt,omitzero"`
	TTS    RoleModel `json:"tts,omitzero"`
	Image  RoleModel `json:"image,omitzero"`
}

// Get 按角色取配置。
func (r RoleModels) Get(role ModelRole) RoleModel {
	switch role {
	case RoleVision:
		return r.Vision
	case RoleSTT:
		return r.STT
	case RoleTTS:
		return r.TTS
	case RoleImage:
		return r.Image
	}
	return RoleModel{}
}
