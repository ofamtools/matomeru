package views

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

const (
	// ScopeLaneBar is the scope of one lane progress bar above a pane's site.
	ScopeLaneBar = "lanebar"
	// LaneBarsID is the stack of lane bars above a pane's site: the finished
	// lane trees kept on show, then the bar of the tree being worked in.
	LaneBarsID = "site.lanebars"
	// LaneBarPrefix prefixes the ID of a finished lane tree's bar.
	LaneBarPrefix = LaneBarID + "."
	// MaxCompletedLaneBars caps the finished bars drawn at once (the newest
	// stay); the dismiss picker still lists them all.
	MaxCompletedLaneBars = 4

	lanePhaseActive    = "active"
	lanePhaseCompleted = "completed"
)

// LaneBarEntry is one bar of a pane's stack: a root lane tree and whether all
// of its work is finished.
type LaneBarEntry struct {
	Roll      LaneRollup
	Completed bool
}

// Key identifies the bar in its present phase. A dismissal is recorded under
// it, so a finished tree that is reopened shows again by itself.
func (e LaneBarEntry) Key() string {
	if e.Completed {
		return LaneBarKey(e.Roll.Root.ID, true)
	}
	return LaneBarKey(e.Roll.Root.ID, false)
}

// LaneBarKey is LaneBarEntry.Key for a lane ID and phase.
func LaneBarKey(laneID string, completed bool) string {
	if completed {
		return laneID + "|" + lanePhaseCompleted
	}
	return laneID + "|" + lanePhaseActive
}

// NodeID is the ViewNode ID of the entry's bar: LaneBarID for the active
// tree, LaneBarPrefix and the lane ID for a finished one.
func (e LaneBarEntry) NodeID() string {
	if e.Completed {
		return LaneBarPrefix + e.Roll.Root.ID
	}
	return LaneBarID
}

// Title is the root lane's title.
func (e LaneBarEntry) Title() string {
	return Title(data.Schema{SummaryField: "title"}, e.Roll.Root)
}

// LaneBarStackOptions say how a pane's lane bars are drawn.
type LaneBarStackOptions struct {
	// Compact draws ui.KindProgress bars; otherwise one text line each.
	Compact bool
	// Hint and Width are LaneBar's; Suffix and SuffixTone LaneBarCompact's.
	// They apply to the active bar.
	Hint               string
	Width              int
	Suffix, SuffixTone string
	// KeepCompleted stacks the pane's finished lane trees above the active bar.
	KeepCompleted bool
	// Dismissed holds the LaneBarEntry keys the owner dismissed.
	Dismissed map[string]bool
}

// CompletedLanes are the finished root lane trees of a pane (claimed by it or
// of its task): every counted lane done or merged, none open. Archived roots
// are left out. Oldest first, so the newest sits next to the active bar.
func CompletedLanes(store *data.Store, pane data.Record) ([]LaneBarEntry, error) {
	if store == nil {
		return nil, nil
	}
	lanes, err := lanesOfPane(store, pane)
	if err != nil {
		return nil, err
	}
	var out []LaneBarEntry
	seen := map[string]bool{}
	for _, lane := range lanes {
		root := laneRoot(store, lane)
		if seen[root.ID] || root.IsArchived() {
			continue
		}
		seen[root.ID] = true
		roll, err := RollupLane(store, root)
		if err != nil {
			return nil, err
		}
		if !roll.IsActive() && roll.Done > 0 {
			out = append(out, LaneBarEntry{Roll: roll, Completed: true})
		}
	}
	slices.SortStableFunc(out, func(a, b LaneBarEntry) int {
		return a.Roll.Root.UpdatedAt.Compare(b.Roll.Root.UpdatedAt)
	})
	return out, nil
}

// PaneLaneBars are the bars a pane's stack can show, before dismissals: its
// finished trees (when keepCompleted), then the active tree, if any.
func PaneLaneBars(store *data.Store, pane data.Record, keepCompleted bool) ([]LaneBarEntry, error) {
	if store == nil {
		return nil, nil
	}
	var out []LaneBarEntry
	if keepCompleted {
		done, err := CompletedLanes(store, pane)
		if err != nil {
			return nil, err
		}
		out = done
	}
	roll, ok, err := ActiveLane(store, pane)
	if err != nil {
		return nil, err
	}
	if ok {
		out = append(out, LaneBarEntry{Roll: roll})
	}
	return out, nil
}

// LaneBarStack renders a pane's lane bars: each a focusable ScopeLaneBar
// node with its lane as Ref, stacked in LaneBarsID above the site. It reports
// false when no bar is left to show.
func LaneBarStack(store *data.Store, pane data.Record, opts LaneBarStackOptions) (ui.ViewNode, bool) {
	entries, err := PaneLaneBars(store, pane, opts.KeepCompleted)
	if err != nil {
		return ui.ViewNode{}, false
	}
	entries = slices.DeleteFunc(entries, func(e LaneBarEntry) bool { return opts.Dismissed[e.Key()] })
	completed := 0
	for _, e := range entries {
		if e.Completed {
			completed++
		}
	}
	hidden := max(0, completed-MaxCompletedLaneBars)
	entries = entries[hidden:] // finished bars lead, oldest first
	if len(entries) == 0 {
		return ui.ViewNode{}, false
	}
	stack := ui.ViewNode{ID: LaneBarsID, Kind: ui.KindVStack, Box: layout.Constraints{FixedH: len(entries)},
		Facts: map[string]any{"laneBars": len(entries), "completedLaneBars": completed - hidden, "hiddenLaneBars": hidden}}
	for _, e := range entries {
		stack.Children = append(stack.Children, laneBarNode(e, opts))
	}
	return stack, true
}

// laneBarNode draws one entry and makes it a focusable, dismissable bar.
func laneBarNode(e LaneBarEntry, opts LaneBarStackOptions) ui.ViewNode {
	var bar ui.ViewNode
	switch {
	case e.Completed && opts.Compact:
		bar = laneBarProgress(e.Roll, "✓ "+laneStatusText(e.Roll.Root), "success")
	case e.Completed:
		bar = laneBarLine(e.Roll, "", opts.Width)
		bar.Tone = "muted"
	case opts.Compact:
		bar = laneBarProgress(e.Roll, opts.Suffix, opts.SuffixTone)
	default:
		bar = laneBarLine(e.Roll, opts.Hint, opts.Width)
	}
	bar.ID, bar.Scope, bar.Focusable = e.NodeID(), ScopeLaneBar, true
	bar.Facts["laneId"] = e.Roll.Root.ID
	bar.Facts["laneStatus"] = laneStatusText(e.Roll.Root)
	bar.Facts["laneBarKey"] = e.Key()
	bar.Facts["laneCompleted"] = e.Completed
	bar.Facts["dismissable"] = true
	bar.Facts["label"] = e.Title()
	return bar
}

func laneStatusText(lane data.Record) string {
	if s := strings.TrimSpace(lane.Text("laneStatus")); s != "" {
		return s
	}
	return "planned"
}

// laneBarFacts are the progress facts every lane bar carries.
func laneBarFacts(roll LaneRollup) map[string]any {
	return map[string]any{"lane": roll.Root.ID, "done": roll.Done, "running": roll.Active, "planned": roll.Planned,
		"trees": roll.OtherTrees + 1, "percent": roll.Percent()}
}

// laneBarLine is the one-line text bar of a lane tree (see LaneBar).
func laneBarLine(roll LaneRollup, hint string, width int) ui.ViewNode {
	line := " " + roll.Text()
	if with := line + "  (" + hint + ")"; hint != "" && (width <= 0 || len([]rune(with)) <= width) {
		line = with
	}
	if r := []rune(line); width > 1 && len(r) > width {
		line = string(r[:width-1]) + "…"
	}
	ref := roll.Root.Ref()
	return ui.ViewNode{ID: LaneBarID, Kind: ui.KindText, Tone: "accent", Ref: &ref, Box: layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": line}, Facts: laneBarFacts(roll)}
}

// laneBarProgress is the compact ui.KindProgress bar of a lane tree (see
// LaneBarCompact).
func laneBarProgress(roll LaneRollup, extraSuffix, suffixTone string) ui.ViewNode {
	suffix := roll.CompactSuffix()
	if extra := strings.TrimSpace(extraSuffix); extra != "" {
		if suffix != "" {
			suffix += " · " + extra
		} else {
			suffix = extra
		}
	}
	props := map[string]any{
		"label":        Title(data.Schema{SummaryField: "title"}, roll.Root),
		"done":         roll.Done,
		"active":       roll.Active,
		"total":        roll.Total(),
		"showFraction": true,
		"showPercent":  true,
	}
	if suffix != "" {
		props["suffix"] = suffix
	}
	if tone := strings.TrimSpace(suffixTone); tone != "" {
		props["suffixTone"] = tone
	}
	ref := roll.Root.Ref()
	return ui.ViewNode{ID: LaneBarID, Kind: ui.KindProgress, Ref: &ref, Box: layout.Constraints{FixedH: 1},
		Props: props, Facts: laneBarFacts(roll)}
}

// LaneBarDismissLabel names an entry in the dismiss picker.
func LaneBarDismissLabel(e LaneBarEntry) string {
	return fmt.Sprintf("%s  %d/%d", e.Title(), e.Roll.Done, e.Roll.Total())
}
