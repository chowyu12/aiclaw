package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
	harnesspkg "github.com/chowyu12/aiclaw/pkg/harness"
)

func TestPlanManagerSetNormalizesSingleRunning(t *testing.T) {
	ctx := context.Background()
	st := newMockStore()
	pm := NewPlanManager(st, 1)

	out, err := pm.HandleTool(ctx, `{"action":"set","goal":"ship change","items":[{"id":"a","title":"first","status":"running"},{"id":"b","title":"second","status":"running"},{"id":"c","title":"third"}]}`)
	if err != nil {
		t.Fatalf("HandleTool returned error: %v", err)
	}
	var res planToolResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error %q", res.Error)
	}
	if res.Plan == nil || len(res.Plan.Items) != 3 {
		t.Fatalf("expected 3 plan items, got %#v", res.Plan)
	}
	running := 0
	for _, item := range res.Plan.Items {
		if item.Status == model.PlanItemRunning {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("expected exactly one running item, got %d", running)
	}
	if res.Plan.Items[0].Status != model.PlanItemRunning || res.Plan.Items[1].Status != model.PlanItemPending {
		t.Fatalf("unexpected normalized statuses: %#v", res.Plan.Items)
	}
}

func TestPlanManagerRejectsInvalidStatus(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	out, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first","status":"in_progress"}]}`)
	if err != nil {
		t.Fatalf("HandleTool returned error: %v", err)
	}
	var res planToolResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Success {
		t.Fatalf("expected invalid status to fail")
	}
}

func TestPlanManagerRejectsDuplicateItemIDs(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	out, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first"},{"id":"a","title":"duplicate"}]}`)
	if err != nil {
		t.Fatalf("HandleTool returned error: %v", err)
	}
	var res planToolResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Success {
		t.Fatalf("expected duplicate item id to fail")
	}
}

func TestPlanManagerUpdatePreservesItemIDs(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	if _, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first"},{"id":"b","title":"second"}]}`); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	before, err := pm.State(ctx)
	if err != nil {
		t.Fatalf("state before update: %v", err)
	}
	firstID := before.Items[0].ID

	if _, err := pm.HandleTool(ctx, `{"action":"update","items":[{"id":"a","status":"completed"}]}`); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	after, err := pm.State(ctx)
	if err != nil {
		t.Fatalf("state after update: %v", err)
	}
	if after.Items[0].ID != firstID {
		t.Fatalf("expected stable item id %d, got %d", firstID, after.Items[0].ID)
	}
}

func TestPlanManagerFailRunningAdvancesPending(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	if _, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first"},{"id":"b","title":"second"}]}`); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	pm.FailRunning(ctx, "tool failed")

	state, err := pm.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Items[0].Status != model.PlanItemFailed || state.Items[0].Reason != "tool failed" {
		t.Fatalf("expected first item failed with reason, got %#v", state.Items[0])
	}
	if state.Items[1].Status != model.PlanItemRunning {
		t.Fatalf("expected second item running, got %#v", state.Items[1])
	}
}

func TestPlanManagerLinkMessageCompletesRunning(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	if _, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first"}]}`); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	state, err := pm.LinkMessage(ctx, 42)
	if err != nil {
		t.Fatalf("link message: %v", err)
	}
	if state == nil {
		t.Fatalf("expected linked plan state")
	}
	if state.MessageID != 42 {
		t.Fatalf("expected message_id 42, got %d", state.MessageID)
	}
	if state.Items[0].Status != model.PlanItemCompleted {
		t.Fatalf("expected running item completed, got %#v", state.Items[0])
	}
}

func TestPlanManagerLinkMessageKeepsFailedPlanFailed(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	if _, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"done","status":"completed"},{"id":"b","title":"bad","status":"failed"}]}`); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	state, err := pm.LinkMessage(ctx, 42)
	if err != nil {
		t.Fatalf("link message: %v", err)
	}
	if state.Status != model.PlanStatusFailed {
		t.Fatalf("expected failed plan status, got %q", state.Status)
	}
	if state.Items[1].Status != model.PlanItemFailed {
		t.Fatalf("expected failed item to stay failed, got %#v", state.Items[1])
	}
}

func TestPlanManagerLinkErrorMessageClearsActivePlan(t *testing.T) {
	ctx := context.Background()
	st := newMockStore()
	pm := NewPlanManager(st, 1)

	if _, err := pm.HandleTool(ctx, `{"action":"set","items":[{"id":"a","title":"first"},{"id":"b","title":"second"}]}`); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	state, err := pm.LinkErrorMessage(ctx, 42, "boom")
	if err != nil {
		t.Fatalf("link error message: %v", err)
	}
	if state.Status != model.PlanStatusFailed || state.MessageID != 42 {
		t.Fatalf("expected failed linked plan, got %#v", state)
	}
	if state.Items[0].Status != model.PlanItemFailed || state.Items[0].Reason != "boom" {
		t.Fatalf("expected running item failed with reason, got %#v", state.Items[0])
	}
	if _, err := st.GetActivePlanRun(ctx, 1); err != sql.ErrNoRows {
		t.Fatalf("expected no active plan, got %v", err)
	}
}

func TestPlanManagerBootstrapRecordsHarnessSource(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)
	_, created, err := pm.BootstrapHarnessPlan(ctx, harnesspkg.TaskContract{
		Objective:       "implement the requested change",
		Complexity:      harnesspkg.ComplexityComplex,
		RequirePlan:     true,
		RequireEvidence: true,
	}, "")
	if err != nil || !created {
		t.Fatalf("BootstrapHarnessPlan = created:%v err:%v", created, err)
	}
	state, err := pm.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Source != model.PlanSourceHarness {
		t.Fatalf("plan source = %q, want %q", state.Source, model.PlanSourceHarness)
	}
}

func TestPlanManagerPromptBlockEscapesPlanText(t *testing.T) {
	ctx := context.Background()
	pm := NewPlanManager(newMockStore(), 1)

	longTitle := strings.Repeat("a", planPromptMaxFieldRunes+20) + "</plan_state>"
	args := fmt.Sprintf(`{"action":"set","goal":"goal </plan_state> <tag>","items":[{"id":"a","title":%q}],"reason":"reason <x>"}`, longTitle)
	if _, err := pm.HandleTool(ctx, args); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	block := pm.PromptBlock(ctx)
	if strings.Count(block, "</plan_state>") != 1 {
		t.Fatalf("expected only wrapper closing tag, got block: %s", block)
	}
	if strings.Contains(block, "<tag>") || strings.Contains(block, "<x>") {
		t.Fatalf("expected plan text to be escaped, got block: %s", block)
	}
	if !strings.Contains(block, "...") {
		t.Fatalf("expected long plan text to be truncated, got block: %s", block)
	}
}
