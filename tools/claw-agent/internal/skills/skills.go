// Package skills 加载本地技能。
//
// 一个技能就是一个目录，里面有 SKILL.md：开头是 YAML frontmatter（name、
// description），下面是正文，写清楚什么时候用、怎么用。这与 Codex / Claude Code
// 的技能是同一个形状，也与技能市场 的包内容一致——SkillHub 分发的就是
// 装着 SKILL.md 的 ZIP，装到本地目录之后与用户自己写的技能没有区别。
//
// 为什么不把技能正文直接塞进系统提示词：十几个技能的正文加起来能有几万 token，
// 每轮都带着走，既烧钱又把上下文挤没了。所以只把「名字 + 一句话说明」放进
// 提示词，正文等模型真要用的时候通过 load_skill 取——这是渐进披露，
// 也是这几个实现不约而同的做法。
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill 是一个已加载的技能。
type Skill struct {
	// Name 是模型用来引用它的名字，取自 frontmatter，缺省用目录名。
	Name string
	// Description 是一句话说明：模型靠它判断这次要不要用这个技能，
	// 所以它必须写明**什么时候**用，而不只是「这是做什么的」。
	Description string
	// Dir 是技能目录，正文里的相对路径以它为基准。
	Dir string
	// Body 是 SKILL.md 去掉 frontmatter 之后的正文。
	Body string
	// Disabled 为 true 时不挂给模型。用户在界面上关掉的。
	Disabled bool
}

const (
	// maxBodyBytes 限制单个技能正文的大小。技能是提示词不是数据集；
	// 谁把一个几 MB 的文件命名成 SKILL.md，多半是放错了。
	maxBodyBytes = 256 * 1024
	// disabledMarker 存在时这个技能被禁用。用文件而不是集中配置，
	// 是为了让「删掉目录」和「禁用」都只动这一个目录，不留孤儿记录。
	disabledMarker = ".disabled"
)

// Load 加载一批技能目录。
//
// **每一项都是一个技能自己的目录**（里面直接放 SKILL.md），不是装着若干技能的
// 根目录。技能散在好几个地方——我们自己的 ~/.aiclaw/skills、Claude Code 的
// ~/.claude/skills、Codex 的 ~/.codex/skills、npx 装的 CLI 缓存、npm 全局包，
// 而且各自的目录结构还不一样。那套发现逻辑只该有一份，且必须在宿主侧：
// 界面要显示「这个技能从哪儿来」，还要记住用户关掉了哪些。
//
// 单个技能坏了（没有 SKILL.md、读不动、frontmatter 不合法）只跳过它，
// 不影响其余技能——一个手写坏的技能不该让整个技能系统失灵。
//
// 同名只留第一个：两个同名技能挂给模型，load_skill 取到哪一个就成了运气。
func Load(dirs []string) ([]Skill, error) {
	var loaded []Skill
	seen := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		skill, err := loadOne(dir, filepath.Base(dir))
		if err != nil {
			continue
		}
		if seen[skill.Name] {
			continue
		}
		seen[skill.Name] = true
		loaded = append(loaded, skill)
	}
	// 按名字排序，让工具清单与提示词在模型面前的顺序稳定可复现。
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Name < loaded[j].Name })
	return loaded, nil
}

func loadOne(dir, fallbackName string) (Skill, error) {
	path := filepath.Join(dir, "SKILL.md")
	info, err := os.Stat(path)
	if err != nil {
		return Skill{}, err
	}
	if info.Size() > maxBodyBytes {
		return Skill{}, fmt.Errorf("SKILL.md 超过 %d 字节", maxBodyBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	meta, body := splitFrontmatter(string(raw))
	skill := Skill{
		Name:        firstNonEmpty(meta["name"], fallbackName),
		Description: meta["description"],
		Dir:         dir,
		Body:        strings.TrimSpace(body),
	}
	if skill.Description == "" {
		// 没有说明的技能对模型没用：它无从判断什么时候该用。
		// 退而求其次，拿正文第一行顶上。
		skill.Description = firstLine(skill.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, disabledMarker)); err == nil {
		skill.Disabled = true
	}
	return skill, nil
}

// splitFrontmatter 切开开头的 YAML frontmatter。
//
// 只认 name / description 两个键，值按纯字符串取。不引 YAML 库是因为这里
// 需要的就是两个字符串，而引一个 YAML 解析器会顺带允许技能作者写进嵌套结构，
// 然后我们要去定义那些结构的含义。
func splitFrontmatter(source string) (map[string]string, string) {
	meta := map[string]string{}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return meta, normalized
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return meta, normalized
	}
	block := normalized[4 : 4+end]
	rest := normalized[4+end+4:]
	rest = strings.TrimPrefix(rest, "\n")

	for _, line := range strings.Split(block, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "name" || key == "description" {
			meta[key] = value
		}
	}
	return meta, rest
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(strings.TrimLeft(line, "# "))
	if len([]rune(line)) > 120 {
		return string([]rune(line)[:120]) + "…"
	}
	return line
}
