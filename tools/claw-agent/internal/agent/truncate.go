package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxToolOutputBytes 是单条工具结果进入历史时的上限。
//
// 注意这只截「给模型看的历史」，不截给用户看的条目——宿主那边展示的是
// 工具自己返回的完整内容。Codex 也是这么切的：截断发生在写入历史的那一刻，
// 界面上的记录保持原样。
//
// 32KB 的取法：源码文件读一次大多在几 KB 到十几 KB，这个额度放得下几次
// 真实的读文件而不至于几轮就把窗口吃光。read_file 自己的 256KB 上限是防
// 一次读进一个巨大文件，和这里的目的不同，两道都要有。
const maxToolOutputBytes = 32 * 1024

// truncateForHistory 把过长的工具输出截成中间省略的形式。
//
// 掐头去尾都不行：命令的输出头部是它在做什么，尾部是结果和报错，
// 两头都比中间有用。Codex 的 truncate_middle 是同一个判断。
func truncateForHistory(text string) string {
	if len(text) <= maxToolOutputBytes {
		return text
	}
	// 头尾各留一半，中间空出说明文字的位置。
	half := maxToolOutputBytes / 2
	head := trimToCharBoundary(text[:half])
	tail := trimFromCharBoundary(text[len(text)-half:])
	omitted := len(text) - len(head) - len(tail)
	lines := strings.Count(text, "\n") + 1
	return fmt.Sprintf(
		"%s\n\n…（输出过长已截断中间部分：共 %d 字节 / %d 行，省略 %d 字节）…\n\n%s",
		head, len(text), lines, omitted, tail,
	)
}

// trimToCharBoundary 把尾部半个 UTF-8 字符去掉。
//
// 按字节切中文输出必然会切在字符中间，留下的半个字符会让部分上游
// 在 JSON 编码时报错，或者被替换成 U+FFFD 之后污染模型看到的内容。
func trimToCharBoundary(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// trimFromCharBoundary 把头部半个 UTF-8 字符去掉。
func trimFromCharBoundary(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

// approxTokens 粗估一段文本的 token 数。
//
// 只用于「要不要压缩」和「摘要里还能放几条用户消息」这类阈值判断，
// 不需要准——为此引一个分词器（每个模型还不一样）不划算。
// 估法：ASCII 按 4 字节 1 token，非 ASCII（主要是中日韩）按 1 字符 1 token，
// 这两个比例在常见分词器上都偏保守，宁可提前压缩也不要估少了撑爆窗口。
func approxTokens(text string) int {
	ascii := 0
	wide := 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			wide++
		}
	}
	return ascii/4 + wide
}
