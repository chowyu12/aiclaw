package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const envKey = "_AICLAW_DAEMON"

func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".aiclaw")
}

func PidFile() string { return filepath.Join(dataDir(), "aiclaw.pid") }
func LogFile() string { return filepath.Join(dataDir(), "aiclaw.log") }
func IsChild() bool   { return os.Getenv(envKey) == "1" }

func launchdPlist() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.aiclaw.agent.plist")
}

// hasSystemd 检测是否已注册 systemd 服务（不要求正在运行）。
func hasSystemd() bool {
	if runtime.GOOS != "linux" || !cmdExists("systemctl") {
		return false
	}
	err := exec.Command("systemctl", "cat", "aiclaw").Run()
	return err == nil
}

// hasLaunchd 检测是否已注册 launchd 服务。
func hasLaunchd() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(launchdPlist())
	return err == nil
}

func Start() {
	switch {
	case hasSystemd():
		fmt.Println("正在通过 systemd 启动 aiclaw ...")
		run("sudo", "systemctl", "start", "aiclaw")
		fmt.Println("aiclaw 已启动")

	case hasLaunchd():
		fmt.Println("正在通过 launchd 启动 aiclaw ...")
		exec.Command("launchctl", "unload", launchdPlist()).Run()
		run("launchctl", "load", "-w", launchdPlist())
		fmt.Println("aiclaw 已启动")

	default:
		startDaemon()
	}
}

func Stop() {
	switch {
	case hasSystemd():
		fmt.Println("正在通过 systemd 停止 aiclaw ...")
		run("sudo", "systemctl", "stop", "aiclaw")
		fmt.Println("aiclaw 已停止")

	case hasLaunchd():
		fmt.Println("正在通过 launchd 停止 aiclaw ...")
		run("launchctl", "unload", launchdPlist())
		fmt.Println("aiclaw 已停止")

	default:
		stopDaemon()
	}
}

func Status() {
	switch {
	case hasSystemd():
		cmd := exec.Command("systemctl", "status", "aiclaw", "--no-pager", "-l")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()

	case hasLaunchd():
		out, _ := exec.Command("launchctl", "list", "com.aiclaw.agent").Output()
		if len(out) > 0 {
			fmt.Println("aiclaw 正在运行 [launchd]")
		} else {
			fmt.Println("aiclaw 未在运行 [launchd 服务已注册但未加载]")
		}
		fmt.Printf("日志文件: %s\n", LogFile())

	default:
		pid, ok := readPid()
		if ok && processAlive(pid) {
			fmt.Printf("aiclaw 正在运行 (PID %d)\n", pid)
			fmt.Printf("日志文件: %s\n", LogFile())
		} else {
			if ok {
				os.Remove(PidFile())
			}
			fmt.Println("aiclaw 未在运行")
		}
	}
}

// ───────────────────── 内置后台模式 ─────────────────────

func startDaemon() {
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
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	os.WriteFile(PidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()

	fmt.Printf("aiclaw 已在后台启动 (PID %d)\n", cmd.Process.Pid)
	fmt.Printf("日志文件: %s\n", logPath)
}

func stopDaemon() {
	pid, ok := readPid()
	if !ok {
		fmt.Println("aiclaw 未在运行")
		return
	}
	if !processAlive(pid) {
		os.Remove(PidFile())
		fmt.Println("aiclaw 未在运行（已清理残留 PID 文件）")
		return
	}
	if err := stopProcess(pid); err != nil {
		fmt.Fprintf(os.Stderr, "发送停止信号失败: %v\n", err)
		os.Exit(1)
	}
	os.Remove(PidFile())
	fmt.Printf("已发送停止信号到 PID %d\n", pid)
}

// ───────────────────── 工具函数 ─────────────────────

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %s %s: %v\n", name, strings.Join(args, " "), err)
		os.Exit(1)
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

func cmdExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
