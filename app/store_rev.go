package app

import (
	"sync"
	"time"

	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

// storeRevFreshWindow bounds how long a file-backed Store's cached revision is
// reused across rapid keypress renders before re-checking SQLite for external
// process writes. In-process Store writes invalidate immediately via
// LocalWriteSeq, and external writes signaled via WatchCheck/wakeUp refresh
// immediately via refreshStoreRev.
const storeRevFreshWindow = 50 * time.Millisecond

type storeRevCache struct {
	mu       sync.Mutex
	valid    bool
	rev      int64
	localSeq uint64
	at       time.Time
}

// storeRev returns the current store change-set revision, amortizing repeated
// calls within an Update/View cycle and across rapid keypresses so SQLite WAL
// fcntl locks are not acquired 10-14x per keystroke.
func (a *App) storeRev() int64 {
	if a == nil || a.Store == nil {
		return 0
	}
	localSeq := a.Store.LocalWriteSeq()
	c := &a.viewMemo.revCache
	c.mu.Lock()
	if c.valid {
		if localSeq != c.localSeq {
			if written := a.Store.LastWrittenRev(); written > 0 {
				c.rev = max(c.rev, written)
				c.localSeq = localSeq
				c.at = time.Now()
				rev := c.rev
				c.mu.Unlock()
				return rev
			}
		} else if a.storeRevFreshLocked(c) {
			rev := c.rev
			c.mu.Unlock()
			return rev
		}
	}
	c.mu.Unlock()
	return a.refreshStoreRev()
}

func (a *App) storeRevFreshLocked(c *storeRevCache) bool {
	if a.rt != nil && a.rt.live.seen.Load() > c.rev {
		return false
	}
	if a.Store.IsMemory() {
		return true
	}
	return time.Since(c.at) < storeRevFreshWindow
}

// refreshStoreRev queries SQLite directly for the latest change set number and
// updates the cached revision. Called on cache miss and from wakeUp so external
// CLI/MCP writes are always observed immediately.
func (a *App) refreshStoreRev() int64 {
	if a == nil || a.Store == nil {
		return 0
	}
	n, err := a.Store.LatestChangeSetNumber()
	if err != nil {
		return 0
	}
	c := &a.viewMemo.revCache
	c.mu.Lock()
	c.valid = true
	c.rev = n
	c.localSeq = a.Store.LocalWriteSeq()
	c.at = time.Now()
	c.mu.Unlock()
	return n
}

// tableFilteredCount returns the filtered row count already computed by
// tbl.View(id) in node.Facts["rowCount"], avoiding a full slices.Clone of
// tbl.Rows via tbl.Filtered().
func tableFilteredCount(node ui.ViewNode, tbl *table.Model) int {
	if node.Facts != nil {
		if n, ok := node.Facts["rowCount"].(int); ok {
			return n
		}
	}
	if tbl == nil {
		return 0
	}
	return len(tbl.Filtered())
}
