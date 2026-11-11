package main

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// directoryViewRows overlays the last off-loop inspection on the saved list.
// Pickers and the page share this projection; archive wins over cached discovery.
func (a *App) directoryViewRows() ([]knownDirectory, map[string]directoryRecord) {
	records, err := a.userDirectories()
	if err != nil {
		a.directories.BaseErr = err.Error()
		return nil, nil
	}
	meta := map[string]directoryRecord{}
	rows := map[string]knownDirectory{}
	for _, row := range a.directories.Rows {
		rows[row.Path] = row
	}
	for _, record := range records {
		key := a.dirKey(record.Path)
		meta[key] = record
		if record.Archived {
			delete(rows, key)
			continue
		}
		if _, ok := rows[key]; !ok {
			rows[key] = knownDirectory{Tree: work.Tree{Kind: "directory", Path: key}}
		}
	}
	out := make([]knownDirectory, 0, len(rows))
	for path, row := range rows {
		if _, ok := meta[path]; !ok {
			meta[path] = directoryRecord{Path: path, Source: "scan"}
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b knownDirectory) int {
		if (meta[a.Path].Depth > 0) != (meta[b.Path].Depth > 0) {
			if meta[a.Path].Depth > 0 {
				return 1
			}
			return -1
		}
		if a.LastUsed != b.LastUsed {
			if a.LastUsed > b.LastUsed {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, meta
}

func (a *App) managedDirectoryItems(query string) []ochazuke.ListItem {
	rows, meta := a.directoryViewRows()
	owners := a.directoryOwners()
	home, _ := a.home()
	now := a.now()
	managed := a.r.Settings().Bool(settingDirectoryManaged)
	columns := knownDirectoryColumns
	if !managed {
		columns = columns[:5]
	}
	a.directoryList.SetColumns(columns...)
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, row := range rows {
		record := meta[row.Path]
		cells := knownCells(row, owners[row.Path], home, now)
		if !managed {
			if a.knownRow(row.Path) == nil {
				continue
			}
			if wordsMatch(query, strings.Join(cells, " ")) {
				items = append(items, ochazuke.ListItem{ID: "directory:" + row.Path, Noun: "known-directory", Label: cells[0], Data: row.Path, Cells: cells})
			}
			continue
		}
		status := ""
		if a.r.Settings().Bool(settingDirectoryStatus) {
			if why := a.directories.Unavailable[row.Path]; why != "" {
				status = "unavailable: " + why
			} else if a.directories.Loading {
				status = "scanning…"
			}
		}
		cells = append(cells, directorySource(record), record.Label, status)
		if !wordsMatch(query, strings.Join(cells, " ")+" "+record.Description) {
			continue
		}
		items = append(items, ochazuke.ListItem{ID: "directory:" + row.Path, Noun: work.DirectoryNoun, Label: directoryLabel(record), Data: record.Path,
			Props: ochazuke.Thing{"path": record.Path, "label": record.Label, "location": record.Path, "depth": record.Depth, "description": record.Description, "source": record.Source}, Cells: cells})
	}
	if !managed {
		items = append(items, ochazuke.ListItem{ID: "directory:browse", Noun: "directory-browse", Label: "browse…", Cells: []string{"browse…"}})
	}
	return items
}
