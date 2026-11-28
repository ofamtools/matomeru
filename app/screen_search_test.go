package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func TestSearchVsFilterOnShowPageAndCollections(t *testing.T) {
	a, d := demoLegacy(t)

	note, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		ID:   "search-show-note",
		Fields: map[string]any{
			"title": "Searchable Release Checklist",
			"body":  "Step one: verify zebra token\nStep two: run benchmarks\nStep three: confirm zebra output\nStep four: ship zebra build",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// 1. On a Show page, `/` stays non-filterable (no error, no ModeFilter),
	//    while `\` enters ModeSearch and finds all matching lines without hiding anything!
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     views.CmdOpen,
		Args:   map[string]any{"ref": map[string]any{"kind": note.Kind, "id": note.ID}},
		Source: "test",
		Actor:  User,
	}})
	d.Keys("/")
	if a.Mode() != ModeNormal {
		t.Fatalf("/ on Show page should stay in ModeNormal, got %q", a.Mode())
	}

	d.Keys("\\")
	if a.Mode() != ModeSearch {
		t.Fatalf("\\ on Show page did not enter ModeSearch: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("zebra")
	ps := a.pageSearch()
	if len(ps.matches) != 3 {
		t.Fatalf("expected 3 'zebra' matches on Show page, got %d: %+v", len(ps.matches), ps.matches)
	}
	if got := ps.countLabel(); got != "1/3" {
		t.Fatalf("initial countLabel = %q, want 1/3", got)
	}
	searchBarFrame := d.Frame()
	if !strings.Contains(searchBarFrame, "1/3") || !strings.Contains(searchBarFrame, "run benchmarks") {
		t.Fatalf("search frame must show match count 1/3 AND keep non-matching lines visible:\n%s", searchBarFrame)
	}

	// Accept search (enter) and navigate matches with n / N, clear with esc.
	d.Keys("enter")
	if a.Mode() != ModeNormal || !a.hasPageSearch() {
		t.Fatalf("enter in ModeSearch did not keep search in ModeNormal: mode=%q hasSearch=%v", a.Mode(), a.hasPageSearch())
	}
	d.Keys("n")
	if ps.countLabel() != "2/3" || !strings.Contains(a.Status(), "2/3") {
		t.Fatalf("n did not step to 2/3: count=%q status=%q", ps.countLabel(), a.Status())
	}
	d.Keys("n")
	if ps.countLabel() != "3/3" {
		t.Fatalf("second n did not step to 3/3: count=%q", ps.countLabel())
	}
	d.Keys("N")
	if ps.countLabel() != "2/3" {
		t.Fatalf("N did not step back to 2/3: count=%q", ps.countLabel())
	}
	d.Keys("esc")
	if a.hasPageSearch() {
		t.Fatalf("esc did not clear active search: query=%q", ps.query)
	}

	// 2. On a collection (3 Runs / 2 Work), `\` Search jumps to matching rows
	//    WITHOUT hiding non-matching rows (unlike `/` Filter)!
	_, _, err = a.Store.Create(data.CreateInput{
		Kind:   data.KindRun,
		Fields: map[string]any{"title": "alpha-unique-run", "command": "echo alpha"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Store.Create(data.CreateInput{
		Kind:   data.KindRun,
		Fields: map[string]any{"title": "omega-target-run", "command": "echo omega"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("3")
	d.Frame()
	runsTbl := a.nav.tables[runsTable]
	totalBefore := len(runsTbl.Items())
	if totalBefore < 2 {
		t.Fatalf("expected >= 2 runs, got %d", totalBefore)
	}

	d.Keys("\\")
	d.Type("omega-target-run")
	d.Keys("enter")
	if len(runsTbl.Items()) != totalBefore {
		t.Fatalf("Search hid rows in table! before=%d after=%d", totalBefore, len(runsTbl.Items()))
	}
	row, ok := runsTbl.FocusedRow()
	if !ok || !strings.Contains(data.Text(row.Cells["title"]), "omega-target-run") {
		t.Fatalf("Search did not jump selection to matching row: ok=%v row=%+v", ok, row)
	}
	d.Keys("esc")

	// 3. Opt-out setting `ui.searchHighlight=false` disables inline highlight spans while keeping search jump working.
	a.cfg.userSettings[SetSearchHighlight] = false
	a.reloadSettings()
	a.applySettings()
	if a.searchHighlightEnabled() {
		t.Fatal("ui.searchHighlight=false did not disable searchHighlightEnabled")
	}
	a.cfg.userSettings[SetSearchHighlight] = true
	a.reloadSettings()
	a.applySettings()
	if !a.searchHighlightEnabled() {
		t.Fatal("ui.searchHighlight=true did not re-enable searchHighlightEnabled")
	}
}
