package server

import (
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"testing"
)

func TestChannelRefreshCannotEnableApplicationConfiguration(t *testing.T) {
	refresh := protocol.SessionRefresh{EnableAppConfig: true}
	pinChannelPolicy(channelSessionPrefix+"external", &refresh, protocol.ApprovalNever)
	if refresh.EnableAppConfig {
		t.Fatal("channel gained application configuration capability")
	}
	local := protocol.SessionRefresh{EnableAppConfig: true}
	pinChannelPolicy("desktop", &local, protocol.ApprovalNever)
	if !local.EnableAppConfig {
		t.Fatal("desktop lost application configuration capability")
	}
}
