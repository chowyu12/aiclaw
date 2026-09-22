package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func path(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "memory.md")
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	// 还没记过任何东西，这是正常状态。
	text, err := Load(path(t))
	if err != nil {
		t.Fatalf("文件不存在不该报错：%v", err)
	}
	if text != "" {
		t.Errorf("应当是空串，实际 %q", text)
	}
}

func TestAppendAddsDatedEntry(t *testing.T) {
	file := path(t)
	updated, err := Append(file, "用户偏好用 Go 1.27，不要提 generics 之前的写法")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "用户偏好用 Go 1.27") {
		t.Errorf("正文没写进去：%q", updated)
	}
	// 带日期：三个月后看到一条记忆，得知道它是什么时候的结论——约定会变，
	// 而记忆本身不会告诉你它过期了。
	if !strings.Contains(updated, "- (20") {
		t.Errorf("应当带上日期：%q", updated)
	}
	onDisk, _ := os.ReadFile(file)
	if string(onDisk) != updated {
		t.Error("返回的内容应当与落盘的一致")
	}
}

func TestAppendKeepsEarlierEntries(t *testing.T) {
	file := path(t)
	if _, err := Append(file, "第一条"); err != nil {
		t.Fatal(err)
	}
	updated, err := Append(file, "第二条")
	if err != nil {
		t.Fatal(err)
	}
	// 追加而不是覆盖：模型不该有能力一次抹掉全部记忆。
	if !strings.Contains(updated, "第一条") || !strings.Contains(updated, "第二条") {
		t.Errorf("两条都该在：%q", updated)
	}
}

func TestAppendSkipsDuplicates(t *testing.T) {
	file := path(t)
	if _, err := Append(file, "工作目录是 ~/Workspace"); err != nil {
		t.Fatal(err)
	}
	updated, err := Append(file, "工作目录是 ~/Workspace")
	if err != nil {
		t.Fatal(err)
	}
	// 模型经常在一轮里重复确认同一件事；不去重的话记忆很快被同义句撑满。
	if strings.Count(updated, "Workspace") != 1 {
		t.Errorf("重复的不该再写一遍：%q", updated)
	}
}

func TestAppendRejectsEmpty(t *testing.T) {
	if _, err := Append(path(t), "   \n  "); err == nil {
		t.Fatal("空内容应当被拒绝")
	}
}

func TestAppendRejectsOversizedEntry(t *testing.T) {
	// 一条几 KB 的多半是把工具输出整个贴进来了。记忆放的是结论。
	if _, err := Append(path(t), strings.Repeat("x", maxEntryBytes+1)); err == nil {
		t.Fatal("超长的单条应当被拒绝")
	}
}

func TestAppendRefusesWhenFileIsFull(t *testing.T) {
	file := path(t)
	if err := os.WriteFile(file, []byte(strings.Repeat("x", MaxBytes-10)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Append(file, "再记一条应该放不下了")
	if err == nil {
		t.Fatal("接近上限时应当拒绝而不是写进去")
	}
	// 报错要指出文件在哪儿：用户得知道去改哪个文件。
	if !strings.Contains(err.Error(), file) {
		t.Errorf("报错应当带上文件路径：%v", err)
	}
}

func TestLoadTruncatesOversizedFileAndSaysSo(t *testing.T) {
	file := path(t)
	if err := os.WriteFile(file, []byte(strings.Repeat("y", MaxBytes+500)), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := Load(file)
	if err == nil {
		t.Fatal("超限时应当返回错误说明，不能静默截断")
	}
	// 但仍然把前面那截给出来：整个放弃比读一部分更糟。
	if len(text) != MaxBytes {
		t.Errorf("应当读回上限那么多，实际 %d", len(text))
	}
}

func TestAppendCreatesDirectory(t *testing.T) {
	// 记忆文件所在目录可能还不存在（全新安装）。
	nested := filepath.Join(t.TempDir(), "a", "b", "memory.md")
	if _, err := Append(nested, "第一条"); err != nil {
		t.Fatalf("应当自动建目录：%v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("文件没建出来：%v", err)
	}
}
