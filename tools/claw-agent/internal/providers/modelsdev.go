package providers

// 从 models.dev 与 LiteLLM 两份公开的能力表同步模型能力。
//
// **为什么要有这个。** 能力标记（看图 / 听写 / 朗读 / 画图）本来要用户逐个模型
// 手动勾。一个网关列出上百个模型时这件事没人会做完，而没标的模型就不会出现在
// 角色候选里——功能配了等于没配。models.dev 是一份公开、按 provider 归档的
// 模型能力表（7000 多个模型），它的 modalities 字段正好对得上我们的四个角色；
// LiteLLM 那份补上它缺的转写 / 朗读 / 生图模型，并用显式的 mode 纠正误标
//（见 litellm.go）。两份都拉，取并集；只拉到一份也能用，结果里会说明。
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

// Entry 是那份表里关于一个模型的、我们用得上的部分。
type Entry struct {
	Roles []protocol.ModelRole
	// Context 是上下文窗口（token）。0 表示那份表没给。
	Context int
	// NonChat 表示有一份表明确说这不是聊天模型（生图、转写、朗读、向量……）。
	// 这种模型不能当看图模型用，哪怕它接受图片输入（那是为了改图）。
	NonChat bool
}

// Catalog 是按模型名索引的能力表。
type Catalog struct {
	// byName 的键是**规范化后的模型名**：小写，带前缀与不带前缀的写法都收。
	// 同名模型在不同网关下的条目取并集，见 add 的说明。
	byName map[string]Entry
	// Note 是这次拉取的说明：某份表没拉到时写在这里，让用户知道结果不完整。
	Note string
}

var (
	catalogMu    sync.Mutex
	cachedAt     time.Time
	cachedResult *Catalog
)

// FetchCatalog 取两份能力表并合并，命中缓存时不打网络。
//
// 两份并行拉：各自几 MB、都在境外，串行要等两倍时间。一份失败不算失败——
// 另一份照用，Note 里说明哪份没拉到；两份都失败才报错。
func FetchCatalog(ctx context.Context) (*Catalog, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if cachedResult != nil && time.Since(cachedAt) < catalogTTL {
		return cachedResult, nil
	}

	type fetched struct {
		catalog *Catalog
		err     error
	}
	results := make([]fetched, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		body, err := fetchFirst(ctx, "models.dev", []string{catalogURL})
		if err == nil {
			results[0].catalog, err = ParseCatalog(body)
		}
		results[0].err = err
	}()
	go func() {
		defer wg.Done()
		body, err := fetchFirst(ctx, "LiteLLM", liteLLMURLs)
		if err == nil {
			results[1].catalog, err = ParseLiteLLM(body)
		}
		results[1].err = err
	}()
	wg.Wait()

	if results[0].err != nil && results[1].err != nil {
		return nil, fmt.Errorf("两份能力表都没拉到：%v；%v", results[0].err, results[1].err)
	}
	merged := &Catalog{byName: map[string]Entry{}}
	for _, result := range results {
		if result.catalog != nil {
			merged.merge(result.catalog)
		}
	}
	switch {
	case results[0].err != nil:
		merged.Note = "models.dev 没拉到（" + results[0].err.Error() + "），只按 LiteLLM 标记了"
	case results[1].err != nil:
		merged.Note = "LiteLLM 没拉到（" + results[1].err.Error() + "），只按 models.dev 标记了"
	}
	// 只缓存两份都齐的结果：缺一份的结果缓存 6 小时，用户重试也拿不到全的。
	if merged.Note == "" {
		cachedResult, cachedAt = merged, time.Now()
	}
	return merged, nil
}

// fetchFirst 按顺序试几个地址，第一个成功的算数。
func fetchFirst(ctx context.Context, source string, urls []string) ([]byte, error) {
	var last error
	for _, url := range urls {
		body, err := fetchOne(ctx, url)
		if err == nil {
			return body, nil
		}
		last = err
	}
	return nil, fmt.Errorf("%s：%w", source, last)
}

func fetchOne(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("读取失败：%w", err)
	}
	return body, nil
}

// merge 把另一份表并进来，规则与 addOne 相同。
func (c *Catalog) merge(other *Catalog) {
	for name, entry := range other.byName {
		c.addOne(name, entry)
	}
}

// catalogProvider 是那份表里的一个服务商。只取用得上的字段。
type catalogProvider struct {
	Models map[string]struct {
		ID         string `json:"id"`
		Modalities struct {
			Input  []string `json:"input"`
			Output []string `json:"output"`
		} `json:"modalities"`
		Limit struct {
			Context int `json:"context"`
		} `json:"limit"`
	} `json:"models"`
}

// ParseCatalog 把那份表解析成索引。单独导出是为了能用样本数据测，
// 不必在测试里打网络。
func ParseCatalog(body []byte) (*Catalog, error) {
	var raw map[string]catalogProvider
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("models.dev 的数据看不懂：%w", err)
	}
	catalog := &Catalog{byName: map[string]Entry{}}
	for _, provider := range raw {
		for key, model := range provider.Models {
			entry := Entry{
				Roles:   rolesOf(model.Modalities.Input, model.Modalities.Output),
				Context: model.Limit.Context,
			}
			if len(entry.Roles) == 0 && entry.Context == 0 {
				continue
			}
			// 同一个模型在表里可能以好几种写法出现（带网关前缀、不带）。
			// 两种都索引：用户配置里填的是发给端点的那个名字，而那取决于他用的网关。
			catalog.add(key, entry)
			catalog.add(model.ID, entry)
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
func (c *Catalog) add(name string, entry Entry) {
	c.addOne(name, entry)
	if index := strings.LastIndex(name, "/"); index >= 0 {
		c.addOne(name[index+1:], entry)
	}
}

func (c *Catalog) addOne(name string, entry Entry) {
	key := normalizeModelName(name)
	if key == "" {
		return
	}
	existing := c.byName[key]
	for _, role := range entry.Roles {
		if !hasRole(existing.Roles, role) {
			existing.Roles = append(existing.Roles, role)
		}
	}
	// 窗口取**最大**的那个：同一个模型在不同网关下报的窗口有出入时，报小了
	// 会让内核提前压缩历史（白丢上下文），报大了只是退回被动压缩——后者的
	// 代价小得多。
	if entry.Context > existing.Context {
		existing.Context = entry.Context
	}
	// 有一份表说它不是聊天模型，就不能当看图模型：画图模型接受图片是为了改图。
	// 这条是「只加不减」原则的唯一例外，而且减的只是表与表之间推断出来的标记，
	// 不碰用户手动勾的（那在 AutoMark 里，这里还没到）。
	existing.NonChat = existing.NonChat || entry.NonChat
	if existing.NonChat {
		existing.Roles = withoutRole(existing.Roles, protocol.RoleVision)
	}
	c.byName[key] = existing
}

func withoutRole(list []protocol.ModelRole, drop protocol.ModelRole) []protocol.ModelRole {
	kept := make([]protocol.ModelRole, 0, len(list))
	for _, item := range list {
		if item != drop {
			kept = append(kept, item)
		}
	}
	return kept
}

// Lookup 查一个模型名。查不到时第二个返回值是 false。
//
// 索引里两种写法都有（见 add），这里再补一次去前缀的查询：用户可能在自建
// 网关下填 `我的前缀/gpt-5.4`，而那个前缀不会出现在任何表里。
func (c *Catalog) Lookup(model string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	if entry, ok := c.byName[normalizeModelName(model)]; ok {
		return entry, true
	}
	if index := strings.LastIndex(model, "/"); index >= 0 {
		if entry, ok := c.byName[normalizeModelName(model[index+1:])]; ok {
			return entry, true
		}
	}
	return Entry{}, false
}

// Roles 查一个模型名的能力。查不到返回 nil。
func (c *Catalog) Roles(model string) []protocol.ModelRole {
	entry, _ := c.Lookup(model)
	return entry.Roles
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

// AutoMark 按两份能力表给一个服务的模型清单补上能力标记。
//
// 只加不减（见文件头）。返回更新后的视图、匹配上与表里没有的模型数，以及
// 拉取说明（某份表没拉到时非空）。
func (s *Store) AutoMark(ctx context.Context, id int64) (protocol.ProviderAutoMarkResult, error) {
	item, err := s.db.GetProvider(ctx, id)
	if err != nil {
		return protocol.ProviderAutoMarkResult{}, fmt.Errorf("模型服务不存在（id=%d）：%w", id, err)
	}
	catalog, err := FetchCatalog(ctx)
	if err != nil {
		return protocol.ProviderAutoMarkResult{}, err
	}

	entries := decodeModels(item.Models)
	matched, unmatched := 0, 0
	updated := make([]string, 0, len(entries))
	for _, entry := range entries {
		parsed := protocol.ParseModelMark(entry)
		found, ok := catalog.Lookup(parsed.Name)
		if !ok {
			unmatched++
			updated = append(updated, entry)
			continue
		}
		matched++
		for _, role := range found.Roles {
			if !hasRole(parsed.Roles, role) {
				parsed.Roles = append(parsed.Roles, role)
			}
		}
		// 窗口以那份表为准：它是模型的客观属性，而用户手填的往往是抄来的
		// 或者干脆空着。手填过更大值的情况保留原值，见下。
		if found.Context > parsed.Context {
			parsed.Context = found.Context
		}
		updated = append(updated, protocol.FormatModelMark(parsed))
	}

	if err := s.db.UpdateProvider(ctx, id, model.UpdateProviderReq{Models: encodeModels(updated)}); err != nil {
		return protocol.ProviderAutoMarkResult{}, err
	}
	item.Models = encodeModels(updated)
	return protocol.ProviderAutoMarkResult{
		Provider: view(item), Matched: matched, Unmatched: unmatched, Note: catalog.Note,
	}, nil
}
