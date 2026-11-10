package contextitems

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
)

// mergeByScan is Merge as it was before the lookup: it decodes the whole
// table to find duplicates. It is the reference the keyed Merge must agree
// with.
func mergeByScan(s *Store, items []Item, owner Owner, now int64) ([]Item, int, error) {
	if len(items) == 0 {
		return nil, 0, nil
	}
	var added, seen []Item
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(Table.Name)
		if err != nil {
			return err
		}
		existing := make(map[string]Item, len(rows))
		for _, raw := range rows {
			var it Item
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			existing[key(it)] = it
		}
		for _, it := range items {
			it.ProjectID, it.TaskID = owner.ProjectID, owner.TaskID
			if it.TaskID != "" {
				it.ProjectID = ""
			}
			k := key(it)
			if prev, ok := existing[k]; ok {
				for _, src := range it.Sources {
					if !slices.ContainsFunc(prev.Sources, func(old Source) bool {
						return old.Kind == src.Kind && old.EntryKind == src.EntryKind && old.Session == src.Session && old.Window == src.Window && old.Pane == src.Pane
					}) {
						prev.Sources = append(prev.Sources, src)
					}
				}
				prev.LastSeen = now
				if it.Exists != nil {
					prev.Exists = it.Exists
				}
				it = prev
				seen = append(seen, it)
			} else {
				if it.ID == "" {
					it.ID = work.NewID()
				}
				if it.Created == 0 {
					it.Created = now
				}
				it.LastSeen = now
				it.Provenance = Provenance{TurnID: it.Provenance.TurnID, Session: it.Session, Window: it.Window, ProjectID: owner.ProjectID, TaskID: owner.TaskID, At: it.Created}
				added = append(added, it)
			}
			existing[k] = it
			if err := tx.Put(Table.Name, it.ID, it); err != nil {
				return err
			}
		}
		return nil
	})
	return append(added, seen...), len(added), err
}

func openEmpty(t testing.TB) *Store {
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
	return s
}

// seedRows writes n rows; the first few are the odd ones the key rules
// treat: padded text, a trailing slash on links, other owners and kinds.
func seedRows(t testing.TB, s *Store, n int) {
	t.Helper()
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		put := func(it Item) {
			if err := tx.Put(Table.Name, it.ID, it); err != nil {
				t.Fatal(err)
			}
		}
		src := []Source{{Kind: "scrollback", Session: "s", Window: "w0", Pane: "p"}}
		put(Item{ID: "pad", Kind: "file", Text: "  /a/padded  ", Created: 1, Sources: src})
		put(Item{ID: "slash", Kind: "link", Text: "https://x.test/a/", Created: 1, Sources: src})
		put(Item{ID: "plain", Kind: "link", Text: "https://x.test/b", Created: 1, Sources: src})
		put(Item{ID: "taskowned", Kind: "file", Text: "/a/task", TaskID: "t1", Created: 1, Sources: src})
		put(Item{ID: "projowned", Kind: "file", Text: "/a/task", ProjectID: "p1", Created: 1, Sources: src})
		put(Item{ID: "othkind", Kind: "dir", Text: "/a/same", Created: 1, Sources: src})
		put(Item{ID: "othkind2", Kind: "file", Text: "/a/same", Created: 1, Sources: src})
		for i := 0; i < n; i++ {
			put(Item{ID: fmt.Sprintf("r%06d", i), Kind: Kinds[i%len(Kinds)], Text: fmt.Sprintf("/work/%d/file-%d.go", i%97, i), Created: int64(i + 2), LastSeen: int64(i + 2), Sources: src,
				Provenance: Provenance{Session: "s", At: int64(i)}})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func incoming() []Item {
	src := []Source{{Kind: "scrollback", Session: "s", Window: "w1", Pane: "q"}}
	t := true
	return []Item{
		{Kind: "file", Text: "/a/padded", Sources: src, Exists: &t},
		{Kind: "link", Text: "https://x.test/a", Sources: src},
		{Kind: "link", Text: "https://x.test/b/", Sources: src},
		{Kind: "file", Text: "/a/task", Sources: src},
		{Kind: "file", Text: "/a/same", Sources: src},
		{Kind: "file", Text: "/a/same", Sources: []Source{{Kind: "scrollback", Session: "s", Window: "w2", Pane: "z"}}},
		{Kind: "file", Text: "/work/3/file-3.go", Sources: src},
		{Kind: "prompt", Text: "/brand/new", Sources: src},
		{Kind: "commit", Text: "abcdef0", Sources: src},
	}
}

func TestMergeKeyedLookupAgreesWithTheScan(t *testing.T) {
	t.Parallel()
	for _, owner := range []Owner{{}, {ProjectID: "p1"}, {TaskID: "t1"}} {
		fast, ref := openEmpty(t), openEmpty(t)
		seedRows(t, fast, 400)
		seedRows(t, ref, 400)
		for round := range 2 { // the second round sees the first round's writes
			a, na, err := fast.Merge(incoming(), owner, 1000+int64(round))
			if err != nil {
				t.Fatal(err)
			}
			b, nb, err := mergeByScan(ref, incoming(), owner, 1000+int64(round))
			if err != nil {
				t.Fatal(err)
			}
			// New items get random ids; compare them by their other fields.
			strip := func(items []Item) []Item {
				out := slices.Clone(items)
				for i := range out {
					if out[i].Created >= 1000 {
						out[i].ID = ""
					}
				}
				return out
			}
			if na != nb || !reflect.DeepEqual(strip(a), strip(b)) {
				t.Fatalf("owner %+v round %d: keyed merge differs\nkeyed %d %+v\nscan  %d %+v", owner, round, na, a, nb, b)
			}
		}
		x, _ := fast.All()
		y, _ := ref.All()
		for i := range x {
			if x[i].Created >= 1000 {
				x[i].ID = ""
			}
		}
		for i := range y {
			if y[i].Created >= 1000 {
				y[i].ID = ""
			}
		}
		slices.SortFunc(x, func(a, b Item) int { return cmpItems(a, b) })
		slices.SortFunc(y, func(a, b Item) int { return cmpItems(a, b) })
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("owner %+v: the tables differ after merging", owner)
		}
	}
}

func cmpItems(a, b Item) int {
	return slices.Compare([]string{a.Kind, a.Text, a.TaskID, a.ProjectID, a.ID}, []string{b.Kind, b.Text, b.TaskID, b.ProjectID, b.ID})
}

func TestMergeReadsOnlyTheRowsItLooksUp(t *testing.T) {
	t.Parallel()
	s := openEmpty(t)
	seedRows(t, s, 20000)
	before := decoded.Load()
	if _, _, err := s.Merge(incoming(), Owner{}, 5000); err != nil {
		t.Fatal(err)
	}
	// Other tests merge in parallel and add to the counter, so allow for
	// them: the bound is far below the 20,000 rows of the table.
	if n := decoded.Load() - before; n >= 2000 {
		t.Fatalf("Merge decoded %d rows of a 20,007 row table to find %d items' duplicates", n, len(incoming()))
	}
}

func BenchmarkMerge20k(b *testing.B) {
	for _, mode := range []string{"keyed", "scan"} {
		b.Run(mode, func(b *testing.B) {
			s := openEmpty(b)
			seedRows(b, s, 20000)
			b.ResetTimer()
			for i := range b.N {
				var err error
				if mode == "keyed" {
					_, _, err = s.Merge(incoming(), Owner{}, int64(i+10))
				} else {
					_, _, err = mergeByScan(s, incoming(), Owner{}, int64(i+10))
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
