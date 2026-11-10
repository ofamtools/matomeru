package contextitems

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatterSkipsRootsRemembersAndIsBounded(t *testing.T) {
	t.Parallel()
	s := NewStatter(time.Minute, 2, []string{"/home", "/net/"})
	calls := map[string]int{}
	s.stat = func(p string) (fs.FileInfo, error) { calls[p]++; return nil, os.ErrNotExist }
	clock := time.Now()
	s.now = func() time.Time { return clock }
	stat := s.Local()

	for _, p := range []string{"/home/u/x", "/home/u", "/net/h/x"} {
		if _, err := stat(p); !errors.Is(err, ErrNotLocal) {
			t.Fatalf("%s: %v, want not local", p, err)
		}
	}
	if _, err := stat("/home"); errors.Is(err, ErrNotLocal) || calls["/home"] != 1 {
		t.Fatalf("the root itself is asked: %v %v", err, calls)
	}
	if _, err := stat("/homework/x"); errors.Is(err, ErrNotLocal) {
		t.Fatal("/homework is not under /home")
	}
	for range 3 {
		stat("/a")
	}
	if calls["/a"] != 1 {
		t.Fatalf("/a asked %d times within the ttl", calls["/a"])
	}
	clock = clock.Add(2 * time.Minute)
	stat("/a")
	if calls["/a"] != 2 {
		t.Fatalf("/a asked %d times after the ttl, want 2", calls["/a"])
	}
	for _, p := range []string{"/b", "/c", "/d", "/e"} {
		stat(p)
	}
	if len(s.answers) > 2 {
		t.Fatalf("%d answers kept, want at most 2", len(s.answers))
	}
}

func TestExtractWithAnUncheckedPathKeepsItsExistsUnknown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "r.txt")
	if err := os.WriteFile(real, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	items := ExtractStat("see /home/x/y/z.md and "+real+" and word/ok", 0, Source{Kind: "scrollback"}, Remote())
	if len(items) != 2 {
		t.Fatalf("items = %+v, want the two explicit paths", items)
	}
	for _, it := range items {
		if it.Exists != nil {
			t.Fatalf("%s: exists %v, want unknown", it.Text, *it.Exists)
		}
	}
}
