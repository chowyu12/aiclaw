// claw-agent 是 AIClaw 的 Agent 执行内核：自研的模型循环，直接在用户本机
// 执行工具，通过 stdio JSON-RPC 由桌面宿主驱动。
//
//	claw-agent serve --data-home=<目录> --app-db=<aiclaw.db>
//	claw-agent mcp-search --app-db=<aiclaw.db>
//
// 第二个是随内核分发的联网搜索 MCP server：宿主在有启用中的搜索引擎时把它挂进
// 会话，内核像挂任何 stdio MCP server 一样把它拉起来。
//
// 模型服务（端点 + Key + 模型清单）存在 --app-db 指的 SQLite 库里，会话按
// providerId 选用；Key 由内核在库里查，不经协议帧。
//
// 环境变量：
//
//	AICLAW_LLM_KEY   兜底的模型 Key：会话没指定模型服务时用它。
//
// **本版本不做 OS 级沙箱**（已确认的产品决定）。工具直接在本机执行，
// 路径约束与审批是仅剩的防护——见 internal/tools 的说明。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/searchmcp"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/server"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "claw-agent: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// 界面语言：桌面端启动时带过来，之后切换走 config/locale。没带（命令行、测试）就还是中文。
	// 放在分派之前：联网搜索 MCP server（mcp-search）回给模型的报错也跟着它。
	if locale := os.Getenv("AICLAW_LOCALE"); locale != "" {
		i18n.SetDefault(locale)
	}
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "mcp-search":
		return runSearchMCP(args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		return usageError()
	}
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	dataHome := flags.String("data-home", server.DefaultDataHome(), "session data directory")
	appDB := flags.String("app-db", server.DefaultAppDB(), "path to the app database (SQLite); empty disables it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dataHome, 0o755); err != nil {
		return fmt.Errorf("%s%w", i18n.D("创建数据目录失败："), err)
	}

	// 没有 Key 也不在这里失败：正常路径是会话按模型服务到库里取 Key，
	// 环境变量只是没配模型服务时的兜底。
	apiKey := os.Getenv("AICLAW_LLM_KEY")

	// stderr 是唯一的日志出口，stdout 被协议占着。
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[claw-agent] "+format+"\n", args...)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(server.Options{
		Version:  version,
		DataHome: *dataHome,
		APIKey:   apiKey,
		AppDB:    *appDB,
		Logf:     logf,
	}, os.Stdout)
	if err != nil {
		return err
	}
	return srv.Serve(ctx, os.Stdin)
}

func runSearchMCP(args []string) error {
	flags := flag.NewFlagSet("mcp-search", flag.ContinueOnError)
	appDB := flags.String("app-db", server.DefaultAppDB(), "path to the app database (SQLite)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*appDB) == "" {
		return i18n.E("mcp-search 需要 --app-db")
	}
	return searchmcp.Serve(*appDB, version)
}

func usageError() error {
	var builder strings.Builder
	printUsage(&builder)
	return fmt.Errorf("%s", strings.TrimRight(builder.String(), "\n"))
}

func printUsage(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, `claw-agent — the AIClaw agent kernel

Usage:
  claw-agent serve [--data-home=<dir>] [--app-db=<aiclaw.db>]
  claw-agent mcp-search [--app-db=<aiclaw.db>]
  claw-agent version

Environment:
  AICLAW_LLM_KEY   fallback model key (used when a session names no provider)
  AICLAW_LOCALE    UI language for messages: en or zh-CN
`)
}
