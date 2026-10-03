package server

import (
	"context"
	"encoding/json"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
)

func (s *Server) handleOAuth(ctx context.Context, f frame) {
	var input mcpclient.OAuthInput
	if err := json.Unmarshal(f.Params, &input); err != nil || input.URL == "" {
		s.writeError(f.ID, codeInvalidParams, "Invalid OAuth parameters")
		return
	}
	var result any
	var err error
	switch f.Method {
	case "mcp/oauth/login":
		result, err = s.oauth.Begin(ctx, input)
	case "mcp/oauth/status":
		result, err = s.oauth.Status(input.URL)
	case "mcp/oauth/logout":
		err = s.oauth.Logout(input.URL)
		result = map[string]bool{"disconnected": err == nil}
	}
	if err != nil {
		s.writeError(f.ID, codeInvalidParams, err.Error())
		return
	}
	s.writeResult(f.ID, result)
}
