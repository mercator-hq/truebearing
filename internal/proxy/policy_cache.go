package proxy

import (
	"container/list"
	"sync"

	"github.com/mercator-hq/truebearing/internal/policy"
)

// lruCapacity is the maximum number of policy versions retained in memory.
// At a GitOps push cadence of roughly one push per day, 16 entries covers
// 16 days of policy history — enough to keep all active sessions valid
// without unbounded growth.
//
// Design: 16 is chosen over a smaller number because it leaves headroom for
// rapid iteration environments (multiple deploys per day) while remaining
// negligibly small in memory (each entry is a parsed *policy.Policy pointer).
const lruCapacity = 16

// policyLRU is a bounded LRU cache of policy versions keyed by fingerprint.
// It is concurrency-safe via its own internal mutex, independent of the
// Proxy's polMu, so cache lookups on the hot request path do not contend
// with hot-reload writes.
//
// Invariant: len() never exceeds the capacity passed to newPolicyLRU.
type policyLRU struct {
	mu    sync.Mutex
	cap   int
	list  *list.List
	index map[string]*list.Element
}

// lruEntry is the value stored in list.List for each cached policy version.
type lruEntry struct {
	fingerprint string
	pol         *policy.Policy
}

// newPolicyLRU returns a policyLRU with the given capacity. cap must be > 0.
func newPolicyLRU(cap int) *policyLRU {
	return &policyLRU{
		cap:   cap,
		list:  list.New(),
		index: make(map[string]*list.Element, cap),
	}
}

// get returns the policy for the given fingerprint and promotes the entry to
// the front of the LRU list (most recently used). Returns false if not cached.
func (c *policyLRU) get(fp string) (*policy.Policy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[fp]
	if !ok {
		return nil, false
	}
	c.list.MoveToFront(el)
	return el.Value.(*lruEntry).pol, true
}

// set inserts or updates the policy for the given fingerprint and promotes the
// entry to the front of the LRU list. If the cache is at capacity, the
// least-recently-used entry is evicted; its fingerprint is returned so the
// caller can log the eviction. An empty string is returned when no eviction
// occurred.
func (c *policyLRU) set(fp string, pol *policy.Policy) (evictedFP string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[fp]; ok {
		c.list.MoveToFront(el)
		el.Value.(*lruEntry).pol = pol
		return ""
	}
	if c.list.Len() >= c.cap {
		back := c.list.Back()
		if back != nil {
			entry := back.Value.(*lruEntry)
			evictedFP = entry.fingerprint
			delete(c.index, entry.fingerprint)
			c.list.Remove(back)
		}
	}
	el := c.list.PushFront(&lruEntry{fingerprint: fp, pol: pol})
	c.index[fp] = el
	return evictedFP
}

// len returns the number of entries currently in the cache.
func (c *policyLRU) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list.Len()
}
