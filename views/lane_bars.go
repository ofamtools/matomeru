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
)

// laneBarNodeID is the ViewNode ID of an entry's bar: LaneBarID for the active
// tree, LaneBarPrefix and the lane ID for a finished one.
func laneBarNodeID(e data.LaneBarEntry) string {
	if e.Completed {
		return LaneBarPrefix + e.Roll.Root.ID
	}
	return LaneBarID
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
	// Dismissed holds the data.LaneBarEntry keys the owner dismissed.
	Dismissed map[string]bool
}

// LaneBarStack renders a pane's lane bars (the store's PaneLaneBars): each a
// focusable ScopeLaneBar node with its lane as Ref, stacked in LaneBarsID
// above the site. It reports false when no bar is left to show.
func LaneBarStack(bars []data.LaneBarEntry, opts LaneBarStackOptions) (ui.ViewNode, bool) {
	entries := slices.DeleteFunc(slices.Clone(bars), func(e data.LaneBarEntry) bool { return opts.Dismissed[e.Key()] })
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
func laneBarNode(e data.LaneBarEntry, opts LaneBarStackOptions) ui.ViewNode {
	var bar ui.ViewNode
	switch {
	case e.Completed && opts.Compact:
		bar = LaneBarCompact(e.Roll, "✓ "+laneStatusText(e.Roll.Root), "success")
	case e.Completed:
		bar = LaneBar(e.Roll, "", opts.Width)
		bar.Tone = "muted"
	case opts.Compact:
		bar = LaneBarCompact(e.Roll, opts.Suffix, opts.SuffixTone)
	default:
		bar = LaneBar(e.Roll, opts.Hint, opts.Width)
	}
	bar.ID, bar.Scope, bar.Focusable = laneBarNodeID(e), ScopeLaneBar, true
	bar.Facts["laneId"] = e.Roll.Root.ID
	bar.Facts["laneStatus"] = laneStatusText(e.Roll.Root)
	bar.Facts["laneBarKey"] = e.Key()
	bar.Facts["laneCompleted"] = e.Completed
	bar.Facts["dismissable"] = true
	bar.Facts["label"] = laneTitle(e.Roll.Root)
	return bar
}

func laneStatusText(lane data.Record) string {
	if s := strings.TrimSpace(lane.Text("laneStatus")); s != "" {
		return s
	}
	return "planned"
}

// laneBarFacts are the progress facts every lane bar carries.
func laneBarFacts(roll data.LaneRollup) map[string]any {
	return map[string]any{"lane": roll.Root.ID, "done": roll.Done, "running": roll.Active, "planned": roll.Planned,
		"trees": roll.OtherTrees + 1, "percent": roll.Percent()}
}

// LaneBar renders the ambient progress strip of a lane tree: one line, shown
// above the pane's site and never in its table of contents. hint names the
// key that opens the lane ("" leaves it out). width, when positive, is the
// room there is: the hint is dropped first, then the line is clipped.
func LaneBar(roll data.LaneRollup, hint string, width int) ui.ViewNode {
	line := " " + laneRollupText(roll)
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

// LaneBarCompact renders a lane tree as a prominent, concise ui.KindProgress
// node using ochazuke's progress theme tokens. Optional extraSuffix and
// suffixTone fold a tiny companion status badge (such as compact hook
// telemetry) onto the same single line.
func LaneBarCompact(roll data.LaneRollup, extraSuffix, suffixTone string) ui.ViewNode {
	suffix := laneCompactSuffix(roll)
	if extra := strings.TrimSpace(extraSuffix); extra != "" {
		if suffix != "" {
			suffix += " · " + extra
		} else {
			suffix = extra
		}
	}
	props := map[string]any{
		"label":        laneTitle(roll.Root),
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
func LaneBarDismissLabel(e data.LaneBarEntry) string {
	return fmt.Sprintf("%s  %d/%d", laneTitle(e.Roll.Root), e.Roll.Done, e.Roll.Total())
}
