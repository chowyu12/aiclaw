package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

// dialTimeout 是连服务器的上限。邮件服务器偶尔很慢，但半分钟还没握上手就是连不上。
const dialTimeout = 20 * time.Second

// maxTextBytes 是一封信正文最多拿多少。营销邮件的 HTML 动辄几百 KB，转成文字后
// 大部分是空白与链接；再多也不该一次塞进上下文。
const maxTextBytes = 256 << 10

// maxAttachmentBytes 是单个附件最多下载多大。
const maxAttachmentBytes = 25 << 20

// insecureForTest 让测试连不带 TLS 的本机服务器。只有测试会改它。
var insecureForTest bool

// conn 是一次登录好的 IMAP 连接。
type conn struct {
	client *imapclient.Client
	stop   chan struct{}
}

func (c *conn) close() {
	close(c.stop)
	_ = c.client.Logout().Wait()
	_ = c.client.Close()
}

// connect 连上并登录。ctx 取消时连接被强制关掉，正在等的命令随之返回错误。
//
// 证书上的名字与服务器对不上（企业邮箱用自己域名的 CNAME 指到服务商）时，按证书
// 上的名字重连一次，并把 account.IMAPHost 改成它——调用方拿得到实际连上的是哪台。
func connect(ctx context.Context, account *Account) (*conn, error) {
	client, err := dialIMAP(account.IMAPHost, account.IMAPPort)
	if err != nil {
		if fixed := certHost(err, account.IMAPHost); fixed != "" && fixed != account.IMAPHost {
			if retried, retryErr := dialIMAP(fixed, account.IMAPPort); retryErr == nil {
				account.IMAPHost, client, err = fixed, retried, nil
			}
		}
	}
	if err != nil {
		address := net.JoinHostPort(account.IMAPHost, strconv.Itoa(account.IMAPPort))
		return nil, fmt.Errorf("连不上收信服务器 %s：%w", address, err)
	}
	c := &conn{client: client, stop: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-c.stop:
		}
	}()
	if err := client.Login(account.Username, account.Password).Wait(); err != nil {
		c.close()
		return nil, fmt.Errorf("收信服务器拒绝登录（%v）。%s", err, account.hint())
	}
	// 163 系不先报家门就拒绝选信箱（Unsafe Login）。别家支持 ID 的也无妨。
	if client.Caps().Has(imap.CapID) {
		_, _ = client.ID(&imap.IDData{Name: "AIClaw", Version: "1.0", Vendor: "AIClaw"}).Wait()
	}
	return c, nil
}

func dialIMAP(host string, port int) (*imapclient.Client, error) {
	options := &imapclient.Options{
		// 国内邮件的标题常是 GBK / GB2312 编码，默认的解码器只认 UTF-8 与 Latin-1。
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
		Dialer:      &net.Dialer{Timeout: dialTimeout},
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	switch {
	case insecureForTest:
		return imapclient.DialInsecure(address, options)
	case port == 993:
		return imapclient.DialTLS(address, options)
	default:
		return imapclient.DialStartTLS(address, options)
	}
}

// selectFolder 选中信箱。找不到时把有哪些信箱一并说出来，模型好换个名字再试。
func (c *conn) selectFolder(folder string, readOnly bool) error {
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if _, err := c.client.Select(folder, &imap.SelectOptions{ReadOnly: readOnly}).Wait(); err != nil {
		names, listErr := c.folders()
		if listErr != nil || len(names) == 0 {
			return fmt.Errorf("打不开信箱 %q：%w", folder, err)
		}
		return fmt.Errorf("打不开信箱 %q（%v）。有这些信箱：%s", folder, err, strings.Join(names, "、"))
	}
	return nil
}

func (c *conn) folders() ([]string, error) {
	items, err := c.client.List("", "*", nil).Collect()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		if hasAttr(item.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		names = append(names, item.Mailbox)
	}
	return names, nil
}

// sentFolder 找「已发送」信箱：先看服务器标的 \Sent，再按常见名字找。
func (c *conn) sentFolder() string {
	items, err := c.client.List("", "*", nil).Collect()
	if err != nil {
		return ""
	}
	for _, item := range items {
		if hasAttr(item.Attrs, imap.MailboxAttrSent) {
			return item.Mailbox
		}
	}
	for _, want := range []string{"Sent Messages", "Sent", "Sent Items", "已发送", "Sent Mail"} {
		for _, item := range items {
			if strings.EqualFold(item.Mailbox, want) {
				return item.Mailbox
			}
		}
	}
	return ""
}

func hasAttr(attrs []imap.MailboxAttr, want imap.MailboxAttr) bool {
	for _, attr := range attrs {
		if strings.EqualFold(string(attr), string(want)) {
			return true
		}
	}
	return false
}

// Folders 列出能打开的信箱。
func Folders(ctx context.Context, account Account) ([]string, error) {
	c, err := connect(ctx, &account)
	if err != nil {
		return nil, err
	}
	defer c.close()
	return c.folders()
}

// Test 试着登录收信与发信两台服务器，给配置页的「测试」用。返回实际连上的服务器
// （证书对不上时会换成证书上的名字）。
func Test(ctx context.Context, account Account) (Account, error) {
	c, err := connect(ctx, &account)
	if err != nil {
		return account, err
	}
	err = c.selectFolder("INBOX", true)
	c.close()
	if err != nil {
		return account, err
	}
	return account, testSMTP(ctx, &account)
}

// ListQuery 是列信的条件。
type ListQuery struct {
	Folder string
	// Unread 只要未读的。
	Unread bool
	// Query 在发件人、主题里找（服务器不支持时退回在最近的信里自己找）。
	Query string
	// Since 只要这天之后收到的。
	Since time.Time
	Limit int
}

// Summary 是信件列表里的一行。
type Summary struct {
	UID         uint32
	Date        time.Time
	From        string
	Subject     string
	Unread      bool
	Flagged     bool
	Attachments bool
}

// clientFilterWindow 是服务器搜不了时自己翻的最近多少封。
const clientFilterWindow = 300

// List 按条件列出信件，新的在前。total 是满足条件的总数（可能多于返回的）。
func List(ctx context.Context, account Account, query ListQuery) (items []Summary, total int, err error) {
	if query.Limit <= 0 || query.Limit > 50 {
		query.Limit = 20
	}
	c, err := connect(ctx, &account)
	if err != nil {
		return nil, 0, err
	}
	defer c.close()
	if err := c.selectFolder(query.Folder, true); err != nil {
		return nil, 0, err
	}

	criteria := &imap.SearchCriteria{Since: query.Since}
	if query.Unread {
		criteria.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	keyword := strings.TrimSpace(query.Query)
	filterLocally := false
	if keyword != "" {
		withKeyword := *criteria
		withKeyword.Or = [][2]imap.SearchCriteria{{
			{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: keyword}}},
			{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: keyword}}},
		}}
		data, searchErr := c.client.UIDSearch(&withKeyword, nil).Wait()
		if searchErr == nil {
			return c.summaries(data.AllUIDs(), query.Limit, nil)
		}
		// 不少服务器对中文搜索报错或不支持 CHARSET：退回在最近的信里自己找。
		filterLocally = true
	}
	data, err := c.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, 0, fmt.Errorf("查信失败：%w", err)
	}
	uids := data.AllUIDs()
	if !filterLocally {
		return c.summaries(uids, query.Limit, nil)
	}
	match := func(item Summary) bool {
		lower := strings.ToLower(keyword)
		return strings.Contains(strings.ToLower(item.From), lower) || strings.Contains(strings.ToLower(item.Subject), lower)
	}
	return c.summaries(uids, query.Limit, match)
}

// summaries 取最新的那几封的信封。match 非空时在最近 clientFilterWindow 封里筛。
func (c *conn) summaries(uids []imap.UID, limit int, match func(Summary) bool) ([]Summary, int, error) {
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	window := uids
	if match == nil {
		if len(window) > limit {
			window = window[:limit]
		}
	} else if len(window) > clientFilterWindow {
		window = window[:clientFilterWindow]
	}
	if len(window) == 0 {
		return []Summary{}, 0, nil
	}
	messages, err := c.client.Fetch(imap.UIDSetNum(window...), &imap.FetchOptions{
		UID: true, Envelope: true, Flags: true, BodyStructure: &imap.FetchItemBodyStructure{},
	}).Collect()
	if err != nil {
		return nil, 0, fmt.Errorf("取信件列表失败：%w", err)
	}
	items := make([]Summary, 0, len(messages))
	for _, message := range messages {
		item := Summary{UID: uint32(message.UID)}
		if envelope := message.Envelope; envelope != nil {
			item.Date = envelope.Date
			item.From = formatAddresses(envelope.From)
			item.Subject = envelope.Subject
		}
		item.Unread = !hasFlag(message.Flags, imap.FlagSeen)
		item.Flagged = hasFlag(message.Flags, imap.FlagFlagged)
		if message.BodyStructure != nil {
			item.Attachments = len(attachmentsOf(message.BodyStructure)) > 0
		}
		if match != nil && !match(item) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UID > items[j].UID })
	total := len(uids)
	if match != nil {
		total = len(items)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, total, nil
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, flag := range flags {
		if strings.EqualFold(string(flag), string(want)) {
			return true
		}
	}
	return false
}

// Attachment 是一封信里的一个附件。
type Attachment struct {
	Index int
	Name  string
	Type  string
	Size  uint32
	part  []int
	enc   string
}

// Message 是读出来的一封信。
type Message struct {
	UID         uint32
	Folder      string
	Date        time.Time
	From        []imap.Address
	ReplyTo     []imap.Address
	To          []imap.Address
	Cc          []imap.Address
	Subject     string
	MessageID   string
	InReplyTo   []string
	References  []string
	Text        string
	Truncated   bool
	Attachments []Attachment
}

// Read 读一封信：信封、正文（优先纯文本，只有 HTML 时转成文字）、附件清单。
// markSeen 为 true 时读完标成已读，和在邮件客户端里点开一样。
func Read(ctx context.Context, account Account, folder string, uid uint32, markSeen bool) (*Message, error) {
	c, err := connect(ctx, &account)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if err := c.selectFolder(folder, !markSeen); err != nil {
		return nil, err
	}
	return c.read(folder, imap.UID(uid), markSeen)
}

func (c *conn) read(folder string, uid imap.UID, markSeen bool) (*Message, error) {
	referencesSection := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"References"}, Peek: true,
	}
	found, err := c.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID: true, Envelope: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		BodySection: []*imap.FetchItemBodySection{referencesSection},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("读信失败：%w", err)
	}
	if len(found) == 0 || found[0].Envelope == nil {
		return nil, fmt.Errorf("信箱 %s 里没有编号 %d 的信（编号来自 email_list，换了信箱要重新列）", folder, uid)
	}
	raw := found[0]
	envelope := raw.Envelope
	message := &Message{
		UID: uint32(uid), Folder: folder, Date: envelope.Date,
		From: envelope.From, ReplyTo: envelope.ReplyTo, To: envelope.To, Cc: envelope.Cc,
		Subject: envelope.Subject, MessageID: envelope.MessageID, InReplyTo: envelope.InReplyTo,
		References: parseReferences(raw.FindBodySection(referencesSection)),
	}
	if raw.BodyStructure != nil {
		message.Attachments = attachmentsOf(raw.BodyStructure)
		text, truncated, err := c.bodyText(uid, raw.BodyStructure)
		if err != nil {
			return nil, err
		}
		message.Text, message.Truncated = text, truncated
	}
	if markSeen {
		_ = c.client.Store(imap.UIDSetNum(uid), &imap.StoreFlags{
			Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen},
		}, nil).Close()
	}
	return message, nil
}

// bodyText 取正文。multipart/alternative 里有纯文本就用纯文本，否则用 HTML 转的文字。
func (c *conn) bodyText(uid imap.UID, structure imap.BodyStructure) (string, bool, error) {
	var plain, html *textPart
	structure.Walk(func(path []int, part imap.BodyStructure) bool {
		single, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		if isAttachment(single) {
			return false
		}
		candidate := &textPart{path: append([]int(nil), path...), part: single}
		switch single.MediaType() {
		case "text/plain":
			if plain == nil {
				plain = candidate
			}
		case "text/html":
			if html == nil {
				html = candidate
			}
		}
		return true
	})
	chosen, isHTML := plain, false
	if chosen == nil {
		chosen, isHTML = html, true
	}
	if chosen == nil {
		return "", false, nil
	}
	_, single := structure.(*imap.BodyStructureSinglePart)
	data, truncated, err := c.section(uid, chosen.path, single, maxTextBytes)
	if err != nil {
		return "", false, err
	}
	text := decodePart(data, chosen.part.Encoding, chosen.part.Params["charset"])
	if isHTML {
		text = HTMLToText(text)
	}
	return strings.TrimSpace(text), truncated, nil
}

type textPart struct {
	path []int
	part *imap.BodyStructureSinglePart
}

// section 取一个部分的原始内容（还没解传输编码）。整封信只有一个部分时它在 TEXT 里。
func (c *conn) section(uid imap.UID, path []int, whole bool, limit uint32) ([]byte, bool, error) {
	item := &imap.FetchItemBodySection{Part: path, Peek: true}
	if whole {
		item = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierText, Peek: true}
	}
	if limit > 0 {
		item.Partial = &imap.SectionPartial{Offset: 0, Size: int64(limit) + 1}
	}
	found, err := c.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID: true, BodySection: []*imap.FetchItemBodySection{item},
	}).Collect()
	if err != nil {
		return nil, false, fmt.Errorf("取信件内容失败：%w", err)
	}
	if len(found) == 0 {
		return nil, false, errors.New("取信件内容失败：服务器没有返回")
	}
	data := found[0].FindBodySection(item)
	truncated := limit > 0 && uint32(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return data, truncated, nil
}

// FetchAttachment 下载一封信里的第 index 个附件（从 1 数，与 Read 给的编号一致）。
func FetchAttachment(ctx context.Context, account Account, folder string, uid uint32, index int) (Attachment, []byte, error) {
	c, err := connect(ctx, &account)
	if err != nil {
		return Attachment{}, nil, err
	}
	defer c.close()
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if err := c.selectFolder(folder, true); err != nil {
		return Attachment{}, nil, err
	}
	found, err := c.client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
		UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("读信失败：%w", err)
	}
	if len(found) == 0 || found[0].BodyStructure == nil {
		return Attachment{}, nil, fmt.Errorf("信箱 %s 里没有编号 %d 的信", folder, uid)
	}
	attachments := attachmentsOf(found[0].BodyStructure)
	if index < 1 || index > len(attachments) {
		return Attachment{}, nil, fmt.Errorf("这封信有 %d 个附件，没有第 %d 个", len(attachments), index)
	}
	attachment := attachments[index-1]
	if attachment.Size > maxAttachmentBytes*4/3 {
		return Attachment{}, nil, fmt.Errorf("附件 %s 太大（约 %s），超过 %s 不下载", attachment.Name, humanSize(attachment.Size*3/4), humanSize(maxAttachmentBytes))
	}
	_, single := found[0].BodyStructure.(*imap.BodyStructureSinglePart)
	data, _, err := c.section(imap.UID(uid), attachment.part, single, 0)
	if err != nil {
		return Attachment{}, nil, err
	}
	return attachment, decodeTransfer(data, attachment.enc), nil
}

// attachmentsOf 列出附件：标了 attachment 的，或者带文件名的非正文部分。
func attachmentsOf(structure imap.BodyStructure) []Attachment {
	var result []Attachment
	structure.Walk(func(path []int, part imap.BodyStructure) bool {
		single, ok := part.(*imap.BodyStructureSinglePart)
		if !ok || !isAttachment(single) {
			return true
		}
		name := single.Filename()
		if name == "" {
			name = single.Params["name"]
		}
		if name == "" && single.MediaType() == "message/rfc822" {
			name = "转发的邮件.eml"
		}
		if name == "" {
			name = fmt.Sprintf("附件%d", len(result)+1)
		}
		result = append(result, Attachment{
			Index: len(result) + 1, Name: name, Type: single.MediaType(), Size: single.Size,
			part: append([]int(nil), path...), enc: single.Encoding,
		})
		return false
	})
	return result
}

func isAttachment(part *imap.BodyStructureSinglePart) bool {
	if disposition := part.Disposition(); disposition != nil && strings.EqualFold(disposition.Value, "attachment") {
		return true
	}
	mediaType := part.MediaType()
	if mediaType == "message/rfc822" {
		return true
	}
	if strings.HasPrefix(mediaType, "text/") || strings.HasPrefix(mediaType, "multipart/") {
		return part.Filename() != ""
	}
	// 内嵌在 HTML 里的图片（cid:）也算：模型要看的话能下下来。
	return true
}

// decodeTransfer 解传输编码（base64 / quoted-printable）。
func decodeTransfer(data []byte, encoding string) []byte {
	var reader io.Reader
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(data))
	case "quoted-printable":
		reader = quotedprintable.NewReader(bytes.NewReader(data))
	default:
		return data
	}
	decoded, err := io.ReadAll(reader)
	if err != nil && len(decoded) == 0 {
		return data
	}
	return decoded
}

// decodePart 解传输编码再按字符集转成 UTF-8。截断在多字节字符中间也不报错。
func decodePart(data []byte, encoding, charsetName string) string {
	decoded := decodeTransfer(data, encoding)
	name := strings.ToLower(strings.TrimSpace(charsetName))
	if name == "" || name == "utf-8" || name == "utf8" || name == "us-ascii" {
		return strings.ToValidUTF8(string(decoded), "")
	}
	reader, err := charset.Reader(name, bytes.NewReader(decoded))
	if err != nil {
		return strings.ToValidUTF8(string(decoded), "")
	}
	converted, err := io.ReadAll(reader)
	if err != nil && len(converted) == 0 {
		return strings.ToValidUTF8(string(decoded), "")
	}
	return strings.ToValidUTF8(string(converted), "")
}

// parseReferences 从「References: <a> <b>」这样的头里取出各个 Message-ID。
func parseReferences(header []byte) []string {
	text := string(header)
	if index := strings.Index(text, ":"); index >= 0 {
		text = text[index+1:]
	}
	var ids []string
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, "<>,")
		if field != "" {
			ids = append(ids, field)
		}
	}
	return ids
}

// formatAddresses 把地址列表写成「张三 <a@b.com>, c@d.com」。
func formatAddresses(addresses []imap.Address) string {
	parts := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if text := formatAddress(address); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ", ")
}

func formatAddress(address imap.Address) string {
	email := address.Addr()
	if email == "" {
		return address.Name
	}
	if address.Name == "" || address.Name == email {
		return email
	}
	return address.Name + " <" + email + ">"
}

func humanSize(size uint32) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%d KB", size>>10)
	}
	return fmt.Sprintf("%d 字节", size)
}
