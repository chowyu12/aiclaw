package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/internal/i18n"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

/*
Office 文件读写：read_office、write_docx、edit_docx、write_xlsx、write_pptx。

为什么要单独的工具：docx/xlsx/pptx 本质是 zip 包，read_file 读出来是一串
二进制，模型只能说「看不了」；让它自己写脚本解压、拼 XML，又要依赖本机有
python-docx 之类的东西，而用户的机器上多半没有。这类文件在办公场景里又是
最常见的输入输出，值得内置。

依赖上的取舍：内核必须 CGO_ENABLED=0 能编（`make nocgo` 守着），所以只用
纯 Go。xlsx 用 excelize——表格的公式、共享字符串、样式、合并单元格细节太多，
自己写不值得；docx/pptx 没有同等分量又免费的纯 Go 库，而我们要的只是「读出
文字」和「写出一份能打开的最小文档」，用 archive/zip + encoding/xml 自己处理
反而更可控，不必为一个库背一整套 API 和升级负担。

写出来的包要能被 Word / WPS / Pages、Excel、PowerPoint / Keynote 打开——
这类软件对包结构很挑，缺一个 rels 或 content type 就会报「文件已损坏」，
测试里专门逐项校验了包结构。
*/

// maxOfficeText 是一次返回给模型的文字上限。Office 文件解出来的字经常比
// 原文件大得多（一张大表可以上 MB），全塞进上下文既贵又没用；超出就截断，
// 并告诉模型用什么参数读剩下的部分。
const maxOfficeText = 60 * 1024

// maxOfficePart 是解压单个包内部件的上限，防 zip 炸弹：几 KB 的压缩包
// 可以展开成几个 GB，这里宁可报错也不把内存吃光。
const maxOfficePart = 64 << 20

// OOXML 里反复要用的命名空间与关系类型。
const (
	nsRelationships = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsContentTypes  = "http://schemas.openxmlformats.org/package/2006/content-types"
	relOfficeDoc    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relCoreProps    = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	relAppProps     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties"
	relPrefix       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
)

// officeKind 按扩展名归类。legacy 为真表示是 97-2003 的二进制格式。
func officeKind(name string) (kind string, legacy bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx", ".docm", ".dotx", ".dotm":
		return "docx", false
	case ".xlsx", ".xlsm", ".xltx", ".xltm":
		return "xlsx", false
	case ".pptx", ".pptm", ".potx", ".potm", ".ppsx":
		return "pptx", false
	case ".doc", ".dot":
		return "docx", true
	case ".xls", ".xlt":
		return "xlsx", true
	case ".ppt", ".pps", ".pot":
		return "pptx", true
	}
	return "", false
}

// legacyOfficeError 说清老格式为什么读不了、用户该怎么办。
//
// 97-2003 的 .doc/.xls/.ppt 是 OLE 复合文档，与 OOXML 完全是两套东西，
// 纯 Go 里没有靠谱的解析实现。与其返回一堆乱码，不如直接给出路子：另存为
// 新格式，或者用本机已有的转换工具——只给提示，不替用户去跑。
func legacyOfficeError(name string) error {
	ext := strings.ToLower(filepath.Ext(name))
	modern := map[string]string{
		".doc": ".docx", ".dot": ".docx", ".xls": ".xlsx", ".xlt": ".xlsx",
		".ppt": ".pptx", ".pps": ".pptx", ".pot": ".pptx",
	}[ext]
	if modern == "" {
		modern = i18n.D("新格式（docx/xlsx/pptx）")
	}
	return i18n.E(
		"{name} 是 97-2003 的旧二进制格式（{ext}），暂不支持读取。请用 Office/WPS 另存为 {modern} 后再读；也可以用本机的转换工具先转一下，例如 macOS 上 `textutil -convert docx 文件.doc`（仅限 Word），或装了 LibreOffice 时用 `soffice --headless --convert-to {format} 文件{ext}`",
		"name", filepath.Base(name), "ext", ext, "modern", modern, "format", strings.TrimPrefix(modern, "."),
	)
}

// oleMagic 是 OLE 复合文档的文件头。有人会把 .doc 直接改名成 .docx，
// 扩展名骗得过归类，文件头骗不过。
var oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// RegisterOfficeTools 登记 Office 文件读写工具。
func RegisterOfficeTools(registry *Registry) error {
	for _, tool := range []Tool{
		readOfficeTool(), writeDocxTool(), editDocxTool(), writeXlsxTool(), writePptxTool(),
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func readOfficeTool() Tool {
	return Tool{
		Name: "read_office",
		Description: "Read the contents of a Word (.docx), Excel (.xlsx/.xlsm) or PowerPoint (.pptx) file as Markdown text. " +
			"Word yields heading levels, lists and tables; Excel yields tables per sheet and range (formulas show the computed value plus the formula); " +
			"PowerPoint yields each slide's title, body and speaker notes. The 97-2003 .doc/.xls/.ppt formats are not supported. Long content is truncated, with a note on how to read the rest.",
		Effect: EffectRead,
		Schema: schema(map[string]any{
			"path": map[string]any{
				"type": "string", "description": "File path. Relative paths resolve against the workspace; absolute paths also work",
			},
			"sheet": map[string]any{
				"type": "string", "description": "Excel only: sheet name; omit to read the first sheet",
			},
			"range": map[string]any{
				"type": "string", "description": "Excel only: range to read, e.g. A1:F200; omit to read the first 200 rows",
			},
			"slides": map[string]any{
				"type": "string", "description": "PowerPoint only: slide range, e.g. 3 or 3-8; omit to read all slides",
			},
			"offset": map[string]any{
				"type": "integer", "minimum": 0,
				"description": "Word only: character offset in the converted text to start from, for continuing past a truncation",
			},
		}, "path"),
		Handler: func(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path   string `json:"path"`
				Sheet  string `json:"sheet"`
				Range  string `json:"range"`
				Slides string `json:"slides"`
				Offset int    `json:"offset"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			path, err := env.ResolveRead(args.Path)
			if err != nil {
				return "", err
			}
			kind, legacy := officeKind(path)
			if kind == "" {
				return "", i18n.E("{path} 不是 Office 文件（支持 .docx/.xlsx/.xlsm/.pptx）；普通文本用 read_file", "path", args.Path)
			}
			if legacy {
				return "", legacyOfficeError(path)
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("%s: %w", i18n.D("读取失败"), err)
			}
			if info.IsDir() {
				return "", i18n.E("{path} 是目录，不是文件", "path", args.Path)
			}
			if err := checkNotOLE(path); err != nil {
				return "", err
			}
			switch kind {
			case "docx":
				return readDocx(path, args.Offset)
			case "xlsx":
				return readXlsx(path, args.Sheet, args.Range)
			default:
				return readPptx(path, args.Slides)
			}
		},
	}
}

// checkNotOLE 认出「改了扩展名的老格式」，给出与老格式相同的提示，
// 而不是让 zip 解析报一句看不懂的 "not a valid zip file"。
func checkNotOLE(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.D("读取失败"), err)
	}
	defer file.Close()
	head := make([]byte, len(oleMagic))
	if n, _ := io.ReadFull(file, head); n == len(oleMagic) && bytes.Equal(head, oleMagic) {
		ext := map[string]string{"docx": ".doc", "xlsx": ".xls", "pptx": ".ppt"}
		kind, _ := officeKind(path)
		return legacyOfficeError(strings.TrimSuffix(path, filepath.Ext(path)) + ext[kind])
	}
	return nil
}

// ---------- 包读取 ----------

// ooxmlPackage 是打开的 OOXML 包，按部件名索引。
type ooxmlPackage struct {
	closer io.Closer
	files  map[string]*zip.File
}

func openPackage(path string) (*ooxmlPackage, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, i18n.E("打不开 {name}：不是有效的 Office 文件（{err}）", "name", filepath.Base(path), "err", err)
	}
	pkg := &ooxmlPackage{closer: reader, files: map[string]*zip.File{}}
	for _, file := range reader.File {
		// 部件名大小写不敏感（OPC 规范如此），统一成小写查找。
		pkg.files[strings.ToLower(strings.TrimPrefix(file.Name, "/"))] = file
	}
	return pkg, nil
}

func (p *ooxmlPackage) Close() error { return p.closer.Close() }

func (p *ooxmlPackage) has(name string) bool {
	_, ok := p.files[strings.ToLower(strings.TrimPrefix(name, "/"))]
	return ok
}

// read 读出一个部件。不存在时返回 os.ErrNotExist，方便调用方区分「可选部件没有」。
func (p *ooxmlPackage) read(name string) ([]byte, error) {
	file, ok := p.files[strings.ToLower(strings.TrimPrefix(name, "/"))]
	if !ok {
		return nil, fmt.Errorf("%s: %w", i18n.D("包内缺少 {name}", "name", name), os.ErrNotExist)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("解压 {name} 失败", "name", name), err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxOfficePart+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("解压 {name} 失败", "name", name), err)
	}
	if len(data) > maxOfficePart {
		return nil, i18n.E("{name} 解压后超过 {mb} MB，不读", "name", name, "mb", maxOfficePart>>20)
	}
	return data, nil
}

// opcRel 是 .rels 里的一条关系。
type opcRel struct {
	ID         string `xml:"Id,attr"`
	Type       string `xml:"Type,attr"`
	Target     string `xml:"Target,attr"`
	TargetMode string `xml:"TargetMode,attr"`
}

// relsOf 读某个部件的关系表，并把 Target 解析成包内绝对部件名。
// 部件没有 rels 时返回空表而不是错误——很多部件本来就没有。
func (p *ooxmlPackage) relsOf(part string) (map[string]opcRel, error) {
	data, err := p.read(relsPathOf(part))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]opcRel{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Rels []opcRel `xml:"Relationship"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("解析 {name} 失败", "name", relsPathOf(part)), err)
	}
	out := make(map[string]opcRel, len(doc.Rels))
	for _, rel := range doc.Rels {
		if !strings.EqualFold(rel.TargetMode, "External") {
			rel.Target = resolvePartTarget(part, rel.Target)
		}
		out[rel.ID] = rel
	}
	return out, nil
}

// relsPathOf 给出部件对应的 rels 路径：word/document.xml → word/_rels/document.xml.rels。
func relsPathOf(part string) string {
	part = strings.TrimPrefix(part, "/")
	if part == "" {
		return "_rels/.rels" // 包本身的关系表
	}
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// resolvePartTarget 把相对 Target 解析成包内部件名。以 / 开头的是包根绝对路径。
func resolvePartTarget(source, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return strings.TrimPrefix(path.Join(path.Dir(strings.TrimPrefix(source, "/")), target), "/")
}

// ---------- 极简 XML 树 ----------
//
// docx/pptx 的正文要按文档顺序遍历、还要看父子关系（段落在不在表格里、
// 形状是不是标题占位符），用 xml.Unmarshal 映射结构体会写出一大堆只用一次的
// 类型，而且拿不到「按原顺序的混合子元素」。一棵只记本地名、属性、子节点
// 和文字的小树够用，也好测。

type xnode struct {
	name  string // 本地名，不带前缀
	attrs []xml.Attr
	kids  []*xnode
	text  string // 直接包含的文字（w:t、a:t 这类叶子）
}

func parseXML(data []byte) (*xnode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root := &xnode{}
	stack := []*xnode{root}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			node := &xnode{name: t.Name.Local, attrs: t.Attr}
			parent := stack[len(stack)-1]
			parent.kids = append(parent.kids, node)
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].text += string(t)
		}
	}
	if len(root.kids) == 0 {
		return nil, i18n.E("XML 为空")
	}
	return root.kids[0], nil
}

// attr 取属性值，只按本地名匹配（w:val 与 val 一视同仁）。
func (n *xnode) attr(name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// relID 取 r:id 这类「带命名空间的 id」。p:sldId 上同时有 id 和 r:id 两个
// 本地名都叫 id 的属性，只按本地名取会拿错。
func (n *xnode) relID() string {
	if n == nil {
		return ""
	}
	for _, a := range n.attrs {
		if a.Name.Local == "id" && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}

// child 取第一个同名子节点。
func (n *xnode) child(name string) *xnode {
	if n == nil {
		return nil
	}
	for _, kid := range n.kids {
		if kid.name == name {
			return kid
		}
	}
	return nil
}

// find 按路径逐级取第一个匹配的子节点。
func (n *xnode) find(names ...string) *xnode {
	for _, name := range names {
		n = n.child(name)
		if n == nil {
			return nil
		}
	}
	return n
}

// ---------- 输出 ----------

// clipOffice 把给模型的文字卡在上限以内，截断时附上怎么继续读。
func clipOffice(text, hint string) string {
	kept, truncated := clipText(text)
	if !truncated {
		return text
	}
	return kept + "\n\n" + i18n.D("[内容已截断：{hint}]", "hint", hint)
}

// officeZh 表示界面语言是中文。列表分隔符、括号这类标点跟着语言走，
// 不值得各占一条词条。
func officeZh() bool { return i18n.Default() == i18n.Chinese }

// officeListSep 是并列项之间的分隔符：中文用顿号，英文用逗号。
func officeListSep() string {
	if officeZh() {
		return "、"
	}
	return ", "
}

// officeClauseSep 是分句之间的分隔符。
func officeClauseSep() string {
	if officeZh() {
		return "；"
	}
	return "; "
}

// officeParen 给一段附注加括号：中文用全角括号紧贴，英文前面空一格。
func officeParen(text string) string {
	if officeZh() {
		return "（" + text + "）"
	}
	return " (" + text + ")"
}

// clipText 按字节算上限、按 rune 边界切，避免把一个汉字切成半个。
func clipText(text string) (string, bool) {
	if len(text) <= maxOfficeText {
		return text, false
	}
	cut := maxOfficeText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// mainPartOf 从 _rels/.rels 找到主文档部件。绝大多数文件是固定路径
// （word/document.xml 之类），但规范只要求关系指得到，有的生成器会放在别处。
func (p *ooxmlPackage) mainPartOf(fallback string) string {
	rels, err := p.relsOf("")
	if err == nil {
		for _, rel := range rels {
			if rel.Type == relOfficeDoc && p.has(rel.Target) {
				return rel.Target
			}
		}
	}
	return fallback
}

// mdCell 把一段文字变成能放进 Markdown 表格单元格的样子：
// 竖线要转义，换行换成 <br>，否则整张表会错位。
func mdCell(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "|", `\|`)
	text = strings.ReplaceAll(text, "\n", "<br>")
	return strings.TrimSpace(text)
}

// mdTable 渲染 Markdown 表格，第一行当表头。行长不齐时补空格，
// 否则 Markdown 渲染器会把多出来的列吞掉。
func mdTable(rows [][]string) string {
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if width == 0 {
		return ""
	}
	var b strings.Builder
	for index, row := range rows {
		b.WriteString("|")
		for col := 0; col < width; col++ {
			cell := ""
			if col < len(row) {
				cell = mdCell(row[col])
			}
			b.WriteString(" " + cell + " |")
		}
		b.WriteString("\n")
		if index == 0 {
			b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------- 包写入 ----------

// zipPart 是要写进包里的一个部件。
type zipPart struct {
	name string
	data []byte
}

// buildPackage 把部件打成 zip。[Content_Types].xml 要放第一个：
// 规范没强制，但有的解析器（和一些老版本 Office）只认开头那一个。
func buildPackage(parts []zipPart) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	now := time.Now()
	for _, part := range parts {
		header := &zip.FileHeader{Name: part.name, Method: zip.Deflate, Modified: now}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(part.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// writeFileAtomic 先写临时文件再改名。
//
// 覆盖一份用户的 Office 文件时，写到一半失败（磁盘满、进程被杀）会留下一个
// 打不开的残包，比「没写成」糟糕得多；同目录临时文件 + rename 保证要么是旧的、
// 要么是完整的新的。
func writeFileAtomic(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.D("创建父目录失败"), err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".aiclaw-*"+filepath.Ext(target))
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName) // 成功改名之后这里是空操作
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
	}
	if err := os.Chmod(tempName, mode); err != nil {
		return fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("%s: %w", i18n.D("写入失败"), err)
	}
	return nil
}

// xmlEscape 转义文字内容。encoding/xml 会把 XML 不允许的控制字符换成 U+FFFD，
// 这正是我们要的：模型偶尔会带出 \x0b 之类的字符，原样写进去 Word 就打不开。
func xmlEscape(text string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}

// corePropsXML 生成 docProps/core.xml。
func corePropsXML(title string) string {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	titleXML := ""
	if strings.TrimSpace(title) != "" {
		titleXML = "<dc:title>" + xmlEscape(title) + "</dc:title>"
	}
	return xml.Header +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/"` +
		` xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		titleXML + `<dc:creator>AIClaw</dc:creator>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified>` +
		`</cp:coreProperties>`
}

// appPropsXML 生成 docProps/app.xml。extra 是各格式自己的统计字段。
func appPropsXML(extra string) string {
	return xml.Header +
		`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"` +
		` xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
		`<Application>AIClaw</Application>` + extra + `</Properties>`
}

// packageRelsXML 生成 _rels/.rels：主文档 + 两份属性。
func packageRelsXML(mainPart string) string {
	return xml.Header +
		`<Relationships xmlns="` + nsRelationships + `">` +
		`<Relationship Id="rId1" Type="` + relOfficeDoc + `" Target="` + mainPart + `"/>` +
		`<Relationship Id="rId2" Type="` + relCoreProps + `" Target="docProps/core.xml"/>` +
		`<Relationship Id="rId3" Type="` + relAppProps + `" Target="docProps/app.xml"/>` +
		`</Relationships>`
}

// relsXML 生成部件自己的关系表。rels 是 (Id, 类型短名, Target) 三元组，
// 类型短名拼在 officeDocument 关系前缀后面。
func relsXML(rels [][3]string) string {
	var b strings.Builder
	b.WriteString(xml.Header + `<Relationships xmlns="` + nsRelationships + `">`)
	for _, rel := range rels {
		b.WriteString(`<Relationship Id="` + rel[0] + `" Type="` + relPrefix + rel[1] + `" Target="` + rel[2] + `"/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

// contentTypesXML 生成 [Content_Types].xml。overrides 是 部件名 → 类型。
func contentTypesXML(overrides [][2]string) string {
	var b strings.Builder
	b.WriteString(xml.Header + `<Types xmlns="` + nsContentTypes + `">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	for _, item := range overrides {
		b.WriteString(`<Override PartName="/` + item[0] + `" ContentType="` + item[1] + `"/>`)
	}
	b.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	b.WriteString(`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	b.WriteString(`</Types>`)
	return b.String()
}

// requireExt 检查写入目标的扩展名。写错扩展名（比如 .doc）会得到一个
// Office 按老格式去解析、然后报损坏的文件，不如当场说清。
func requireExt(target string, allowed ...string) error {
	ext := strings.ToLower(filepath.Ext(target))
	for _, item := range allowed {
		if ext == item {
			return nil
		}
	}
	or := " or "
	if officeZh() {
		or = " 或 "
	}
	return i18n.E("文件扩展名必须是 {allowed}，当前是 {ext}", "allowed", strings.Join(allowed, or), "ext", strconv.Quote(ext))
}

// approveOfficeWrite 与 write_file 同一套审批：工作区内不问，工作区外问一句，
// 可以按目录批准。precheck 在问用户之前跑——注定失败的写不该先让人点一次「允许」。
func approveOfficeWrite(
	ctx context.Context, env *Env, raw, title string, precheck func(target string) error,
) (string, error) {
	target, inside, err := env.ResolveWrite(raw)
	if err != nil {
		return "", err
	}
	if precheck != nil {
		if err := precheck(target); err != nil {
			return "", err
		}
	}
	if err := env.requestApprovalScoped(
		ctx, writeEffect(inside), protocol.ApprovalWrite,
		title, target, outsideReason(env, inside), scopeOf(inside, target),
	); err != nil {
		return "", err
	}
	return target, nil
}
