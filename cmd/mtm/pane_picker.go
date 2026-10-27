package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// The pane picker (pane.pick) is the page picker's twin for panes: a
// chooser of every pane the daemon lists (a.panes: shells and agents, the
// ones the pin hides and the unassociated ones too), the panes attached
// before most recent first, then the rest in the tree's order (the panes
// the pin hides after them, in listing order), then the attached pane last
// and marked current, so enter on the first row goes back to the pane
// last left. Typing filters (label, project and kind); enter opens the
// Panes page and attaches the pane live through attachLive, the path
// enter on a row and the leader's j and k take; esc closes.
//
// Keys: the leader's p while live (ctrl+a p) and on the Panes page, and \
// on every page in normal mode (no other row in mtm, ochazuke or the
// editor tables answers \, Vim's own leader; it sits by ', the page
// picker). The recency is this session's attaches (paneVisits), stamped
// by attachLive; it is not persisted.

const panePickerKind = "pane-picker"

// panePickerKey is the app-wide key of pane.pick in normal mode.
const panePickerKey = `\`

// paneVisits stamps each pane row id with the order of its last attach.
type paneVisits struct {
	n  uint64
	at map[string]uint64
}

// visit stamps the pane as the newest attach.
func (v *paneVisits) visit(row string) {
	if v.at == nil {
		v.at = map[string]uint64{}
	}
	v.n++
	v.at[row] = v.n
}

func (a *App) definePanePicker() {
	a.r.Define("pane").Verb("pick", a.pickPane, "Choose a pane to attach live: panes attached before most recent first, the attached one last")
	a.r.Define("app").Mode("normal").Bind(panePickerKey, ochazuke.Do("pane.pick").Guard("no-modal"))
	a.r.Define("terminal").Mode("leader_pending").Bind("p", ochazuke.Do("pane.pick").Hinted("pick pane"))
}

// panePickerOrder is the chooser's pane order and the current pane's row
// id ("" while no terminal is attached).
func (a *App) panePickerOrder() ([]pane, string) {
	current := ""
	if a.attached.ID != "" && a.r.Terminal(paneTermID) != nil {
		current = a.attached.rowID()
	}
	tree := map[string]int{}
	for i, id := range a.pv.view.order {
		tree[id] = i
	}
	var visited, rest []pane
	var cur []pane
	for _, p := range a.panes {
		if p.ID == "" {
			continue
		}
		switch row := p.rowID(); {
		case row == current:
			cur = append(cur, p)
		case a.recent.at[row] > 0:
			visited = append(visited, p)
		default:
			rest = append(rest, p)
		}
	}
	slices.SortStableFunc(visited, func(x, y pane) int { return cmp.Compare(a.recent.at[y.rowID()], a.recent.at[x.rowID()]) })
	slices.SortStableFunc(rest, func(x, y pane) int {
		ix, okx := tree[x.rowID()]
		iy, oky := tree[y.rowID()]
		return cmp.Or(-cmp.Compare(btoi(okx), btoi(oky)), cmp.Compare(ix, iy))
	})
	return append(append(visited, rest...), cur...), current
}

// panePickerRows are the chooser's rows: label, project, kind, state
// glyph and age, then "current" on the attached pane.
func (a *App) panePickerRows(now time.Time) []ochazuke.ChooserRow {
	panes, current := a.panePickerOrder()
	rows := make([]ochazuke.ChooserRow, 0, len(panes))
	for _, p := range panes {
		project := a.ownerLabel(a.paneOwner(p).ProjectID)
		state := pickerState(p, now)
		mark := ""
		if p.rowID() == current {
			mark = "current"
		}
		rows = append(rows, ochazuke.ChooserRow{
			ID:    p.rowID(),
			Label: p.label() + " " + project + " " + p.kind(),
			Data:  p,
			Cells: []string{p.label(), project, p.kind(), state, mark},
		})
	}
	return rows
}

// pickerState is an agent's state glyph and cell ("● working 2m"), "×
// exited" for an exited pane, and nothing for a live shell, whose kind
// column already says what it is.
func pickerState(p pane, now time.Time) string {
	if !p.isAgent() && !p.Exited {
		return ""
	}
	return stateGlyph(p) + " " + p.stateCell(now)
}

// panePreview is the highlighted pane's text already in memory, never a
// daemon call: its last captured screen, else its Activity tab's text,
// else what the row says (kind, state, directory).
func (a *App) panePreview(row ochazuke.ChooserRow) string {
	if a.preview.Pane == row.ID && a.preview.Text != "" {
		return trimBlankTail(a.preview.Text)
	}
	if text := a.pv.text[fmt.Sprintf("%s|%d", row.ID, tabActivity)]; text != "" {
		return text
	}
	p, ok := row.Data.(pane)
	if !ok {
		return ""
	}
	lines := []string{p.label(), strings.TrimSuffix(p.kind()+" · "+pickerState(p, time.Now()), " · ")}
	if p.Cwd != "" {
		lines = append(lines, p.Cwd)
	}
	return strings.Join(lines, "\n")
}

// panePickerFilter keeps a row whose label, project or kind holds every
// word of the query, ignoring case, as the list filter (/) matches.
func panePickerFilter(query string, row ochazuke.ChooserRow) bool {
	label := strings.ToLower(row.Label)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(label, word) {
			return false
		}
	}
	return true
}

// pickPane is pane.pick: open the pane chooser over the focus.
func (a *App) pickPane(ctx ochazuke.Ctx) ochazuke.Result {
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:    panePickerKind,
		Title:   "Panes",
		Rows:    a.panePickerRows(time.Now()),
		Empty:   "no panes",
		Keep:    []int{0},
		Filter:  panePickerFilter,
		Preview: a.panePreview,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			p, _ := row.Data.(pane)
			if err := a.attachPicked(p); err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(p.label())
		},
	})
	return resultError(err)
}

// attachPicked opens the Panes page, selects the pane's row and attaches
// it live; the attached pane is focused again rather than reattached.
// Moving from one live pane to another keeps the zoom, as the leader's j
// and k do.
func (a *App) attachPicked(p pane) error {
	row := p.rowID()
	if err := a.r.OpenPage(panesPage); err != nil {
		return err
	}
	if a.attached.ID == p.ID && a.r.Terminal(paneTermID) != nil {
		if a.r.Instances[row] != nil {
			if err := a.pv.tree.Select(row); err != nil {
				return err
			}
		}
		return a.r.SetFocus(paneTermID)
	}
	zoom := a.pv.zoom && a.r.Terminal(paneTermID) != nil
	if a.r.Instances[row] != nil {
		if err := a.pv.tree.Select(row); err != nil {
			return err
		}
	}
	if err := a.attachLive(p, row); err != nil {
		return err
	}
	if a.r.Instances[row] != nil {
		// A pane outside the pin has a row only once attached.
		if err := a.pv.tree.Select(row); err != nil {
			return err
		}
	}
	a.pv.zoom = zoom
	return nil
}
