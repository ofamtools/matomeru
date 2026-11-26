package views

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

func TestFilesViewTabs(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	spec := FilesViewSpec{
		SortMode:  data.PathSortFrecency,
		ScanDepth: 3,
		Entries: []data.PathEntry{
			{Path: "/work/alpha/readme.md", Count: 5, LastUsedAt: now.Add(-2 * time.Hour)},
			{Path: "/work/beta", IsDir: true, Count: 2, LastUsedAt: now.Add(-5 * time.Minute)},
		},
		BaseDirs: []data.BaseDir{{Path: "/work", MaxDepth: 4, RepoCount: 2}},
		Repos:    []data.DiscoveredRepo{{Path: "/work/alpha", Name: "alpha", BaseDir: "/work", Depth: 1}},
	}
	for _, tc := range []struct {
		tab         string
		selected    int
		path        string
		dir         bool
		count       int
		visible     []string
		firstRow    []any
		activeLabel string
	}{
		{
			tab: FilesTabPaths, selected: 0, path: "/work/alpha/readme.md", count: 2,
			visible:     []string{"Last Used", "Uses", "2h ago", "5m ago"},
			firstRow:    []any{"/work/alpha/readme.md", "2h ago", 5},
			activeLabel: "[Paths (2)]",
		},
		{
			tab: FilesTabPaths, selected: 1, path: "/work/beta", dir: true, count: 2,
			visible:     []string{"/work/beta"},
			firstRow:    []any{"/work/alpha/readme.md", "2h ago", 5},
			activeLabel: "[Paths (2)]",
		},
		{
			tab: FilesTabBaseDirs, selected: 0, path: "/work", dir: true, count: 1,
			visible:     []string{"Base Dir", "Depth", "Repos"},
			firstRow:    []any{"/work", 4, 2},
			activeLabel: "[Base Dirs (1)]",
		},
		{
			tab: FilesTabRepos, selected: 0, path: "/work/alpha", dir: true, count: 1,
			visible:     []string{"Repository", "Base Dir", "Depth", "/work/alpha"},
			firstRow:    []any{"alpha", "/work/alpha", "/work", 1},
			activeLabel: "[Git Repos (1)]",
		},
	} {
		t.Run(tc.tab+tc.path, func(t *testing.T) {
			spec.ActiveTab, spec.SelectedIdx = tc.tab, tc.selected
			root := FilesViewAt(spec, now)
			list := root.Find(FilesListID)
			if root.Scope != ScopeFiles || list == nil || list.Kind != ui.KindTable || !list.Focusable {
				t.Fatalf("Files page scope %q with list %+v", root.Scope, list)
			}
			chain := ui.ContextChain(root, FilesListID, nil)
			want := map[string]any{
				"filesTab": tc.tab, "sortMode": "frecency", "scanDepth": 3,
				"hasSelection": true, "selectedPath": tc.path, "selectedIsDir": tc.dir,
				"count": tc.count, "hasFilter": false,
			}
			if chain[0].Scope != ScopeFiles || !maps.Equal(chain[0].Facts, want) {
				t.Fatalf("selected context = %+v, want %v", chain, want)
			}
			if filter, id := ui.ResolveFilterSpec(root, FilesListID); filter == nil || id != FilesListID || filter.Prefix != "files " {
				t.Fatalf("list filter = %+v (%q)", filter, id)
			}
			rows, ok := list.Props["rows"].([][]any)
			if !ok || len(rows) != tc.count || !reflect.DeepEqual(rows[0], tc.firstRow) || list.Props["selected"] != tc.selected {
				t.Fatalf("table props = %+v", list.Props)
			}
			header := root.Find("files.header")
			if header == nil || header.Border {
				t.Fatalf("expected borderless header, got %+v", header)
			}
			headerText := []rune(header.Props["text"].(string))
			var activeLabel, sortPill string
			for _, span := range header.Props["spans"].([]ui.ToneSpan) {
				switch span.Tone {
				case "selected":
					activeLabel = string(headerText[span.From:span.To])
				case "badge.accent":
					sortPill = string(headerText[span.From:span.To])
				}
			}
			if activeLabel != tc.activeLabel || strings.TrimSpace(sortPill) != "Sort: Frecency" {
				t.Fatalf("active tab = %q and sort pill = %q", activeLabel, sortPill)
			}
			rendered := frame(t, root, FilesListID, 80, 12)
			for _, label := range append([]string{
				"[Paths (2)]", "[Base Dirs (1)]", "[Git Repos (1)]", "Sort: Frecency", "Scan depth: 3",
			}, tc.visible...) {
				if !strings.Contains(rendered, label) {
					t.Errorf("expected %q in frame:\n%s", label, rendered)
				}
			}
		})
	}
}

func TestFilesViewWithoutSelection(t *testing.T) {
	spec := FilesViewSpec{ActiveTab: FilesTabPaths, FilterQuery: "missing", ScanDepth: 3}
	root := FilesView(spec)
	if root.Facts["hasSelection"] != false || root.Facts["selectedPath"] != "" ||
		root.Facts["selectedIsDir"] != false || root.Facts["count"] != 0 || root.Facts["hasFilter"] != true {
		t.Fatalf("empty selection facts = %+v", root.Facts)
	}
	if list := root.Find(FilesListID); list == nil || !list.Focusable || list.Filter == nil {
		t.Fatalf("empty list lost focus or filter: %+v", list)
	}
	rendered := frame(t, root, FilesListID, 80, 12)
	for _, label := range []string{"/missing", "No paths match the filter.", "Sort: Recent"} {
		if !strings.Contains(rendered, label) {
			t.Errorf("expected %q in filtered frame:\n%s", label, rendered)
		}
	}

	spec.FilterQuery = ""
	spec.Entries = []data.PathEntry{{Path: "/work"}}
	spec.SelectedIdx = 2
	root = FilesView(spec)
	if root.Facts["hasSelection"] != false || root.Facts["count"] != 1 ||
		root.Find(FilesListID).Props["selected"] != -1 {
		t.Fatalf("out-of-range cursor asserted a selection: %+v", root.Facts)
	}
}
