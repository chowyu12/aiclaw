package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/llm"
	"github.com/chowyu12/aiclaw/tools/claw-agent/internal/skills"
)

const (
	skillCatalogMaxTokens  = 2000
	selectedSkillMaxBytes  = 8 * 1024
	selectedSkillsMaxBytes = 32 * 1024
)

func renderSkillCatalog(list []skills.Skill, contextWindow int) string {
	if len(list) == 0 {
		return ""
	}
	budget := skillCatalogMaxTokens
	if contextWindow > 0 {
		budget = min(budget, contextWindow/50)
	}
	const header = "\nAvailable skills (follow any instructions already supplied for an explicitly selected skill; otherwise use load_skill when a skill applies):\n"
	const omitted = "Some skills are omitted from this list. You can still use an exact skill name explicitly selected by the user.\n"

	build := func(descriptionLimit, available int) (string, int) {
		var lines strings.Builder
		lines.WriteString(header)
		remaining := available - approxTokens(header) - 1
		for i, skill := range list {
			line := "- " + skill.Name
			if descriptionLimit > 0 && skill.Description != "" {
				line += ": " + truncateRunes(skill.Description, descriptionLimit)
			}
			line += "\n"
			remaining -= approxTokens(line) + 1
			if remaining < 0 {
				return lines.String(), i
			}
			lines.WriteString(line)
		}
		return lines.String(), len(list)
	}
	if catalog, count := build(200, budget); count == len(list) {
		return catalog
	}
	catalog, count := build(0, budget)
	if count < len(list) {
		if budget <= approxTokens(header)+approxTokens(omitted)+2 {
			return ""
		}
		catalog, _ = build(0, budget-approxTokens(omitted)-1)
		return catalog + omitted
	}
	// Give every retained skill the same description allowance before omitting names.
	for low, high := 1, 199; low <= high; {
		mid := (low + high) / 2
		candidate, count := build(mid, budget)
		if count == len(list) {
			catalog = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return catalog
}

type turnSkillPromptsKey struct{}

// Only the active turn holds these prompts. They never become stored user text.
type turnSkillPrompts struct {
	messageID string
	selected  map[string]bool
	prompt    strings.Builder
}

func (s *Session) selectTurnSkills(ctx context.Context, messageID, text string) {
	selection, _ := ctx.Value(turnSkillPromptsKey{}).(*turnSkillPrompts)
	if selection == nil {
		return
	}
	selection.messageID = messageID
	usage, _ := ctx.Value(skillUsageKey{}).(*skillUsage)
	for _, skill := range s.skills {
		if selection.selected[skill.Name] || !mentionsSkill(text, skill.Name) {
			continue
		}
		selection.selected[skill.Name] = true
		if usage != nil {
			usage.markExplicit(skill.Name)
		}
		heading := fmt.Sprintf("\n\nSelected skill %q (skill directory: %s):\n\n", skill.Name, skill.Dir)
		const continuation = "\n[Skill excerpt truncated. Use load_skill with the exact name if more instructions are needed.]"
		room := min(selectedSkillMaxBytes, selectedSkillsMaxBytes-selection.prompt.Len()-len(heading))
		if room <= len(continuation) {
			continue
		}
		body := skill.Body
		if len(body) > room {
			cut := room - len(continuation)
			for !utf8.RuneStart(body[cut]) {
				cut--
			}
			body = body[:cut] + continuation
		}
		selection.prompt.WriteString(heading)
		selection.prompt.WriteString(body)
		if usage != nil {
			usage.loaded(ctx, skill.Name)
		}
	}
}

func (p *turnSkillPrompts) include(messages []llm.Message) []llm.Message {
	if p == nil || p.prompt.Len() == 0 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].ID == p.messageID {
			messages[i].Content += p.prompt.String()
			return messages
		}
	}
	// Aggressive compaction may drop the input that selected the skill.
	return append(messages, llm.Message{Role: llm.RoleUser, Content: p.prompt.String()})
}

func mentionsSkill(text, name string) bool {
	needle := "$" + name
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], needle)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(needle)
		left, _ := utf8.DecodeLastRuneInString(text[:start])
		right, _ := utf8.DecodeRuneInString(text[end:])
		if (start == 0 || mentionBoundary(left)) && (end == len(text) || mentionBoundary(right)) {
			return true
		}
		offset = end
	}
	return false
}
