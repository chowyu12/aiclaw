package agent

// BudgetTracker 追踪单次 Agent 执行的 token 消耗，超出预算时可提前终止。
// limit <= 0 表示不限制。
type BudgetTracker struct {
	limit    int
	consumed int
}

func NewBudgetTracker(limit int) *BudgetTracker {
	return &BudgetTracker{limit: limit}
}

func (b *BudgetTracker) Add(tokens int)  { b.consumed += tokens }
func (b *BudgetTracker) Consumed() int   { return b.consumed }

func (b *BudgetTracker) Remaining() int {
	if b.limit <= 0 {
		return -1
	}
	return max(b.limit-b.consumed, 0)
}

func (b *BudgetTracker) Exceeded() bool {
	return b.limit > 0 && b.consumed >= b.limit
}
