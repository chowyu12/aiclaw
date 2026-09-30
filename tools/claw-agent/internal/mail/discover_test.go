package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"testing"
)

// fakeDNS 换掉三个查 DNS 的函数，测试结束换回来。
func fakeDNS(t *testing.T, cnames map[string]string, mx, publicMX map[string][]string) {
	t.Helper()
	oldCNAME, oldMX, oldPublic := lookupCNAME, lookupMX, lookupPublicMX
	t.Cleanup(func() {
		lookupCNAME, lookupMX, lookupPublicMX = oldCNAME, oldMX, oldPublic
		discoverMu.Lock()
		discoverCache = map[string]discovered{}
		discoverMu.Unlock()
	})
	discoverMu.Lock()
	discoverCache = map[string]discovered{}
	discoverMu.Unlock()
	lookupCNAME = func(_ context.Context, host string) (string, error) {
		if target, ok := cnames[host]; ok {
			return target + ".", nil
		}
		return host + ".", nil
	}
	records := func(table map[string][]string) func(context.Context, string) ([]*net.MX, error) {
		return func(_ context.Context, domain string) ([]*net.MX, error) {
			hosts, ok := table[domain]
			if !ok {
				return nil, errors.New("no such host")
			}
			result := make([]*net.MX, 0, len(hosts))
			for _, host := range hosts {
				result = append(result, &net.MX{Host: host + ".", Pref: 5})
			}
			return result, nil
		}
	}
	lookupMX, lookupPublicMX = records(mx), records(publicMX)
}

func TestDiscoverFollowsCNAME(t *testing.T) {
	// 真实情况：公司内网 DNS 里 imap./smtp.<域名> 是指到网易企业邮的 CNAME，MX 却查不到。
	fakeDNS(t, map[string]string{
		"imap.ickey.cn": "imap.qiye.163.com",
		"smtp.ickey.cn": "smtp.qiye.163.com",
	}, nil, nil)
	account, err := Discover(context.Background(), Account{Address: "someone@ickey.cn"}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if account.IMAPHost != "imap.qiye.163.com" || account.IMAPPort != 993 || account.SMTPHost != "smtp.qiye.163.com" || account.SMTPPort != 465 {
		t.Fatalf("应当跟着 CNAME 走：%+v", account)
	}
}

func TestDiscoverByMXFallsBackToPublicDNS(t *testing.T) {
	fakeDNS(t, nil, map[string][]string{}, map[string][]string{
		"corp.example":    {"qiye163mx01.mxmail.netease.com", "qiye163mx02.mxmail.netease.com"},
		"tencent.example": {"mxbiz1.qq.com"},
		"ali.example":     {"mx1.qiye.aliyun.com"},
		"self.example":    {"mail.self.example"},
	})
	for domain, want := range map[string]string{
		"corp.example":    "imap.qiye.163.com",
		"tencent.example": "imap.exmail.qq.com",
		"ali.example":     "imap.qiye.aliyun.com",
		"self.example":    "imap.self.example",
	} {
		account, _ := Discover(context.Background(), Account{Address: "a@" + domain}).Normalize()
		if account.IMAPHost != want {
			t.Fatalf("%s 应当识别成 %s，得到 %s", domain, want, account.IMAPHost)
		}
	}
}

func TestDiscoverKeepsWhatUserFilled(t *testing.T) {
	fakeDNS(t, map[string]string{"imap.corp.example": "x.example", "smtp.corp.example": "y.example"}, nil, nil)
	account := Discover(context.Background(), Account{Address: "a@corp.example", IMAPHost: "mail.corp.example"})
	if account.IMAPHost != "mail.corp.example" || account.SMTPHost != "" {
		t.Fatalf("填了服务器就不该再猜：%+v", account)
	}
	qq := Discover(context.Background(), Account{Address: "a@qq.com"})
	if qq.IMAPHost != "" {
		t.Fatalf("常见邮箱不用查 DNS：%+v", qq)
	}
}

func TestCertHostPicksNameFromCertificate(t *testing.T) {
	certificate := &x509.Certificate{DNSNames: []string{"*.qiye.163.com", "qiye.163.com"}}
	err := &tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: certificate, Host: "imap.ickey.cn"}}
	wrapped := fmt.Errorf("dial: %w", err)
	if got := certHost(wrapped, "imap.ickey.cn"); got != "imap.qiye.163.com" {
		t.Fatalf("应当按证书换成 imap.qiye.163.com，得到 %q", got)
	}
	if got := certHost(wrapped, "smtp.ickey.cn"); got != "smtp.qiye.163.com" {
		t.Fatalf("发信那边应当是 smtp.qiye.163.com，得到 %q", got)
	}
	if got := certHost(errors.New("connection refused"), "imap.ickey.cn"); got != "" {
		t.Fatalf("不是证书问题时不该换：%q", got)
	}
}
