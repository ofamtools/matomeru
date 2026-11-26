package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

const CmdProjectSectionPick = "project.section.pick"

// chromeSeparatorNode builds the 1-line chrome separator placed between the
// top page tabs and page content when ui.topChromeRedesign is enabled:
// system stats (CPU/RAM/disk) on the left, context chain + MODE badge on the
// right, connected by a subtle horizontal hairline.
func (a *App) chromeSeparatorNode(s core.State, chain []core.ContextFrame, w int) ui.ViewNode {
	right := strings.TrimSpace(a.chromeContextAndMode(s, chain))
	left := ""
	if a.chromeSystemStatsEnabled() && a.rt != nil && a.rt.live.sysStats != nil {
		left = FormatChromeSysStats(a.rt.live.sysStats.Snapshot(), max(w/2, 24))
	}
	var text string
	switch {
	case left != "" && right != "" && w > 0:
		lw, rw := visibleWidth(left), visibleWidth(right)
		rem := w - lw - rw - 4
		if rem >= 3 {
			text = " " + left + " " + strings.Repeat("─", rem) + " " + right + " "
		} else {
			text = MergeChromeStatusLine(" "+left, right, w)
		}
	case right != "" && w > 0:
		rw := visibleWidth(right)
		rem := w - rw - 3
		if rem >= 3 {
			text = " " + strings.Repeat("─", rem) + " " + right + " "
		} else {
			text = " " + right
		}
	case left != "" && w > 0:
		lw := visibleWidth(left)
		rem := w - lw - 3
		if rem >= 3 {
			text = " " + left + " " + strings.Repeat("─", rem) + " "
		} else {
			text = " " + left
		}
	default:
		text = " " + strings.TrimSpace(left+" "+right)
	}
	if w > 0 {
		text = clip(text, w)
	}
	spans := []ui.ToneSpan{{From: 0, Tone: "muted"}}
	if right != "" {
		if idx := strings.LastIndex(text, right); idx >= 0 {
			from := len([]rune(text[:idx]))
			spans = []ui.ToneSpan{
				{From: 0, To: from, Tone: "muted"},
				{From: from, Tone: "accent"},
			}
		}
	}
	return ui.ViewNode{
		ID:    "chrome.sep",
		Kind:  ui.KindText,
		Props: map[string]any{"text": text, "spans": spans},
	}
}

func (a *App) chromeContextAndMode(s core.State, chain []core.ContextFrame) string {
	scopes := make([]string, 0, len(chain))
	for _, f := range chain {
		if strings.HasPrefix(f.Scope, "trait.") {
			continue
		}
		scopes = append(scopes, f.Scope)
	}
	slices.Reverse(scopes)
	mode := strings.ToUpper(a.Engine.Mode(s))
	if l := s.Input.Layer; l != nil {
		mode += " · " + l.Name
	}
	out := strings.Join(scopes, " › ") + "  [" + mode + "]"
	if p := s.Input.Prefix; len(p) > 0 {
		out += " · " + strings.Join(p, " ") + "…"
	}
	return out
}

// bottomStatusLine builds the compact bottom-left status / kept-filter bar
// under the redesigned chrome (mode/context/stats live in the top separator).
func (a *App) bottomStatusLine(s core.State, root ui.ViewNode, w int) string {
	var parts []string
	typing := a.Engine.Mode(s) == ModeFilter
	if !typing || !a.filterBarBottomLeftEnabled() {
		if f, spec, _ := a.activeFilterTarget(s, &root); f != nil && (f.IsFiltered() || typing) {
			prefix := activeFilterPrefix(f, spec)
			q := f.FilterQuery()
			shownQuery := q
			if typing {
				a.nav.filterLine.Sync(q)
				rs := []rune(q)
				ci := min(max(a.nav.filterLine.Cursor, 0), len(rs))
				shownQuery = string(rs[:ci]) + "█" + string(rs[ci:])
			}
			parts = append(parts, prefix+"/"+shownQuery+" ("+string(f.FilterMode())+")")
		}
	}
	if a.status != "" {
		parts = append(parts, a.status)
	}
	if len(parts) == 0 {
		return " : command · / filter · ? help"
	}
	return clip(" "+strings.Join(parts, " · "), w)
}

func isSpaceScope(scope string) bool {
	switch scope {
	case SpacePanes, SpaceWork, SpaceRuns, SpaceExtensions, SpaceProjects, SpaceFeatures, views.ScopeProjectTab:
		return true
	}
	return false
}

// hintScopeRank ranks a binding's scope so actions unique to the focused noun
// or component come FIRST (rank 0..N-1), enclosing space actions come SECOND
// (100+), and shared/global ("", "app") actions come LAST (200+).
func hintScopeRank(scope string, chain []core.ContextFrame) int {
	if scope == "" || scope == "app" {
		return 200
	}
	for i, f := range chain {
		if f.Scope == scope {
			if isSpaceScope(scope) {
				return 100 + i
			}
			return i
		}
	}
	if isSpaceScope(scope) {
		return 150
	}
	return 50
}

// projectSectionPick opens a quick-jump picker over all sections of the active
// Project Page workspace without needing to collapse/expand the nav tree.
func (a *App) projectSectionPick(s core.State, _ core.Command) core.StepResult {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		a.say("open a project workspace first")
		return core.Next(s)
	}
	defs := a.projectNavSectionDefs()
	pc := a.projectCtrl()
	activeSec := pc.activeSection(projKey)
	items := make([]picker.Item, 0, len(defs))
	for _, def := range defs {
		badge := def.Group
		if def.Key == activeSec {
			badge = "active"
		}
		items = append(items, picker.Item{
			ID:       def.Key,
			Title:    def.Label,
			Subtitle: def.Key,
			Badges:   []ui.Badge{{Text: badge, Tone: "muted"}},
		})
	}
	pm := picker.New(fmt.Sprintf("Jump to Project Section (%d)", len(items)), items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "project.sections",
		PageSize: 12,
		OnConfirm: func(it picker.Item) modal.Result {
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{
					ID:   CmdProjectSectionSelect,
					Args: map[string]any{"section": it.ID},
				}},
			}
		},
	})
	return core.Next(a.pushModal(s, m))
}
