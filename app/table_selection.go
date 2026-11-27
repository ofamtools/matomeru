package app

import (
	"slices"

	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// syncTableSelection replaces tbl's rows while keeping tbl as the single owner
// of selection state. It returns the post-filter visible rows after restoring
// selection to preferredID (or the previously focused row ID when preferredID
// is empty), falling back to a clamped index within the visible rows.
func syncTableSelection(tbl *table.Model, rows []table.Row, preferredID string) []table.Row {
	if tbl == nil {
		return nil
	}
	targetID := preferredID
	if targetID == "" {
		if cur, ok := tbl.FocusedRow(); ok {
			targetID = cur.ID
		}
	}
	tbl.Rows = rows
	shown := tbl.Filtered()
	if targetID != "" {
		if i := slices.IndexFunc(shown, func(r table.Row) bool { return r.ID == targetID }); i >= 0 {
			tbl.SelectedIdx = i
			tbl.Focused()
			return shown
		}
	}
	tbl.SelectedIdx = min(max(tbl.SelectedIdx, 0), max(0, len(shown)-1))
	tbl.Focused()
	return shown
}

// selectTableRowID moves tbl's selection to the visible row with id.
// It returns the matched index and true when found.
func selectTableRowID(tbl *table.Model, id string) (int, bool) {
	if tbl == nil || id == "" {
		return -1, false
	}
	for i, it := range tbl.Items() {
		if !it.Header && it.Row.ID == id {
			tbl.SelectedIdx = i
			tbl.Focused()
			return i, true
		}
	}
	return -1, false
}

// selectTableGroupHeader moves tbl's selection to the group header for groupName.
func selectTableGroupHeader(tbl *table.Model, groupName string) bool {
	if tbl == nil || groupName == "" {
		return false
	}
	for i, it := range tbl.Items() {
		if it.Header && it.Group == groupName {
			tbl.SelectedIdx = i
			tbl.Focused()
			return true
		}
	}
	return false
}

// resetTableSelection resets tbl's cursor and scroll offset to the start.
func resetTableSelection(tbl *table.Model) {
	if tbl == nil {
		return
	}
	tbl.SelectedIdx = 0
	tbl.Offset = 0
}

// selectPickerItemID moves pm's selection to the item with id when present.
func selectPickerItemID(pm *picker.Model, id string) bool {
	if pm == nil || id == "" {
		return false
	}
	if i := slices.IndexFunc(pm.Items, func(it picker.Item) bool { return it.ID == id }); i >= 0 {
		pm.SelectedIdx = i
		return true
	}
	return false
}
