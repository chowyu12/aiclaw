package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadParsesFrontmatter(t *testing.T) {
	root := t.TempDir()
	write(t, root, "daily-report", `---
name: 销售日报
description: 需要生成或核对每日销售流水报表时用
---

# 怎么做

1. 先拉昨日流水
2. 再比对上月同期
`)

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("期望 1 个技能，实际 %d", len(loaded))
	}
	skill := loaded[0]
	if skill.Name != "销售日报" {
		t.Errorf("name = %q", skill.Name)
	}
	if skill.Description != "需要生成或核对每日销售流水报表时用" {
		t.Errorf("description = %q", skill.Description)
	}
	if !strings.Contains(skill.Body, "先拉昨日流水") {
		t.Errorf("正文没取到：%q", skill.Body)
	}
	// frontmatter 不该留在正文里，否则会被当成内容喂给模型。
	if strings.Contains(skill.Body, "description:") {
		t.Errorf("frontmatter 漏进正文了：%q", skill.Body)
	}
}

func TestLoadFallsBackToDirNameAndFirstLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "no-meta", "# 清理临时文件\n\n把 tmp 下超过 7 天的删掉。")

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("期望 1 个技能，实际 %d", len(loaded))
	}
	if loaded[0].Name != "no-meta" {
		t.Errorf("没有 name 时应当用目录名，实际 %q", loaded[0].Name)
	}
	// 没有说明的技能对模型没用——它无从判断什么时候该用，所以拿首行顶上。
	if loaded[0].Description != "清理临时文件" {
		t.Errorf("应当拿正文首行当说明，实际 %q", loaded[0].Description)
	}
}

func TestLoadSkipsBrokenSkillsWithoutFailingTheRest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "good", "---\nname: 好的\ndescription: 能用\n---\n正文")
	write(t, root, "no-skill-md", "") // 目录在，但没有 SKILL.md
	if err := os.WriteFile(filepath.Join(root, "散落的文件.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	// 一个手写坏的技能不该让整个技能系统失灵。
	if len(loaded) != 1 || loaded[0].Name != "好的" {
		t.Fatalf("坏的应当被跳过、好的应当留下，实际 %+v", loaded)
	}
}

func TestLoadRespectsDisabledMarker(t *testing.T) {
	root := t.TempDir()
	dir := write(t, root, "paused", "---\nname: 停用的\ndescription: x\n---\n正文")
	if err := os.WriteFile(filepath.Join(dir, ".disabled"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, root, "active", "---\nname: 启用的\ndescription: y\n---\n正文")

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, skill := range loaded {
		byName[skill.Name] = skill.Disabled
	}
	if !byName["停用的"] {
		t.Error("有 .disabled 标记的技能应当标成禁用")
	}
	if byName["启用的"] {
		t.Error("没有标记的技能不该被禁用")
	}
}

func TestLoadIsSortedAndSkipsHiddenDirs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "b-skill", "---\nname: b\ndescription: x\n---\n正文")
	write(t, root, "a-skill", "---\nname: a\ndescription: x\n---\n正文")
	write(t, root, ".git", "---\nname: 不该出现\ndescription: x\n---\n正文")

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("隐藏目录应当跳过，实际 %+v", loaded)
	}
	// 顺序稳定，模型看到的清单才可复现。
	if loaded[0].Name != "a" || loaded[1].Name != "b" {
		t.Errorf("应当按名字排序，实际 %q %q", loaded[0].Name, loaded[1].Name)
	}
}

func TestLoadOnMissingDirIsNotAnError(t *testing.T) {
	// 全新安装还没有技能目录，这不是错误。
	loaded, err := Load([]string{filepath.Join(t.TempDir(), "还没建")})
	if err != nil {
		t.Errorf("目录不存在时不该报错：%v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("不该凭空出技能：%+v", loaded)
	}
}

func TestLoadRejectsOversizedSkill(t *testing.T) {
	root := t.TempDir()
	// 技能是提示词不是数据集。几 MB 的 SKILL.md 多半是放错了东西，
	// 挂上去会把上下文一次吃光。
	write(t, root, "huge", strings.Repeat("x", maxBodyBytes+1))

	loaded, err := Load(dirsIn(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Errorf("超限的技能应当被跳过，实际 %+v", loaded)
	}
}

func TestSplitFrontmatterHandlesCRLFAndQuotes(t *testing.T) {
	meta, body := splitFrontmatter("---\r\nname: \"带引号\"\r\ndescription: '单引号'\r\n---\r\n正文\r\n")
	if meta["name"] != "带引号" {
		t.Errorf("引号应当去掉，实际 %q", meta["name"])
	}
	if meta["description"] != "单引号" {
		t.Errorf("单引号也要去掉，实际 %q", meta["description"])
	}
	if strings.TrimSpace(body) != "正文" {
		t.Errorf("正文 = %q", body)
	}
}

func TestSplitFrontmatterLeavesPlainMarkdownAlone(t *testing.T) {
	// 没有 frontmatter 的文件整篇都是正文，不能被啃掉开头。
	_, body := splitFrontmatter("# 标题\n\n内容")
	if !strings.HasPrefix(body, "# 标题") {
		t.Errorf("正文被改动了：%q", body)
	}
}

// dirsIn 把一个「装着若干技能的目录」展开成 Load 要的技能目录清单。
//
// Load 现在收的是具体技能目录：技能散在 Claude / Codex / npm 等好几处，
// 发现逻辑统一放在宿主侧了。测试里用这个小助手保留原来的写法。
func dirsIn(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(root, entry.Name()))
	}
	return dirs
}

// YAML 块标量：说明写长了只能折行，而折行在 YAML 里就是 `>` 或 `|` 加缩进。
// 不认的话取到的值是一个 ">"，界面上显示成一个尖括号，模型也拿不到判断依据。
// Codex 装的技能就是这么写的，实际踩到过。
func TestFoldedDescriptionIsRead(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "storage-analyzer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `---
name: storage-analyzer
description: >
  macOS / Windows 只读存储分析助手。扫描整机磁盘占用，找出
  占空间大户，把每一项分成三级并给出可执行处置方案。

  使用：用户说"存储分析""磁盘满了"时。
---

# 正文
第一段。
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := Load([]string{skillDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("应当发现一个技能：%+v", found)
	}
	skill := found[0]
	if skill.Name != "storage-analyzer" {
		t.Errorf("名字 = %q", skill.Name)
	}
	// 折叠式：相邻行用空格连起来，空行成为换行。
	if !strings.Contains(skill.Description, "找出 占空间大户") {
		t.Errorf("折行应当用空格连起来：%q", skill.Description)
	}
	if !strings.Contains(skill.Description, `用户说"存储分析"`) {
		t.Errorf("后面几行也要收进来：%q", skill.Description)
	}
	if strings.HasPrefix(skill.Description, ">") || skill.Description == ">" {
		t.Errorf("引子不该成为值：%q", skill.Description)
	}
	if !strings.Contains(skill.Body, "第一段") {
		t.Errorf("正文要从 --- 之后开始：%q", skill.Body)
	}
}

// 字面式（|）保留换行；chomping 指示符（>-、|-）也要认。
func TestLiteralAndChompedBlockScalars(t *testing.T) {
	cases := map[string]struct {
		source string
		want   string
	}{
		"字面式保留换行": {"description: |\n  第一行\n  第二行\n", "第一行\n第二行"},
		"折叠式带 -":  {"description: >-\n  第一行\n  第二行\n", "第一行 第二行"},
		"字面式带 -":  {"description: |-\n  只有一行\n", "只有一行"},
	}
	for name, testCase := range cases {
		meta, _ := splitFrontmatter("---\n" + testCase.source + "---\n正文\n")
		if meta["description"] != testCase.want {
			t.Errorf("%s：得到 %q，想要 %q", name, meta["description"], testCase.want)
		}
	}
}

// 单行写法照旧，`>` 只有作为引子（后面没别的字）时才当块标量。
func TestPlainDescriptionsStillWork(t *testing.T) {
	cases := map[string]string{
		`description: 一句话说明`:     "一句话说明",
		`description: "带引号的说明"`:  "带引号的说明",
		`description: 用 > 表示大于`:  "用 > 表示大于",
		`description: >不是块标量的写法`: ">不是块标量的写法",
	}
	for source, want := range cases {
		meta, _ := splitFrontmatter("---\nname: n\n" + source + "\n---\n正文\n")
		if meta["description"] != want {
			t.Errorf("%s：得到 %q，想要 %q", source, meta["description"], want)
		}
	}
}

// 块标量后面的键照常读到：块在回到顶格的那一行结束。
func TestKeysAfterABlockScalarAreRead(t *testing.T) {
	meta, _ := splitFrontmatter("---\ndescription: >\n  说明第一行\n  第二行\nname: 后写的名字\n---\n正文\n")
	if meta["description"] != "说明第一行 第二行" {
		t.Errorf("说明 = %q", meta["description"])
	}
	if meta["name"] != "后写的名字" {
		t.Errorf("块后面的名字没读到：%q", meta["name"])
	}
}
