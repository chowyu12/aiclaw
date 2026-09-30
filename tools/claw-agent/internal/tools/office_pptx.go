package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

const (
	nsPresentationML = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsDrawingML      = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsOfficeRels     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	relSlide         = relPrefix + "slide"
	relNotesSlide    = relPrefix + "notesSlide"
)

// ============================================================
// 读 pptx
// ============================================================

func readPptx(target, slideRange string) (string, error) {
	pkg, err := openPackage(target)
	if err != nil {
		return "", err
	}
	defer pkg.Close()

	slides, err := pptxSlideOrder(pkg)
	if err != nil {
		return "", err
	}
	if len(slides) == 0 {
		return i18n.D("（演示文稿里没有幻灯片）"), nil
	}
	from, to, err := parsePageRange(slideRange, len(slides))
	if err != nil {
		return "", err
	}

	var out strings.Builder
	if from != 1 || to != len(slides) {
		out.WriteString(i18n.D("共 {total} 页，以下是第 {from}–{to} 页", "total", len(slides), "from", from, "to", to))
	} else {
		out.WriteString(i18n.D("共 {total} 页", "total", len(slides)))
	}
	out.WriteString("\n")
	// 按页累加，超出上限时停在整页边界上，并告诉模型从哪一页接着读——
	// 比在一页中间截断好用得多。
	for number := from; number <= to; number++ {
		text, err := pptxSlideText(pkg, slides[number-1], number)
		if err != nil {
			return "", err
		}
		if out.Len()+len(text) > maxOfficeText && number > from {
			out.WriteString("\n\n" + i18n.D("[内容已截断：只返回到第 {last} 页；用 slides={next} 接着读]",
				"last", number-1, "next", fmt.Sprintf("%d-%d", number, to)))
			return out.String(), nil
		}
		out.WriteString("\n" + text)
	}
	return clipOffice(out.String(), i18n.D("单页内容过长；可以缩小 slides 范围逐页读")), nil
}

// pptxSlideOrder 按 presentation.xml 里 sldIdLst 的顺序给出幻灯片部件名。
//
// 不能按文件名排：slide10.xml 排在 slide2.xml 前面只是小事，真正的问题是
// 用户在 PowerPoint 里拖动过页序之后，文件名和实际顺序就毫无关系了。
// 只有在 sldIdLst 缺失（极少见的生成器）时才退回按文件名里的数字排。
func pptxSlideOrder(pkg *ooxmlPackage) ([]string, error) {
	main := pkg.mainPartOf("ppt/presentation.xml")
	data, err := pkg.read(main)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("解析 {name} 失败", "name", main), err)
	}
	rels, err := pkg.relsOf(main)
	if err != nil {
		return nil, err
	}
	var slides []string
	if list := root.child("sldIdLst"); list != nil {
		for _, item := range list.kids {
			if rel, ok := rels[item.relID()]; ok && item.name == "sldId" && pkg.has(rel.Target) {
				slides = append(slides, rel.Target)
			}
		}
	}
	if len(slides) > 0 {
		return slides, nil
	}
	number := regexp.MustCompile(`(\d+)\.xml$`)
	for _, rel := range rels {
		if rel.Type == relSlide && pkg.has(rel.Target) {
			slides = append(slides, rel.Target)
		}
	}
	numberOf := func(name string) int {
		if match := number.FindStringSubmatch(name); match != nil {
			n, _ := strconv.Atoi(match[1])
			return n
		}
		return 0
	}
	sort.Slice(slides, func(i, j int) bool { return numberOf(slides[i]) < numberOf(slides[j]) })
	return slides, nil
}

// parsePageRange 解析 "3"、"3-8"、"3-"。不传就是全部。
func parsePageRange(text string, total int) (int, int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 1, total, nil
	}
	parts := strings.SplitN(strings.NewReplacer("–", "-", "~", "-", "～", "-").Replace(text), "-", 2)
	from, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || from < 1 {
		return 0, 0, i18n.E("slides {slides} 不合法，应当形如 3 或 3-8", "slides", strconv.Quote(text))
	}
	to := from
	if len(parts) == 2 {
		if strings.TrimSpace(parts[1]) == "" {
			to = total
		} else if to, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil || to < from {
			return 0, 0, i18n.E("slides {slides} 不合法，应当形如 3 或 3-8", "slides", strconv.Quote(text))
		}
	}
	if from > total {
		return 0, 0, i18n.E("第 {n} 页不存在，演示文稿共 {total} 页", "n", from, "total", total)
	}
	return from, min(to, total), nil
}

// pptxSlideText 渲染一页：标题、按顺序的正文、表格、演讲者备注。
func pptxSlideText(pkg *ooxmlPackage, part string, number int) (string, error) {
	data, err := pkg.read(part)
	if err != nil {
		return "", err
	}
	root, err := parseXML(data)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.D("解析 {name} 失败", "name", part), err)
	}
	collector := &pptxCollector{}
	collector.walk(root.find("cSld", "spTree"))

	var b strings.Builder
	if collector.title != "" {
		b.WriteString(i18n.D("## 第 {n} 页：{title}", "n", number, "title", strings.ReplaceAll(collector.title, "\n", " ")))
	} else {
		b.WriteString(i18n.D("## 第 {n} 页", "n", number))
	}
	if root.attr("show") == "0" {
		b.WriteString(i18n.D("（已隐藏）"))
	}
	b.WriteString("\n")
	if len(collector.lines) > 0 {
		b.WriteString("\n" + strings.Join(collector.lines, "\n") + "\n")
	}

	if rels, err := pkg.relsOf(part); err == nil {
		for _, rel := range rels {
			if rel.Type != relNotesSlide {
				continue
			}
			if notes := pptxNotes(pkg, rel.Target); notes != "" {
				b.WriteString("\n" + i18n.D("备注：") + "\n" + notes + "\n")
			}
		}
	}
	return b.String(), nil
}

// pptxCollector 按形状树的顺序收文字。
type pptxCollector struct {
	title string
	lines []string
}

func (c *pptxCollector) walk(tree *xnode) {
	if tree == nil {
		return
	}
	for _, kid := range tree.kids {
		switch kid.name {
		case "sp":
			c.shape(kid)
		case "grpSp":
			c.walk(kid)
		case "graphicFrame":
			if table := kid.find("graphic", "graphicData", "tbl"); table != nil {
				c.lines = append(c.lines, "", pptxTable(table), "")
			}
		case "AlternateContent":
			// Choice 与 Fallback 是同一内容的两种写法，只取 Choice。
			if choice := kid.child("Choice"); choice != nil {
				c.walk(choice)
			}
		}
	}
}

func (c *pptxCollector) shape(sp *xnode) {
	ph := sp.find("nvSpPr", "nvPr", "ph")
	kind := ph.attr("type")
	switch kind {
	case "sldNum", "dt", "ftr", "hdr", "sldImg":
		return // 页码、日期、页脚是母版上的装饰，不是这一页的内容
	}
	paragraphs := pptxParagraphs(sp.child("txBody"))
	if len(paragraphs) == 0 {
		return
	}
	if kind == "title" || kind == "ctrTitle" {
		var parts []string
		for _, p := range paragraphs {
			parts = append(parts, p.text)
		}
		title := strings.Join(parts, " ")
		if c.title == "" {
			c.title = title
		} else {
			c.lines = append(c.lines, title)
		}
		return
	}
	// 占位符里的正文（内容框、副标题）按列表呈现；自由文本框原样逐行给出。
	bulleted := ph != nil && kind != "subTitle"
	for _, p := range paragraphs {
		if bulleted {
			c.lines = append(c.lines, strings.Repeat("  ", p.level)+"- "+p.text)
		} else {
			c.lines = append(c.lines, p.text)
		}
	}
}

type pptxParagraph struct {
	text  string
	level int
}

// pptxParagraphs 取文本框里的段落，空段落丢掉。
func pptxParagraphs(body *xnode) []pptxParagraph {
	if body == nil {
		return nil
	}
	var out []pptxParagraph
	for _, p := range body.kids {
		if p.name != "p" {
			continue
		}
		var b strings.Builder
		for _, kid := range p.kids {
			switch kid.name {
			case "r", "fld":
				if t := kid.child("t"); t != nil {
					b.WriteString(t.text)
				}
			case "br":
				b.WriteString("\n")
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		level, _ := strconv.Atoi(p.child("pPr").attr("lvl"))
		out = append(out, pptxParagraph{text: text, level: max(level, 0)})
	}
	return out
}

func pptxTable(table *xnode) string {
	var rows [][]string
	for _, tr := range table.kids {
		if tr.name != "tr" {
			continue
		}
		var row []string
		for _, tc := range tr.kids {
			if tc.name != "tc" {
				continue
			}
			var parts []string
			for _, p := range pptxParagraphs(tc.child("txBody")) {
				parts = append(parts, p.text)
			}
			row = append(row, strings.Join(parts, "\n"))
		}
		rows = append(rows, row)
	}
	return mdTable(rows)
}

// pptxNotes 取演讲者备注：备注页里 body 占位符的文字。备注页上还有幻灯片
// 缩略图和页码占位符，那些不是备注。
func pptxNotes(pkg *ooxmlPackage, part string) string {
	data, err := pkg.read(part)
	if err != nil {
		return ""
	}
	root, err := parseXML(data)
	if err != nil {
		return ""
	}
	var lines []string
	var walk func(tree *xnode)
	walk = func(tree *xnode) {
		if tree == nil {
			return
		}
		for _, kid := range tree.kids {
			switch kid.name {
			case "grpSp":
				walk(kid)
			case "sp":
				if kid.find("nvSpPr", "nvPr", "ph").attr("type") != "body" {
					continue
				}
				for _, p := range pptxParagraphs(kid.child("txBody")) {
					lines = append(lines, p.text)
				}
			}
		}
	}
	walk(root.find("cSld", "spTree"))
	return strings.Join(lines, "\n")
}

// ============================================================
// 写 pptx
// ============================================================

type pptxSlideSpec struct {
	Title   string   `json:"title"`
	Bullets []string `json:"bullets"`
	Notes   string   `json:"notes"`
	Layout  string   `json:"layout"`
}

func writePptxTool() Tool {
	return Tool{
		Name: "write_pptx",
		Description: "Create a PowerPoint presentation (.pptx, 16:9); an existing file with the same name is overwritten. Give each slide a title, a bullet list and optional speaker notes; " +
			"layout=title is a cover slide (large title + subtitle, with bullets as subtitle lines); the default is \"title + bullets\". " +
			"Indent a bullet with two spaces for the next level. Writing outside the workspace asks the user for confirmation first.",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path":  map[string]any{"type": "string", "description": "Output path with a .pptx extension. Relative paths resolve against the workspace"},
			"title": map[string]any{"type": "string", "description": "Title in the document properties; optional"},
			"slides": map[string]any{
				"type": "array", "minItems": 1, "description": "The slides, in order",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":   map[string]any{"type": "string", "description": "Slide title"},
						"bullets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Bullet points, one per item"},
						"notes":   map[string]any{"type": "string", "description": "Speaker notes; may span multiple lines"},
						"layout": map[string]any{
							"type": "string", "enum": []string{"content", "title"},
							"description": "content: title + bullets (default); title: cover slide",
						},
					},
					"additionalProperties": false,
				},
			},
		}, "path", "slides"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path   string          `json:"path"`
				Title  string          `json:"title"`
				Slides []pptxSlideSpec `json:"slides"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if err := requireExt(args.Path, ".pptx"); err != nil {
				return "", err
			}
			if len(args.Slides) == 0 {
				return "", i18n.E("slides 不能为空")
			}
			for index, slide := range args.Slides {
				if slide.Layout != "" && slide.Layout != "content" && slide.Layout != "title" {
					return "", i18n.E("第 {n} 页的 layout 只能是 content 或 title，收到 {layout}", "n", index+1, "layout", strconv.Quote(slide.Layout))
				}
			}
			data, err := buildPptx(args.Slides, args.Title)
			if err != nil {
				return "", err
			}
			target, err := approveOfficeWrite(ctx, env, args.Path, i18n.D("写入 PPT 演示文稿"), nil)
			if err != nil {
				return "", err
			}
			if err := writeFileAtomic(target, data); err != nil {
				return "", err
			}
			return i18n.D("已写入 {path}（{slides} 页，{bytes} 字节）", "path", args.Path, "slides", len(args.Slides), "bytes", len(data)), nil
		},
	}
}

// 幻灯片尺寸：16:9，单位 EMU（1 英寸 = 914400）。
const (
	pptxWidth  = 12192000
	pptxHeight = 6858000
)

const pptxNS = `xmlns:a="` + nsDrawingML + `" xmlns:r="` + nsOfficeRels + `" xmlns:p="` + nsPresentationML + `"`

// buildPptx 生成一份最小但完整的演示文稿包。
//
// PowerPoint 对包结构的要求比 Word 严：幻灯片必须挂在版式上、版式挂在母版上、
// 母版必须有主题，缺任何一环都会报「需要修复」。备注页则另需一个备注母版
// （它也要自己的主题）。只要有一页带备注，就把备注母版整套带上。
func buildPptx(slides []pptxSlideSpec, title string) ([]byte, error) {
	hasNotes := false
	for _, slide := range slides {
		if strings.TrimSpace(slide.Notes) != "" {
			hasNotes = true
		}
	}

	var parts []zipPart
	overrides := [][2]string{
		{"ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"},
		{"ppt/presProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"},
		{"ppt/viewProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"},
		{"ppt/tableStyles.xml", "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"},
		{"ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml"},
		{"ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"},
		{"ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
		{"ppt/slideLayouts/slideLayout2.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
	}
	if hasNotes {
		overrides = append(overrides,
			[2]string{"ppt/theme/theme2.xml", "application/vnd.openxmlformats-officedocument.theme+xml"},
			[2]string{"ppt/notesMasters/notesMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"},
		)
	}

	// presentation.xml 的关系：rId1 母版，rId2.. 幻灯片，之后是主题、属性、备注母版。
	presRels := [][3]string{{"rId1", "slideMaster", "slideMasters/slideMaster1.xml"}}
	var slideIDs strings.Builder
	for index := range slides {
		rid := "rId" + strconv.Itoa(index+2)
		presRels = append(presRels, [3]string{rid, "slide", fmt.Sprintf("slides/slide%d.xml", index+1)})
		fmt.Fprintf(&slideIDs, `<p:sldId id="%d" r:id="%s"/>`, 256+index, rid)
	}
	next := len(slides) + 2
	nextRID := func() string {
		rid := "rId" + strconv.Itoa(next)
		next++
		return rid
	}
	presRels = append(presRels,
		[3]string{nextRID(), "theme", "theme/theme1.xml"},
		[3]string{nextRID(), "presProps", "presProps.xml"},
		[3]string{nextRID(), "viewProps", "viewProps.xml"},
		[3]string{nextRID(), "tableStyles", "tableStyles.xml"},
	)
	notesMasterList := ""
	if hasNotes {
		rid := nextRID()
		presRels = append(presRels, [3]string{rid, "notesMaster", "notesMasters/notesMaster1.xml"})
		notesMasterList = `<p:notesMasterIdLst><p:notesMasterId r:id="` + rid + `"/></p:notesMasterIdLst>`
	}
	// 子元素顺序是 schema 规定的：母版列表、备注母版列表、幻灯片列表、尺寸。
	presentation := xml.Header + `<p:presentation ` + pptxNS + ` saveSubsetFonts="1">` +
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>` +
		notesMasterList +
		`<p:sldIdLst>` + slideIDs.String() + `</p:sldIdLst>` +
		fmt.Sprintf(`<p:sldSz cx="%d" cy="%d"/>`, pptxWidth, pptxHeight) +
		`<p:notesSz cx="6858000" cy="9144000"/>` +
		`<p:defaultTextStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:defaultTextStyle>` +
		`</p:presentation>`

	parts = append(parts,
		zipPart{"_rels/.rels", []byte(packageRelsXML("ppt/presentation.xml"))},
		zipPart{"ppt/presentation.xml", []byte(presentation)},
		zipPart{"ppt/_rels/presentation.xml.rels", []byte(relsXML(presRels))},
		zipPart{"ppt/presProps.xml", []byte(xml.Header + `<p:presentationPr ` + pptxNS + `/>`)},
		zipPart{"ppt/viewProps.xml", []byte(xml.Header + `<p:viewPr ` + pptxNS + `><p:gridSpacing cx="72008" cy="72008"/></p:viewPr>`)},
		zipPart{"ppt/tableStyles.xml", []byte(xml.Header +
			`<a:tblStyleLst xmlns:a="` + nsDrawingML + `" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`)},
		zipPart{"ppt/theme/theme1.xml", []byte(pptxThemeXML)},
		zipPart{"ppt/slideMasters/slideMaster1.xml", []byte(pptxMasterXML)},
		zipPart{"ppt/slideMasters/_rels/slideMaster1.xml.rels", []byte(relsXML([][3]string{
			{"rId1", "slideLayout", "../slideLayouts/slideLayout1.xml"},
			{"rId2", "slideLayout", "../slideLayouts/slideLayout2.xml"},
			{"rId3", "theme", "../theme/theme1.xml"},
		}))},
		zipPart{"ppt/slideLayouts/slideLayout1.xml", []byte(pptxTitleLayoutXML)},
		zipPart{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", []byte(relsXML([][3]string{
			{"rId1", "slideMaster", "../slideMasters/slideMaster1.xml"},
		}))},
		zipPart{"ppt/slideLayouts/slideLayout2.xml", []byte(pptxContentLayoutXML)},
		zipPart{"ppt/slideLayouts/_rels/slideLayout2.xml.rels", []byte(relsXML([][3]string{
			{"rId1", "slideMaster", "../slideMasters/slideMaster1.xml"},
		}))},
	)
	if hasNotes {
		parts = append(parts,
			zipPart{"ppt/theme/theme2.xml", []byte(pptxThemeXML)},
			zipPart{"ppt/notesMasters/notesMaster1.xml", []byte(pptxNotesMasterXML)},
			zipPart{"ppt/notesMasters/_rels/notesMaster1.xml.rels", []byte(relsXML([][3]string{
				{"rId1", "theme", "../theme/theme2.xml"},
			}))},
		)
	}

	for index, slide := range slides {
		number := index + 1
		name := fmt.Sprintf("slide%d.xml", number)
		overrides = append(overrides, [2]string{"ppt/slides/" + name,
			"application/vnd.openxmlformats-officedocument.presentationml.slide+xml"})
		layout := "../slideLayouts/slideLayout2.xml"
		if slide.Layout == "title" {
			layout = "../slideLayouts/slideLayout1.xml"
		}
		slideRels := [][3]string{{"rId1", "slideLayout", layout}}
		if strings.TrimSpace(slide.Notes) != "" {
			notesName := fmt.Sprintf("notesSlide%d.xml", number)
			slideRels = append(slideRels, [3]string{"rId2", "notesSlide", "../notesSlides/" + notesName})
			overrides = append(overrides, [2]string{"ppt/notesSlides/" + notesName,
				"application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"})
			parts = append(parts,
				zipPart{"ppt/notesSlides/" + notesName, []byte(pptxNotesSlideXML(slide.Notes))},
				zipPart{"ppt/notesSlides/_rels/" + notesName + ".rels", []byte(relsXML([][3]string{
					{"rId1", "notesMaster", "../notesMasters/notesMaster1.xml"},
					{"rId2", "slide", "../slides/" + name},
				}))},
			)
		}
		parts = append(parts,
			zipPart{"ppt/slides/" + name, []byte(pptxSlideXML(slide))},
			zipPart{"ppt/slides/_rels/" + name + ".rels", []byte(relsXML(slideRels))},
		)
	}

	parts = append(parts,
		zipPart{"docProps/core.xml", []byte(corePropsXML(title))},
		zipPart{"docProps/app.xml", []byte(appPropsXML(fmt.Sprintf("<Slides>%d</Slides>", len(slides))))},
	)
	parts = append([]zipPart{{"[Content_Types].xml", []byte(contentTypesXML(overrides))}}, parts...)
	data, err := buildPackage(parts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.D("打包 pptx 失败"), err)
	}
	return data, nil
}

// pptxRuns 写一段文字的 run。lang 标成 zh-CN：PowerPoint 按它选东亚字体，
// 不标的话中文可能落到西文字体上显示成方框或难看的替代字体。
func pptxRuns(text string) string {
	var b strings.Builder
	for index, line := range strings.Split(text, "\n") {
		if index > 0 {
			b.WriteString(`<a:br><a:rPr lang="zh-CN" altLang="en-US"/></a:br>`)
		}
		if line == "" {
			continue
		}
		b.WriteString(`<a:r><a:rPr lang="zh-CN" altLang="en-US" dirty="0"/><a:t>` + xmlEscape(line) + `</a:t></a:r>`)
	}
	return b.String()
}

// pptxBulletParagraphs 把要点写成段落。前导空格两个一级；模型常顺手带上
// 「- 」「• 」前缀，这里去掉，项目符号由母版的正文样式统一画。
func pptxBulletParagraphs(bullets []string, withLevels bool) string {
	var b strings.Builder
	for _, bullet := range bullets {
		trimmed := strings.TrimLeft(bullet, " \t")
		indent := indentLevel(bullet[:len(bullet)-len(trimmed)])
		for _, prefix := range []string{"- ", "* ", "• ", "· "} {
			if strings.HasPrefix(trimmed, prefix) {
				trimmed = strings.TrimPrefix(trimmed, prefix)
				break
			}
		}
		level := min(indent, 4)
		b.WriteString(`<a:p>`)
		if withLevels && level > 0 {
			fmt.Fprintf(&b, `<a:pPr lvl="%d"/>`, level)
		}
		b.WriteString(pptxRuns(strings.TrimSpace(trimmed)))
		b.WriteString(`<a:endParaRPr lang="zh-CN" altLang="en-US" dirty="0"/></a:p>`)
	}
	return b.String()
}

// pptxShape 写一个占位符形状。spPr 留空，位置与样式都从版式继承——
// 这样用户在 PowerPoint 里换版式、换主题时，这些内容能跟着走。
func pptxShape(id int, name, placeholder, paragraphs string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`+
		`<p:nvPr>%s</p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>%s</p:txBody></p:sp>`,
		id, name, placeholder, paragraphs)
}

const pptxGroupHeader = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

func pptxSlideXML(slide pptxSlideSpec) string {
	var shapes strings.Builder
	titlePH, bodyPH := `<p:ph type="title"/>`, `<p:ph idx="1"/>`
	if slide.Layout == "title" {
		titlePH, bodyPH = `<p:ph type="ctrTitle"/>`, `<p:ph type="subTitle" idx="1"/>`
	}
	if strings.TrimSpace(slide.Title) != "" {
		shapes.WriteString(pptxShape(2, "标题 1", titlePH,
			`<a:p>`+pptxRuns(strings.TrimSpace(slide.Title))+`<a:endParaRPr lang="zh-CN" altLang="en-US" dirty="0"/></a:p>`))
	}
	if len(slide.Bullets) > 0 {
		shapes.WriteString(pptxShape(3, "内容占位符 2", bodyPH, pptxBulletParagraphs(slide.Bullets, slide.Layout != "title")))
	}
	return xml.Header + `<p:sld ` + pptxNS + `><p:cSld><p:spTree>` + pptxGroupHeader + shapes.String() +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
}

func pptxNotesSlideXML(notes string) string {
	var paragraphs strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(notes), "\r\n", "\n"), "\n") {
		paragraphs.WriteString(`<a:p>` + pptxRuns(line) + `<a:endParaRPr lang="zh-CN" altLang="en-US" dirty="0"/></a:p>`)
	}
	return xml.Header + `<p:notes ` + pptxNS + `><p:cSld><p:spTree>` + pptxGroupHeader +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="幻灯片图像占位符 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>` +
		`<p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		pptxShape(3, "备注占位符 2", `<p:ph type="body" idx="1"/>`, paragraphs.String()) +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`
}

// pptxPlaceholder 写母版/版式上带位置的占位符。
func pptxPlaceholder(id int, name, placeholder string, x, y, cx, cy int, anchor, lstStyle, prompt string) string {
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>`+
		`<p:nvPr>%s</p:nvPr></p:nvSpPr>`+
		`<p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>`+
		`<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0" anchor="%s"><a:normAutofit/></a:bodyPr>`+
		`<a:lstStyle>%s</a:lstStyle><a:p><a:r><a:rPr lang="zh-CN" altLang="en-US"/><a:t>%s</a:t></a:r>`+
		`<a:endParaRPr lang="zh-CN" altLang="en-US"/></a:p></p:txBody></p:sp>`,
		id, name, placeholder, x, y, cx, cy, anchor, lstStyle, prompt)
}

const pptxClrMap = `<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" ` +
	`accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>`

// pptxBodyLevel 是正文某一级的段落样式：项目符号、缩进、字号。
func pptxBodyLevel(level, size int) string {
	margin := 228600 + level*457200
	before := 1000 // 一级要点之间留得开一些，下级紧凑
	if level > 0 {
		before = 500
	}
	return fmt.Sprintf(`<a:lvl%dpPr marL="%d" indent="-228600" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">`+
		`<a:lnSpc><a:spcPct val="90000"/></a:lnSpc><a:spcBef><a:spcPts val="%d"/></a:spcBef>`+
		`<a:buFont typeface="Arial" panose="020B0604020202020204" pitchFamily="34" charset="0"/><a:buChar char="•"/>`+
		`<a:defRPr sz="%d" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill>`+
		`<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl%dpPr>`,
		level+1, margin, before, size, level+1)
}

var pptxMasterXML = func() string {
	var body strings.Builder
	for level, size := range []int{2800, 2400, 2000, 1800, 1800} {
		body.WriteString(pptxBodyLevel(level, size))
	}
	title := `<a:lvl1pPr algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">` +
		`<a:lnSpc><a:spcPct val="90000"/></a:lnSpc><a:spcBef><a:spcPct val="0"/></a:spcBef><a:buNone/>` +
		`<a:defRPr sz="4400" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill>` +
		`<a:latin typeface="+mj-lt"/><a:ea typeface="+mj-ea"/><a:cs typeface="+mj-cs"/></a:defRPr></a:lvl1pPr>`
	other := `<a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr>` +
		`<a:lvl1pPr marL="0" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">` +
		`<a:defRPr sz="1800" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill>` +
		`<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl1pPr>`
	return xml.Header + `<p:sldMaster ` + pptxNS + `><p:cSld>` +
		`<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + pptxGroupHeader +
		pptxPlaceholder(2, "标题占位符 1", `<p:ph type="title"/>`, 838200, 365125, 10515600, 1325563, "ctr", "", "单击此处编辑母版标题样式") +
		pptxPlaceholder(3, "文本占位符 2", `<p:ph type="body" idx="1"/>`, 838200, 1825625, 10515600, 4351338, "t", "", "编辑母版文本样式") +
		`</p:spTree></p:cSld>` + pptxClrMap +
		`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/><p:sldLayoutId id="2147483650" r:id="rId2"/></p:sldLayoutIdLst>` +
		`<p:txStyles><p:titleStyle>` + title + `</p:titleStyle><p:bodyStyle>` + body.String() + `</p:bodyStyle>` +
		`<p:otherStyle>` + other + `</p:otherStyle></p:txStyles></p:sldMaster>`
}()

var pptxTitleLayoutXML = xml.Header + `<p:sldLayout ` + pptxNS + ` type="title" preserve="1"><p:cSld name="标题幻灯片"><p:spTree>` +
	pptxGroupHeader +
	pptxPlaceholder(2, "标题 1", `<p:ph type="ctrTitle"/>`, 1524000, 1122363, 9144000, 2387600, "b",
		`<a:lvl1pPr algn="ctr"><a:defRPr sz="6000"/></a:lvl1pPr>`, "单击此处编辑母版标题样式") +
	pptxPlaceholder(3, "副标题 2", `<p:ph type="subTitle" idx="1"/>`, 1524000, 3602038, 9144000, 1655762, "t",
		`<a:lvl1pPr marL="0" indent="0" algn="ctr"><a:buNone/><a:defRPr sz="2400"/></a:lvl1pPr>`, "单击此处编辑母版副标题样式") +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

var pptxContentLayoutXML = xml.Header + `<p:sldLayout ` + pptxNS + ` type="obj" preserve="1"><p:cSld name="标题和内容"><p:spTree>` +
	pptxGroupHeader +
	pptxShape(2, "标题 1", `<p:ph type="title"/>`, `<a:p><a:r><a:rPr lang="zh-CN" altLang="en-US"/><a:t>单击此处编辑母版标题样式</a:t></a:r></a:p>`) +
	pptxShape(3, "内容占位符 2", `<p:ph idx="1"/>`, `<a:p><a:r><a:rPr lang="zh-CN" altLang="en-US"/><a:t>编辑母版文本样式</a:t></a:r></a:p>`) +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

var pptxNotesMasterXML = xml.Header + `<p:notesMaster ` + pptxNS + `><p:cSld>` +
	`<p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + pptxGroupHeader +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="幻灯片图像占位符 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>` +
	`<p:nvPr><p:ph type="sldImg" idx="2"/></p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="1143000"/><a:ext cx="5486400" cy="3086100"/></a:xfrm>` +
	`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln w="12700"><a:solidFill><a:prstClr val="black"/></a:solidFill></a:ln></p:spPr></p:sp>` +
	pptxPlaceholder(3, "备注占位符 2", `<p:ph type="body" sz="quarter" idx="3"/>`, 685800, 4400550, 5486400, 3600450, "t", "", "编辑母版文本样式") +
	`</p:spTree></p:cSld>` + pptxClrMap +
	`<p:notesStyle><a:lvl1pPr marL="0" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">` +
	`<a:defRPr sz="1200" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill>` +
	`<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/></a:defRPr></a:lvl1pPr></p:notesStyle>` +
	`</p:notesMaster>`

// pptxThemeXML 是一份完整的 Office 主题。
//
// 主题里的三组格式列表（填充、线条、效果）和背景填充列表**各必须至少三项**，
// 少一项 PowerPoint 就判定文件损坏——看起来冗余，其实是硬性要求。东亚字体
// 直接写成等线：只靠 <a:font script="Hans"> 的话，要看系统语言才会生效。
var pptxThemeXML = func() string {
	colors := [][2]string{
		{"dk2", "44546A"}, {"lt2", "E7E6E6"}, {"accent1", "4472C4"}, {"accent2", "ED7D31"}, {"accent3", "A5A5A5"},
		{"accent4", "FFC000"}, {"accent5", "5B9BD5"}, {"accent6", "70AD47"}, {"hlink", "0563C1"}, {"folHlink", "954F72"},
	}
	var scheme strings.Builder
	scheme.WriteString(`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>`)
	for _, color := range colors {
		scheme.WriteString(`<a:` + color[0] + `><a:srgbClr val="` + color[1] + `"/></a:` + color[0] + `>`)
	}
	var lines strings.Builder
	for _, width := range []int{6350, 12700, 19050} {
		fmt.Fprintf(&lines, `<a:ln w="%d" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`+
			`<a:prstDash val="solid"/><a:miter lim="800000"/></a:ln>`, width)
	}
	fills := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
		`<a:solidFill><a:schemeClr val="phClr"><a:tint val="50000"/></a:schemeClr></a:solidFill>` +
		`<a:solidFill><a:schemeClr val="phClr"><a:shade val="80000"/></a:schemeClr></a:solidFill>`
	return xml.Header + `<a:theme xmlns:a="` + nsDrawingML + `" name="Office 主题"><a:themeElements>` +
		`<a:clrScheme name="Office">` + scheme.String() + `</a:clrScheme>` +
		`<a:fontScheme name="Office">` +
		`<a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface="等线 Light"/><a:cs typeface=""/>` +
		`<a:font script="Hans" typeface="等线 Light"/></a:majorFont>` +
		`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface="等线"/><a:cs typeface=""/>` +
		`<a:font script="Hans" typeface="等线"/></a:minorFont></a:fontScheme>` +
		`<a:fmtScheme name="Office"><a:fillStyleLst>` + fills + `</a:fillStyleLst>` +
		`<a:lnStyleLst>` + lines.String() + `</a:lnStyleLst>` +
		`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle>` +
		`<a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
		`<a:bgFillStyleLst>` + fills + `</a:bgFillStyleLst></a:fmtScheme>` +
		`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
}()
