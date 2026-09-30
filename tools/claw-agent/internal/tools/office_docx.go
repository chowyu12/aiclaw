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
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

const (
	nsWordML   = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	relStyles  = relPrefix + "styles"
	relNumbers = relPrefix + "numbering"
)

// ============================================================
// 读 docx
// ============================================================

// docxStyle 是一个段落样式里对「转成 Markdown」有用的那点信息。
type docxStyle struct {
	heading int    // 0 表示不是标题
	numID   string // 样式自带的编号（有的模板把列表做进样式里）
	basedOn string
}

// docxReader 保存一次读取需要的上下文：样式表、编号定义、列表计数。
type docxReader struct {
	styles   map[string]docxStyle
	numAbs   map[string]string            // numId → abstractNumId
	numFmt   map[string]map[string]string // abstractNumId → ilvl → numFmt
	counters map[string][]int             // numId → 各级当前序号
}

func readDocx(target string, offset int) (string, error) {
	pkg, err := openPackage(target)
	if err != nil {
		return "", err
	}
	defer pkg.Close()

	main := pkg.mainPartOf("word/document.xml")
	data, err := pkg.read(main)
	if err != nil {
		return "", err
	}
	root, err := parseXML(data)
	if err != nil {
		return "", fmt.Errorf("解析 %s 失败：%w", main, err)
	}
	reader := &docxReader{
		styles: map[string]docxStyle{}, numAbs: map[string]string{},
		numFmt: map[string]map[string]string{}, counters: map[string][]int{},
	}
	// 样式与编号是可选部件：缺了只是认不出标题/列表，正文照读。
	if rels, err := pkg.relsOf(main); err == nil {
		for _, rel := range rels {
			switch rel.Type {
			case relStyles:
				if raw, err := pkg.read(rel.Target); err == nil {
					reader.loadStyles(raw)
				}
			case relNumbers:
				if raw, err := pkg.read(rel.Target); err == nil {
					reader.loadNumbering(raw)
				}
			}
		}
	}

	body := root.child("body")
	if body == nil {
		return "", fmt.Errorf("%s 里没有正文（w:body）", main)
	}
	var out strings.Builder
	lastList := false
	emit := func(block string, isList bool) {
		if strings.TrimSpace(block) == "" {
			return
		}
		if out.Len() > 0 {
			// 相邻列表项之间只换一行，否则 Markdown 会把它们渲染成松散列表。
			if isList && lastList {
				out.WriteString("\n")
			} else {
				out.WriteString("\n\n")
			}
		}
		out.WriteString(block)
		lastList = isList
	}
	reader.walkBlocks(body, emit)

	text := out.String()
	if text == "" {
		return "（文档没有文字内容）", nil
	}
	runes := utf8.RuneCountInString(text)
	if offset > 0 {
		if offset >= runes {
			return "", fmt.Errorf("offset %d 超出文档长度（共 %d 字符）", offset, runes)
		}
		text = string([]rune(text)[offset:])
	}
	kept, truncated := clipText(text)
	if truncated {
		next := offset + utf8.RuneCountInString(kept)
		kept += fmt.Sprintf("\n\n[内容已截断：全文共 %d 字符，已返回到第 %d 字符；用 offset=%d 接着读]", runes, next, next)
	}
	return kept, nil
}

// walkBlocks 按文档顺序遍历块级元素。sdt（内容控件）、customXml 只是包装，
// 里面照样是段落和表格，要钻进去。
func (r *docxReader) walkBlocks(parent *xnode, emit func(string, bool)) {
	for _, node := range parent.kids {
		switch node.name {
		case "p":
			text, isList := r.paragraph(node)
			emit(text, isList)
		case "tbl":
			emit(r.table(node), false)
		case "sdt":
			if content := node.child("sdtContent"); content != nil {
				r.walkBlocks(content, emit)
			}
		case "customXml":
			r.walkBlocks(node, emit)
		}
	}
}

func (r *docxReader) loadStyles(data []byte) {
	root, err := parseXML(data)
	if err != nil {
		return
	}
	headingName := regexp.MustCompile(`^heading\s*([1-9])$`)
	for _, node := range root.kids {
		if node.name != "style" || node.attr("type") != "paragraph" {
			continue
		}
		id := node.attr("styleId")
		style := docxStyle{basedOn: node.child("basedOn").attr("val")}
		name := strings.ToLower(strings.TrimSpace(node.child("name").attr("val")))
		switch {
		case headingName.MatchString(name):
			style.heading, _ = strconv.Atoi(headingName.FindStringSubmatch(name)[1])
		case name == "title":
			style.heading = 1
		}
		// 中文版 Word 的标题样式 id 是 "1""2"，名字仍是 "heading 1"；自定义标题
		// 样式则常常只有大纲级别。三条都认。
		if style.heading == 0 {
			if level := node.find("pPr", "outlineLvl").attr("val"); level != "" {
				if n, err := strconv.Atoi(level); err == nil && n >= 0 && n < 6 {
					style.heading = n + 1
				}
			}
		}
		style.numID = node.find("pPr", "numPr", "numId").attr("val")
		r.styles[id] = style
	}
}

func (r *docxReader) loadNumbering(data []byte) {
	root, err := parseXML(data)
	if err != nil {
		return
	}
	for _, node := range root.kids {
		switch node.name {
		case "abstractNum":
			levels := map[string]string{}
			for _, lvl := range node.kids {
				if lvl.name == "lvl" {
					levels[lvl.attr("ilvl")] = lvl.child("numFmt").attr("val")
				}
			}
			r.numFmt[node.attr("abstractNumId")] = levels
		case "num":
			r.numAbs[node.attr("numId")] = node.child("abstractNumId").attr("val")
		}
	}
}

// styleOf 沿 basedOn 链找标题级别与编号。深度设上限，防样式表里有环。
func (r *docxReader) styleOf(id string) docxStyle {
	var merged docxStyle
	for depth := 0; id != "" && depth < 16; depth++ {
		style, ok := r.styles[id]
		if !ok {
			break
		}
		if merged.heading == 0 {
			merged.heading = style.heading
		}
		if merged.numID == "" {
			merged.numID = style.numID
		}
		id = style.basedOn
	}
	return merged
}

// paragraph 把一个段落转成一行 Markdown，并说明它是不是列表项。
func (r *docxReader) paragraph(p *xnode) (string, bool) {
	var segments []docxSegment
	var extra []string // 文本框里的段落，接在本段后面
	r.collectRuns(p, false, &segments, &extra)
	text := renderSegments(segments)

	pPr := p.child("pPr")
	style := r.styleOf(pPr.child("pStyle").attr("val"))
	heading := style.heading
	if level := pPr.child("outlineLvl").attr("val"); heading == 0 && level != "" {
		if n, err := strconv.Atoi(level); err == nil && n >= 0 && n < 6 {
			heading = n + 1
		}
	}
	numID := style.numID
	ilvl := "0"
	if numPr := pPr.child("numPr"); numPr != nil {
		if id := numPr.child("numId").attr("val"); id != "" {
			numID = id
		}
		if level := numPr.child("ilvl").attr("val"); level != "" {
			ilvl = level
		}
	}

	isList := false
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		text = ""
	case heading > 0:
		text = strings.Repeat("#", heading) + " " + trimmed
	case numID != "" && numID != "0": // numId 0 表示「显式取消编号」
		isList = true
		text = r.listPrefix(numID, ilvl) + trimmed
	}
	if len(extra) > 0 {
		if text != "" {
			text += "\n"
		}
		text += strings.Join(extra, "\n")
	}
	return text, isList
}

// listPrefix 给出列表项前缀：项目符号统一成 "- "，编号按级别计数。
// 实际显示的编号格式（一、(1)、a.）不去还原——模型要的是结构，不是排版。
func (r *docxReader) listPrefix(numID, ilvl string) string {
	level, _ := strconv.Atoi(ilvl)
	if level < 0 || level > 8 {
		level = 0
	}
	indent := strings.Repeat("  ", level)
	format := r.numFmt[r.numAbs[numID]][ilvl]
	if format == "bullet" || format == "none" || format == "" {
		return indent + "- "
	}
	counters := r.counters[numID]
	for len(counters) <= level {
		counters = append(counters, 0)
	}
	counters[level]++
	for deeper := level + 1; deeper < len(counters); deeper++ {
		counters[deeper] = 0 // 上一级前进一格，下级重新从 1 数
	}
	r.counters[numID] = counters
	return indent + strconv.Itoa(counters[level]) + ". "
}

// docxSegment 是一段连续的、格式相同的文字。
type docxSegment struct {
	text string
	bold bool
}

// collectRuns 按顺序收集段落里的文字。
//
// 要跳过的：w:del（修订里被删掉的字）、mc:Fallback（与 Choice 是同一份
// 内容的两种写法，两边都收会重复）、域代码 instrText。文本框（txbxContent）
// 里的段落单独收进 extra，免得和正文搅在一行里。
func (r *docxReader) collectRuns(node *xnode, bold bool, segments *[]docxSegment, extra *[]string) {
	for _, kid := range node.kids {
		switch kid.name {
		case "pPr", "rPr", "del", "moveFrom", "Fallback", "instrText", "delText":
			continue
		case "r":
			runBold := bold || isOn(kid.find("rPr", "b"))
			r.collectRuns(kid, runBold, segments, extra)
		case "t":
			*segments = append(*segments, docxSegment{text: kid.text, bold: bold})
		case "tab", "ptab":
			*segments = append(*segments, docxSegment{text: "\t", bold: bold})
		case "br", "cr":
			if kid.attr("type") != "page" {
				*segments = append(*segments, docxSegment{text: "\n", bold: bold})
			}
		case "noBreakHyphen":
			*segments = append(*segments, docxSegment{text: "-", bold: bold})
		case "txbxContent":
			r.walkBlocks(kid, func(text string, _ bool) {
				if strings.TrimSpace(text) != "" {
					*extra = append(*extra, text)
				}
			})
		default:
			r.collectRuns(kid, bold, segments, extra)
		}
	}
}

// isOn 判断 w:b 这类开关属性。<w:b/> 是开，<w:b w:val="0"/> 是关。
func isOn(node *xnode) bool {
	if node == nil {
		return false
	}
	switch strings.ToLower(node.attr("val")) {
	case "0", "false", "off":
		return false
	}
	return true
}

// renderSegments 合并相邻同格式的片段再加粗。Word 常把一个词拆成好几个
// run（拼写检查、修订都会拆），逐个包 ** 会得到 **a****b** 这种东西。
func renderSegments(segments []docxSegment) string {
	var merged []docxSegment
	for _, segment := range segments {
		if n := len(merged); n > 0 && merged[n-1].bold == segment.bold {
			merged[n-1].text += segment.text
			continue
		}
		merged = append(merged, segment)
	}
	var b strings.Builder
	for _, segment := range merged {
		if segment.bold && strings.TrimSpace(segment.text) != "" {
			// ** 要贴着文字，前后空白放在外面，否则 Markdown 不认。
			core := strings.TrimSpace(segment.text)
			lead := segment.text[:strings.Index(segment.text, core)]
			tail := segment.text[len(lead)+len(core):]
			b.WriteString(lead + "**" + core + "**" + tail)
			continue
		}
		b.WriteString(segment.text)
	}
	return b.String()
}

// table 把 w:tbl 渲染成 Markdown 表格。横向合并（gridSpan）补空格保持列对齐，
// 纵向合并的续格本来就是空的；表中表压成单元格里的文字。
func (r *docxReader) table(tbl *xnode) string {
	var rows [][]string
	for _, tr := range tbl.kids {
		if tr.name != "tr" {
			continue
		}
		var row []string
		for _, tc := range tr.kids {
			if tc.name != "tc" {
				continue
			}
			var parts []string
			r.walkBlocks(tc, func(text string, _ bool) {
				if strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			})
			row = append(row, strings.Join(parts, "\n"))
			if span, err := strconv.Atoi(tc.find("tcPr", "gridSpan").attr("val")); err == nil {
				for i := 1; i < span && i < 64; i++ {
					row = append(row, "")
				}
			}
		}
		rows = append(rows, row)
	}
	return mdTable(rows)
}

// ============================================================
// 写 docx
// ============================================================

func writeDocxTool() Tool {
	return Tool{
		Name: "write_docx",
		Description: "用 Markdown 风格的文字生成一个 Word 文档（.docx），已有同名文件会被覆盖。支持：# 到 ###### 标题、" +
			"普通段落（每行一段）、- 或 * 开头的项目符号、1. 开头的编号列表（缩进两格为下一级）、**加粗**、*斜体*、`代码`、" +
			"``` 代码块、> 引用、| a | b | 形式的表格（第二行 |---| 分隔时首行为表头）、单独一行 --- 为分页。" +
			"写到工作区之外会先请用户确认。",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path":    map[string]any{"type": "string", "description": "输出路径，扩展名 .docx。相对路径按工作区解析"},
			"content": map[string]any{"type": "string", "description": "文档内容（Markdown 风格）"},
			"title":   map[string]any{"type": "string", "description": "文档属性里的标题，可不传"},
		}, "path", "content"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
				Title   string `json:"title"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if err := requireExt(args.Path, ".docx"); err != nil {
				return "", err
			}
			data, stats, err := buildDocx(args.Content, args.Title)
			if err != nil {
				return "", err
			}
			target, err := approveOfficeWrite(ctx, env, args.Path, "写入 Word 文档", nil)
			if err != nil {
				return "", err
			}
			if err := writeFileAtomic(target, data); err != nil {
				return "", err
			}
			return fmt.Sprintf("已写入 %s（%d 段，%d 个表格，%d 字节）", args.Path, stats.paragraphs, stats.tables, len(data)), nil
		},
	}
}

type docxStats struct{ paragraphs, tables int }

// docxBuilder 把 Markdown 风格的文字逐行翻成 WordprocessingML。
//
// 故意只做一个子集：每一行就是一段（不做 Markdown 的「连续行并成一段」——
// 中文里把两行并成一段要不要加空格本身就说不清，逐行更可预期）。
type docxBuilder struct {
	body         strings.Builder
	orderedLists int  // 已分配的编号列表实例数；每个编号列表从 1 重新数
	inOrdered    bool // 当前是否处在一个编号列表里
	stats        docxStats
	lastTable    bool
}

var (
	mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdBullet  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	// 「1. 」要求点后有空白，免得把「2023.10 总结」当成列表；中文的「1、」不要求。
	mdOrdered  = regexp.MustCompile(`^(\s*)\d+(?:[.)]\s+|、\s*)(.*)$`)
	mdQuote    = regexp.MustCompile(`^\s*>\s?(.*)$`)
	mdTableSep = regexp.MustCompile(`^\s*:?-{1,}:?\s*$`)
)

func buildDocx(content, title string) ([]byte, docxStats, error) {
	builder := &docxBuilder{}
	builder.render(content)
	document := xml.Header +
		`<w:document xmlns:w="` + nsWordML + `" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<w:body>` + builder.body.String() +
		// A4、常见页边距。sectPr 必须是 body 的最后一个子元素。
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1440" w:right="1800" w:bottom="1440" w:left="1800" w:header="851" w:footer="992" w:gutter="0"/>` +
		`<w:cols w:space="425"/></w:sectPr></w:body></w:document>`

	parts := []zipPart{
		{"[Content_Types].xml", []byte(contentTypesXML([][2]string{
			{"word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"},
			{"word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"},
			{"word/numbering.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"},
			{"word/settings.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"},
		}))},
		{"_rels/.rels", []byte(packageRelsXML("word/document.xml"))},
		{"word/document.xml", []byte(document)},
		{"word/_rels/document.xml.rels", []byte(relsXML([][3]string{
			{"rId1", "styles", "styles.xml"},
			{"rId2", "numbering", "numbering.xml"},
			{"rId3", "settings", "settings.xml"},
		}))},
		{"word/styles.xml", []byte(docxStylesXML)},
		{"word/numbering.xml", []byte(docxNumberingXML(builder.orderedLists))},
		{"word/settings.xml", []byte(docxSettingsXML)},
		{"docProps/core.xml", []byte(corePropsXML(title))},
		{"docProps/app.xml", []byte(appPropsXML(""))},
	}
	data, err := buildPackage(parts)
	if err != nil {
		return nil, docxStats{}, fmt.Errorf("打包 docx 失败：%w", err)
	}
	return data, builder.stats, nil
}

func (b *docxBuilder) render(content string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			// 空行不结束编号列表：「1. a\n\n2. b」在 Markdown 里仍是同一个列表。
			continue
		case strings.HasPrefix(trimmed, "```"):
			b.inOrdered = false
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				b.paragraph("CodeBlock", "", 0, docxPlainRuns(lines[i], true))
			}
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			b.inOrdered = false
			b.raw(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		case mdHeading.MatchString(line):
			b.inOrdered = false
			match := mdHeading.FindStringSubmatch(line)
			b.paragraph("Heading"+strconv.Itoa(len(match[1])), "", 0, docxInlineRuns(strings.TrimSpace(match[2])))
		case strings.HasPrefix(trimmed, "|"):
			b.inOrdered = false
			var rows []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				rows = append(rows, strings.TrimSpace(lines[i]))
			}
			i--
			b.table(rows)
		case mdBullet.MatchString(line):
			match := mdBullet.FindStringSubmatch(line)
			b.paragraph("", "1", indentLevel(match[1]), docxInlineRuns(match[2]))
		case mdOrdered.MatchString(line):
			match := mdOrdered.FindStringSubmatch(line)
			if !b.inOrdered {
				b.orderedLists++
				b.inOrdered = true
			}
			b.paragraph("", strconv.Itoa(1+b.orderedLists), indentLevel(match[1]), docxInlineRuns(match[2]))
		case mdQuote.MatchString(line):
			b.inOrdered = false
			b.paragraph("Quote", "", 0, docxInlineRuns(mdQuote.FindStringSubmatch(line)[1]))
		default:
			b.inOrdered = false
			b.paragraph("", "", 0, docxInlineRuns(trimmed))
		}
	}
	// 以表格结尾的文档 Word 能开，但光标没处放、也没法在表格后面接着打字；
	// Word 自己保存时总会在表格后留一个空段落，照做。
	if b.lastTable || b.body.Len() == 0 {
		b.raw(`<w:p/>`)
	}
}

// indentLevel 按缩进算列表级别：两个空格（或一个制表符）一级。
func indentLevel(indent string) int {
	width := 0
	for _, r := range indent {
		if r == '\t' {
			width += 4
		} else {
			width++
		}
	}
	level := width / 2
	if level > 8 {
		level = 8
	}
	return level
}

func (b *docxBuilder) raw(xmlText string) {
	b.body.WriteString(xmlText)
	b.lastTable = false
}

// paragraph 写一个段落。numID 非空时是列表项。pPr 子元素的顺序是规范规定的
// （pStyle 在 numPr 前），顺序错了 Word 会直接报文件损坏。
func (b *docxBuilder) paragraph(style, numID string, level int, runs string) {
	var pPr strings.Builder
	if style != "" {
		pPr.WriteString(`<w:pStyle w:val="` + style + `"/>`)
	}
	if numID != "" {
		pPr.WriteString(`<w:numPr><w:ilvl w:val="` + strconv.Itoa(level) + `"/><w:numId w:val="` + numID + `"/></w:numPr>`)
	}
	b.body.WriteString(`<w:p>`)
	if pPr.Len() > 0 {
		b.body.WriteString(`<w:pPr>` + pPr.String() + `</w:pPr>`)
	}
	b.body.WriteString(runs + `</w:p>`)
	b.stats.paragraphs++
	b.lastTable = false
}

// table 写一张 Markdown 表格。第二行是 |---| 分隔行时，首行当表头（加粗、底色、
// 跨页重复）；否则全部当数据行。
func (b *docxBuilder) table(lines []string) {
	var rows [][]string
	header := false
	for index, line := range lines {
		cells := splitTableRow(line)
		if index == 1 && isSeparatorRow(cells) {
			header = true
			continue
		}
		if isSeparatorRow(cells) {
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	// 总宽按 A4 减页边距（约 8300 twip）平均分。
	width := 8300 / cols
	var t strings.Builder
	t.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/>` +
		`<w:tblLook w:val="04A0" w:firstRow="1" w:lastRow="0" w:firstColumn="1" w:lastColumn="0" w:noHBand="0" w:noVBand="1"/>` +
		`</w:tblPr><w:tblGrid>`)
	for col := 0; col < cols; col++ {
		t.WriteString(`<w:gridCol w:w="` + strconv.Itoa(width) + `"/>`)
	}
	t.WriteString(`</w:tblGrid>`)
	for index, row := range rows {
		isHeader := header && index == 0
		t.WriteString(`<w:tr>`)
		if isHeader {
			t.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for col := 0; col < cols; col++ {
			cell := ""
			if col < len(row) {
				cell = row[col]
			}
			t.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(width) + `" w:type="dxa"/>`)
			if isHeader {
				t.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="D9E2F3"/>`)
			}
			t.WriteString(`</w:tcPr><w:p>`)
			runs := docxInlineRuns(cell)
			if isHeader {
				runs = docxInlineRunsBold(cell)
			}
			t.WriteString(runs + `</w:p></w:tc>`)
		}
		t.WriteString(`</w:tr>`)
	}
	t.WriteString(`</w:tbl>`)
	b.body.WriteString(t.String())
	b.stats.tables++
	b.lastTable = true
}

// splitTableRow 拆一行 | a | b |，认 \| 转义。
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = strings.TrimSuffix(line, "|")
	}
	var cells []string
	var current strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			current.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteByte(line[i])
	}
	cells = append(cells, strings.TrimSpace(current.String()))
	return cells
}

func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if !mdTableSep.MatchString(cell) {
			return false
		}
	}
	return true
}

// docxInlineRuns 解析行内格式：**加粗**、*斜体*、`代码`、\* 转义。
//
// 标记只有在后面找得到配对时才生效：「3 * 4」里孤零零的星号保持原样，
// 而不是把后半行全变成斜体。
func docxInlineRuns(text string) string { return inlineRuns(text, false) }

func docxInlineRunsBold(text string) string { return inlineRuns(text, true) }

func inlineRuns(text string, forceBold bool) string {
	var out strings.Builder
	var pending strings.Builder
	bold, italic := false, false
	flush := func() {
		if pending.Len() > 0 {
			out.WriteString(docxRun(pending.String(), bold || forceBold, italic, false))
			pending.Reset()
		}
	}
	for i := 0; i < len(text); {
		rest := text[i:]
		switch {
		case strings.HasPrefix(rest, `\`) && len(rest) > 1 && strings.ContainsRune("*_`\\|", rune(rest[1])):
			pending.WriteByte(rest[1])
			i += 2
		case rest[0] == '`':
			end := strings.IndexByte(rest[1:], '`')
			if end < 0 {
				pending.WriteByte('`')
				i++
				continue
			}
			flush()
			out.WriteString(docxRun(rest[1:1+end], bold || forceBold, italic, true))
			i += end + 2
		case strings.HasPrefix(rest, "**"):
			// 不认 __加粗__：__init__ 这类标识符在技术文档里太常见，误伤比漏认糟。
			if bold || strings.Contains(rest[2:], "**") {
				flush()
				bold = !bold
			} else {
				pending.WriteString("**")
			}
			i += 2
		case rest[0] == '*':
			if italic || strings.Contains(rest[1:], "*") {
				flush()
				italic = !italic
			} else {
				pending.WriteByte('*')
			}
			i++
		default:
			_, size := utf8.DecodeRuneInString(rest)
			pending.WriteString(rest[:size])
			i += size
		}
	}
	flush()
	return out.String()
}

// docxPlainRuns 不解析行内格式，代码块里的 * 和 ` 都是字面意思。
func docxPlainRuns(text string, code bool) string {
	if text == "" {
		return ""
	}
	return docxRun(text, false, false, code)
}

// docxRun 写一个 run。rPr 子元素顺序同样是规范规定的：rFonts、b、i、shd。
func docxRun(text string, bold, italic, code bool) string {
	var rPr strings.Builder
	if code {
		rPr.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/>`)
	}
	if bold {
		rPr.WriteString(`<w:b/><w:bCs/>`)
	}
	if italic {
		rPr.WriteString(`<w:i/><w:iCs/>`)
	}
	if code {
		rPr.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`)
	}
	var b strings.Builder
	b.WriteString(`<w:r>`)
	if rPr.Len() > 0 {
		b.WriteString(`<w:rPr>` + rPr.String() + `</w:rPr>`)
	}
	b.WriteString(runTextXML(text, "w"))
	b.WriteString(`</w:r>`)
	return b.String()
}

// runTextXML 把一段文字写成 run 里的内容：制表符是 w:tab、换行是 w:br，
// 直接留在 w:t 里的话 Word 会把它们当成空格。
func runTextXML(text, prefix string) string {
	open := `<` + prefix + `:t xml:space="preserve">`
	closeTag := `</` + prefix + `:t>`
	var b strings.Builder
	b.WriteString(open)
	for _, r := range text {
		switch r {
		case '\t':
			b.WriteString(closeTag + `<` + prefix + `:tab/>` + open)
		case '\n':
			b.WriteString(closeTag + `<` + prefix + `:br/>` + open)
		case '\r':
		default:
			b.WriteString(xmlEscape(string(r)))
		}
	}
	b.WriteString(closeTag)
	return b.String()
}

// docxStylesXML 是生成文档用的样式表。
//
// 字体：西文 Calibri，东亚字体用 eastAsia 单独指定为等线——不指定的话，
// 中文会落到 Word 的默认东亚字体，在没装对应字体的机器上可能显示成方框。
// 标题样式用内置的 styleId（Heading1…）和名字（heading 1…），这样 Word 的
// 导航窗格、目录、WPS 的大纲都认得出来。
var docxStylesXML = func() string {
	sizes := []int{44, 36, 32, 28, 24, 21}
	var headings strings.Builder
	for index, size := range sizes {
		level := strconv.Itoa(index + 1)
		fmt.Fprintf(&headings,
			`<w:style w:type="paragraph" w:styleId="Heading%s"><w:name w:val="heading %s"/>`+
				`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:uiPriority w:val="9"/><w:qFormat/>`+
				`<w:pPr><w:keepNext/><w:keepLines/><w:spacing w:before="%d" w:after="%d" w:line="240" w:lineRule="auto"/>`+
				`<w:outlineLvl w:val="%d"/></w:pPr>`+
				`<w:rPr><w:rFonts w:ascii="Calibri Light" w:eastAsia="等线 Light" w:hAnsi="Calibri Light"/>`+
				`<w:b/><w:bCs/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:style>`,
			level, level, size*6, size*4, index, size, size)
	}
	return xml.Header +
		`<w:styles xmlns:w="` + nsWordML + `">` +
		`<w:docDefaults><w:rPrDefault><w:rPr>` +
		`<w:rFonts w:ascii="Calibri" w:eastAsia="等线" w:hAnsi="Calibri" w:cs="Times New Roman"/>` +
		`<w:kern w:val="2"/><w:sz w:val="21"/><w:szCs w:val="22"/><w:lang w:val="en-US" w:eastAsia="zh-CN" w:bidi="ar-SA"/>` +
		`</w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="300" w:lineRule="auto"/></w:pPr></w:pPrDefault>` +
		`</w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
		`<w:style w:type="character" w:default="1" w:styleId="DefaultParagraphFont"><w:name w:val="Default Paragraph Font"/>` +
		`<w:uiPriority w:val="1"/><w:semiHidden/><w:unhideWhenUsed/></w:style>` +
		`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/>` +
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/><w:tblPr><w:tblInd w:w="0" w:type="dxa"/>` +
		`<w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/><w:bottom w:w="0" w:type="dxa"/>` +
		`<w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>` +
		`<w:style w:type="numbering" w:default="1" w:styleId="NoList"><w:name w:val="No List"/>` +
		`<w:uiPriority w:val="99"/><w:semiHidden/><w:unhideWhenUsed/></w:style>` +
		headings.String() +
		`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/>` +
		`<w:next w:val="Normal"/><w:uiPriority w:val="29"/><w:qFormat/><w:pPr><w:ind w:left="420" w:right="420"/></w:pPr>` +
		`<w:rPr><w:i/><w:iCs/><w:color w:val="595959"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:customStyle="1" w:styleId="CodeBlock"><w:name w:val="Code Block"/>` +
		`<w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>` +
		`<w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr>` +
		`<w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:sz w:val="19"/></w:rPr></w:style>` +
		`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:basedOn w:val="TableNormal"/>` +
		`<w:uiPriority w:val="39"/><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr>` +
		`<w:tblPr><w:tblBorders>` +
		`<w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/>` +
		`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/>` +
		`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/>` +
		`</w:tblBorders></w:tblPr></w:style>` +
		`</w:styles>`
}()

const docxSettingsXML = xml.Header +
	`<w:settings xmlns:w="` + nsWordML + `"><w:defaultTabStop w:val="420"/>` +
	`<w:characterSpacingControl w:val="compressPunctuation"/><w:compat>` +
	`<w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/>` +
	`</w:compat></w:settings>`

// docxNumberingXML 生成编号定义。numId 1 是项目符号；每个编号列表各占一个
// numId（2 起），都指向同一个十进制定义、用 startOverride 从 1 重新数——
// 共用一个 numId 的话，第二个列表会接着第一个的序号往下数。
func docxNumberingXML(orderedLists int) string {
	bullets := []string{"•", "◦", "▪"}
	formats := []string{"decimal", "lowerLetter", "lowerRoman"}
	var b strings.Builder
	b.WriteString(xml.Header + `<w:numbering xmlns:w="` + nsWordML + `">`)
	for abstract := 0; abstract < 2; abstract++ {
		fmt.Fprintf(&b, `<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="hybridMultilevel"/>`, abstract)
		for level := 0; level < 9; level++ {
			indent := 420 * (level + 1)
			if abstract == 0 {
				fmt.Fprintf(&b,
					`<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="%s"/>`+
						`<w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="420"/></w:pPr>`+
						`<w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:hint="default"/></w:rPr></w:lvl>`,
					level, bullets[level%3], indent)
				continue
			}
			fmt.Fprintf(&b,
				`<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/><w:lvlText w:val="%%%d."/>`+
					`<w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="420"/></w:pPr></w:lvl>`,
				level, formats[level%3], level+1, indent)
		}
		b.WriteString(`</w:abstractNum>`)
	}
	b.WriteString(`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for list := 0; list < orderedLists; list++ {
		fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="1"/>`+
			`<w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`, list+2)
	}
	b.WriteString(`</w:numbering>`)
	return b.String()
}

// ============================================================
// 改 docx
// ============================================================

func editDocxTool() Tool {
	return Tool{
		Name: "edit_docx",
		Description: "在已有的 Word 文档（.docx）里把一段精确文字替换成新文字，其余内容与格式保持不动。" +
			"old_text 必须在正文中唯一出现，且不能跨段落；被 Word 拆成多段格式的文字也能匹配，替换后沿用匹配开头处的格式。" +
			"new_text 里的换行会成为段内换行。先用 read_office 看原文。写到工作区之外会先请用户确认。",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path":     map[string]any{"type": "string", "description": "docx 文件路径。相对路径按工作区解析"},
			"old_text": map[string]any{"type": "string", "description": "要被替换的原文（纯文字，不带 Markdown 标记），必须唯一"},
			"new_text": map[string]any{"type": "string", "description": "替换成的新文字"},
		}, "path", "old_text", "new_text"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if args.OldText == "" {
				return "", errors.New("old_text 不能为空；要写全新文档用 write_docx")
			}
			if strings.Contains(args.OldText, "\n") {
				return "", errors.New("old_text 不能跨段落（不能含换行）；请分段各替换一次")
			}
			if err := requireExt(args.Path, ".docx", ".docm"); err != nil {
				return "", err
			}
			target, inside, err := env.ResolveWrite(args.Path)
			if err != nil {
				return "", err
			}
			if err := checkNotOLE(target); err != nil {
				return "", err
			}
			data, err := editDocx(target, args.OldText, args.NewText)
			if err != nil {
				return "", err
			}
			// 与 edit_file 一样：先确认改得了，再问用户，别让人批准一次注定失败的修改。
			if err := env.requestApprovalScoped(
				ctx, writeEffect(inside), protocol.ApprovalWrite,
				"修改 Word 文档", target, outsideReason(env, inside), scopeOf(inside, target),
			); err != nil {
				return "", err
			}
			if err := writeFileAtomic(target, data); err != nil {
				return "", err
			}
			return fmt.Sprintf("已修改 %s", args.Path), nil
		},
	}
}

// docxTextNode 是 document.xml 里一个 w:t 元素：原文中的字节区间与解码后的文字。
type docxTextNode struct {
	start, end int
	prefix     string
	text       string
}

// editDocx 在正文里做一次精确替换，返回新的整个包。
//
// 做法是在原始 XML 字节上「只换 w:t 元素」：匹配跨了几个 run，就把新文字放进
// 第一个 run 的 w:t、把后面几个 run 被覆盖的部分删掉。run 本身、它们的格式、
// 段落属性、书签、批注锚点都原样保留——这比把段落拆了重写安全得多，也正好
// 满足「沿用第一段的格式」。包里除 document.xml 以外的部件逐字节拷贝
// （zip.Writer.Copy 连压缩数据都不重算），图片、页眉、宏都不会被碰到。
func editDocx(target, oldText, newText string) ([]byte, error) {
	reader, err := zip.OpenReader(target)
	if err != nil {
		return nil, fmt.Errorf("打不开 %s：不是有效的 docx（%v）", target, err)
	}
	defer reader.Close()
	pkg := &ooxmlPackage{closer: reader, files: map[string]*zip.File{}} // 由上面的 defer 关闭
	for _, file := range reader.File {
		pkg.files[strings.ToLower(strings.TrimPrefix(file.Name, "/"))] = file
	}
	main := pkg.mainPartOf("word/document.xml")
	data, err := pkg.read(main)
	if err != nil {
		return nil, err
	}
	paragraphs, err := scanDocxParagraphs(data)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", main, err)
	}

	count := 0
	var hit []docxTextNode
	matchAt := 0
	for _, nodes := range paragraphs {
		var full strings.Builder
		for _, node := range nodes {
			full.WriteString(node.text)
		}
		n := strings.Count(full.String(), oldText)
		if n > 0 && count == 0 {
			hit, matchAt = nodes, strings.Index(full.String(), oldText)
		}
		count += n
	}
	if count == 0 {
		return nil, errors.New("文档正文里找不到 old_text；先用 read_office 确认原文（它不能跨段落，也不能含 Markdown 标记）")
	}
	if count > 1 {
		return nil, fmt.Errorf("old_text 在文档里出现了 %d 次，无法确定改哪一处；请带上更多上下文使其唯一", count)
	}

	// 把匹配区间 [matchAt, matchEnd) 落到各个 w:t 上。
	matchEnd := matchAt + len(oldText)
	type change struct {
		node docxTextNode
		text string
	}
	var changes []change
	position := 0
	placed := false
	for _, node := range hit {
		from, to := position, position+len(node.text)
		position = to
		if to <= matchAt || from >= matchEnd || (from == to) {
			continue
		}
		var updated string
		if !placed {
			updated = node.text[:matchAt-from] + newText
			placed = true
		}
		if matchEnd < to {
			updated += node.text[matchEnd-from:]
		}
		changes = append(changes, change{node: node, text: updated})
	}

	var out bytes.Buffer
	cursor := 0
	for _, item := range changes {
		out.Write(data[cursor:item.node.start])
		out.WriteString(runTextXML(item.text, item.node.prefix))
		cursor = item.node.end
	}
	out.Write(data[cursor:])

	var packed bytes.Buffer
	writer := zip.NewWriter(&packed)
	for _, file := range reader.File {
		if !strings.EqualFold(file.Name, main) {
			if err := writer.Copy(file); err != nil {
				return nil, fmt.Errorf("复制 %s 失败：%w", file.Name, err)
			}
			continue
		}
		header := file.FileHeader
		header.Method = zip.Deflate
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(out.Bytes()); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return packed.Bytes(), nil
}

// scanDocxParagraphs 找出每个段落里的 w:t 及其在原文中的字节区间。
//
// 用 RawToken + InputOffset 而不是解析成树：要的正是原始字节位置，好做外科
// 手术式的替换。前缀按命名空间认（找绑定到 WordprocessingML 的那个前缀），
// 不写死 "w"——DrawingML 的 a:p/a:t 本地名一样，混进来就会改错地方。
// 文本框里的段落嵌在外层段落里，文字归最内层那个段落。
func scanDocxParagraphs(data []byte) ([][]docxTextNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var paragraphs [][]docxTextNode
	var stack [][]docxTextNode
	wordPrefix := "w"
	skip := 0
	var current *docxTextNode
	first := true
	for {
		start := int(decoder.InputOffset())
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			if first {
				first = false
				for _, attr := range t.Attr {
					if attr.Name.Space == "xmlns" && attr.Value == nsWordML {
						wordPrefix = attr.Name.Local
					}
				}
			}
			if skip > 0 {
				skip++
				continue
			}
			if t.Name.Local == "Fallback" {
				skip = 1 // 与 Choice 重复的那一份，不参与匹配
				continue
			}
			if t.Name.Space != wordPrefix {
				continue
			}
			switch t.Name.Local {
			case "p":
				stack = append(stack, nil)
			case "t":
				if len(stack) > 0 {
					current = &docxTextNode{start: start, prefix: t.Name.Space}
				}
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if t.Name.Space != wordPrefix {
				continue
			}
			switch t.Name.Local {
			case "t":
				if current != nil {
					current.end = int(decoder.InputOffset())
					stack[len(stack)-1] = append(stack[len(stack)-1], *current)
					current = nil
				}
			case "p":
				if len(stack) > 0 {
					paragraphs = append(paragraphs, stack[len(stack)-1])
					stack = stack[:len(stack)-1]
				}
			}
		case xml.CharData:
			if current != nil && skip == 0 {
				current.text += string(t)
			}
		}
	}
	return paragraphs, nil
}
