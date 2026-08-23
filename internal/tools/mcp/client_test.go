package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	mcpproto "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func TestQualifyToolNameUsesCodexCompatibleNamespace(t *testing.T) {
	got := qualifyToolName("Local Files", "read/file")
	if got != "mcp__local_files__read_file" {
		t.Fatalf("qualified name = %q", got)
	}
}

func TestStreamableHTTPDiscoveryAndNamespacedDispatch(t *testing.T) {
	backend := mcpserver.NewMCPServer("files", "1.0.0")
	backend.AddTool(
		mcpproto.NewTool("read/file", mcpproto.WithDescription("read a file")),
		func(_ context.Context, request mcpproto.CallToolRequest) (*mcpproto.CallToolResult, error) {
			if request.Params.Name != "read/file" {
				t.Fatalf("server received exposed name %q", request.Params.Name)
			}
			return mcpproto.NewToolResultText("routed"), nil
		},
	)
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(backend, mcpserver.WithStateLess(true)))
	defer httpServer.Close()

	manager := NewManager()
	defer manager.Close()
	err := manager.Connect(context.Background(), []model.MCPServer{{
		UUID: "server-files", Name: "Local Files", Transport: model.MCPTransportStreamableHTTP,
		Endpoint: httpServer.URL, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name != "mcp__local_files__read_file" || tools[0].OriginalName != "read/file" {
		t.Fatalf("unexpected discovered tools: %+v", tools)
	}
	result, err := manager.CallTool(context.Background(), tools[0].Name, `{}`)
	if err != nil || result != "routed" {
		t.Fatalf("namespaced call result=%q err=%v", result, err)
	}
}

func TestDuplicateServerNamespacesAreDisambiguated(t *testing.T) {
	m := NewManager()
	m.toolIndex["mcp__github__search"] = toolRoute{serverUUID: "one", toolName: "search"}
	namespace := m.uniqueServerNamespace(model.MCPServer{UUID: "two", Name: "GitHub"})
	if namespace == "github" || !strings.HasPrefix(namespace, "github_") {
		t.Fatalf("duplicate namespace was not disambiguated: %q", namespace)
	}
}

func TestSanitizedToolNameCollisionGetsStableSuffix(t *testing.T) {
	first := qualifyToolName("server", "read-file")
	second := qualifyToolName("server", "read/file")
	if first != second {
		t.Fatalf("test setup expected collision: %q != %q", first, second)
	}
	if first+"_"+shortStableSuffix("read/file") == first {
		t.Fatal("collision suffix was empty")
	}
}
