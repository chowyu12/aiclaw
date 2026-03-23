package channels

import (
	"testing"
)

func TestThreadLookupKeys(t *testing.T) {
	t.Run("dedupe_and_order", func(t *testing.T) {
		keys := threadLookupKeys(&Inbound{
			ThreadKey:        "a",
			ThreadKeyAliases: []string{"b", "a"},
			SenderID:         "b",
		})
		if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
			t.Fatalf("got %v", keys)
		}
	})
	t.Run("default", func(t *testing.T) {
		keys := threadLookupKeys(&Inbound{})
		if len(keys) != 1 || keys[0] != "default" {
			t.Fatalf("got %v", keys)
		}
	})
	t.Run("sender_only", func(t *testing.T) {
		keys := threadLookupKeys(&Inbound{SenderID: "u1"})
		if len(keys) != 1 || keys[0] != "u1" {
			t.Fatalf("got %v", keys)
		}
	})
}

func TestThreadFlightKey_stableOrder(t *testing.T) {
	k1 := threadFlightKey(3, []string{"b", "a"})
	k2 := threadFlightKey(3, []string{"a", "b"})
	if k1 != k2 {
		t.Fatalf("expected equal keys, got %q vs %q", k1, k2)
	}
	if threadFlightKey(2, []string{"a", "b"}) == k1 {
		t.Fatal("channel id should affect key")
	}
}
