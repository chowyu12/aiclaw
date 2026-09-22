// claw-agent 是 AIClaw 的 Agent 执行内核：自研的模型循环，直接在用户本机
// 执行工具，通过 stdio JSON-RPC 由桌面宿主驱动。
//
//	claw-agent serve --data-home=<目录>
//
// 环境变量：
//
//	AICLAW_LLM_KEY   模型 Key。只从环境变量来，不经协议帧、不落文件。
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dataHome, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败：%w", err)
	}

	apiKey := os.Getenv("AICLAW_LLM_KEY")
	if strings.TrimSpace(apiKey) == "" {
		// 不在这里直接失败：宿主可能只是先起进程列会话，模型 Key 到开会话时才需要。
		fmt.Fprintln(os.Stderr, "[claw-agent] 警告：AICLAW_LLM_KEY 未设置，开会话时会失败")
	}

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
  claw-agent serve [--data-home=<目录>]
  claw-agent version

环境变量：
  AICLAW_LLM_KEY   模型 Key（必填，开会话时使用）
`)
}
