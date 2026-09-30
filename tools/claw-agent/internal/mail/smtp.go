package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"mime"
	"net"
	"net/smtp"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	gomail "github.com/emersion/go-message/mail"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

// Outgoing 是要发出去的一封信。
type Outgoing struct {
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
	// InReplyTo / References 让回信在对方的客户端里归进同一个会话。
	InReplyTo  string
	References []string
	// Attachments 是已经读进内存的附件。
	Attachments []OutgoingAttachment
}

// OutgoingAttachment 是一个要附上的文件。
type OutgoingAttachment struct {
	Name string
	Data []byte
}

// SendResult 说清发完之后的情况。
type SendResult struct {
	MessageID string
	// SavedTo 是补存进去的「已发送」信箱；为空表示没补存（服务器自己存或者没找到）。
	SavedTo string
	// SaveError 是补存失败的原因。信已经发出去了，这只是提醒。
	SaveError string
}

// ParseRecipients 把「a@b.com, 张三 <c@d.com>」这样的文字或列表解析成地址。
func ParseRecipients(values []string) ([]*gomail.Address, error) {
	var result []*gomail.Address
	for _, value := range values {
		for _, piece := range splitRecipients(value) {
			address, err := gomail.ParseAddress(piece)
			if err != nil {
				return nil, i18n.E("收件地址不对：{address}", "address", strconv.Quote(piece))
			}
			result = append(result, address)
		}
	}
	return result, nil
}

// splitRecipients 按逗号、分号切开，但不切引号里的（"张, 三" <a@b.com>）。
func splitRecipients(value string) []string {
	var (
		parts   []string
		current strings.Builder
		quoted  bool
	)
	for _, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case (r == ',' || r == ';' || r == '，' || r == '；') && !quoted:
			if piece := strings.TrimSpace(current.String()); piece != "" {
				parts = append(parts, piece)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if piece := strings.TrimSpace(current.String()); piece != "" {
		parts = append(parts, piece)
	}
	return parts
}

// Send 发一封信，发完在「已发送」里补存一份（服务器自己会存的除外）。
func Send(ctx context.Context, account Account, outgoing Outgoing) (SendResult, error) {
	to, err := ParseRecipients(outgoing.To)
	if err != nil {
		return SendResult{}, err
	}
	cc, err := ParseRecipients(outgoing.Cc)
	if err != nil {
		return SendResult{}, err
	}
	bcc, err := ParseRecipients(outgoing.Bcc)
	if err != nil {
		return SendResult{}, err
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return SendResult{}, i18n.E("没有收件人")
	}
	message, messageID, err := compose(account, to, cc, outgoing)
	if err != nil {
		return SendResult{}, err
	}
	recipients := make([]string, 0, len(to)+len(cc)+len(bcc))
	for _, list := range [][]*gomail.Address{to, cc, bcc} {
		for _, address := range list {
			recipients = append(recipients, address.Address)
		}
	}
	if err := deliver(ctx, account, recipients, message); err != nil {
		return SendResult{}, err
	}
	result := SendResult{MessageID: messageID}
	if account.savesSentItself() {
		return result, nil
	}
	saved, err := saveSent(ctx, account, message)
	if err != nil {
		result.SaveError = err.Error()
	}
	result.SavedTo = saved
	return result, nil
}

// compose 按 RFC 5322 拼出整封信：纯文本正文，附件各成一段。
func compose(account Account, to, cc []*gomail.Address, outgoing Outgoing) ([]byte, string, error) {
	var header gomail.Header
	header.SetDate(time.Now())
	header.SetAddressList("From", []*gomail.Address{{Name: account.Name, Address: account.Address}})
	header.SetAddressList("To", to)
	if len(cc) > 0 {
		header.SetAddressList("Cc", cc)
	}
	header.SetSubject(outgoing.Subject)
	if err := header.GenerateMessageIDWithHostname(Domain(account.Address)); err != nil {
		return nil, "", err
	}
	messageID, _ := header.MessageID()
	if outgoing.InReplyTo != "" {
		header.SetMsgIDList("In-Reply-To", []string{outgoing.InReplyTo})
	}
	if len(outgoing.References) > 0 {
		header.SetMsgIDList("References", outgoing.References)
	}

	var buffer bytes.Buffer
	body := strings.ReplaceAll(outgoing.Body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	if len(outgoing.Attachments) == 0 {
		header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		writer, err := gomail.CreateSingleInlineWriter(&buffer, header)
		if err != nil {
			return nil, "", err
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			return nil, "", err
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), messageID, nil
	}

	writer, err := gomail.CreateWriter(&buffer, header)
	if err != nil {
		return nil, "", err
	}
	var inline gomail.InlineHeader
	inline.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	part, err := writer.CreateSingleInline(inline)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write([]byte(body)); err != nil {
		return nil, "", err
	}
	if err := part.Close(); err != nil {
		return nil, "", err
	}
	for _, attachment := range outgoing.Attachments {
		var attachmentHeader gomail.AttachmentHeader
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.Name)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if index := strings.Index(contentType, ";"); index >= 0 {
			contentType = contentType[:index]
		}
		attachmentHeader.SetContentType(contentType, nil)
		attachmentHeader.SetFilename(attachment.Name)
		part, err := writer.CreateAttachment(attachmentHeader)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(attachment.Data); err != nil {
			return nil, "", err
		}
		if err := part.Close(); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), messageID, nil
}

// smtpClient 连上发信服务器并登录。465 是一上来就 TLS，其余端口走 STARTTLS。
// 证书上的名字对不上时按证书上的名字重连一次，并把 account.SMTPHost 改成它。
func smtpClient(ctx context.Context, account *Account) (*smtp.Client, func(), error) {
	client, cleanup, err := smtpClientAt(ctx, *account, account.SMTPHost)
	if err == nil {
		return client, cleanup, nil
	}
	if fixed := certHost(err, account.SMTPHost); fixed != "" && fixed != account.SMTPHost {
		if client, cleanup, retryErr := smtpClientAt(ctx, *account, fixed); retryErr == nil {
			account.SMTPHost = fixed
			return client, cleanup, nil
		}
	}
	return nil, nil, err
}

func smtpClientAt(ctx context.Context, account Account, host string) (*smtp.Client, func(), error) {
	account.SMTPHost = host
	address := net.JoinHostPort(account.SMTPHost, strconv.Itoa(account.SMTPPort))
	dialer := &net.Dialer{Timeout: dialTimeout}
	var (
		connection net.Conn
		err        error
	)
	if account.SMTPPort == 465 && !insecureForTest {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: account.SMTPHost})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, nil, wrapErr(i18n.D("连不上发信服务器 {address}", "address", address), err)
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stop:
		}
	}()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))
	client, err := smtp.NewClient(connection, account.SMTPHost)
	if err != nil {
		close(stop)
		_ = connection.Close()
		return nil, nil, wrapErr(i18n.D("发信服务器 {address} 没有正常应答", "address", address), err)
	}
	cleanup := func() {
		close(stop)
		_ = client.Close()
	}
	if err := client.Hello("localhost"); err != nil {
		cleanup()
		return nil, nil, wrapErr(i18n.D("发信服务器握手失败"), err)
	}
	if account.SMTPPort != 465 && !insecureForTest {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			cleanup()
			return nil, nil, i18n.E("发信服务器 {address} 不支持加密连接，不能在明文上发密码", "address", address)
		}
		if err := client.StartTLS(&tls.Config{ServerName: account.SMTPHost}); err != nil {
			cleanup()
			return nil, nil, wrapErr(i18n.D("发信服务器加密握手失败"), err)
		}
	}
	if err := client.Auth(loginAuth(account)); err != nil {
		cleanup()
		return nil, nil, i18n.E("发信服务器拒绝登录（{error}）。{hint}", "error", err, "hint", account.hint())
	}
	return client, cleanup, nil
}

// loginAuth 用 PLAIN。net/smtp 的 PlainAuth 只在 TLS 或 localhost 上肯发密码，
// 测试连的是本机明文服务器，正合适；真实连接都已经是 TLS。
func loginAuth(account Account) smtp.Auth {
	return smtp.PlainAuth("", account.Username, account.Password, account.SMTPHost)
}

func testSMTP(ctx context.Context, account *Account) error {
	client, cleanup, err := smtpClient(ctx, account)
	if err != nil {
		return err
	}
	defer cleanup()
	return client.Quit()
}

func deliver(ctx context.Context, account Account, recipients []string, message []byte) error {
	client, cleanup, err := smtpClient(ctx, &account)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := client.Mail(account.Address); err != nil {
		return wrapErr(i18n.D("发信服务器不接受发件人 {address}", "address", account.Address), err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return wrapErr(i18n.D("发信服务器不接受收件人 {address}", "address", recipient), err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return wrapErr(i18n.D("发信失败"), err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return wrapErr(i18n.D("发信失败"), err)
	}
	if err := writer.Close(); err != nil {
		return wrapErr(i18n.D("发信服务器没有收下这封信"), err)
	}
	return client.Quit()
}

// saveSent 把发出去的信补存进「已发送」，标成已读。
func saveSent(ctx context.Context, account Account, message []byte) (string, error) {
	c, err := connect(ctx, &account)
	if err != nil {
		return "", err
	}
	defer c.close()
	folder := c.sentFolder()
	if folder == "" {
		return "", i18n.E("没找到「已发送」信箱")
	}
	command := c.client.Append(folder, int64(len(message)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}, Time: time.Now()})
	if _, err := command.Write(message); err != nil {
		_ = command.Close()
		return "", err
	}
	if err := command.Close(); err != nil {
		return "", err
	}
	if _, err := command.Wait(); err != nil {
		return "", err
	}
	return folder, nil
}
