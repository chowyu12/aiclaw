package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const envKey = "_AICLAW_DAEMON"

func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".aiclaw")
}

func PidFile() string  { return filepath.Join(dataDir(), "aiclaw.pid") }
func LogFile() string  { return filepath.Join(dataDir(), "aiclaw.log") }
func IsChild() bool    { return os.Getenv(envKey) == "1" }

func Start() {
	if pid, ok := readPid(); ok {
		if processAlive(pid) {
			fmt.Printf("aiclaw 已在运行 (PID %d)\n", pid)
			return
		}
		os.Remove(PidFile())
	}

	dir := dataDir()
	os.MkdirAll(dir, 0o755)

	logPath := LogFile()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法创建日志文件 %s: %v\n", logPath, err)
		os.Exit(1)
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法获取可执行文件路径: %v\n", err)
		os.Exit(1)
	}
	self, _ = filepath.EvalSymlinks(self)

	args := filterArgs(os.Args[1:])
	cmd := exec.Command(self, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), envKey+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	os.WriteFile(PidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()

	fmt.Printf("aiclaw 已在后台启动 (PID %d)\n", cmd.Process.Pid)
	fmt.Printf("日志文件: %s\n", logPath)
}

func Stop() {
	pid, ok := readPid()
	if !ok {
		fmt.Println("未找到 PID 文件，aiclaw 可能未在运行")
		return
	}
	if !processAlive(pid) {
		os.Remove(PidFile())
		fmt.Println("aiclaw 未在运行（已清理残留 PID 文件）")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法找到进程 %d: %v\n", pid, err)
		os.Exit(1)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "发送停止信号失败: %v\n", err)
		os.Exit(1)
	}
	os.Remove(PidFile())
	fmt.Printf("已发送停止信号到 PID %d\n", pid)
}

func Status() {
	pid, ok := readPid()
	if !ok {
		fmt.Println("aiclaw 未在运行")
		return
	}
	if processAlive(pid) {
		fmt.Printf("aiclaw 正在运行 (PID %d)\n", pid)
		fmt.Printf("日志文件: %s\n", LogFile())
	} else {
		os.Remove(PidFile())
		fmt.Println("aiclaw 未在运行（已清理残留 PID 文件）")
	}
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "start" {
			out = append(out, a)
		}
	}
	return out
}

func readPid() (int, bool) {
	data, err := os.ReadFile(PidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
