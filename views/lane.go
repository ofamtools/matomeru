package views

import (
	"fmt"
	"math"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// LaneBarID is the node ID of the ambient lane progress bar above a pane's site.
const LaneBarID = "site.lanebar"

// LaneTOCPrefix prefixes the compact lane entry rendered inside a pane's TOC.
const LaneTOCPrefix = "site.toc.lane."

// laneTitle is a lane's title.
func laneTitle(lane data.Record) string {
	return Title(data.Schema{SummaryField: "title"}, lane)
}

// laneCompactSuffix returns an ultra-concise badge for the compact progress bar
// (e.g. "▶1", "+1", or "▶1 +1"), or "" when nothing extra needs calling out.
func laneCompactSuffix(r data.LaneRollup) string {
	var parts []string
	if r.Active > 0 {
		parts = append(parts, fmt.Sprintf("▶%d", r.Active))
	}
	if r.OtherTrees > 0 {
		parts = append(parts, fmt.Sprintf("+%d", r.OtherTrees))
	}
	return strings.Join(parts, " ")
}

// laneCells is the bar itself: one cell per lane, done first, then running,
// then planned.
func laneCells(r data.LaneRollup) string {
	return strings.Repeat("█", r.Done) + strings.Repeat("▓", r.Active) + strings.Repeat("░", r.Planned)
}

// LaneTreeRollupCounts reports done, active, and planned counts for a tree node.
func LaneTreeRollupCounts(nd data.TreeNode) (done, active, planned int) {
	children := make([]data.Record, 0, len(nd.Children))
	for _, child := range nd.Children {
		children = append(children, child.Record)
	}
	progress := data.LaneTreeProgress(nd.Record, children)
	return progress.Done, len(progress.Active), progress.Planned
}

// CompactLaneBarRuns builds a fixed-width (default 4-char) colored progress bar
// as declarative ui.InlineRun segments whose tones (success, state.working,
// muted) stay distinct from the surrounding label text.
func CompactLaneBarRuns(done, active, planned, width int) []ui.InlineRun {
	if width <= 0 {
		width = 4
	}
	total := done + active + planned
	if total <= 0 {
		return []ui.InlineRun{{Text: strings.Repeat("·", width), Tone: "muted"}}
	}
	var dCells, aCells, pCells int
	if total == 1 {
		switch {
		case done == 1:
			dCells = width
		case active == 1:
			aCells = max(1, width/2)
			pCells = width - aCells
		default:
			pCells = width
		}
	} else {
		dCells = int(math.Round(float64(done) * float64(width) / float64(total)))
		aCells = int(math.Round(float64(active) * float64(width) / float64(total)))
		if done > 0 && dCells == 0 {
			dCells = 1
		}
		if active > 0 && aCells == 0 {
			aCells = 1
		}
		for dCells+aCells > width {
			if dCells >= aCells && dCells > 1 {
				dCells--
			} else if aCells > 1 {
				aCells--
			} else {
				break
			}
		}
		pCells = max(0, width-dCells-aCells)
		if planned > 0 && pCells == 0 && width >= 3 {
			if dCells > 1 {
				dCells--
				pCells++
			} else if aCells > 1 {
				aCells--
				pCells++
			}
		}
	}
	var runs []ui.InlineRun
	if dCells > 0 {
		runs = append(runs, ui.InlineRun{Text: strings.Repeat("█", dCells), Tone: "success"})
	}
	if aCells > 0 {
		runs = append(runs, ui.InlineRun{Text: strings.Repeat("▓", aCells), Tone: "state.working"})
	}
	if pCells > 0 {
		runs = append(runs, ui.InlineRun{Text: strings.Repeat("░", pCells), Tone: "muted"})
	}
	return runs
}

// laneRollupText is the one-line strip: "Lane: title  [███▓░░] 3/6 done · 1
// running (name) · 2 planned".
func laneRollupText(r data.LaneRollup) string {
	title := laneTitle(r.Root)
	if r.OtherTrees > 0 {
		title += fmt.Sprintf(" (+%d)", r.OtherTrees)
	}
	parts := []string{fmt.Sprintf("%d/%d done", r.Done, r.Total())}
	if r.Active > 0 {
		part := fmt.Sprintf("%d running", r.Active)
		if len(r.Running) > 0 {
			part += " (" + laneTitle(r.Running[0]) + ")"
		}
		parts = append(parts, part)
	}
	if r.Planned > 0 {
		parts = append(parts, fmt.Sprintf("%d planned", r.Planned))
	}
	return fmt.Sprintf("Lane: %s  [%s] %s", title, laneCells(r), strings.Join(parts, " · "))
}

// LaneTOCEntry renders a pane's active lane tree as a compact 1-line row inside
// the pane's Pages TOC (e.g. "◈ 3/6 Lane Title"), putting progress before the
// title so it remains visible even in narrow TOC rails.
func LaneTOCEntry(roll data.LaneRollup) ui.ViewNode {
	ref := roll.Root.Ref()
	title := laneTitle(roll.Root)
	if rs := []rune(title); len(rs) > 22 {
		title = string(rs[:21]) + "…"
	}
	label := fmt.Sprintf("◈ %d/%d %s", roll.Done, roll.Total(), title)
	return ui.ViewNode{
		ID:        LaneTOCPrefix + ref.ID,
		Kind:      ui.KindText,
		Scope:     ScopeTOC,
		Ref:       &ref,
		Focusable: true,
		Tone:      "accent",
		Box:       layout.Constraints{FixedH: 1},
		Props:     map[string]any{"text": "  " + label, "focusedText": "▸ " + label},
		Facts: map[string]any{
			"isLane":  true,
			"lane":    ref.ID,
			"done":    roll.Done,
			"running": roll.Active,
			"planned": roll.Planned,
		},
	}
}

// --- timeline ---------------------------------------------------------------------

var timelineGlyph = map[string]string{"milestone": "◆", "decision": "◇", "blocker": "■", "verification": "✓", "change": "·"}

// timelineWhat is an entry's text, with who made it.
func timelineWhat(e data.TimelineEntry) string {
	if e.By != "" {
		return e.Text + "  (" + e.By + ")"
	}
	return e.Text
}

// timelineKindGlyph is the glyph of an entry's kind.
func timelineKindGlyph(e data.TimelineEntry) string {
	if glyph := timelineGlyph[e.Kind]; glyph != "" {
		return glyph
	}
	return "•"
}

// timelineFacts are the facts a Show page publishes about its timeline.
func timelineFacts(entries []data.TimelineEntry) []any {
	facts := make([]any, 0, len(entries))
	for _, e := range entries {
		facts = append(facts, map[string]any{"kind": e.Kind, "at": e.At.Format(timeLayout), "text": e.Text})
	}
	return facts
}

// timelinePanel is the Timeline section of a classic Show page.
func timelinePanel(entries []data.TimelineEntry) ui.ViewNode {
	// A table: one row per entry, clipped to the panel rather than wrapped.
	rows := make([]any, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []any{e.At.Format(timeLayout), timelineKindGlyph(e) + " " + e.Kind, timelineWhat(e)})
	}
	body := text("show.timeline.rows", "nothing logged yet", "muted")
	if len(rows) > 0 {
		body = fixed(ui.ViewNode{ID: "show.timeline.rows", Kind: ui.KindTable, Props: map[string]any{
			"columns": []any{map[string]any{"key": "when", "title": "When", "width": 16}, map[string]any{"key": "kind", "title": "Kind", "width": 14},
				map[string]any{"key": "what", "title": "What"}},
			"rows": rows,
		}}, 2+len(rows))
	}
	return ui.ViewNode{ID: "show.timeline", Kind: ui.KindPanel, Title: "Timeline", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d", len(entries))}}, Facts: map[string]any{"entries": timelineFacts(entries)},
		Children: []ui.ViewNode{body}}
}
