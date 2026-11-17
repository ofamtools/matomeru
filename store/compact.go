package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Space is the file's page layout, read in a read transaction (it writes
// nothing): the page size, the pages it has, the free pages (space rows
// left inside the file, reused by later writes) and its auto-vacuum mode
// (none, full or incremental).
type Space struct {
	PageSize   int64  `json:"page_size"`
	Pages      int64  `json:"pages"`
	FreePages  int64  `json:"free_pages"`
	FreeBytes  int64  `json:"free_bytes"`
	AutoVacuum string `json:"auto_vacuum"`
}

var autoVacuumModes = map[int64]string{0: "none", 1: "full", 2: "incremental"}

// Space reads the file's page layout.
func (s *Store) Space() (Space, error) {
	var sp Space
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return sp, err
	}
	defer tx.Rollback()
	var mode int64
	for _, q := range []struct {
		pragma string
		into   *int64
	}{{"page_size", &sp.PageSize}, {"page_count", &sp.Pages}, {"freelist_count", &sp.FreePages}, {"auto_vacuum", &mode}} {
		if err := tx.QueryRow(`PRAGMA ` + q.pragma).Scan(q.into); err != nil {
			return sp, err
		}
	}
	sp.FreeBytes = sp.FreePages * sp.PageSize
	sp.AutoVacuum = autoVacuumModes[mode]
	return sp, nil
}

// CompactStep is how many free pages one incremental step gives back (16
// MiB of 4 KiB pages: a write lock of a few milliseconds).
const CompactStep = 4096

// Compacted is what Compact did: the file's bytes (pages times page size)
// before and after, whether it switched the file to incremental
// auto-vacuum (the one whole rewrite), the steps, the longest write lock
// and the time it took.
type Compacted struct {
	Before      int64         `json:"bytes_before"`
	After       int64         `json:"bytes_after"`
	Switched    bool          `json:"switched,omitempty"`
	Steps       int           `json:"steps"`
	LongestHold time.Duration `json:"longest_hold"`
	Took        time.Duration `json:"took"`
}

// Compact gives the file's free pages back to the filesystem. A file in
// incremental auto-vacuum mode gives them back in steps of CompactStep
// pages (PRAGMA incremental_vacuum), each its own short write. Any other
// file is first switched to that mode by one VACUUM, which rewrites the
// whole file under the write lock: about half a second for the owner's
// journal after expiry (36 MB). In WAL mode readers in other processes keep
// reading their snapshot meanwhile, and their writes wait (busy_timeout)
// and then go through. A caller makes a copy first (BackupCopy).
func (s *Store) Compact() (out Compacted, err error) {
	start := time.Now()
	sp, err := s.Space()
	if err != nil {
		return out, err
	}
	out.Before = sp.Pages * sp.PageSize
	defer func() { out.Took = time.Since(start) }()
	if sp.AutoVacuum != "incremental" {
		held := time.Now()
		if _, err := s.db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return out, err
		}
		if _, err := s.db.Exec(`VACUUM`); err != nil {
			return out, err
		}
		out.Switched, out.Steps, out.LongestHold = true, 1, time.Since(held)
	}
	for last := int64(-1); ; {
		if sp, err = s.Space(); err != nil || sp.FreePages == 0 || sp.FreePages == last {
			out.After = sp.Pages * sp.PageSize
			return out, err
		}
		last = sp.FreePages
		held := time.Now()
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, CompactStep)); err != nil {
			return out, err
		}
		out.Steps++
		out.LongestHold = max(out.LongestHold, time.Since(held))
	}
}
