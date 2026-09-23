// Package secrets 给落库的凭据加密：模型服务的 Key、搜索引擎的 Key、插件里标了
// secret 的配置值。
//
// 加密防的是什么，说清楚免得高估它：**库文件本身**——被拷走、进了备份、被网盘同步、
// 被别的程序读到——里面不再是明文；配合沙箱对库文件与密钥文件的禁读，模型也读不到。
// 它防不了同一个账号下有 shell 的人：密钥文件就在旁边，权限 0600 只挡其他账号。
// 真正的机密隔离要靠系统钥匙串，而那会让开发构建与安装版各持一把钥匙、同一个库
// 互相读不了，这个项目里两者天天切换，所以不走那条路。
//
// 密钥从哪儿来：环境变量 AICLAW_MASTER_KEY（64 位十六进制）优先，没有就用库旁边的
// secret.key，没有就生成。密文形如 `enc:v1:<base64>`，没这个前缀的一律当明文——
// 老库里的值就是这样，读到时原样返回，启动时统一换成密文（见 gormstore.MigrateSecrets）。
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvKey 是主密钥的环境变量名。宿主想自己管密钥（比如从系统钥匙串取）就设它。
const EnvKey = "AICLAW_MASTER_KEY"

// KeyFile 是密钥文件名，放在应用库同一目录。
const KeyFile = "secret.key"

const prefix = "enc:v1:"

// Cipher 用一把 32 字节的主密钥做 AES-256-GCM。
type Cipher struct {
	aead cipher.AEAD
}

// Load 取主密钥：先看环境变量，再看 dir 下的密钥文件，都没有就生成一个写进去。
func Load(dir string) (*Cipher, error) {
	if raw := strings.TrimSpace(os.Getenv(EnvKey)); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s 必须是 64 位十六进制（32 字节）", EnvKey)
		}
		return New(key)
	}
	path := filepath.Join(dir, KeyFile)
	if raw, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("密钥文件 %s 内容不对（应为 64 位十六进制）", path)
		}
		return New(key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取密钥文件失败：%w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成主密钥失败：%w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建密钥目录失败：%w", err)
	}
	// 0600：只有本账号能读。先写临时文件再改名，免得写一半崩了留下半个密钥。
	temp := path + ".tmp"
	if err := os.WriteFile(temp, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("写密钥文件失败：%w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return nil, fmt.Errorf("写密钥文件失败：%w", err)
	}
	return New(key)
}

// New 用给定的 32 字节密钥建 Cipher。
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("主密钥必须是 32 字节，给了 %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Sealed 判断一个值是不是密文。
func Sealed(value string) bool { return strings.HasPrefix(value, prefix) }

// Seal 加密一个明文。空串还是空串——「没配」就该存成没配，不该存成一段密文。
// 已经是密文的原样返回，免得二次加密。
func (c *Cipher) Seal(plain string) string {
	if plain == "" || Sealed(plain) {
		return plain
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		// crypto/rand 失败只发生在系统随机源坏掉时；那时什么都不该继续。
		panic("secrets: 随机源不可用：" + err.Error())
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed)
}

// ErrUnreadable 表示密文解不开：主密钥换了，或者值被改过。
var ErrUnreadable = errors.New("凭据解不开：主密钥变了，或者库里的值被改过")

// Open 解一个存起来的值。没有密文前缀的当明文原样返回（老库里的值）。
func (c *Cipher) Open(stored string) (string, error) {
	if !Sealed(stored) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil || len(raw) < c.aead.NonceSize() {
		return "", ErrUnreadable
	}
	nonce, body := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", ErrUnreadable
	}
	return string(plain), nil
}
