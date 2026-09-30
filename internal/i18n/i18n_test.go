package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	if got := T(Chinese, "已中断"); got != "已中断" {
		t.Errorf("中文原样：%q", got)
	}
	if got := T(English, "已中断"); got != "Interrupted" {
		t.Errorf("英文查词典：%q", got)
	}
	if got := T(English, "没有这条"); got != "没有这条" {
		t.Errorf("漏翻的回退中文：%q", got)
	}
	if got := T(Chinese, "删除「{name}」？{n} 个", "name", "周报", "n", 3); got != "删除「周报」？3 个" {
		t.Errorf("参数：%q", got)
	}
	SetDefault(English)
	defer SetDefault(Chinese)
	if got := D("已中断"); got != "Interrupted" {
		t.Errorf("默认语言：%q", got)
	}
}

// 防漏：内核源码里每一处 i18n.T(…, "…") / i18n.D("…") 的中文键都要有英文。
func TestEveryKeyHasEnglish(t *testing.T) {
	// 仓库根：内核（tools/claw-agent）与根下的 internal 都扫。
	root := filepath.Join("..", "..")
	pattern := regexp.MustCompile(`i18n\.(?:T\([^,"]+,\s*|[DE]\()\s*"((?:[^"\\]|\\.)*)"`)
	var missing []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".git" || info.Name() == "dist") {
			return filepath.SkipDir
		}
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		// 注释里的示例不算（本包的说明里就写着 i18n.T(locale, "…")）。
		var code []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				code = append(code, line)
			}
		}
		for _, match := range pattern.FindAllStringSubmatch(strings.Join(code, "\n"), -1) {
			key := strings.ReplaceAll(match[1], `\"`, `"`)
			if regexp.MustCompile(`[\p{Han}]`).MatchString(key) {
				if _, ok := en[key]; !ok {
					missing = append(missing, path+": "+key)
				}
			}
		}
		return nil
	})
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("缺英文词条：\n%s", strings.Join(missing, "\n"))
	}
}

// 英文与原文的占位符一致：少一个就会把 {name} 原样露在界面上。
func TestPlaceholdersMatch(t *testing.T) {
	holes := func(text string) string {
		found := regexp.MustCompile(`\{(\w+)\}`).FindAllString(text, -1)
		sort.Strings(found)
		return strings.Join(found, ",")
	}
	for source, target := range en {
		if holes(source) != holes(target) {
			t.Errorf("占位符对不上：%q → %q", source, target)
		}
	}
}
