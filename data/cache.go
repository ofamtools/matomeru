package data

import (
	"context"
	"slices"
	"sync"
	"time"

	"ofam.dev/ochazuke/core"
)

// CmdCacheFetched is the command a FetchEffect's result arrives as, unless
// the caller names another: its "slot" argument is the effect's slot, its
// "value" the Snapshot and its "error" the fetch's error.
const CmdCacheFetched = "cache.fetched"

// Snapshot is what a ReadCache holds for one key: the items last fetched,
// how many there are in all, and whether they are known to be out of date.
type Snapshot struct {
	Items     []Record  `json:"items"`
	Total     int       `json:"total"`
	Stale     bool      `json:"stale"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Generation grows by one each time the key is filled, so a reader can
	// tell a new answer from the one it already drew.
	Generation uint64 `json:"generation"`
}

// ReadCache keeps the last answer of slow reads (an external tool's query)
// by key, so the UI draws at once from what it has and fetches off the UI
// loop. It is safe for concurrent use; its zero value is ready.
type ReadCache struct {
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu      sync.RWMutex
	entries map[string]Snapshot
}

// NewReadCache returns an empty cache.
func NewReadCache() *ReadCache { return &ReadCache{} }

func (c *ReadCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns a copy of the snapshot of key. It reports false for a key
// that was never filled.
func (c *ReadCache) Get(key string) (Snapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap, ok := c.entries[key]
	snap.Items = slices.Clone(snap.Items)
	return snap, ok
}

// MarkStale notes that key's items are out of date. They stay readable
// until the next Put. It reports whether the key was there.
func (c *ReadCache) MarkStale(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap, ok := c.entries[key]
	if ok {
		snap.Stale = true
		c.entries[key] = snap
	}
	return ok
}

// Put fills key with fresh items and returns the new snapshot.
func (c *ReadCache) Put(key string, items []Record, total int) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]Snapshot{}
	}
	snap := Snapshot{Items: slices.Clone(items), Total: total, UpdatedAt: c.now(), Generation: c.entries[key].Generation + 1}
	c.entries[key] = snap
	snap.Items = slices.Clone(snap.Items)
	return snap
}

// Drop forgets key.
func (c *ReadCache) Drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Keys returns the keys held, sorted.
func (c *ReadCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.entries))
	for k := range c.entries {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// FetchEffect returns the effect that fills key by running fn off the UI
// loop. A newer effect in the same slot cancels this one: fn's context is
// cancelled and what it returns is not stored. The result arrives as the
// command CmdCacheFetched carrying the new Snapshot; a fetch that fails
// leaves the old items in place and marks them stale.
func (c *ReadCache) FetchEffect(slot, key string, fn func(ctx context.Context) ([]Record, int, error)) core.AsyncEffect {
	return core.AsyncEffect{Slot: slot, Command: CmdCacheFetched, Run: func(ctx context.Context) (any, error) {
		items, total, err := fn(ctx)
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr // superseded: the newer fetch owns the key
		}
		if err != nil {
			c.MarkStale(key)
			return nil, err
		}
		return c.Put(key, items, total), nil
	}}
}
