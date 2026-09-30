// Package mail 是邮件工具的收发两端：IMAP 读信箱、SMTP 发信。
//
// 账号是「邮件」插件的配置（插件页里填的那个），密码与其它插件秘密一样加密落库。
// 这个包本身不碰库：调用方把解出来的 Account 交进来，每次调用现连现断——
// 模型读一封信隔几十秒才会再读下一封，长连接要处理掉线重连，换来的只是省一次握手。
//
// 国内邮箱（QQ、163、126）要在网页版设置里打开 IMAP/SMTP 并生成「授权码」，
// 填的是授权码不是登录密码；163 系还要求登录后先发一条 IMAP ID，否则选信箱时报
// 「Unsafe Login」。这两件事在这里都照顾到了。
package mail

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// Account 是一个邮箱账号的连接信息。Password 是明文，只在内存里经手。
type Account struct {
	Address  string
	Name     string
	Username string
	Password string
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
}

// preset 是常见邮箱的服务器地址。
type preset struct {
	imapHost string
	imapPort int
	smtpHost string
	smtpPort int
}

// presets 按邮箱域名给出服务器。没列到的域名（公司自己的域名）先经 Discover 查
// DNS，查不出来才按 imap.<域名> / smtp.<域名> 猜，猜错了用户在「高级」里改。
var presets = map[string]preset{
	"qq.com":         {"imap.qq.com", 993, "smtp.qq.com", 465},
	"foxmail.com":    {"imap.qq.com", 993, "smtp.qq.com", 465},
	"vip.qq.com":     {"imap.qq.com", 993, "smtp.qq.com", 465},
	"163.com":        {"imap.163.com", 993, "smtp.163.com", 465},
	"126.com":        {"imap.126.com", 993, "smtp.126.com", 465},
	"yeah.net":       {"imap.yeah.net", 993, "smtp.yeah.net", 465},
	"vip.163.com":    {"imap.vip.163.com", 993, "smtp.vip.163.com", 465},
	"sina.com":       {"imap.sina.com", 993, "smtp.sina.com", 465},
	"sohu.com":       {"imap.sohu.com", 993, "smtp.sohu.com", 465},
	"aliyun.com":     {"imap.aliyun.com", 993, "smtp.aliyun.com", 465},
	"139.com":        {"imap.139.com", 993, "smtp.139.com", 465},
	"gmail.com":      {"imap.gmail.com", 993, "smtp.gmail.com", 465},
	"googlemail.com": {"imap.gmail.com", 993, "smtp.gmail.com", 465},
	"outlook.com":    {"outlook.office365.com", 993, "smtp-mail.outlook.com", 587},
	"hotmail.com":    {"outlook.office365.com", 993, "smtp-mail.outlook.com", 587},
	"live.com":       {"outlook.office365.com", 993, "smtp-mail.outlook.com", 587},
	"icloud.com":     {"imap.mail.me.com", 993, "smtp.mail.me.com", 587},
	"me.com":         {"imap.mail.me.com", 993, "smtp.mail.me.com", 587},
	"yahoo.com":      {"imap.mail.yahoo.com", 993, "smtp.mail.yahoo.com", 465},
}

// Domain 取邮箱地址的域名，小写。
func Domain(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(address[at+1:]))
}

// Normalize 校验地址并补齐没填的服务器、端口、用户名。
func (a Account) Normalize() (Account, error) {
	a.Address = strings.TrimSpace(a.Address)
	parsed, err := mail.ParseAddress(a.Address)
	if err != nil || parsed.Address != a.Address {
		return a, i18n.E("邮箱地址不对：{address}", "address", strconv.Quote(a.Address))
	}
	a.Name = strings.TrimSpace(a.Name)
	a.Username = strings.TrimSpace(a.Username)
	if a.Username == "" {
		a.Username = a.Address
	}
	domain := Domain(a.Address)
	known, ok := presets[domain]
	if !ok {
		known = preset{"imap." + domain, 993, "smtp." + domain, 465}
	}
	a.IMAPHost = strings.TrimSpace(a.IMAPHost)
	if a.IMAPHost == "" {
		a.IMAPHost = known.imapHost
	}
	if a.IMAPPort <= 0 {
		if a.IMAPHost == known.imapHost {
			a.IMAPPort = known.imapPort
		} else {
			a.IMAPPort = 993
		}
	}
	a.SMTPHost = strings.TrimSpace(a.SMTPHost)
	if a.SMTPHost == "" {
		a.SMTPHost = known.smtpHost
	}
	if a.SMTPPort <= 0 {
		if a.SMTPHost == known.smtpHost {
			a.SMTPPort = known.smtpPort
		} else {
			a.SMTPPort = 465
		}
	}
	if a.IMAPPort > 65535 || a.SMTPPort > 65535 {
		return a, i18n.E("端口号不对")
	}
	return a, nil
}

// Ready 报这个账号能不能用：地址与密码都有。
func (a Account) Ready() bool {
	return strings.TrimSpace(a.Address) != "" && a.Password != ""
}

// savesSentItself 报这家的 SMTP 会不会自己把发出去的信放进「已发送」。
// 会的话不再经 IMAP 补一份，否则已发送里会有两封。
func (a Account) savesSentItself() bool {
	switch Domain(a.Address) {
	case "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com":
		return true
	}
	host := strings.ToLower(a.SMTPHost)
	return strings.HasSuffix(host, ".gmail.com") || strings.Contains(host, "office365.com") || strings.Contains(host, "outlook.com")
}

// hint 在登录失败时补一句最常见的原因。
func (a Account) hint() string {
	host := strings.ToLower(a.IMAPHost + " " + a.SMTPHost)
	switch {
	case strings.Contains(host, "qiye.163.com"):
		return i18n.D("网易企业邮箱：在网页版「设置 → 客户端设置」里开启 IMAP/SMTP；开了安全登录的话要生成「客户端专用密码」填在这里")
	case strings.Contains(host, "exmail.qq.com"):
		return i18n.D("腾讯企业邮箱：在网页版「设置 → 客户端设置」里开启 IMAP/SMTP；开了安全登录的话要生成「客户端专用密码」填在这里")
	case strings.Contains(host, "qiye.aliyun.com"):
		return i18n.D("阿里企业邮箱：确认管理员开放了 IMAP/SMTP；开了二次验证的话要用「三方客户端安全密码」")
	}
	switch Domain(a.Address) {
	case "qq.com", "foxmail.com", "vip.qq.com":
		return i18n.D("QQ 邮箱要在网页版「设置 → 账号」里开启 IMAP/SMTP 服务，密码处填生成的授权码")
	case "163.com", "126.com", "yeah.net", "vip.163.com":
		return i18n.D("网易邮箱要在网页版「设置 → POP3/SMTP/IMAP」里开启 IMAP/SMTP 服务，密码处填授权码")
	case "gmail.com", "googlemail.com":
		return i18n.D("Gmail 要开两步验证，再到 Google 账号「应用专用密码」里生成一个填在这里")
	case "outlook.com", "hotmail.com", "live.com":
		return i18n.D("Outlook 个人邮箱要用应用密码；微软已逐步关闭这类邮箱的密码登录，可能连不上")
	case "icloud.com", "me.com":
		return i18n.D("iCloud 邮箱要在 Apple 账号里生成「App 专用密码」填在这里")
	}
	return i18n.D("检查密码（很多邮箱要用单独生成的授权码 / 应用密码）以及是否开启了 IMAP/SMTP")
}

// wrapErr 在一句（已翻译的）说明后面接上原始错误，保留 %w；冒号跟着界面语言。
func wrapErr(message string, err error) error {
	if i18n.Default() == i18n.Chinese {
		return fmt.Errorf("%s：%w", message, err)
	}
	return fmt.Errorf("%s: %w", message, err)
}
