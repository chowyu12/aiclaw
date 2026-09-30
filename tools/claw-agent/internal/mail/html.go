package mail

import (
	"html"
	"regexp"
	"strings"
)

var (
	htmlDropped   = regexp.MustCompile(`(?is)<(script|style|head|title)\b[^>]*>.*?</(script|style|head|title)>`)
	htmlComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBreak     = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlBlockEnd  = regexp.MustCompile(`(?i)</(p|div|tr|li|h[1-6]|table|blockquote|section|article)>`)
	htmlCell      = regexp.MustCompile(`(?i)</t[dh]>`)
	htmlListItem  = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	htmlLink      = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	htmlTag       = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRun      = regexp.MustCompile(`[ \t\x{00a0}\x{200b}]+`)
	blankLineRun  = regexp.MustCompile(`\n\s*\n\s*\n+`)
	linkTextTrail = regexp.MustCompile(`\s+`)
)

// HTMLToText 把 HTML 信件转成能读的纯文本：去掉样式脚本、保留段落与链接地址。
//
// 不追求排版还原：模型要的是「这封信说了什么、有哪些链接」，表格对不齐无所谓。
func HTMLToText(source string) string {
	text := htmlDropped.ReplaceAllString(source, "")
	text = htmlComment.ReplaceAllString(text, "")
	text = htmlLink.ReplaceAllStringFunc(text, func(match string) string {
		parts := htmlLink.FindStringSubmatch(match)
		href := strings.TrimSpace(parts[1])
		label := strings.TrimSpace(linkTextTrail.ReplaceAllString(htmlTag.ReplaceAllString(parts[2], ""), " "))
		if href == "" || strings.HasPrefix(strings.ToLower(href), "mailto:") || strings.HasPrefix(href, "#") {
			return label
		}
		if label == "" || html.UnescapeString(label) == href {
			return href
		}
		return label + " (" + href + ")"
	})
	text = htmlBreak.ReplaceAllString(text, "\n")
	text = htmlListItem.ReplaceAllString(text, "\n• ")
	text = htmlBlockEnd.ReplaceAllString(text, "\n")
	text = htmlCell.ReplaceAllString(text, "\t")
	text = htmlTag.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(spaceRun.ReplaceAllString(line, " "))
	}
	text = strings.Join(lines, "\n")
	text = blankLineRun.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
