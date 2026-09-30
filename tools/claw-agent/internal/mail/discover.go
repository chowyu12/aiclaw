package mail

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// 自己域名的企业邮箱（name@company.cn）按 imap.<域名> 猜常常不对：邮箱其实托管在
// 网易、腾讯、阿里这些企业邮里，imap.company.cn 要么不存在，要么是指过去的 CNAME，
// 而对方证书上写的是 *.qiye.163.com——直接连 imap.company.cn 会因为证书对不上被拒。
//
// 所以没填服务器时按这个顺序找：
//  1. imap.<域名> / smtp.<域名> 的 CNAME：指向哪就连哪（证书是按那个名字签的）；
//  2. 域名的 MX 记录：认得出是哪家企业邮，就用那家的服务器；
//  3. 都不行才用 imap.<域名> 这个猜测。连的时候证书对不上，再按证书上的名字重试一次。

// mxProviders 按 MX 主机名的后缀认企业邮。
var mxProviders = []struct {
	suffix string
	preset preset
}{
	{".mxmail.netease.com", preset{"imap.qiye.163.com", 993, "smtp.qiye.163.com", 465}},
	{".qiye.163.com", preset{"imap.qiye.163.com", 993, "smtp.qiye.163.com", 465}},
	{".exmail.qq.com", preset{"imap.exmail.qq.com", 993, "smtp.exmail.qq.com", 465}},
	{"mxbiz1.qq.com", preset{"imap.exmail.qq.com", 993, "smtp.exmail.qq.com", 465}},
	{"mxbiz2.qq.com", preset{"imap.exmail.qq.com", 993, "smtp.exmail.qq.com", 465}},
	{".mxhichina.com", preset{"imap.qiye.aliyun.com", 993, "smtp.qiye.aliyun.com", 465}},
	{".qiye.aliyun.com", preset{"imap.qiye.aliyun.com", 993, "smtp.qiye.aliyun.com", 465}},
	{".feishu.cn", preset{"imap.feishu.cn", 993, "smtp.feishu.cn", 465}},
	{".larksuite.com", preset{"imap.larksuite.com", 993, "smtp.larksuite.com", 465}},
	{".google.com", preset{"imap.gmail.com", 993, "smtp.gmail.com", 465}},
	{".googlemail.com", preset{"imap.gmail.com", 993, "smtp.gmail.com", 465}},
	{".mail.protection.outlook.com", preset{"outlook.office365.com", 993, "smtp.office365.com", 587}},
	{".zoho.com", preset{"imap.zoho.com", 993, "smtp.zoho.com", 465}},
	{".zoho.com.cn", preset{"imap.zoho.com.cn", 993, "smtp.zoho.com.cn", 465}},
}

// 查 DNS 的函数，测试里换掉。
var (
	lookupCNAME    = net.DefaultResolver.LookupCNAME
	lookupMX       = net.DefaultResolver.LookupMX
	lookupPublicMX = publicResolver.LookupMX
)

// publicResolver 在本机 DNS 查不到 MX 时再问一次公共 DNS：公司内网的 DNS 常常只
// 解析内部记录，MX 是空的。
var publicResolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: 3 * time.Second}
		return dialer.DialContext(ctx, network, "223.5.5.5:53")
	},
}

type discovered struct {
	preset preset
	ok     bool
	at     time.Time
}

var (
	discoverMu    sync.Mutex
	discoverCache = map[string]discovered{}
)

// discoverTTL 是一次查找结果记多久。邮箱服务商不会常换，但也别一直记着错的。
const discoverTTL = 30 * time.Minute

// Discover 给没填服务器的账号找服务器（见文件顶上的说明）。填了任何一个服务器、
// 或者是常见的个人邮箱（qq.com 这些）时原样返回。
func Discover(ctx context.Context, account Account) Account {
	if strings.TrimSpace(account.IMAPHost) != "" || strings.TrimSpace(account.SMTPHost) != "" {
		return account
	}
	domain := Domain(account.Address)
	if domain == "" {
		return account
	}
	if _, known := presets[domain]; known {
		return account
	}
	found := discoverDomain(ctx, domain)
	if !found.ok {
		return account
	}
	account.IMAPHost, account.SMTPHost = found.preset.imapHost, found.preset.smtpHost
	if account.IMAPPort <= 0 {
		account.IMAPPort = found.preset.imapPort
	}
	if account.SMTPPort <= 0 {
		account.SMTPPort = found.preset.smtpPort
	}
	return account
}

func discoverDomain(ctx context.Context, domain string) discovered {
	discoverMu.Lock()
	cached, hit := discoverCache[domain]
	discoverMu.Unlock()
	if hit && time.Since(cached.at) < discoverTTL {
		return cached
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	result := discovered{at: time.Now()}
	if found, ok := discoverByCNAME(ctx, domain); ok {
		result.preset, result.ok = found, true
	} else if found, ok := discoverByMX(ctx, domain); ok {
		result.preset, result.ok = found, true
	}
	discoverMu.Lock()
	discoverCache[domain] = result
	discoverMu.Unlock()
	return result
}

// discoverByCNAME 看 imap.<域名> 与 smtp.<域名> 是不是指向别处。两个都指过去才算：
// 只有一个的话另一个大概率是自建的，混着用更容易错。
func discoverByCNAME(ctx context.Context, domain string) (preset, bool) {
	target := func(host string) string {
		name, err := lookupCNAME(ctx, host)
		if err != nil {
			return ""
		}
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if name == "" || name == host {
			return ""
		}
		return name
	}
	imapHost, smtpHost := target("imap."+domain), target("smtp."+domain)
	if imapHost == "" || smtpHost == "" {
		return preset{}, false
	}
	return preset{imapHost, 993, smtpHost, 465}, true
}

func discoverByMX(ctx context.Context, domain string) (preset, bool) {
	records, err := lookupMX(ctx, domain)
	if err != nil || len(records) == 0 {
		records, err = lookupPublicMX(ctx, domain)
		if err != nil {
			return preset{}, false
		}
	}
	for _, record := range records {
		host := strings.ToLower(strings.TrimSuffix(record.Host, "."))
		for _, provider := range mxProviders {
			if strings.HasSuffix(host, provider.suffix) || host == strings.TrimPrefix(provider.suffix, ".") {
				return provider.preset, true
			}
		}
	}
	return preset{}, false
}

// certHost 在证书与主机名对不上时，从证书里挑一个能用的名字：*.qiye.163.com 配上
// 原来的前缀（imap / smtp）就是 imap.qiye.163.com。挑不出来返回空串。
func certHost(err error, host string) string {
	var mismatch x509.HostnameError
	if !errors.As(err, &mismatch) || mismatch.Certificate == nil {
		return ""
	}
	prefix := host
	if index := strings.Index(host, "."); index > 0 {
		prefix = host[:index]
	}
	names := mismatch.Certificate.DNSNames
	for _, name := range names {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "*.") {
			return prefix + name[1:]
		}
	}
	for _, name := range names {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, prefix+".") {
			return name
		}
	}
	return ""
}
