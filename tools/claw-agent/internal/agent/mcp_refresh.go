package agent

import (
	"context"
	"sort"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/mcpclient"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/tools"
)

type mcpBinding struct {
	client  *mcpclient.Client
	config  protocol.MCPServerConfig
	names   []string
	version uint64
}

// Only the sampling boundary installs catalogs. Notifications never mutate a
// registry from a transport goroutine, or interrupt an admitted tool call.
func (s *Session) refreshMCP(ctx context.Context, turn string, emitter Emitter) {
	names := make([]string, 0, len(s.mcpBindings))
	for name := range s.mcpBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		binding := s.mcpBindings[name]
		if err := binding.client.RefreshTools(ctx); err != nil {
			s.notify(emitter, turn, "MCP tool refresh failed; keeping the previous catalog: "+name)
			continue
		}
		if binding.version == binding.client.Version() {
			continue
		}
		s.mu.Lock()
		target := s.registry
		if s.codeModeBase != nil {
			target = s.codeModeBase
		}
		removed := map[string]bool{}
		for _, tool := range binding.names {
			removed[tool] = true
		}
		kept := []tools.Tool{}
		for _, tool := range target.List() {
			if !removed[tool.Name] {
				kept = append(kept, tool)
			}
		}
		_ = target.Replace(kept)
		delete(s.mcpStatus, name)
		s.mountMCP(name, binding.config, binding.client, "")
		if s.codeModeBase != nil {
			s.installCodeMode()
			s.foldStatusIntoExec()
		}
		// Refresh tool instructions as well, so newly discovered names are visible.
		s.refreshSystemPromptLocked()
		s.mu.Unlock()
		emitter.Notify("session/toolsUpdated", map[string]any{"sessionId": s.ID, "tools": s.Tools(), "mcpStatus": s.MCPStatus(), "foldedTools": s.FoldedTools()})
	}
}
