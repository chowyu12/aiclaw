// Package searchmcp 是随内核分发的联网搜索 MCP server：`claw-agent mcp-search`。
//
// 为什么是 MCP server 而不是内核的一个内置工具：搜索是「有没有配引擎」决定的
// 可选能力，做成 MCP server 之后宿主在有启用中的引擎时挂上、没有就不挂，
// 内核的工具注册表与循环一行不改；换引擎、关引擎都只是改配置。
//
// 它是内核自己的代码，所以宿主挂它时标 trusted——而且这个工具本来就是只读的。
package searchmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/chowyu12/aiclaw/internal/tools/websearch"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/searchengines"
)

// Serve 在 stdio 上跑 MCP server，直到对端关掉输入。
//
// 每次调用都重新读一遍当前生效的引擎：用户在配置页换了引擎，下一次搜索就用新的，
// 不用重挂。
func Serve(appDBPath, version string) error {
	db, err := appdb.Open(appDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	engines := searchengines.New(db)

	s := server.NewMCPServer("aiclaw-web-search", version, server.WithToolCapabilities(false))
	s.AddTool(
		mcp.NewTool("web_search",
			mcp.WithDescription("联网搜索。给一个查询词，返回若干条结果（标题、链接、摘要）。"),
			mcp.WithString("query", mcp.Required(), mcp.Description("搜索词")),
			mcp.WithNumber("limit", mcp.Description("要几条结果，1–10，默认 5")),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, err := req.RequireString("query")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			engine, err := engines.Active(ctx)
			if err != nil {
				return mcp.NewToolResultError("读搜索引擎配置失败：" + err.Error()), nil
			}
			if engine == nil {
				return mcp.NewToolResultError("没有启用中的搜索引擎。到「搜索引擎」页启用一个并填上 Key。"), nil
			}
			resp, err := websearch.Search(ctx, engine, query, req.GetInt("limit", 5))
			if err != nil {
				// 工具失败回给模型而不是断连：模型据此改做法或告诉用户。
				return mcp.NewToolResultError(fmt.Sprintf("搜索失败（%s）：%v", engine.Name, err)), nil
			}
			data, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		},
	)
	return server.ServeStdio(s)
}
