package actionitems

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

func openStore(t *testing.T) (*Store, *store.Store, *int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	clock := int64(1_000)
	s.SetClock(func() int64 { return clock })
	return s, db, &clock
}

// The owner's database already declares matomeru-old's action_items; the
// same declaration opens it, and an old row reads with its marks.
func TestOpensMatomeruOldTableAndRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Declare(store.Table{Name: "action_items", Owner: "actions", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	old := map[string]any{"id": "a1", "service": "github", "source_url": "https://x/pr/1", "title": "Review PR 1", "text": "", "action_type": "pr-review", "created": 5, "updated": 5, "seen_at": 7, "dismissed_at": 0}
	if err := db.Tx([]string{"action_items"}, func(tx *store.Tx) error { return tx.Put("action_items", "a1", old) }); err != nil {
		t.Fatal(err)
	}
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	it, err := s.Get("a1")
	if err != nil {
		t.Fatal(err)
	}
	if it.Source != "github" || it.URL != "https://x/pr/1" || it.Kind != "pr-review" || !it.Read() || it.State(10) != StateOpen {
		t.Fatalf("old row read as %+v", it)
	}
	db.Close()
}

func TestAddIsIdempotentOnSourceAndURL(t *testing.T) {
	s, _, clock := openStore(t)
	a, created, err := s.Add(Spec{Source: "GH", URL: "https://github.com/o/r/pull/1", Why: "review requested", Title: "Fix it"})
	if err != nil || !created {
		t.Fatalf("first add: %+v %v %v", a, created, err)
	}
	if a.Source != "gh" {
		t.Fatalf("source not normalized: %q", a.Source)
	}
	if _, err := s.SetRead(a.ID, true); err != nil {
		t.Fatal(err)
	}
	*clock = 2_000
	b, created, err := s.Add(Spec{Source: "gh", URL: "https://github.com/o/r/pull/1", Why: "review requested again"})
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("second add duplicated: %+v created=%v %v", b, created, err)
	}
	if b.Why != "review requested again" || b.Title != "Fix it" || !b.Read() || b.Updated != 2_000 {
		t.Fatalf("refresh: %+v", b)
	}
	// Another source with the same URL is another item.
	if _, created, _ := s.Add(Spec{Source: "slack", URL: "https://github.com/o/r/pull/1"}); !created {
		t.Fatal("other source folded into gh item")
	}
	items, _ := s.All()
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
}

func TestAddByKeyAndReopen(t *testing.T) {
	s, _, _ := openStore(t)
	a, _, _ := s.Add(Spec{Source: "ci", Key: "build-42", URL: "https://ci/1", Why: "CI failed"})
	if _, err := s.Archive(a.ID); err != nil {
		t.Fatal(err)
	}
	b, created, _ := s.Add(Spec{Source: "ci", Key: "build-42", URL: "https://ci/2"})
	if created || b.ID != a.ID || b.URL != "https://ci/2" || b.State(s.Now()) != StateArchived {
		t.Fatalf("key refresh: %+v created=%v", b, created)
	}
	c, _, _ := s.Add(Spec{Source: "ci", Key: "build-42", Reopen: true})
	if c.State(s.Now()) != StateOpen || c.Read() {
		t.Fatalf("reopen: %+v", c)
	}
}

// Record first: nothing is required; an item with no association is
// unassociated, and every add without a key or URL is a new item.
func TestAddRecordsAnEmptySpec(t *testing.T) {
	s, _, _ := openStore(t)
	a, created, err := s.Add(Spec{})
	if err != nil || !created || a.Source != ManualSource || a.Associated() {
		t.Fatalf("empty add: %+v %v %v", a, created, err)
	}
	if _, created, _ := s.Add(Spec{}); !created {
		t.Fatal("an add with no key or URL folded into another")
	}
	if a.Label() == "" {
		t.Fatal("no label")
	}
	b, _, _ := s.Add(Spec{Title: "two\nlines"})
	if b.Title != "two lines" {
		t.Fatalf("title %q", b.Title)
	}
}

func TestStatesOrderAndCounts(t *testing.T) {
	s, _, clock := openStore(t)
	add := func(title string) Item {
		*clock++
		it, _, err := s.Add(Spec{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	unread, read, snoozed, archived := add("unread"), add("read"), add("snoozed"), add("archived")
	s.SetRead(read.ID, true)
	s.Snooze(snoozed.ID, *clock+100)
	s.Archive(archived.ID)
	items, _ := s.All()
	var got []string
	for _, it := range items {
		got = append(got, it.Title)
	}
	want := []string{"unread", "read", "snoozed", "archived"}
	if len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("order %v", got)
	}
	c, _ := s.Counts()
	if c.Open != 2 || c.Unread != 1 {
		t.Fatalf("counts %+v", c)
	}
	// The snooze passes: the item is open again.
	*clock += 200
	if c, _ := s.Counts(); c.Open != 3 || c.Unread != 2 {
		t.Fatalf("after snooze: %+v", c)
	}
	if it, _ := s.Restore(archived.ID); it.State(*clock) != StateOpen || !it.Read() {
		t.Fatalf("restore: %+v", it)
	}
	if it, _ := s.SetRead(unread.ID, true); !it.Read() {
		t.Fatal("not read")
	}
	if it, _ := s.SetRead(unread.ID, false); it.Read() {
		t.Fatal("still read")
	}
}

func TestAssociatePromoteDelete(t *testing.T) {
	s, _, _ := openStore(t)
	a, _, _ := s.Add(Spec{Title: "x"})
	it, err := s.Associate(a.ID, Owner{ProjectID: "p", TaskID: "t", Window: "w"})
	if err != nil || it.ProjectID != "p" || it.TaskID != "t" || it.Window != "w" {
		t.Fatalf("associate: %+v %v", it, err)
	}
	if it, _ := s.Associate(a.ID, Owner{}); it.Associated() {
		t.Fatalf("scratch: %+v", it)
	}
	it, err = s.Promoted(a.ID, "task", work.Task{ID: "t9", ProjectID: "p"})
	if err != nil || it.PromotedTaskID != "t9" || it.TaskID != "t9" || it.State(s.Now()) != StateArchived {
		t.Fatalf("promoted: %+v %v", it, err)
	}
	raw, _ := json.Marshal(it)
	var back map[string]any
	json.Unmarshal(raw, &back)
	if back["service"] != ManualSource || back["promoted_task_id"] != "t9" {
		t.Fatalf("row shape: %s", raw)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(a.ID); err == nil {
		t.Fatal("still there")
	}
	if err := s.Delete(a.ID); err == nil {
		t.Fatal("deleting a missing item succeeded")
	}
}
