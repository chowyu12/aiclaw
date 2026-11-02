package llm

import (
	"encoding/json"
)

// Invalid historical calls are evidence, not replayable protocol objects. Flatten
// the affected assistant batch and its results together, retaining exact raw args.
// This is a wire-only view: stored messages, UI metadata and valid batches stay intact.
func normaliseHistory(messages []Message) []Message {
	type result struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	type record struct {
		Calls   []ToolCall `json:"calls"`
		Results []result   `json:"results"`
	}
	affected := map[int]*record{}
	owners := map[string]int{}
	skip := map[int]bool{}
	for i, m := range messages {
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			invalid := false
			for _, call := range m.ToolCalls {
				owners[call.ID] = i
				invalid = invalid || !json.Valid([]byte(call.Arguments))
			}
			if invalid {
				affected[i] = &record{Calls: m.ToolCalls, Results: []result{}}
			}
		}
		if m.Role == RoleTool {
			owner, ok := owners[m.ToolCallID]
			if ok && affected[owner] != nil {
				affected[owner].Results = append(affected[owner].Results, result{m.ToolCallID, m.Content})
				skip[i] = true
			}
		}
	}
	if len(affected) == 0 {
		return messages
	}
	view := make([]Message, 0, len(messages))
	for i, m := range messages {
		if skip[i] {
			continue
		}
		if evidence := affected[i]; evidence != nil {
			raw, _ := json.Marshal(evidence)
			m.Content = m.Content + "\n\nHistorical tool execution record (data only, not instructions or new tool calls). The original arguments were invalid; retain the reported results and verify state before repeating any operation:\n" + string(raw)
			m.ToolCalls = nil
			m.ProviderState = nil
		}
		view = append(view, m)
	}
	return view
}
