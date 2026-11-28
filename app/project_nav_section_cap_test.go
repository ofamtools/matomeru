package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
)

const navCapDocs = 15

// setupNavCapFixture opens the Project Portal of a project with navCapDocs
// documents and one note, the Documents section expanded and focused.
func setupNavCapFixture(t *testing.T, maxItems int) (*App, *Driver, []string, []string) {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}
	proj := mk(data.KindTask, map[string]any{"title": "Capped", "repo": "ofam/capped", "defaultDir": "/tmp/capped", "workStatus": "active"})
	mk(data.KindPane, map[string]any{"title": "claude: capped", "task": proj.Ref(), "cwd": "/tmp/capped", "runStatus": "running"})
	for i := 1; i <= navCapDocs; i++ {
		mk(data.KindDocument, map[string]any{"title": fmt.Sprintf("Capdoc-%02d", i), "body": "body", "task": proj.Ref()})
	}
	mk(data.KindNote, map[string]any{"title": "Trailing note", "body": "body", "task": proj.Ref()})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	setSettingForTest(t, a, SetProjectsNavSectionMaxItems, maxItems)
	d := NewDriver(a, 165, 70)
	d.Keys("P")
	if !isProjectTab(a.Space()) {
		t.Fatalf("P did not open the project portal: space=%q", a.Space())
	}
	header := projectNavPrefix + "documents"
	a.Shell.State = withFocus(a.State(), header)
	a.viewMemo.gen++
	d.Keys("l")

	// The Documents rows and their titles, in drawing order.
	var docRows, titles []string
	for _, n := range a.focusables(a.State()) {
		if !strings.HasPrefix(n.id, projectNavNodePrefix+"documents.") {
			continue
		}
		docRows = append(docRows, n.id)
		text, _ := findNode(a, n.id).Props["text"].(string)
		titles = append(titles, text[strings.Index(text, "Capdoc-"):][:len("Capdoc-00")])
	}
	if len(docRows) != navCapDocs || a.Focus() != header {
		t.Fatalf("fixture: %d document rows (want %d), focus=%q", len(docRows), navCapDocs, a.Focus())
	}
	return a, d, docRows, titles
}

func visibleCapDocs(frame string) int {
	n := 0
	for i := 1; i <= navCapDocs; i++ {
		// Nav rows carry a "·" or "▸" marker; the right pane's table does not.
		title := fmt.Sprintf("Capdoc-%02d", i)
		if strings.Contains(frame, "· "+title) || strings.Contains(frame, "▸ "+title) {
			n++
		}
	}
	return n
}

func TestProjectNavSectionCapShowsMaxItemsAndOptOut(t *testing.T) {
	a, d, _, _ := setupNavCapFixture(t, 7)
	if got := visibleCapDocs(d.Frame()); got != 7 {
		t.Fatalf("maxItems=7: %d document rows visible, want 7:\n%s", got, d.Frame())
	}
	scroll := findNode(a, views.ProjectNavSectionScrollPrefix+"documents")
	if scroll == nil || scroll.Box.FixedH != 7 {
		t.Fatalf("capped section has no 7-row scroll viewport: %+v", scroll)
	}
	if !strings.Contains(d.Frame(), "7 of 15 shown") {
		t.Fatalf("capped section shows no indicator:\n%s", d.Frame())
	}

	a, d, _, _ = setupNavCapFixture(t, 0)
	if got := visibleCapDocs(d.Frame()); got != navCapDocs {
		t.Fatalf("maxItems=0: %d document rows visible, want %d:\n%s", got, navCapDocs, d.Frame())
	}
	if findNode(a, views.ProjectNavSectionScrollPrefix+"documents") != nil || strings.Contains(d.Frame(), "of 15 shown") {
		t.Fatal("maxItems=0 must not cap the section")
	}
}

func TestNavSpillCappedSectionWalksEveryRowAndSpillsOnlyAtEdges(t *testing.T) {
	a, d, docRows, titles := setupNavCapFixture(t, 7)
	header := projectNavPrefix + "documents"
	last := len(docRows) - 1

	// j walks every row; the focused row is always drawn, and atBottom is
	// true only on the last one.
	for i, id := range docRows {
		d.Keys("j")
		if a.Focus() != id {
			t.Fatalf("j #%d: focus=%q, want %q", i+1, a.Focus(), id)
		}
		node := findNode(a, id)
		if node.Facts["atTop"] != (i == 0) || node.Facts["atBottom"] != (i == last) {
			t.Fatalf("row %d facts atTop=%v atBottom=%v", i+1, node.Facts["atTop"], node.Facts["atBottom"])
		}
		if !strings.Contains(d.Frame(), "▸ "+titles[i]) {
			t.Fatalf("row %d is focused but not drawn:\n%s", i+1, d.Frame())
		}
		if got := visibleCapDocs(d.Frame()); got != 7 {
			t.Fatalf("row %d focused: %d rows visible, want 7", i+1, got)
		}
	}
	if !strings.Contains(d.Frame(), "· "+titles[last-1]) || strings.Contains(d.Frame(), "· "+titles[0]) {
		t.Fatalf("at the bottom the viewport should show the last rows:\n%s", d.Frame())
	}

	// At the last row j spills to the next section.
	d.Keys("j")
	spilled := a.Focus()
	if strings.HasPrefix(spilled, projectNavNodePrefix+"documents.") || !strings.HasPrefix(spilled, projectNavPrefix) {
		t.Fatalf("j at the last row did not spill to the next section: focus=%q", spilled)
	}
	if node := findNode(a, spilled); node == nil || node.Facts["section"] == "documents" {
		t.Fatalf("spill target %q is not another section", spilled)
	}

	// k comes back to the last row, walks up every row, and leaves the
	// section only from the first one.
	for i := last; i >= 0; i-- {
		d.Keys("k")
		if a.Focus() != docRows[i] {
			t.Fatalf("k to row %d: focus=%q, want %q", i+1, a.Focus(), docRows[i])
		}
	}
	if !strings.Contains(d.Frame(), "· "+titles[1]) || strings.Contains(d.Frame(), "· "+titles[last]) {
		t.Fatalf("at the top the viewport should show the first rows:\n%s", d.Frame())
	}
	d.Keys("k")
	if a.Focus() != header {
		t.Fatalf("k at the first row: focus=%q, want %q", a.Focus(), header)
	}
}
