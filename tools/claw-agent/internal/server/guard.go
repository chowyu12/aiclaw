package server

import (
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
	return []string{path, path + "-wal", path + "-shm", path + "-journal", appdb.KeyFile(path)}
}

// guard 给一个会话加上内核这边知道的敏感路径。每个建出来或恢复出来的会话都要过一遍。
func (s *Server) guard(session *agent.Session) {
	session.Guard(appDBGuards(s.options.AppDB)...)
	session.Guard(s.options.ProtectedPaths...)
}

// pinChannelPolicy 让通道会话的审批档位不随宿主的 refresh 变。
//
// 通道会话（企业微信、微信）是替外部用户跑的，建出来就是「无人值守」：要确认的
// 操作直接失败。但桌面端恢复任何会话时都会带一份 refresh，里面的 approvalPolicy
// 是桌面上的全局档位——用户在侧边栏点开一个通道会话看一眼，这个会话就换成了
// 桌面档位，之后外部用户发来的消息也按那个档位执行。**实际发生过。** 谁发的消息
// 决定档位，而不是谁在看；所以通道会话的档位在这里钉回去。
func pinChannelPolicy(sessionID string, refresh *protocol.SessionRefresh) {
	if refresh == nil || !strings.HasPrefix(sessionID, channelSessionPrefix) {
		return
	}
	refresh.ApprovalPolicy = protocol.ApprovalNever
}
