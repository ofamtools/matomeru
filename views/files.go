package views

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

const ScopeFiles = "files"

const (
	FilesTabPaths    = "paths"
	FilesTabBaseDirs = "basedirs"
	FilesTabRepos    = "repos"
	FilesListID      = "files.list"
)

// FilesViewSpec contains the visible rows and the selected index in the active tab.
type FilesViewSpec struct {
	ActiveTab   string
	SortMode    data.PathSortMode
	FilterQuery string
	ScanDepth   int
	Entries     []data.PathEntry
	BaseDirs    []data.BaseDir
	Repos       []data.DiscoveredRepo
	SelectedIdx int
}

// FilesView presents the supplied files using the current time for relative ages.
func FilesView(spec FilesViewSpec) ui.ViewNode {
	return FilesViewAt(spec, time.Now())
}

// FilesViewAt presents the files at a fixed reference time without reading or mutating their sources.
func FilesViewAt(spec FilesViewSpec, now time.Time) ui.ViewNode {
	switch spec.ActiveTab {
	case FilesTabBaseDirs, FilesTabRepos:
	default:
		spec.ActiveTab = FilesTabPaths
	}
	if spec.SortMode == "" {
		spec.SortMode = data.PathSortRecent
	}

	columns, entries, emptyText := filesRows(spec, now)
	rows := make([][]any, len(entries))
	for i, entry := range entries {
		rows[i] = entry.cells
	}
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	facts := map[string]any{
		"filesTab":      spec.ActiveTab,
		"sortMode":      string(spec.SortMode),
		"scanDepth":     spec.ScanDepth,
		"hasSelection":  false,
		"selectedPath":  "",
		"selectedIsDir": false,
		"count":         len(rows),
		"hasFilter":     filtering,
	}

	selected := -1
	if i := spec.SelectedIdx; i >= 0 && i < len(entries) {
		selected = i
		facts["hasSelection"] = true
		facts["selectedPath"] = entries[i].path
		facts["selectedIsDir"] = entries[i].isDir
	}

	body := ui.ViewNode{
		ID:        FilesListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: FilesListID, Prefix: "files "},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":   columns,
			"rows":      rows,
			"selected":  selected,
			"scrollKey": spec.ActiveTab,
		},
	}
	if len(rows) == 0 {
		if filtering {
			emptyText = "No paths match the filter."
		}
		body.Kind = ui.KindText
		body.Tone = "muted"
		body.Props = map[string]any{"text": " " + emptyText}
	}

	children := []ui.ViewNode{filesHeader(spec)}
	if filtering {
		children = append(children, fixed(text("files.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children, body)
	return ui.ViewNode{
		ID:       "files.page",
		Kind:     ui.KindVStack,
		Scope:    ScopeFiles,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

// FilesSortLabel is the display name shared by the Files page and path picker.
func FilesSortLabel(mode data.PathSortMode) string {
	switch mode {
	case data.PathSortFrequent:
		return "Frequent"
	case data.PathSortFrecency:
		return "Frecency"
	case data.PathSortName:
		return "Name"
	default:
		return "Recent"
	}
}

func filesHeader(spec FilesViewSpec) ui.ViewNode {
	var line strings.Builder
	var spans []ui.ToneSpan
	cursor := 0
	write := func(value, tone string) {
		line.WriteString(value)
		end := cursor + utf8.RuneCountInString(value)
		if tone != "" {
			spans = append(spans, ui.ToneSpan{From: cursor, To: end, Tone: tone})
		}
		cursor = end
	}
	for _, tab := range []struct {
		id, label string
		count     int
	}{
		{FilesTabPaths, "Paths", len(spec.Entries)},
		{FilesTabBaseDirs, "Base Dirs", len(spec.BaseDirs)},
		{FilesTabRepos, "Git Repos", len(spec.Repos)},
	} {
		tone := "muted"
		if tab.id == spec.ActiveTab {
			tone = "selected"
		}
		write(" ", "")
		write(fmt.Sprintf("[%s (%d)]", tab.label, tab.count), tone)
	}
	write("  ", "")
	write(" Sort: "+FilesSortLabel(spec.SortMode)+" ", "badge.accent")
	write(fmt.Sprintf("  Scan depth: %d", spec.ScanDepth), "muted")
	return fixed(ui.ViewNode{
		ID:    "files.header",
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": spans},
	}, 1)
}

type filesRow struct {
	path  string
	isDir bool
	cells []any
}

func filesRows(spec FilesViewSpec, now time.Time) (columns []any, rows []filesRow, emptyText string) {
	switch spec.ActiveTab {
	case FilesTabBaseDirs:
		for _, dir := range spec.BaseDirs {
			rows = append(rows, filesRow{dir.Path, true, []any{dir.Path, dir.MaxDepth, dir.RepoCount}})
		}
		return []any{"Base Dir", "Depth", "Repos"}, rows, "No base directories."
	case FilesTabRepos:
		for _, repo := range spec.Repos {
			rows = append(rows, filesRow{repo.Path, true, []any{repo.Name, repo.Path, repo.BaseDir, repo.Depth}})
		}
		return []any{"Repository", "Path", "Base Dir", "Depth"}, rows, "No Git repositories discovered."
	default:
		for _, entry := range spec.Entries {
			rows = append(rows, filesRow{entry.Path, entry.IsDir, []any{entry.Path, filesAge(now, entry.LastUsedAt), entry.Count}})
		}
		return []any{"Path", "Last Used", "Uses"}, rows, "No paths used yet."
	}
}

func filesAge(now, lastUsed time.Time) string {
	if lastUsed.IsZero() {
		return "—"
	}
	age := now.Sub(lastUsed)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
	}
}
