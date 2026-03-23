package browser

import (
	"context"
	"testing"
	"time"
)

func TestNavigateMergedDeadline_NotInPast(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), past)
	defer cancel()

	deadline, _, _ := navigateMergedDeadline(ctx, "https://example.com")
	if !deadline.After(time.Now().Add(-500 * time.Millisecond)) {
		t.Fatalf("deadline should be extended into the future, got %v (now ~%v)", deadline, time.Now())
	}
}
