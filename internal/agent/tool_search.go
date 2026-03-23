package agent

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	openai "github.com/sashabaranov/go-openai"
)

const (
	toolSearchName       = "tool_search"
	toolSearchMaxResults = 5

	// ToolSearchAutoFullThreshold 当「LLM 可见」的工具定义条数（含 MCP / 技能声明工具）不超过该值时，
	// 即使开启 Tool Search，也自动下发全量工具定义，避免少量工具时反复 tool_search 浪费轮次。
	ToolSearchAutoFullThreshold = 24
)

// UseLazyToolSearch 是否在 Agent 执行时进入「tool_search + 渐进发现」模式。
func UseLazyToolSearch(agentSearchEnabled bool, toolDefCount int) bool {
	return agentSearchEnabled && toolDefCount > ToolSearchAutoFullThreshold
}

type toolSearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func toolSearchDefWithContext(totalTools int, discoveredCount int, discoveredNames []string) openai.Tool {
	desc := "搜索可用工具。搜索一次即可，匹配的工具会自动加入可用列表，之后直接调用即可，无需重复搜索。"
	if totalTools > 0 {
		desc += fmt.Sprintf(" 当前共 %d 个工具", totalTools)
		if discoveredCount > 0 {
			desc += fmt.Sprintf("，你已发现 %d 个", discoveredCount)
		}
		desc += "。"
	}
	if len(discoveredNames) > 0 {
		desc += " 已可用: " + strings.Join(discoveredNames, ", ") + "。如需其他工具再搜索。"
	}

	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        toolSearchName,
			Description: desc,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "搜索关键词（英文优先，如 read、exec、browser），也支持中文描述。一次搜索即可，无需反复搜索同一类工具。",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hiragana, r)
}

// splitCJKBoundary splits s into segments at transitions between CJK and
// non-CJK characters. For example "get天气" → ["get", "天气"].
func splitCJKBoundary(s string) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	var segments []string
	start := 0
	prevCJK := isCJK(runes[0])
	for i := 1; i < len(runes); i++ {
		curCJK := isCJK(runes[i])
		if curCJK != prevCJK {
			segments = append(segments, string(runes[start:i]))
			start = i
			prevCJK = curCJK
		}
	}
	segments = append(segments, string(runes[start:]))
	return segments
}

// extractKeywords produces search tokens from a query string.
// It splits on whitespace and CJK/non-CJK boundaries, and generates CJK
// bigrams so that "执行命令" can match descriptions like "执行Shell命令".
func extractKeywords(query string) []string {
	query = strings.ToLower(query)
	seen := make(map[string]bool)
	var result []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}

	for _, field := range strings.Fields(query) {
		add(field)

		segments := splitCJKBoundary(field)
		if len(segments) > 1 {
			for _, seg := range segments {
				add(seg)
			}
		}

		for _, seg := range segments {
			runes := []rune(seg)
			if len(runes) > 2 && isCJK(runes[0]) {
				for i := range len(runes) - 1 {
					add(string(runes[i : i+2]))
				}
			}
		}
	}
	return result
}

// searchTools performs keyword-based scoring on all tool definitions.
// Name matches are weighted higher than description matches.
// CJK text is handled via bigram tokenization so that Chinese queries
// can match descriptions containing the same characters.
func searchTools(query string, allDefs []openai.Tool) []toolSearchResult {
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return nil
	}

	type scored struct {
		name  string
		desc  string
		score int
	}

	var hits []scored
	for _, def := range allDefs {
		if def.Function == nil {
			continue
		}
		name := strings.ToLower(def.Function.Name)
		desc := strings.ToLower(def.Function.Description)

		score := 0
		for _, kw := range keywords {
			if strings.EqualFold(def.Function.Name, kw) {
				score += 5
			} else if strings.Contains(name, kw) {
				score += 3
			}
			if strings.Contains(desc, kw) {
				score += 1
			}
		}

		if score > 0 {
			hits = append(hits, scored{name: def.Function.Name, desc: def.Function.Description, score: score})
		}
	}

	slices.SortFunc(hits, func(a, b scored) int {
		return cmp.Compare(b.score, a.score)
	})

	limit := min(toolSearchMaxResults, len(hits))
	out := make([]toolSearchResult, limit)
	for i := range limit {
		out[i] = toolSearchResult{Name: hits[i].name, Description: hits[i].desc}
	}
	return out
}

func formatToolSearchResults(results []toolSearchResult, totalTools int, newCount int, totalDiscovered int) string {
	if len(results) == 0 {
		return fmt.Sprintf("未找到匹配的工具（共 %d 个工具）。尝试更宽泛的关键词，或直接使用已发现的 %d 个工具。", totalTools, totalDiscovered)
	}
	msg := fmt.Sprintf("找到 %d 个工具", len(results))
	if newCount > 0 {
		msg += fmt.Sprintf("（%d 个新发现）", newCount)
	} else {
		msg += "（均已在可用列表中）"
	}
	msg += fmt.Sprintf("，已发现 %d/%d 个工具。直接调用即可，无需再次搜索。", totalDiscovered, totalTools)

	resp := struct {
		FoundTools []toolSearchResult `json:"found_tools"`
		Message    string             `json:"message"`
	}{
		FoundTools: results,
		Message:    msg,
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

// preloadSkillTools pre-populates the discovered set with tools that are
// explicitly associated with skills, so skill-dependent tools are always
// available without requiring a search round-trip.
func preloadSkillTools(toolSkillMap map[string]string, discovered map[string]bool) {
	for toolName := range toolSkillMap {
		discovered[toolName] = true
	}
}

// buildToolSearchDefs creates the LLM-visible tool list for tool search mode:
// always includes tool_search itself (with dynamic context), plus any previously discovered tools.
func buildToolSearchDefs(allDefs []openai.Tool, discovered map[string]bool) []openai.Tool {
	var discoveredNames []string
	for name := range discovered {
		discoveredNames = append(discoveredNames, name)
	}
	slices.Sort(discoveredNames)

	result := make([]openai.Tool, 0, 1+len(discovered))
	result = append(result, toolSearchDefWithContext(len(allDefs), len(discovered), discoveredNames))
	for _, def := range allDefs {
		if def.Function != nil && discovered[def.Function.Name] {
			result = append(result, def)
		}
	}
	return result
}
