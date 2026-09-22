// claw-agent 是 AIClaw 的 Agent 执行内核：自研的模型循环，直接在用户本机
// 执行工具，通过 stdio JSON-RPC 由桌面宿主驱动。
//
//	claw-agent serve --data-home=<目录> --app-db=<aiclaw.db>
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
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
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
	dataHome := flags.String("data-home", server.DefaultDataHome(), "会话文件目录")
	appDB := flags.String("app-db", server.DefaultAppDB(), "模型配置库（SQLite）路径；空表示不开")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dataHome, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败：%w", err)
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

func usageError() error {
	var builder strings.Builder
	printUsage(&builder)
	return fmt.Errorf("%s", strings.TrimRight(builder.String(), "\n"))
}

func printUsage(w interface{ Write([]byte) (int, error) }) {
	fmt.Fprintf(w, `claw-agent —— AIClaw 的 Agent 执行内核

用法：
  claw-agent serve [--data-home=<目录>] [--app-db=<aiclaw.db>]
  claw-agent version

环境变量：
  AICLAW_LLM_KEY   兜底的模型 Key（会话没指定模型服务时使用）
`)
}
