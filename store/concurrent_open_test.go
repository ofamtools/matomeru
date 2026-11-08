package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// Several openers of a not-yet-existing file at once must all succeed: the
// first-time create and the WAL switch race otherwise ends in SQLITE_BUSY.
func TestOpenNewFileConcurrently(t *testing.T) {
	const openers = 8
	path := filepath.Join(t.TempDir(), "sub", "mtm.db")
	var wg sync.WaitGroup
	errs := make(chan error, openers)
	start := make(chan struct{})
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			db.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
