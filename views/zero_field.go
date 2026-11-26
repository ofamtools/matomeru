package views

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// ZeroFieldModalID and scope constants for the unified Zero-Field Filter/Group/Sort view.
const (
	ScopeZeroField = "zero.field"
	ZeroFieldID    = "zero.field.stage"
)

// ZeroFieldChip represents one hint-labeled, color-coded toggleable UI chip.
type ZeroFieldChip struct {
	Hint         string
	Label        string
	Active       bool
	HasClause    bool
	ActiveTone   string
	ActiveBg     string
	InactiveTone string
}

// ZeroFieldSettingPill represents one active or implicit setting pill at the top
// of the Zero-Field modal (Filter, Group, or Sort).
type ZeroFieldSettingPill struct {
	ModeLabel    string
	ValueLabel   string
	RemoveHint   string
	Implicit     bool
	ActiveTone   string
	InactiveTone string
}

// ZeroFieldExprPill represents one token in the live boolean filter expression strip.
type ZeroFieldExprPill struct {
	Kind       string // "clause" | "op" | "lparen" | "rparen"
	Label      string
	RemoveHint string
	Invert     bool
}

// ZeroFieldViewSpec is the pure value-object input for ZeroFieldModalView.
type ZeroFieldViewSpec struct {
	Title           string
	Mode            string // "filter" | "group" | "sort"
	ModeTone        string // distinct semantic color per mode
	PendingHint     string
	SelectedField   string
	ActiveSettings  []ZeroFieldSettingPill
	ExprPills       []ZeroFieldExprPill
	ModeChips       []ZeroFieldChip
	CombinatorChips []ZeroFieldChip
	FieldRows       [][]ZeroFieldChip
	ValueChips      []ZeroFieldChip
	PresetChips     []ZeroFieldChip
	MatchedCount    int
	TotalCount      int
	StatusHint      string
	Width           int
}

// ZeroFieldModalView renders the unified 2-section Zero-Field modal with ZERO
// text input boxes: top current/implicit settings strip -> horizontal rule ->
// color-coded Mode, Combinator, Schema Field, Value, and Generic Preset chips.
func ZeroFieldModalView(spec ZeroFieldViewSpec) ui.ViewNode {
	width := spec.Width
	if width < 56 {
		width = 76
	}
	w := &zfChipWriter{}

	// Section 1: Active & Implicit Current Settings (Filter, Group, Sort).
	w.text("CURRENT ", "muted")
	for i, pill := range spec.ActiveSettings {
		if i > 0 {
			w.text("  ", "")
		}
		w.addSettingPill(pill, spec.PendingHint)
	}
	if len(spec.ExprPills) > 0 {
		w.nl()
		w.text("EXPR    ", "muted")
		for i, ep := range spec.ExprPills {
			if i > 0 {
				w.text(" ", "")
			}
			w.addExprPill(ep, spec.PendingHint)
		}
	}
	w.nl()
	w.rule(width - 4)
	w.nl()

	// Section 2A: Color-coded Mode chips (Filter / Group / Sort).
	w.addRow("MODE    ", spec.ModeChips, spec.PendingHint)
	w.nl()

	// Section 2B: Combinators / Modifiers BEFORE fields (Any / All / AND / OR / NOT / ( / )).
	if len(spec.CombinatorChips) > 0 {
		w.addRow("COMBINE ", spec.CombinatorChips, spec.PendingHint)
		w.nl()
	}

	// Section 2C: Schema Fields (id, kind, all props, timestamps).
	for i, row := range spec.FieldRows {
		prefix := "        "
		if i == 0 {
			prefix = "FIELDS  "
		}
		w.addRow(prefix, row, spec.PendingHint)
		w.nl()
	}

	// Section 2D: Candidate Values for the active field (when in filter mode).
	if len(spec.ValueChips) > 0 {
		label := "VALUES  "
		if spec.SelectedField != "" {
			label = fmt.Sprintf("%-7s ", clipText(spec.SelectedField, 7))
		}
		w.addRow(label, spec.ValueChips, spec.PendingHint)
		w.nl()
	}

	// Section 2E: Generic Presets & Quick Actions.
	if len(spec.PresetChips) > 0 {
		w.addRow("PRESETS ", spec.PresetChips, spec.PendingHint)
		w.nl()
	}
	w.rule(width - 4)
	w.nl()
	w.addAction("S", "save preset", "accent", spec.PendingHint)
	w.text("  ", "")
	w.addAction("R", "reset", "warning", spec.PendingHint)
	w.text("  ", "")
	w.addAction(")", "classic view", "info", spec.PendingHint)
	w.text("  ", "")
	w.addAction("esc", "apply & close", "success", spec.PendingHint)
	if spec.StatusHint != "" {
		w.nl()
		w.text(spec.StatusHint, "muted")
	}

	badges := []ui.Badge{
		{Text: strings.ToUpper(nonEmptyStr(spec.Mode, "filter")), Tone: nonEmptyStr(spec.ModeTone, "badge.accent")},
		{Text: fmt.Sprintf("%d/%d rows", spec.MatchedCount, spec.TotalCount), Tone: "info"},
	}
	if spec.PendingHint != "" {
		badges = append(badges, ui.Badge{Text: "CHORD " + spec.PendingHint + "_", Tone: "badge.warning"})
	} else {
		badges = append(badges, ui.Badge{Text: "ZERO-FIELD", Tone: "muted"})
	}

	return ui.ViewNode{
		ID:        ZeroFieldID,
		Kind:      ui.KindPanel,
		Scope:     ScopeZeroField,
		Title:     nonEmptyStr(spec.Title, "Query · Filter / Group / Sort"),
		Tone:      spec.ModeTone,
		Border:    true,
		Badges:    badges,
		Focusable: true,
		Facts: map[string]any{
			"zeroField":     true,
			"queryMode":     spec.Mode,
			"pendingHint":   spec.PendingHint != "",
			"selectedField": spec.SelectedField,
			"matchedCount":  spec.MatchedCount,
		},
		Box: layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{
			{
				ID:    ZeroFieldID + ".body",
				Kind:  ui.KindText,
				Box:   layout.Constraints{Flex: 1},
				Props: map[string]any{"runs": w.runs},
			},
		},
	}
}

type zfChipWriter struct {
	runs []ui.InlineRun
}

func (w *zfChipWriter) text(s, tone string) {
	if s != "" {
		w.runs = append(w.runs, ui.InlineRun{Kind: "text", Text: s, Tone: tone})
	}
}

func (w *zfChipWriter) nl() {
	w.runs = append(w.runs, ui.InlineRun{Kind: "text", Text: "\n"})
}

func (w *zfChipWriter) rule(n int) {
	if n < 36 {
		n = 36
	}
	w.text(strings.Repeat("─", n), "border")
}

func (w *zfChipWriter) badge(label, tone, bg string) {
	if label != "" {
		w.runs = append(w.runs, ui.InlineRun{Kind: "badge", Label: label, Tone: tone, Bg: bg})
	}
}

func (w *zfChipWriter) addHintCap(hint, pending string) bool {
	if hint == "" {
		return false
	}
	if pending != "" {
		if strings.HasPrefix(hint, pending) {
			rem := hint[len(pending):]
			w.badge(" "+pending, "dim", "surface")
			w.badge(rem+" ", "badge.warning", "")
			return false
		}
		w.badge(" "+hint+" ", "dim", "surface")
		return true
	}
	w.badge(" "+hint+" ", "match", "selection")
	return false
}

func (w *zfChipWriter) addChip(c ZeroFieldChip, pending string) {
	dimmed := w.addHintCap(c.Hint, pending)
	label := " " + c.Label + " "
	if c.HasClause && !c.Active {
		label = " " + c.Label + " ● "
	}
	if dimmed {
		w.badge(label, "dim", "badge")
		return
	}
	if c.Active {
		w.badge(label, nonEmptyStr(c.ActiveTone, "badge.accent"), c.ActiveBg)
		return
	}
	tone := nonEmptyStr(c.InactiveTone, "badge")
	bg := "badge"
	if tone == "badge" {
		bg = ""
	}
	w.badge(label, tone, bg)
}

func (w *zfChipWriter) addAction(hint, label, tone, pending string) {
	if w.addHintCap(hint, pending) {
		w.badge(" "+label+" ", "dim", "badge")
		return
	}
	w.badge(" "+label+" ", tone, "badge")
}

func (w *zfChipWriter) addSettingPill(p ZeroFieldSettingPill, pending string) {
	dimmed := false
	if p.RemoveHint != "" {
		dimmed = w.addHintCap(p.RemoveHint, pending)
	}
	if dimmed {
		w.badge(" "+p.ModeLabel+" ", "dim", "badge")
		w.badge(" "+p.ValueLabel+" ", "dim", "surface")
		return
	}
	if p.Implicit {
		w.badge(" "+p.ModeLabel+" ", p.InactiveTone, "badge")
		w.badge(" "+p.ValueLabel+" ", "muted", "surface")
		return
	}
	w.badge(" "+p.ModeLabel+" ", p.ActiveTone, "")
	w.badge(" "+p.ValueLabel+" ", p.InactiveTone, "badge")
}

func (w *zfChipWriter) addExprPill(ep ZeroFieldExprPill, pending string) {
	if ep.Kind == "op" {
		w.badge(" "+ep.Label+" ", "muted", "surface")
		return
	}
	if ep.Kind == "lparen" || ep.Kind == "rparen" {
		w.badge(" "+ep.Label+" ", "info", "surface")
		return
	}
	dimmed := false
	if ep.RemoveHint != "" {
		dimmed = w.addHintCap(ep.RemoveHint, pending)
	}
	if dimmed {
		w.badge(" "+ep.Label+" ", "dim", "badge")
		return
	}
	if ep.Invert {
		w.badge(" NOT ", "badge.error", "")
	}
	w.badge(" "+ep.Label+" ", "info", "badge")
}

func (w *zfChipWriter) addRow(prefix string, chips []ZeroFieldChip, pending string) {
	w.text(prefix, "muted")
	for i, c := range chips {
		if i > 0 {
			w.text("  ", "")
		}
		w.addChip(c, pending)
	}
}

func nonEmptyStr(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}

func clipText(s string, maxLen int) string {
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	return string(rs[:maxLen])
}
