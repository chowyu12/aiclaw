package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
)

type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]any
	ServerUUID  string
	ServerName  string
}

type conn struct {
	client *mcpclient.Client
	server model.MCPServer
	tools  []mcpgo.Tool
}

type Manager struct {
	mu    sync.Mutex
	conns map[string]*conn
	// toolIndex maps tool name -> server UUID for dispatch
	toolIndex map[string]string
}

func NewManager() *Manager {
	return &Manager{
		conns:     make(map[string]*conn),
		toolIndex: make(map[string]string),
	}
}

func (m *Manager) Connect(ctx context.Context, servers []model.MCPServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		c, err := m.dial(ctx, srv)
		if err != nil {
			log.WithFields(log.Fields{"server": srv.Name, "transport": srv.Transport}).WithError(err).Warn("[MCP] connect failed, skipping")
			continue
		}

		initReq := mcpgo.InitializeRequest{}
		initReq.Params.ClientInfo = mcpgo.Implementation{
			Name:    "aiclaw",
			Version: "1.0.0",
		}
		initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
		if _, err := c.Initialize(ctx, initReq); err != nil {
			log.WithFields(log.Fields{"server": srv.Name}).WithError(err).Warn("[MCP] initialize failed, skipping")
			c.Close()
			continue
		}

		toolsResult, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
		if err != nil {
			log.WithFields(log.Fields{"server": srv.Name}).WithError(err).Warn("[MCP] list tools failed, skipping")
			c.Close()
			continue
		}

		cn := &conn{
			client: c,
			server: srv,
			tools:  toolsResult.Tools,
		}
		m.conns[srv.UUID] = cn

		for _, t := range toolsResult.Tools {
			m.toolIndex[t.Name] = srv.UUID
		}
		log.WithFields(log.Fields{"server": srv.Name, "tools": len(toolsResult.Tools)}).Info("[MCP] connected")
	}
	return nil
}

func (m *Manager) dial(_ context.Context, srv model.MCPServer) (*mcpclient.Client, error) {
	switch srv.Transport {
	case model.MCPTransportStdio:
		env := envSlice(srv.GetEnv())
		return mcpclient.NewStdioMCPClient(srv.Endpoint, env, srv.GetArgs()...)
	case model.MCPTransportSSE:
		var opts []transport.ClientOption
		if h := srv.GetHeaders(); len(h) > 0 {
			opts = append(opts, transport.WithHeaders(h))
		}
		return mcpclient.NewSSEMCPClient(srv.Endpoint, opts...)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", srv.Transport)
	}
}

func (m *Manager) Tools() []ToolInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []ToolInfo
	for _, cn := range m.conns {
		for _, t := range cn.tools {
			params := map[string]any{
				"type":       t.InputSchema.Type,
				"properties": t.InputSchema.Properties,
			}
			if len(t.InputSchema.Required) > 0 {
				params["required"] = t.InputSchema.Required
			}
			if t.InputSchema.Type == "" {
				params["type"] = "object"
			}
			if t.InputSchema.Properties == nil {
				params["properties"] = map[string]any{}
			}
			result = append(result, ToolInfo{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
				ServerUUID:  cn.server.UUID,
				ServerName:  cn.server.Name,
			})
		}
	}
	return result
}

func (m *Manager) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	m.mu.Lock()
	serverUUID, ok := m.toolIndex[name]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("mcp tool %q not found", name)
	}
	cn, ok := m.conns[serverUUID]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("mcp server for tool %q not connected", name)
	}

	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid tool arguments: %w", err)
		}
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := cn.client.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp call %q: %w", name, err)
	}

	return formatCallResult(result), nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for uuid, cn := range m.conns {
		if err := cn.client.Close(); err != nil {
			log.WithFields(log.Fields{"server": cn.server.Name}).WithError(err).Warn("[MCP] close failed")
		}
		delete(m.conns, uuid)
	}
	m.toolIndex = make(map[string]string)
}

func (m *Manager) HasTools() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.toolIndex) > 0
}

func formatCallResult(result *mcpgo.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, c := range result.Content {
		switch v := c.(type) {
		case mcpgo.TextContent:
			parts = append(parts, v.Text)
		case mcpgo.ImageContent:
			parts = append(parts, fmt.Sprintf("[image: %s]", v.MIMEType))
		case mcpgo.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio: %s]", v.MIMEType))
		default:
			b, _ := json.Marshal(c)
			parts = append(parts, string(b))
		}
	}
	return strings.Join(parts, "\n")
}

func envSlice(m map[string]string) []string {
	result := make([]string, 0, len(m))
	for k, v := range m {
		result = append(result, k+"="+v)
	}
	return result
}
