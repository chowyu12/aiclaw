// Package memory 是跨会话的长期记忆。
//
// 与会话上下文是两件事，别混：
//   - 会话上下文是这一轮对话的历史，撑满了会被压缩（见 agent 包的 compact）；
//   - 长期记忆是**用户希望跨会话记住的少量事实**，每次开会话原样进系统提示词。
//
// 所以记忆必须小。它不是日志也不是知识库——那两样各有各的地方（会话存档、
// 知识库）。这里只放「下次也该知道」的几句话：我是谁、这个项目的约定、
// 用过什么踩过什么。超过上限就拒绝写入并说清楚，而不是悄悄截断——
// 截断会把一条记忆砍成半句，比没有更糟。
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxBytes 是记忆文件的上限。
	//
	// 32KB 大约是几百条短句。再多就该考虑那些内容是不是本来属于技能或知识库了——
	// 每次开会话都把它整个塞进提示词，代价是每一轮都在为它付 token。
	MaxBytes = 32 * 1024
	// maxEntryBytes 单条上限。一条写进来几 KB 的多半是把工具输出贴进来了。
	maxEntryBytes = 2000
)

// Load 读记忆。文件不存在返回空串，这不是错误。
func Load(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取长期记忆失败：%w", err)
	}
	if len(raw) > MaxBytes {
		// 超限的文件多半是被手工塞了别的东西。读前面那截总比整个放弃强，
		// 但要让调用方知道。
		return string(raw[:MaxBytes]), fmt.Errorf("长期记忆超过 %d 字节，只读取了前面部分", MaxBytes)
	}
	return string(raw), nil
}

// Append 追加一条记忆，返回追加后的全文。
//
// 追加而不是覆盖：模型每次只该说「再记住这一条」，不该有能力一次抹掉全部。
// 要删就让用户自己去编辑那个文件——那是他的东西。
func Append(path, entry string) (string, error) {
	text := strings.TrimSpace(entry)
	if text == "" {
		return "", fmt.Errorf("记忆内容为空")
	}
	if len(text) > maxEntryBytes {
		return "", fmt.Errorf("单条记忆超过 %d 字节；长期记忆放的是结论，不是原始内容", maxEntryBytes)
	}

	existing, _ := Load(path)
	if strings.Contains(existing, text) {
		// 模型经常在一轮里重复确认同一件事。重复写入会让记忆很快被同义句撑满。
		return existing, nil
	}
	if len(existing)+len(text) > MaxBytes {
		return "", fmt.Errorf(
			"长期记忆已接近 %d 字节上限，这条没有写入；请先整理 %s", MaxBytes, path,
		)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("创建记忆目录失败：%w", err)
	}

	var builder strings.Builder
	builder.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		builder.WriteString("\n")
	}
	// 带上日期：三个月后看到一条记忆，知道它是什么时候的结论很重要——
	// 约定会变，而记忆本身不会告诉你它过期了。
	fmt.Fprintf(&builder, "- (%s) %s\n", time.Now().Format("2006-01-02"), text)

	updated := builder.String()
	// 先写临时文件再改名：进程在写一半时被杀不会留下半条记忆。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), 0o600); err != nil {
		return "", fmt.Errorf("写入长期记忆失败：%w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("写入长期记忆失败：%w", err)
	}
	return updated, nil
}
