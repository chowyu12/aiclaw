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

const (
	modeNone     = ""
	modePid      = "pid"
	modeSystemd  = "systemd"
	modeLaunchd  = "launchd"
)

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

// detectMode 检测当前以哪种方式在运行。
func detectMode() string {
	if _, ok := readPid(); ok {
		return modePid
	}
	switch runtime.GOOS {
	case "linux":
		if cmdExists("systemctl") {
			out, err := exec.Command("systemctl", "is-active", "aiclaw").Output()
			if err == nil && strings.TrimSpace(string(out)) == "active" {
				return modeSystemd
			}
		}
	case "darwin":
		if _, err := os.Stat(launchdPlist()); err == nil {
			out, _ := exec.Command("launchctl", "list", "com.aiclaw.agent").Output()
			if len(out) > 0 {
				return modeLaunchd
			}
		}
	}
	return modeNone
}

func Start() {
	mode := detectMode()
	switch mode {
	case modePid:
		pid, _ := readPid()
		if processAlive(pid) {
			fmt.Printf("aiclaw 已在运行 (PID %d)\n", pid)
			return
		}
		os.Remove(PidFile())
	case modeSystemd:
		fmt.Println("aiclaw 已通过 systemd 运行，使用以下命令管理：")
		fmt.Println("  sudo systemctl restart aiclaw")
		return
	case modeLaunchd:
		fmt.Println("aiclaw 已通过 launchd 运行，使用以下命令管理：")
		fmt.Printf("  launchctl kickstart -k gui/%d/com.aiclaw.agent\n", os.Getuid())
		return
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

func Stop() {
	mode := detectMode()
	switch mode {
	case modePid:
		pid, _ := readPid()
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

	case modeSystemd:
		fmt.Println("正在通过 systemd 停止 aiclaw ...")
		cmd := exec.Command("sudo", "systemctl", "stop", "aiclaw")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "停止失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("aiclaw 已停止")

	case modeLaunchd:
		fmt.Println("正在通过 launchd 停止 aiclaw ...")
		cmd := exec.Command("launchctl", "unload", launchdPlist())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "停止失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("aiclaw 已停止（如需重新启动: launchctl load -w " + launchdPlist() + "）")

	default:
		fmt.Println("aiclaw 未在运行")
	}
}

func Status() {
	mode := detectMode()
	switch mode {
	case modePid:
		pid, _ := readPid()
		if processAlive(pid) {
			fmt.Printf("aiclaw 正在运行 (PID %d) [后台模式]\n", pid)
			fmt.Printf("日志文件: %s\n", LogFile())
		} else {
			os.Remove(PidFile())
			fmt.Println("aiclaw 未在运行（已清理残留 PID 文件）")
		}

	case modeSystemd:
		fmt.Println("aiclaw 正在运行 [systemd 服务]")
		cmd := exec.Command("systemctl", "status", "aiclaw", "--no-pager", "-l")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()

	case modeLaunchd:
		fmt.Println("aiclaw 正在运行 [launchd 服务]")
		fmt.Printf("日志文件: %s\n", LogFile())

	default:
		fmt.Println("aiclaw 未在运行")
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
