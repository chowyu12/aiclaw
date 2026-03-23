package model

// WebTokenLoginReq Web 控制台登录：与 config 中 auth.web_token 比对。
type WebTokenLoginReq struct {
	Token string `json:"token"`
}
