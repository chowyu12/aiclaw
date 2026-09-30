package mail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestNormalizeFillsServers(t *testing.T) {
	account, err := Account{Address: "someone@qq.com", Password: "x"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if account.IMAPHost != "imap.qq.com" || account.IMAPPort != 993 || account.SMTPHost != "smtp.qq.com" || account.SMTPPort != 465 {
		t.Fatalf("QQ 邮箱的服务器没补对：%+v", account)
	}
	if account.Username != "someone@qq.com" {
		t.Fatalf("用户名应默认是地址：%q", account.Username)
	}
	guessed, err := Account{Address: "a@corp.example"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if guessed.IMAPHost != "imap.corp.example" || guessed.SMTPHost != "smtp.corp.example" {
		t.Fatalf("没列到的域名应按 imap./smtp. 猜：%+v", guessed)
	}
	custom, _ := Account{Address: "a@corp.example", SMTPHost: "mail.corp.example", SMTPPort: 587}.Normalize()
	if custom.SMTPHost != "mail.corp.example" || custom.SMTPPort != 587 || custom.IMAPPort != 993 {
		t.Fatalf("填了的不该被改：%+v", custom)
	}
	if _, err := (Account{Address: "not an address"}).Normalize(); err == nil {
		t.Fatal("不合法的地址应当报错")
	}
}

func TestHTMLToText(t *testing.T) {
	source := `<html><head><style>p{color:red}</style></head><body>
<p>您好，&nbsp;张三：</p><p>请查看<a href="https://example.com/r?id=1">报告</a>。</p>
<ul><li>第一项</li><li>第二项</li></ul><script>alert(1)</script></body></html>`
	text := HTMLToText(source)
	for _, want := range []string{"您好， 张三：", "报告 (https://example.com/r?id=1)", "• 第一项", "• 第二项"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺 %q：\n%s", want, text)
		}
	}
	if strings.Contains(text, "alert") || strings.Contains(text, "color") {
		t.Fatalf("脚本与样式应当去掉：\n%s", text)
	}
}

func TestSplitRecipients(t *testing.T) {
	got := splitRecipients(`"张, 三" <a@b.com>, c@d.com；e@f.com`)
	if len(got) != 3 || got[0] != `"张, 三" <a@b.com>` {
		t.Fatalf("切错了：%q", got)
	}
}

// ---- 用内存 IMAP 服务器与一个假的 SMTP 服务器走一遍 ----

type literal struct {
	*bytes.Reader
}

func (l literal) Size() int64 { return int64(l.Len()) }

func appendRaw(t *testing.T, user *imapmemserver.User, mailbox, raw string, flags ...imap.Flag) {
	t.Helper()
	data := strings.ReplaceAll(raw, "\n", "\r\n")
	if _, err := user.Append(mailbox, literal{bytes.NewReader([]byte(data))}, &imap.AppendOptions{Flags: flags}); err != nil {
		t.Fatal(err)
	}
}

func gbk(t *testing.T, text string) string {
	t.Helper()
	encoded, err := simplifiedchinese.GBK.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func startIMAP(t *testing.T) (*imapmemserver.User, int) {
	t.Helper()
	memory := imapmemserver.New()
	user := imapmemserver.NewUser("me@example.com", "secret")
	memory.AddUser(user)
	for _, name := range []string{"INBOX", "Sent"} {
		if err := user.Create(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memory.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return user, listener.Addr().(*net.TCPAddr).Port
}

// fakeSMTP 收下一封信并记住信封与正文。
type fakeSMTP struct {
	mu         sync.Mutex
	from       string
	recipients []string
	data       string
	auth       string
}

func startSMTP(t *testing.T) (*fakeSMTP, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	fake := &fakeSMTP{}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go fake.serve(connection)
		}
	}()
	return fake, listener.Addr().(*net.TCPAddr).Port
}

func (f *fakeSMTP) serve(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = io.WriteString(connection, line+"\r\n") }
	write("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-fake")
			write("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			decoded, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			f.mu.Lock()
			f.auth = string(decoded)
			f.mu.Unlock()
			if strings.HasSuffix(string(decoded), "\x00secret") {
				write("235 ok")
			} else {
				write("535 bad credentials")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			f.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			f.mu.Lock()
			f.recipients = append(f.recipients, strings.Trim(line[len("RCPT TO:"):], "<> "))
			f.mu.Unlock()
			write("250 ok")
		case upper == "DATA":
			write("354 go ahead")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.data = body.String()
			f.mu.Unlock()
			write("250 queued")
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func testAccount(imapPort, smtpPort int) Account {
	return Account{
		Address: "me@example.com", Name: "我", Username: "me@example.com", Password: "secret",
		IMAPHost: "127.0.0.1", IMAPPort: imapPort, SMTPHost: "127.0.0.1", SMTPPort: smtpPort,
	}
}

func TestListReadSendAgainstServers(t *testing.T) {
	insecureForTest = true
	t.Cleanup(func() { insecureForTest = false })
	user, imapPort := startIMAP(t)
	fake, smtpPort := startSMTP(t)
	account := testAccount(imapPort, smtpPort)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	appendRaw(t, user, "INBOX", `From: Alice <alice@example.com>
To: me@example.com
Subject: 周报
Date: Mon, 28 Sep 2026 09:00:00 +0800
Message-ID: <weekly@example.com>
Content-Type: multipart/alternative; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8

本周完成了三件事。
--b1
Content-Type: text/html; charset=utf-8

<p>本周完成了<b>三件事</b>。</p>
--b1--
`, imap.FlagSeen)
	appendRaw(t, user, "INBOX", "From: =?GBK?B?"+base64.StdEncoding.EncodeToString([]byte(gbk(t, "张三")))+"?= <zhang@example.com>\n"+
		"To: me@example.com\n"+
		"Subject: =?GBK?B?"+base64.StdEncoding.EncodeToString([]byte(gbk(t, "报价单")))+"?=\n"+
		"Date: Tue, 29 Sep 2026 10:00:00 +0800\n"+
		"Message-ID: <quote@example.com>\n"+
		"References: <older@example.com>\n"+
		"Content-Type: multipart/mixed; boundary=\"m1\"\n\n"+
		"--m1\nContent-Type: text/html; charset=gbk\nContent-Transfer-Encoding: base64\n\n"+
		base64.StdEncoding.EncodeToString([]byte(gbk(t, "<div>请看附件里的报价。</div>")))+"\n"+
		"--m1\nContent-Type: text/csv; name=\"price.csv\"\nContent-Disposition: attachment; filename=\"price.csv\"\nContent-Transfer-Encoding: base64\n\n"+
		base64.StdEncoding.EncodeToString([]byte("item,price\napple,3\n"))+"\n--m1--\n")

	items, total, err := List(ctx, account, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("应当列出两封：total=%d %+v", total, items)
	}
	newest := items[0]
	if newest.Subject != "报价单" || !strings.Contains(newest.From, "张三") || !newest.Unread || !newest.Attachments {
		t.Fatalf("最新一封（GBK 标题、未读、带附件）不对：%+v", newest)
	}
	if items[1].Subject != "周报" || items[1].Unread {
		t.Fatalf("第二封不对：%+v", items[1])
	}

	unread, _, err := List(ctx, account, ListQuery{Unread: true})
	if err != nil || len(unread) != 1 || unread[0].Subject != "报价单" {
		t.Fatalf("只列未读不对：%v %+v", err, unread)
	}
	byKeyword, _, err := List(ctx, account, ListQuery{Query: "alice"})
	if err != nil || len(byKeyword) != 1 || byKeyword[0].Subject != "周报" {
		t.Fatalf("按发件人找不对：%v %+v", err, byKeyword)
	}

	weekly, err := Read(ctx, account, "INBOX", items[1].UID, true)
	if err != nil {
		t.Fatal(err)
	}
	if weekly.Text != "本周完成了三件事。" {
		t.Fatalf("有纯文本时应当用纯文本：%q", weekly.Text)
	}

	quote, err := Read(ctx, account, "INBOX", newest.UID, true)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Text != "请看附件里的报价。" {
		t.Fatalf("GBK 的 HTML 正文应当转成文字：%q", quote.Text)
	}
	if len(quote.Attachments) != 1 || quote.Attachments[0].Name != "price.csv" {
		t.Fatalf("附件清单不对：%+v", quote.Attachments)
	}
	if quote.MessageID != "quote@example.com" || len(quote.References) != 1 || quote.References[0] != "older@example.com" {
		t.Fatalf("Message-ID / References 不对：%q %q", quote.MessageID, quote.References)
	}
	if after, _, _ := List(ctx, account, ListQuery{Unread: true}); len(after) != 0 {
		t.Fatalf("读过之后应当是已读：%+v", after)
	}

	attachment, data, err := FetchAttachment(ctx, account, "INBOX", newest.UID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Name != "price.csv" || string(data) != "item,price\napple,3\n" {
		t.Fatalf("附件内容不对：%s %q", attachment.Name, data)
	}
	if _, _, err := FetchAttachment(ctx, account, "INBOX", newest.UID, 2); err == nil {
		t.Fatal("没有第二个附件时应当报错")
	}

	if _, err := Read(ctx, account, "不存在", 1, false); err == nil || !strings.Contains(err.Error(), "INBOX") {
		t.Fatalf("信箱不存在时应当列出有哪些：%v", err)
	}

	result, err := Send(ctx, account, Outgoing{
		To: []string{"张三 <zhang@example.com>, bob@example.com"}, Bcc: []string{"boss@example.com"},
		Subject: "Re: 报价单", Body: "收到，谢谢。\n下周给你回复。",
		InReplyTo: quote.MessageID, References: append(quote.References, quote.MessageID),
		Attachments: []OutgoingAttachment{{Name: "回执.txt", Data: []byte("ok")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	from, recipients, sent := fake.from, fake.recipients, fake.data
	fake.mu.Unlock()
	if from != "me@example.com" || strings.Join(recipients, ",") != "zhang@example.com,bob@example.com,boss@example.com" {
		t.Fatalf("信封不对：from=%s to=%v", from, recipients)
	}
	for _, want := range []string{"In-Reply-To: <quote@example.com>", "References: <older@example.com> <quote@example.com>", "=?utf-8?"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("发出去的信里缺 %q：\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "boss@example.com") {
		t.Fatalf("密送地址不该出现在信头里：\n%s", sent)
	}
	if result.SavedTo != "Sent" || result.SaveError != "" {
		t.Fatalf("应当补存进 Sent：%+v", result)
	}
	sentItems, _, err := List(ctx, account, ListQuery{Folder: "Sent"})
	if err != nil || len(sentItems) != 1 || sentItems[0].Subject != "Re: 报价单" || sentItems[0].Unread {
		t.Fatalf("已发送里应当有一封已读的：%v %+v", err, sentItems)
	}
	saved, err := Read(ctx, account, "Sent", sentItems[0].UID, false)
	if err != nil || !strings.Contains(saved.Text, "下周给你回复。") || len(saved.Attachments) != 1 || saved.Attachments[0].Name != "回执.txt" {
		t.Fatalf("存进去的信读回来不对：%v %+v", err, saved)
	}

	wrong := account
	wrong.Password = "nope"
	if err := Test(ctx, wrong); err == nil || !strings.Contains(err.Error(), "拒绝登录") {
		t.Fatalf("密码错时应当说拒绝登录：%v", err)
	}
	if err := Test(ctx, account); err != nil {
		t.Fatalf("测试连接应当通过：%v", err)
	}
}
