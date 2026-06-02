package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chowyu12/aiclaw/internal/model"
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
