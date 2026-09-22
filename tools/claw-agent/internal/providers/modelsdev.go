package providers

// 从 models.dev 同步模型能力。
//
// **为什么要有这个。** 能力标记（看图 / 听写 / 朗读 / 画图）本来要用户逐个模型
// 手动勾。一个网关列出上百个模型时这件事没人会做完，而没标的模型就不会出现在
// 角色候选里——功能配了等于没配。models.dev 是一份公开、按 provider 归档的
// 模型能力表（7000 多个模型），它的 modalities 字段正好对得上我们的四个角色。
//
// **它是外部数据，所以只做加法。** 同步只会给模型加上标记，不会去掉用户手动
// 勾过的：那份表未必覆盖用户自建的端点，也未必跟得上新模型；而「我明明勾了，
// 同步一下就没了」比「多了一个用不了的候选」难受得多。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/protocol"
)

// catalogURL 是那份表的地址。没有 API Key，公开可读。
const catalogURL = "https://models.dev/api.json"

// catalogTTL 是缓存时长。一天之内同一份表不会变第二次，而用户可能连着给
// 几个服务点同步——每次拉几 MB 没有道理。
const catalogTTL = 6 * time.Hour

// maxCatalogBytes 兜住响应体。那份表目前 6MB 上下，留足余量但不让一个
// 出问题的响应把内存吃光。
const maxCatalogBytes = 64 << 20

// Catalog 是按模型名索引的能力表。
type Catalog struct {
	// byName 的键是**规范化后的模型名**：小写、去掉 provider 前缀。
	// 同名模型在不同网关下的能力取并集，见 add 的说明。
	byName map[string][]protocol.ModelRole
}

var (
	catalogMu    sync.Mutex
	cachedAt     time.Time
	cachedResult *Catalog
)

// FetchCatalog 取那份能力表，命中缓存时不打网络。
func FetchCatalog(ctx context.Context) (*Catalog, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if cachedResult != nil && time.Since(cachedAt) < catalogTTL {
		return cachedResult, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接 models.dev 失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev 返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 models.dev 失败：%w", err)
	}

	catalog, err := ParseCatalog(body)
	if err != nil {
		return nil, err
	}
	cachedResult, cachedAt = catalog, time.Now()
	return catalog, nil
}

// catalogProvider 是那份表里的一个服务商。只取用得上的字段。
type catalogProvider struct {
	Models map[string]struct {
		ID         string `json:"id"`
		Modalities struct {
			Input  []string `json:"input"`
			Output []string `json:"output"`
		} `json:"modalities"`
	} `json:"models"`
}

// ParseCatalog 把那份表解析成索引。单独导出是为了能用样本数据测，
// 不必在测试里打网络。
func ParseCatalog(body []byte) (*Catalog, error) {
	var raw map[string]catalogProvider
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("models.dev 的数据看不懂：%w", err)
	}
	catalog := &Catalog{byName: map[string][]protocol.ModelRole{}}
	for _, provider := range raw {
		for key, model := range provider.Models {
			roles := rolesOf(model.Modalities.Input, model.Modalities.Output)
			if len(roles) == 0 {
				continue
			}
			// 同一个模型在表里可能以好几种写法出现（带网关前缀、不带）。
			// 两种都索引：用户配置里填的是发给端点的那个名字，而那取决于他用的网关。
			catalog.add(key, roles)
			catalog.add(model.ID, roles)
		}
	}
	return catalog, nil
}

// rolesOf 把 modalities 翻成我们的四个角色。
//
// 视频不映射：内核没有处理视频的路径，标了也只会让一个用不了的模型出现在
// 候选里。pdf 同理——那是文档解析，与这四件事不是一回事。
func rolesOf(input, output []string) []protocol.ModelRole {
	var roles []protocol.ModelRole
	if contains(input, "image") {
		roles = append(roles, protocol.RoleVision)
	}
	if contains(input, "audio") {
		roles = append(roles, protocol.RoleSTT)
	}
	if contains(output, "audio") {
		roles = append(roles, protocol.RoleTTS)
	}
	if contains(output, "image") {
		roles = append(roles, protocol.RoleImage)
	}
	return roles
}

// add 记一个名字的能力，带前缀与不带前缀的写法都记。
//
// 两种都记是因为用户配置里填的是**发给端点的那个名字**，而那取决于他用的
// 网关：OpenRouter 要 `google/gemini-3-pro`，Gemini 官方端点要 `gemini-3-pro`，
// 而那份表里同一个模型未必两种写法都收了。
//
// 同名取**并集**而不是覆盖或交集：同一个模型在二十个网关下的条目基本一致，
// 偶有出入时，漏标会让用户找不到那个模型（功能像是坏的），而多标顶多是
// 选中之后失败一次——后者的反馈清楚得多。
func (c *Catalog) add(name string, roles []protocol.ModelRole) {
	c.addOne(name, roles)
	if index := strings.LastIndex(name, "/"); index >= 0 {
		c.addOne(name[index+1:], roles)
	}
}

func (c *Catalog) addOne(name string, roles []protocol.ModelRole) {
	key := normalizeModelName(name)
	if key == "" {
		return
	}
	existing := c.byName[key]
	for _, role := range roles {
		if !hasRole(existing, role) {
			existing = append(existing, role)
		}
	}
	c.byName[key] = existing
}

// Roles 查一个模型名的能力。查不到返回 nil。
//
// 索引里两种写法都有（见 add），这里再补一次去前缀的查询：用户可能在自建
// 网关下填 `我的前缀/gpt-5.4`，而那个前缀不会出现在任何表里。
func (c *Catalog) Roles(model string) []protocol.ModelRole {
	if c == nil {
		return nil
	}
	if roles, ok := c.byName[normalizeModelName(model)]; ok {
		return roles
	}
	if index := strings.LastIndex(model, "/"); index >= 0 {
		if roles, ok := c.byName[normalizeModelName(model[index+1:])]; ok {
			return roles
		}
	}
	return nil
}

// Size 是索引里有多少个名字。冒烟与诊断用。
func (c *Catalog) Size() int {
	if c == nil {
		return 0
	}
	return len(c.byName)
}

// normalizeModelName 把模型名规范成索引键。
//
// 只做小写与去空格，**不去版本后缀**：`gpt-5.4` 与 `gpt-5.4-mini` 是两个
// 能力不同的模型，按前缀匹配会把前者的能力安到后者头上。
func normalizeModelName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

func hasRole(list []protocol.ModelRole, want protocol.ModelRole) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// AutoMark 按 models.dev 给一个服务的模型清单补上能力标记。
//
// 只加不减（见文件头）。返回更新后的视图、匹配上的模型数、以及表里没有的数。
func (s *Store) AutoMark(ctx context.Context, id int64) (protocol.ProviderView, int, int, error) {
	item, err := s.db.GetProvider(ctx, id)
	if err != nil {
		return protocol.ProviderView{}, 0, 0, fmt.Errorf("模型服务不存在（id=%d）：%w", id, err)
	}
	catalog, err := FetchCatalog(ctx)
	if err != nil {
		return protocol.ProviderView{}, 0, 0, err
	}

	entries := decodeModels(item.Models)
	matched, unmatched := 0, 0
	updated := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, roles := protocol.ParseModelMark(entry)
		found := catalog.Roles(name)
		if len(found) == 0 {
			unmatched++
			updated = append(updated, entry)
			continue
		}
		matched++
		for _, role := range found {
			if !hasRole(roles, role) {
				roles = append(roles, role)
			}
		}
		updated = append(updated, protocol.FormatModelMark(name, roles))
	}

	if err := s.db.UpdateProvider(ctx, id, model.UpdateProviderReq{Models: encodeModels(updated)}); err != nil {
		return protocol.ProviderView{}, 0, 0, err
	}
	item.Models = encodeModels(updated)
	return view(item), matched, unmatched, nil
}
