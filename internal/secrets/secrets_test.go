package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSealAndOpenRoundTrip(t *testing.T) {
	c, err := New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed := c.Seal("sk-secret-123")
	if !Sealed(sealed) || strings.Contains(sealed, "sk-secret") {
		t.Fatalf("密文不该含明文：%s", sealed)
	}
	if c.Seal(sealed) != sealed {
		t.Error("密文再 Seal 一次应原样返回")
	}
	plain, err := c.Open(sealed)
	if err != nil || plain != "sk-secret-123" {
		t.Fatalf("解出 %q, %v", plain, err)
	}
	// 每次 nonce 不同，同一明文两次密文不一样——不然能从密文相等推出 Key 相等。
	if c.Seal("sk-secret-123") == sealed {
		t.Error("两次加密不该得到同一密文")
	}
}

func TestEmptyStaysEmptyAndPlaintextPassesThrough(t *testing.T) {
	c, _ := New(make([]byte, 32))
	if c.Seal("") != "" {
		t.Error("空串不该变成密文")
	}
	plain, err := c.Open("sk-legacy-plaintext")
	if err != nil || plain != "sk-legacy-plaintext" {
		t.Errorf("老库里的明文应原样返回：%q %v", plain, err)
	}
}

func TestWrongKeyOrTamperIsUnreadable(t *testing.T) {
	a, _ := New(make([]byte, 32))
	other := make([]byte, 32)
	other[0] = 1
	b, _ := New(other)
	sealed := a.Seal("sk-x")
	if _, err := b.Open(sealed); !errors.Is(err, ErrUnreadable) {
		t.Errorf("换了密钥应解不开，得到 %v", err)
	}
	tampered := sealed[:len(sealed)-2] + "AA"
	if _, err := a.Open(tampered); !errors.Is(err, ErrUnreadable) {
		t.Errorf("改过的密文应解不开，得到 %v", err)
	}
	if _, err := a.Open("enc:v1:not-base64!!"); !errors.Is(err, ErrUnreadable) {
		t.Errorf("坏掉的密文应解不开，得到 %v", err)
	}
}

func TestLoadCreatesKeyFileOnceAndReusesIt(t *testing.T) {
	t.Setenv(EnvKey, "")
	dir := t.TempDir()
	first, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("密钥文件权限应为 0600，实际 %o", info.Mode().Perm())
	}
	second, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := second.Open(first.Seal("sk-a"))
	if err != nil || plain != "sk-a" {
		t.Errorf("第二次 Load 应拿到同一把密钥：%q %v", plain, err)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvKey, strings.Repeat("ab", 32))
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("环境变量给了密钥时不该写文件")
	}
	direct, _ := New([]byte(strings.Repeat("\xab", 32)))
	if plain, err := direct.Open(c.Seal("sk-b")); err != nil || plain != "sk-b" {
		t.Errorf("环境变量里的密钥没被用上：%q %v", plain, err)
	}
	t.Setenv(EnvKey, "short")
	if _, err := Load(dir); err == nil {
		t.Error("格式不对的环境变量应报错，而不是静默换一把密钥")
	}
}
