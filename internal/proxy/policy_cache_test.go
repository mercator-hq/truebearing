package proxy

import (
	"fmt"
	"testing"
)

// TestPolicyLRU_SizeCap verifies that inserting 20 distinct fingerprints into
// a cache with capacity lruCapacity (16) never causes the cache to exceed its
// cap. The oldest entries must be silently evicted rather than growing without
// bound.
func TestPolicyLRU_SizeCap(t *testing.T) {
	cache := newPolicyLRU(lruCapacity)

	// Use a nil *policy.Policy — the cache stores pointers and does not
	// dereference them, so nil is sufficient to exercise the size invariant.
	for i := 0; i < 20; i++ {
		cache.set(fmt.Sprintf("fp-%02d", i), nil)
	}

	if got := cache.len(); got > lruCapacity {
		t.Errorf("cache size = %d after inserting 20 entries, want ≤ %d", got, lruCapacity)
	}
}

// TestPolicyLRU_EvictsLRU verifies that the least-recently-used entry is
// evicted when the cache reaches capacity — not an arbitrary entry.
func TestPolicyLRU_EvictsLRU(t *testing.T) {
	cache := newPolicyLRU(2)

	cache.set("a", nil)
	cache.set("b", nil)
	// Promote "a" to MRU so "b" becomes the LRU.
	cache.get("a")
	// Insert "c" — "b" must be evicted.
	evicted := cache.set("c", nil)
	if evicted != "b" {
		t.Errorf("evicted = %q, want %q", evicted, "b")
	}
	if _, ok := cache.get("a"); !ok {
		t.Error("entry \"a\" should still be cached (it was most recently used)")
	}
	if _, ok := cache.get("b"); ok {
		t.Error("entry \"b\" should have been evicted")
	}
}

// TestPolicyLRU_UpdateExistingDoesNotGrow verifies that re-setting an already
// cached fingerprint updates the entry in place rather than adding a duplicate.
func TestPolicyLRU_UpdateExistingDoesNotGrow(t *testing.T) {
	cache := newPolicyLRU(lruCapacity)
	cache.set("fp-same", nil)
	cache.set("fp-same", nil)

	if got := cache.len(); got != 1 {
		t.Errorf("cache size = %d after two sets of the same key, want 1", got)
	}
	if evicted := cache.set("fp-same", nil); evicted != "" {
		t.Errorf("set on existing key must not report an eviction, got %q", evicted)
	}
}
