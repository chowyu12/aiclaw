package server

import (
	"github.com/chowyu12/aiclaw/internal/sqlitehealth"
	"strings"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/agent"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/appdb"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// appDBGuards 是应用库及其 SQLite 伴随文件。
//
// 应用库里是全部模型服务的 Key。它不在默认的凭据名单里（那份名单是按用户主目录
// 下的通用位置写的），而桌面宿主传下来的只有它自己的 userData——**实际发生过**：
// 代码模式下模型调不到画图工具，转头 `sqlite3 ~/.aiclaw/aiclaw.db` 把 Key 读出来
// 自己 curl，Key 就此进了会话历史。内核自己知道库在哪，就该自己守。
//
// 伴随文件要单独列：沙箱的 subpath 规则对文件只匹配它本身，-wal 里同样有明文。
// 密钥文件也在名单里：库里的凭据是用它加密的，两个都读到就等于明文。
func appDBGuards(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return []string{path, path + "-wal", path + "-shm", path + "-journal", appdb.KeyFile(path), sqlitehealth.BackupDir(path)}
}

// guard 给一个会话加上内核这边知道的敏感路径。每个建出来或恢复出来的会话都要过一遍。
//
// 用量记录也在这里接上：这是所有会话（桌面、通道、恢复出来的）都必经的一处，
// 接在别处总会漏掉一种。
func (s *Server) guard(session *agent.Session) {
	session.Guard(appDBGuards(s.options.AppDB)...)
	if s.oauth != nil {
		session.Guard(s.oauth.CredentialPath(), s.oauth.KeyPath())
	}
	session.Guard(s.options.ProtectedPaths...)
	if s.db != nil {
		session.SetUsageSink(s.db.RecordUsage)
		session.SetPersistence(s.db)
		session.SetGoalStop(func() { s.collab.InterruptTree(session.ID) })
	}
}

// pinChannelPolicy uses this external conversation's saved mode, never the desktop global mode.
func pinChannelPolicy(sessionID string, refresh *protocol.SessionRefresh, policy protocol.ApprovalPolicy) {
	if refresh == nil || !strings.HasPrefix(sessionID, channelSessionPrefix) {
		return
	}
	refresh.ApprovalPolicy = protocol.ApprovalNever
	if policy == protocol.ApprovalBypass {
		refresh.ApprovalPolicy = protocol.ApprovalBypass
	}
	refresh.EnabledBuiltins = []string{}
	// A mode switch does not grant access to the owner's mailbox, configuration or background automation.
	refresh.EnableEmail = false
	refresh.EnableSchedule = false
	refresh.EnableAppConfig = false
	refresh.EnableBrowser = false
	refresh.EnableComputerUse = false
	refresh.SkillDirs = nil
	refresh.MemoryFile = ""
}
