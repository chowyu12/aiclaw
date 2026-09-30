package tools

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

func officeRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	if err := RegisterFileTools(registry); err != nil {
		t.Fatal(err)
	}
	if err := RegisterOfficeTools(registry); err != nil {
		t.Fatal(err)
	}
	return registry
}

// callJSON 把参数编码成 JSON 再调用，省得在测试里手拼转义。
func callJSON(t *testing.T, registry *Registry, name string, args map[string]any, env *Env) (string, error) {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return call(t, registry, name, string(encoded), env)
}

func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("输出里应当有 %q，实际：\n%s", want, text)
		}
	}
}

// ---------- 包结构校验 ----------
//
// Office 打不开文件时只会说「已损坏」，不说哪里坏。所以这里把最常见的几类
// 损坏逐项查掉：XML 不合法、rels 指向不存在的部件、部件没有 content type、
// content type 声明了不存在的部件。

func validatePackage(t *testing.T, file string) {
	t.Helper()
	reader, err := zip.OpenReader(file)
	if err != nil {
		t.Fatalf("不是合法 zip：%v", err)
	}
	defer reader.Close()
	parts := map[string][]byte{}
	for _, entry := range reader.File {
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[entry.Name] = data
	}
	// 自己打的包把 [Content_Types].xml 放第一个；excelize 打的包不保证，不强求。
	if !strings.HasSuffix(file, ".xlsx") && reader.File[0].Name != "[Content_Types].xml" {
		t.Errorf("[Content_Types].xml 应当是第一个部件，实际是 %s", reader.File[0].Name)
	}

	// 1. 每个 XML 部件都要能完整解析。
	for name, data := range parts {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s 不是合法 XML：%v", name, err)
				break
			}
		}
	}

	// 2. content type 覆盖所有部件，且声明的部件都存在。
	var types struct {
		Defaults []struct {
			Extension string `xml:"Extension,attr"`
		} `xml:"Default"`
		Overrides []struct {
			PartName string `xml:"PartName,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(parts["[Content_Types].xml"], &types); err != nil {
		t.Fatalf("解析 [Content_Types].xml：%v", err)
	}
	defaults := map[string]bool{}
	for _, item := range types.Defaults {
		defaults[strings.ToLower(item.Extension)] = true
	}
	overrides := map[string]bool{}
	for _, item := range types.Overrides {
		name := strings.TrimPrefix(item.PartName, "/")
		overrides[name] = true
		if _, ok := parts[name]; !ok {
			t.Errorf("content type 声明了不存在的部件 %s", name)
		}
	}
	for name := range parts {
		if name == "[Content_Types].xml" {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
		if !overrides[name] && !defaults[ext] {
			t.Errorf("部件 %s 没有 content type", name)
		}
	}
	// 主要部件不能只靠 Default xml 蒙混过去：Office 靠 Override 认出它是什么。
	for name := range parts {
		if strings.HasSuffix(name, ".xml") && !strings.HasPrefix(name, "[") && !overrides[name] {
			t.Errorf("XML 部件 %s 应当有自己的 Override", name)
		}
	}

	// 3. 每条内部关系都指向存在的部件。
	for name, data := range parts {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		var rels struct {
			Items []opcRel `xml:"Relationship"`
		}
		if err := xml.Unmarshal(data, &rels); err != nil {
			t.Errorf("解析 %s：%v", name, err)
			continue
		}
		// word/_rels/document.xml.rels 的源部件是 word/document.xml。
		source := strings.TrimSuffix(strings.Replace(name, "_rels/", "", 1), ".rels")
		if name == "_rels/.rels" {
			source = ""
		}
		ids := map[string]bool{}
		for _, rel := range rels.Items {
			if ids[rel.ID] {
				t.Errorf("%s 里关系 Id %s 重复", name, rel.ID)
			}
			ids[rel.ID] = true
			if strings.EqualFold(rel.TargetMode, "External") {
				continue
			}
			target := resolvePartTarget(source, rel.Target)
			if _, ok := parts[target]; !ok {
				t.Errorf("%s 里 %s 指向不存在的部件 %s", name, rel.ID, target)
			}
		}
	}
}

// ---------- Word ----------

const sampleMarkdown = "# 季度报告\n\n" +
	"这是第一段，含 **加粗文字** 和 *斜体* 以及 `code`。\n" +
	"## 要点\n" +
	"- 要点一\n" +
	"- 要点二\n" +
	"  - 子要点\n" +
	"1. 第一步\n" +
	"2. 第二步\n\n" +
	"| 姓名 | 年龄 |\n" +
	"| --- | --- |\n" +
	"| 张三 | 18 |\n" +
	"| 李四 \\| 王五 | 20 |\n" +
	"---\n" +
	"### 附录\n" +
	"```\n" +
	"x = 1 * 2\n" +
	"```\n" +
	"> 引用一句话\n" +
	"价格 3 * 4 元\n"

func TestWriteDocxThenReadOffice(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := officeRegistry(t)
	result, err := callJSON(t, registry, "write_docx", map[string]any{
		"path": "report.docx", "content": sampleMarkdown, "title": "季度报告",
	}, env)
	if err != nil {
		t.Fatalf("write_docx：%v", err)
	}
	mustContain(t, result, "report.docx", "1 个表格")
	validatePackage(t, filepath.Join(env.Workspace, "report.docx"))

	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "report.docx"}, env)
	if err != nil {
		t.Fatalf("read_office：%v", err)
	}
	mustContain(t, text,
		"# 季度报告", "## 要点", "### 附录",
		"**加粗文字**", "斜体", "code",
		"- 要点一\n- 要点二\n  - 子要点\n1. 第一步\n2. 第二步",
		"| **姓名** | **年龄** |", "| 张三 | 18 |", `李四 \| 王五`,
		"x = 1 * 2", "引用一句话", "价格 3 * 4 元",
	)
	if strings.Contains(text, "*斜体*") {
		t.Error("斜体标记应当被解析成格式，而不是原样写进文字")
	}
}

func TestWriteDocxOrderedListsRestartNumbering(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	content := "1. 甲\n2. 乙\n中间一段\n1. 丙\n2. 丁\n"
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "a.docx", "content": content}, env); err != nil {
		t.Fatal(err)
	}
	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "a.docx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	// 第二个列表要从 1 重新数，而不是接着 3、4。
	mustContain(t, text, "1. 甲\n2. 乙", "1. 丙\n2. 丁")
}

func TestGeneratedDocxIsReadableByTextutil(t *testing.T) {
	// 额外的一道校验：用 macOS 自带的 textutil 打开生成的文件。
	// 它解析失败会直接报错，比「我们自己能读回来」更接近真实的 Word/Pages。
	textutil, err := exec.LookPath("textutil")
	if err != nil {
		t.Skip("本机没有 textutil")
	}
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "r.docx", "content": sampleMarkdown}, env); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(textutil, "-convert", "txt", "-stdout", filepath.Join(env.Workspace, "r.docx")).CombinedOutput()
	if err != nil {
		t.Fatalf("textutil 打不开生成的 docx：%v\n%s", err, output)
	}
	mustContain(t, string(output), "季度报告", "要点一", "张三", "加粗文字")
}

func TestReadDocxTruncatesAndContinuesWithOffset(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	var content strings.Builder
	for i := 0; i < 3000; i++ {
		content.WriteString("这是一段用来撑大文档的中文内容，第")
		content.WriteString(strings.Repeat("字", 5))
		content.WriteString("段。\n")
	}
	content.WriteString("最后一段标记\n")
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "big.docx", "content": content.String()}, env); err != nil {
		t.Fatal(err)
	}
	first, err := callJSON(t, registry, "read_office", map[string]any{"path": "big.docx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) > maxOfficeText+500 {
		t.Errorf("返回 %d 字节，超过上限", len(first))
	}
	mustContain(t, first, "内容已截断", "offset=")
	if strings.Contains(first, "最后一段标记") {
		t.Error("截断之后不该还有末尾内容")
	}
	// 按提示里的 offset 一路读下去，最终能读到末尾。
	offset := 0
	text := first
	for round := 0; round < 10 && strings.Contains(text, "offset="); round++ {
		hint := text[strings.LastIndex(text, "offset=")+len("offset="):]
		hint = strings.Fields(hint)[0]
		if err := json.Unmarshal([]byte(hint), &offset); err != nil {
			t.Fatalf("解析 offset 提示 %q：%v", hint, err)
		}
		text, err = callJSON(t, registry, "read_office", map[string]any{"path": "big.docx", "offset": offset}, env)
		if err != nil {
			t.Fatal(err)
		}
	}
	mustContain(t, text, "最后一段标记")
}

// ---------- edit_docx ----------

func TestEditDocxReplacesTextSplitAcrossRuns(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := officeRegistry(t)
	// 「你好」是单独一个加粗 run，「世界后缀」是另一个 run：要替换的「好世界」横跨两个 run。
	content := "# 标题\n前缀**你好**世界后缀\n另一段保持不变\n"
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "e.docx", "content": content}, env); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(env.Workspace, "e.docx")
	before := readParts(t, file)

	if _, err := callJSON(t, registry, "edit_docx", map[string]any{
		"path": "e.docx", "old_text": "好世界", "new_text": "朋友们 & <伙伴>",
	}, env); err != nil {
		t.Fatalf("edit_docx：%v", err)
	}
	validatePackage(t, file)
	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "e.docx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	// 替换的文字沿用匹配开头那个 run 的格式（加粗）。
	mustContain(t, text, "前缀**你朋友们 & <伙伴>**后缀", "另一段保持不变", "# 标题")
	if strings.Contains(text, "好世界") {
		t.Error("原文应当已被替换")
	}

	// 除 document.xml 之外的部件逐字节不变。
	after := readParts(t, file)
	for name, data := range before {
		if name == "word/document.xml" {
			continue
		}
		if !bytes.Equal(data, after[name]) {
			t.Errorf("%s 不该被改动", name)
		}
	}
	if len(after) != len(before) {
		t.Errorf("部件数从 %d 变成了 %d", len(before), len(after))
	}
}

func TestEditDocxRequiresUniqueMatch(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "u.docx", "content": "重复\n重复\n"}, env); err != nil {
		t.Fatal(err)
	}
	_, err := callJSON(t, registry, "edit_docx", map[string]any{"path": "u.docx", "old_text": "重复", "new_text": "x"}, env)
	if err == nil || !strings.Contains(err.Error(), "出现了 2 次") {
		t.Errorf("不唯一时应当报错，得到 %v", err)
	}
	_, err = callJSON(t, registry, "edit_docx", map[string]any{"path": "u.docx", "old_text": "没有的", "new_text": "x"}, env)
	if err == nil || !strings.Contains(err.Error(), "找不到") {
		t.Errorf("找不到时应当报错，得到 %v", err)
	}
	// 跨段落不支持，要当场说清。
	_, err = callJSON(t, registry, "edit_docx", map[string]any{"path": "u.docx", "old_text": "重复\n重复", "new_text": "x"}, env)
	if err == nil || !strings.Contains(err.Error(), "跨段落") {
		t.Errorf("跨段落时应当报错，得到 %v", err)
	}
}

func readParts(t *testing.T, file string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	out := map[string][]byte{}
	for _, entry := range reader.File {
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(handle)
		handle.Close()
		out[entry.Name] = data
	}
	return out
}

// ---------- Excel ----------

func TestWriteXlsxThenReadOffice(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	_, err := callJSON(t, registry, "write_xlsx", map[string]any{
		"path":        "book.xlsx",
		"header_bold": true,
		"sheets": []any{
			map[string]any{"name": "数据", "rows": []any{
				[]any{"名称", "数量", "启用"},
				[]any{"苹果", 10, true},
				[]any{"香蕉", 20.5, false},
				[]any{"合计", "=SUM(B2:B3)", nil},
				[]any{"'=不是公式", 1, nil},
			}},
			map[string]any{"name": "说明", "start": "B2", "rows": []any{[]any{"备注 | 含竖线"}}},
		},
	}, env)
	if err != nil {
		t.Fatalf("write_xlsx：%v", err)
	}
	validatePackage(t, filepath.Join(env.Workspace, "book.xlsx"))

	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "book.xlsx"}, env)
	if err != nil {
		t.Fatalf("read_office：%v", err)
	}
	mustContain(t, text,
		"[数据]", "说明", "共 5 行 × 3 列",
		"| 行 | A | B | C |", "| 1 | 名称 | 数量 | 启用 |", "| 2 | 苹果 | 10 | TRUE |",
		"30.5", "B4 = SUM(B2:B3)", "=不是公式",
	)

	second, err := callJSON(t, registry, "read_office", map[string]any{"path": "book.xlsx", "sheet": "说明"}, env)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, second, "[说明]", `备注 \| 含竖线`, "| 2 |")

	_, err = callJSON(t, registry, "read_office", map[string]any{"path": "book.xlsx", "sheet": "不存在"}, env)
	if err == nil || !strings.Contains(err.Error(), "现有") {
		t.Errorf("工作表不存在时应当列出现有的，得到 %v", err)
	}
}

func TestWriteXlsxUpdateKeepsOtherSheets(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	write := func(sheets []any) {
		t.Helper()
		if _, err := callJSON(t, registry, "write_xlsx", map[string]any{"path": "b.xlsx", "sheets": sheets}, env); err != nil {
			t.Fatalf("write_xlsx：%v", err)
		}
	}
	read := func(sheet string) string {
		t.Helper()
		text, err := callJSON(t, registry, "read_office", map[string]any{"path": "b.xlsx", "sheet": sheet}, env)
		if err != nil {
			t.Fatalf("read_office：%v", err)
		}
		return text
	}
	write([]any{
		map[string]any{"name": "甲", "rows": []any{[]any{"a1", "b1"}, []any{"a2", "=1+1"}, []any{"a3", "b3"}}},
		map[string]any{"name": "乙", "rows": []any{[]any{"乙表原有"}}},
	})

	// 只写「乙」：「甲」原样保留。
	write([]any{map[string]any{"name": "乙", "rows": []any{[]any{"乙表新内容"}}}})
	mustContain(t, read("甲"), "a1", "a3", "B2 = 1+1 → 2")
	second := read("乙")
	mustContain(t, second, "乙表新内容")
	if strings.Contains(second, "乙表原有") {
		t.Error("replace 模式应当先清空")
	}

	// 默认 replace：「甲」只剩新写的一行，原来的第三行和公式都没了。
	write([]any{map[string]any{"name": "甲", "rows": []any{[]any{"新"}}}})
	first := read("甲")
	mustContain(t, first, "共 1 行 × 1 列", "新")
	if strings.Contains(first, "a3") || strings.Contains(first, "1+1") {
		t.Errorf("replace 之后旧内容不该还在：\n%s", first)
	}

	// update：只覆盖写到的格子，其余保留。
	write([]any{map[string]any{"name": "甲", "mode": "update", "start": "B1", "rows": []any{[]any{"右边"}}}})
	mustContain(t, read("甲"), "| 1 | 新 | 右边 |")

	// 新加一张表也不影响已有的。
	write([]any{map[string]any{"name": "丙", "rows": []any{[]any{1}}}})
	mustContain(t, read("丙"), "甲、乙、[丙]")
}

func TestReadXlsxDefaultWindowAndRange(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	var rows []any
	for i := 1; i <= 250; i++ {
		rows = append(rows, []any{i, "行" + strings.Repeat("x", i%3)})
	}
	if _, err := callJSON(t, registry, "write_xlsx", map[string]any{
		"path": "long.xlsx", "sheets": []any{map[string]any{"name": "S", "rows": rows}},
	}, env); err != nil {
		t.Fatal(err)
	}
	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "long.xlsx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, text, "共 250 行", "| 200 | 200 |", "range=A201:B250")
	if strings.Contains(text, "| 201 |") {
		t.Error("默认只读前 200 行")
	}
	tail, err := callJSON(t, registry, "read_office", map[string]any{"path": "long.xlsx", "range": "A240:B260"}, env)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, tail, "| 240 | 240 |", "| 250 | 250 |")
	if strings.Contains(tail, "| 239 |") || strings.Contains(tail, "range=") {
		t.Errorf("range 读到末尾不该再提示后面还有：\n%s", tail)
	}
}

func TestParseCellRange(t *testing.T) {
	cases := map[string][4]int{
		"A1:F200":     {1, 1, 6, 200},
		"b2":          {2, 2, 2, 2},
		"$A$1:$C$3":   {1, 1, 3, 3},
		"Sheet1!A1:B": {1, 1, 2, 2147483647},
		"3:10":        {1, 3, 0, 10},
	}
	for input, want := range cases {
		c1, r1, c2, r2, err := parseCellRange(input)
		if err != nil {
			t.Errorf("%s：%v", input, err)
			continue
		}
		if got := [4]int{c1, r1, c2, r2}; got != want {
			t.Errorf("%s：得到 %v，应当 %v", input, got, want)
		}
	}
	for _, bad := range []string{"", "1A", "A1:B2:C3", "!!"} {
		if _, _, _, _, err := parseCellRange(bad); err == nil {
			t.Errorf("%q 应当报错", bad)
		}
	}
}

// ---------- PowerPoint ----------

func pptxArgs(file string) map[string]any {
	return map[string]any{
		"path": file, "title": "演示",
		"slides": []any{
			map[string]any{"layout": "title", "title": "封面标题", "bullets": []string{"副标题一行"}},
			map[string]any{"title": "第二页标题", "bullets": []string{"要点甲", "  子要点", "- 要点乙"}, "notes": "第二页备注\n第二行"},
			map[string]any{"title": "第三页 & <特殊字符>", "bullets": []string{"最后一点"}, "notes": "第三页备注"},
		},
	}
}

func TestWritePptxThenReadOffice(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	if _, err := callJSON(t, registry, "write_pptx", pptxArgs("deck.pptx"), env); err != nil {
		t.Fatalf("write_pptx：%v", err)
	}
	file := filepath.Join(env.Workspace, "deck.pptx")
	validatePackage(t, file)

	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "deck.pptx"}, env)
	if err != nil {
		t.Fatalf("read_office：%v", err)
	}
	mustContain(t, text,
		"共 3 页", "## 第 1 页：封面标题", "副标题一行",
		"## 第 2 页：第二页标题", "- 要点甲\n  - 子要点\n- 要点乙", "备注：\n第二页备注\n第二行",
		"## 第 3 页：第三页 & <特殊字符>", "第三页备注",
	)
	order := []string{"第 1 页", "第 2 页", "第 3 页"}
	last := -1
	for _, marker := range order {
		index := strings.Index(text, marker)
		if index <= last {
			t.Errorf("页序不对：%s 出现在第 %d 字节", marker, index)
		}
		last = index
	}
	if strings.Contains(text, "- - 要点乙") {
		t.Error("模型带上的 - 前缀应当去掉，由母版画项目符号")
	}

	only, err := callJSON(t, registry, "read_office", map[string]any{"path": "deck.pptx", "slides": "2"}, env)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, only, "第 2–2 页", "第二页标题")
	if strings.Contains(only, "封面标题") || strings.Contains(only, "最后一点") {
		t.Error("slides=2 只该返回第二页")
	}
}

func TestReadPptxFollowsPresentationOrderNotFileNames(t *testing.T) {
	// 用户在 PowerPoint 里拖动页序后，slideN.xml 的编号不会变，变的只是
	// presentation.xml 里 sldIdLst 的顺序。这里把第三页挪到最前面。
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	if _, err := callJSON(t, registry, "write_pptx", pptxArgs("deck.pptx"), env); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(env.Workspace, "deck.pptx")
	parts := readParts(t, file)
	presentation := string(parts["ppt/presentation.xml"])
	third := `<p:sldId id="258" r:id="rId4"/>`
	if !strings.Contains(presentation, third) {
		t.Fatalf("presentation.xml 结构和预想的不一样：%s", presentation)
	}
	presentation = strings.Replace(presentation, third, "", 1)
	presentation = strings.Replace(presentation, "<p:sldIdLst>", "<p:sldIdLst>"+third, 1)
	parts["ppt/presentation.xml"] = []byte(presentation)
	var ordered []zipPart
	ordered = append(ordered, zipPart{"[Content_Types].xml", parts["[Content_Types].xml"]})
	for name, data := range parts {
		if name != "[Content_Types].xml" {
			ordered = append(ordered, zipPart{name, data})
		}
	}
	data, err := buildPackage(ordered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := callJSON(t, registry, "read_office", map[string]any{"path": "deck.pptx"}, env)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, text, "## 第 1 页：第三页", "## 第 2 页：封面标题", "## 第 3 页：第二页标题")
}

// ---------- 老格式与误用 ----------

func TestLegacyOfficeFormatsGiveAClearError(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	ole := append([]byte{}, oleMagic...)
	ole = append(ole, make([]byte, 512)...)
	for _, name := range []string{"old.doc", "old.xls", "old.ppt", "renamed.docx"} {
		if err := os.WriteFile(filepath.Join(env.Workspace, name), ole, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := callJSON(t, registry, "read_office", map[string]any{"path": name}, env)
		if err == nil || !strings.Contains(err.Error(), "旧二进制格式") || !strings.Contains(err.Error(), "另存为") {
			t.Errorf("%s 应当说明是旧格式并给出办法，得到 %v", name, err)
		}
	}
	_, err := callJSON(t, registry, "read_office", map[string]any{"path": "notes.txt"}, env)
	if err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Errorf("非 Office 文件应当指回 read_file，得到 %v", err)
	}
}

func TestReadFilePointsOfficeFilesToReadOffice(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalBypass, true)
	registry := officeRegistry(t)
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "a.docx", "content": "x"}, env); err != nil {
		t.Fatal(err)
	}
	_, err := callJSON(t, registry, "read_file", map[string]any{"path": "a.docx"}, env)
	if err == nil || !strings.Contains(err.Error(), "read_office") {
		t.Errorf("read_file 读 docx 应当指向 read_office，得到 %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.Workspace, "b.xls"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = callJSON(t, registry, "read_file", map[string]any{"path": "b.xls"}, env)
	if err == nil || !strings.Contains(err.Error(), "旧二进制格式") {
		t.Errorf("read_file 读 xls 应当说明旧格式，得到 %v", err)
	}
}

func TestOfficeWritesRejectWrongExtension(t *testing.T) {
	env, asked := newEnv(t, protocol.ApprovalAlways, true)
	registry := officeRegistry(t)
	cases := map[string]map[string]any{
		"write_docx": {"path": "a.doc", "content": "x"},
		"write_xlsx": {"path": "a.xls", "sheets": []any{map[string]any{"name": "S", "rows": []any{}}}},
		"write_pptx": {"path": "a.ppt", "slides": []any{map[string]any{"title": "x"}}},
	}
	for name, args := range cases {
		if _, err := callJSON(t, registry, name, args, env); err == nil || !strings.Contains(err.Error(), "扩展名") {
			t.Errorf("%s 写错扩展名应当报错，得到 %v", name, err)
		}
	}
	if len(*asked) != 0 {
		t.Error("参数不对的写入不该先去问用户")
	}
}

// ---------- 审批 ----------

func TestOfficeWritesOutsideTheWorkspaceAskFirst(t *testing.T) {
	env, asked := newEnv(t, protocol.ApprovalOnWrite, true)
	registry := officeRegistry(t)
	outside := t.TempDir()
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"write_docx", map[string]any{"path": filepath.Join(outside, "a.docx"), "content": "你好"}},
		{"write_xlsx", map[string]any{"path": filepath.Join(outside, "a.xlsx"), "sheets": []any{map[string]any{"name": "S", "rows": []any{[]any{1}}}}}},
		{"write_pptx", pptxArgs(filepath.Join(outside, "a.pptx"))},
		{"edit_docx", map[string]any{"path": filepath.Join(outside, "a.docx"), "old_text": "你好", "new_text": "再见"}},
	}
	for index, item := range calls {
		if _, err := callJSON(t, registry, item.tool, item.args, env); err != nil {
			t.Fatalf("%s：%v", item.tool, err)
		}
		if len(*asked) != index+1 {
			t.Fatalf("%s 写工作区外应当问一次，累计 %d 次", item.tool, len(*asked))
		}
		request := (*asked)[index]
		if request.Kind != protocol.ApprovalWrite || request.Reason == "" || request.ScopePath == "" {
			t.Errorf("%s 的审批请求不完整：%+v", item.tool, request)
		}
	}

	// 工作区内照旧不问。
	before := len(*asked)
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": "in.docx", "content": "x"}, env); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != before {
		t.Error("工作区内的写不该弹审批")
	}
}

func TestOfficeWriteDeniedLeavesNothingBehind(t *testing.T) {
	env, _ := newEnv(t, protocol.ApprovalOnWrite, false)
	registry := officeRegistry(t)
	target := filepath.Join(t.TempDir(), "denied.docx")
	if _, err := callJSON(t, registry, "write_docx", map[string]any{"path": target, "content": "x"}, env); err == nil {
		t.Fatal("用户拒绝后应当失败")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("被拒绝的写不该留下文件")
	}
}

func TestOfficeToolEffects(t *testing.T) {
	// 通道会话的只读白名单是按 Effect 从注册表算出来的：read_office 必须是只读，
	// 写工具必须不是，否则外部来的消息就能不经授权改用户的文件。
	registry := officeRegistry(t)
	want := map[string]Effect{
		"read_office": EffectRead, "write_docx": EffectWrite, "edit_docx": EffectWrite,
		"write_xlsx": EffectWrite, "write_pptx": EffectWrite,
	}
	for name, effect := range want {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("没有登记 %s", name)
		}
		if tool.Effect != effect {
			t.Errorf("%s 的 Effect 是 %s，应当是 %s", name, tool.Effect, effect)
		}
		var decoded map[string]any
		if err := json.Unmarshal(tool.Schema, &decoded); err != nil {
			t.Errorf("%s 的 schema 不是合法 JSON：%v", name, err)
		}
	}
}
