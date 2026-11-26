package views

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scope and node IDs of the unified Zero-Field Filter/Group/Sort overlay.
const (
	ScopeZeroField   = "zero.field"
	ZeroFieldID      = "zero.field.stage"
	ZeroFieldModalID = "modal.zero.field"

	zeroFieldMaxWidth = 112
	zeroFieldChrome   = 4 // border and padding, left and right
	zeroFieldLabelW   = 8 // the row label column ("FIELDS  ")
)

// ZeroFieldChip is one hint-labeled, color-coded chip.
type ZeroFieldChip struct {
	Hint         string
	Label        string
	Active       bool
	HasClause    bool
	ActiveTone   string
	ActiveBg     string
	InactiveTone string
}

// ZeroFieldSettingPill is one active or implicit setting of the CURRENT
// strip (Filter, Group or Sort).
type ZeroFieldSettingPill struct {
	ModeLabel    string
	ValueLabel   string
	RemoveHint   string
	Implicit     bool
	ActiveTone   string
	InactiveTone string
}

// ZeroFieldExprPill is one token of the live filter expression strip.
type ZeroFieldExprPill struct {
	Kind       string // "clause" | "op" | "lparen" | "rparen"
	Label      string
	RemoveHint string
	Invert     bool
}

// ZeroFieldViewSpec is the pure input of ZeroFieldModalView. Every label
// is already human (references resolved by the caller); every hint is a
// row of the caller's action table.
type ZeroFieldViewSpec struct {
	Title           string
	Mode            string // "filter" | "group" | "sort"
	ModeTone        string
	PendingHint     string
	SelectedField   string
	ActiveSettings  []ZeroFieldSettingPill
	ExprPills       []ZeroFieldExprPill
	ModeChips       []ZeroFieldChip
	CombinatorChips []ZeroFieldChip
	TargetChips     []ZeroFieldChip // Sort mode: records or groups
	FieldChips      []ZeroFieldChip
	ValueChips      []ZeroFieldChip
	PresetChips     []ZeroFieldChip
	ActionChips     []ZeroFieldChip
	MatchedCount    int
	TotalCount      int
	StatusHint      string
	ScreenWidth     int
}

// ZeroFieldOverlayWidth is the overlay's fixed width on a screen w wide.
func ZeroFieldOverlayWidth(w int) int {
	return max(min(w-4, zeroFieldMaxWidth), 24)
}

// ZeroFieldModalView renders the Zero-Field query as a centered overlay of
// fixed width whose height fits its content: the CURRENT strip, a rule,
// then Mode, Combine, Target, Field, Value, Preset and action chips, each
// row wrapped to the overlay's inner width.
func ZeroFieldModalView(spec ZeroFieldViewSpec) ui.ViewNode {
	overlayW := ZeroFieldOverlayWidth(spec.ScreenWidth)
	w := &zfChipWriter{width: overlayW - zeroFieldChrome, pending: spec.PendingHint}

	w.settings(spec.ActiveSettings)
	w.expr(spec.ExprPills)
	w.rule()
	w.row("MODE", spec.ModeChips)
	w.row("COMBINE", spec.CombinatorChips)
	w.row("TARGET", spec.TargetChips)
	w.row("FIELDS", spec.FieldChips)
	w.row(clipText(nonEmptyStr(spec.SelectedField, "VALUES"), zeroFieldLabelW-1), spec.ValueChips)
	w.row("PRESETS", spec.PresetChips)
	w.rule()
	w.row("", spec.ActionChips)
	if spec.StatusHint != "" {
		w.text(spec.StatusHint, "muted")
		w.nl()
	}

	stage := ui.ViewNode{
		ID:        ZeroFieldID,
		Kind:      ui.KindText,
		Scope:     ScopeZeroField,
		Focusable: true,
		Facts:     zeroFieldFacts(spec),
		Props:     map[string]any{"runs": w.trimmed()},
	}
	return ui.ViewNode{
		ID:       ZeroFieldModalID,
		Kind:     ui.KindOverlay,
		Scope:    ScopeZeroField,
		Title:    nonEmptyStr(spec.Title, "Query · Filter / Group / Sort"),
		Tone:     spec.ModeTone,
		Badges:   zeroFieldBadges(spec),
		Facts:    zeroFieldFacts(spec),
		Box:      layout.Constraints{FixedW: overlayW},
		Children: []ui.ViewNode{stage},
	}
}

func zeroFieldFacts(spec ZeroFieldViewSpec) map[string]any {
	return map[string]any{
		"zeroField":     true,
		"queryMode":     spec.Mode,
		"pendingHint":   spec.PendingHint != "",
		"selectedField": spec.SelectedField,
		"matchedCount":  spec.MatchedCount,
	}
}

func zeroFieldBadges(spec ZeroFieldViewSpec) []ui.Badge {
	badges := []ui.Badge{
		{Text: strings.ToUpper(nonEmptyStr(spec.Mode, "filter")), Tone: "badge." + nonEmptyStr(spec.ModeTone, "accent")},
		{Text: fmt.Sprintf("%d/%d rows", spec.MatchedCount, spec.TotalCount), Tone: "info"},
	}
	if spec.PendingHint != "" {
		return append(badges, ui.Badge{Text: "CHORD " + spec.PendingHint + "_", Tone: "badge.warning"})
	}
	return badges
}

// zfChipWriter lays chips out as inline runs, wrapping each row at width.
type zfChipWriter struct {
	runs    []ui.InlineRun
	width   int
	col     int
	pending string
}

func (w *zfChipWriter) text(s, tone string) {
	if s != "" {
		w.runs = append(w.runs, ui.InlineRun{Kind: "text", Text: s, Tone: tone})
		w.col += utf8.RuneCountInString(s)
	}
}

func (w *zfChipWriter) nl() {
	w.runs = append(w.runs, ui.InlineRun{Kind: "text", Text: "\n"})
	w.col = 0
}

// trimmed drops the final newline so the overlay's height is its content.
func (w *zfChipWriter) trimmed() []ui.InlineRun {
	runs := w.runs
	if n := len(runs); n > 0 && runs[n-1].Text == "\n" {
		runs = runs[:n-1]
	}
	return runs
}

// rule is a divider across the overlay's whole inner width.
func (w *zfChipWriter) rule() {
	w.text(strings.Repeat("─", w.width), "border")
	w.nl()
}

func (w *zfChipWriter) badge(label, tone, bg string) {
	if label != "" {
		w.runs = append(w.runs, ui.InlineRun{Kind: "badge", Label: label, Tone: tone, Bg: bg})
		w.col += utf8.RuneCountInString(label)
	}
}

// fit starts a new, indented line when the next item (n wide) would pass
// the width.
func (w *zfChipWriter) fit(n int) {
	if w.col > zeroFieldLabelW && w.col+n > w.width {
		w.nl()
		w.text(strings.Repeat(" ", zeroFieldLabelW), "")
	}
}

// hintCap draws a hint key; it reports whether the chip is dimmed because
// a pending chord rules it out.
func (w *zfChipWriter) hintCap(hint string) bool {
	if hint == "" {
		return false
	}
	if w.pending == "" {
		w.badge(" "+hint+" ", "match", "selection")
		return false
	}
	if rem, ok := strings.CutPrefix(hint, w.pending); ok {
		w.badge(" "+w.pending, "dim", "surface")
		w.badge(rem+" ", "badge.warning", "")
		return false
	}
	w.badge(" "+hint+" ", "dim", "surface")
	return true
}

func chipWidth(c ZeroFieldChip) int {
	n := utf8.RuneCountInString(c.Label) + 2
	if c.Hint != "" {
		n += utf8.RuneCountInString(c.Hint) + 2
	}
	if c.HasClause {
		n += 2
	}
	return n
}

func (w *zfChipWriter) chip(c ZeroFieldChip) {
	dimmed := w.hintCap(c.Hint)
	label := " " + c.Label + " "
	if c.HasClause && !c.Active {
		label = " " + c.Label + " ● "
	}
	switch {
	case dimmed:
		w.badge(label, "dim", "badge")
	case c.Active:
		w.badge(label, nonEmptyStr(c.ActiveTone, "badge.accent"), c.ActiveBg)
	default:
		tone := nonEmptyStr(c.InactiveTone, "badge")
		bg := "badge"
		if tone == "badge" {
			bg = ""
		}
		w.badge(label, tone, bg)
	}
}

// row draws a labeled, wrapped row of chips; an empty row draws nothing.
func (w *zfChipWriter) row(label string, chips []ZeroFieldChip) {
	if len(chips) == 0 {
		return
	}
	w.text(fmt.Sprintf("%-*s", zeroFieldLabelW, label), "muted")
	for i, c := range chips {
		if i > 0 {
			w.fit(chipWidth(c) + 2)
			if w.col > zeroFieldLabelW {
				w.text("  ", "")
			}
		}
		w.chip(c)
	}
	w.nl()
}

func (w *zfChipWriter) settings(pills []ZeroFieldSettingPill) {
	w.text(fmt.Sprintf("%-*s", zeroFieldLabelW, "CURRENT"), "muted")
	for i, p := range pills {
		if i > 0 {
			w.fit(utf8.RuneCountInString(p.ModeLabel+p.ValueLabel+p.RemoveHint) + 8)
			if w.col > zeroFieldLabelW {
				w.text("  ", "")
			}
		}
		w.settingPill(p)
	}
	w.nl()
}

func (w *zfChipWriter) settingPill(p ZeroFieldSettingPill) {
	if w.hintCap(p.RemoveHint) {
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

func (w *zfChipWriter) expr(pills []ZeroFieldExprPill) {
	if len(pills) == 0 {
		return
	}
	w.text(fmt.Sprintf("%-*s", zeroFieldLabelW, "EXPR"), "muted")
	for i, ep := range pills {
		if i > 0 {
			w.fit(utf8.RuneCountInString(ep.Label+ep.RemoveHint) + 7)
			if w.col > zeroFieldLabelW {
				w.text(" ", "")
			}
		}
		w.exprPill(ep)
	}
	w.nl()
}

// zfTokenTones are the tones of the expression's structural tokens; a
// clause (not listed) is a removable pill.
var zfTokenTones = map[string]string{"op": "muted", "lparen": "info", "rparen": "info"}

func (w *zfChipWriter) exprPill(ep ZeroFieldExprPill) {
	if tone, structural := zfTokenTones[ep.Kind]; structural {
		w.badge(" "+ep.Label+" ", tone, "surface")
		return
	}
	if w.hintCap(ep.RemoveHint) {
		w.badge(" "+ep.Label+" ", "dim", "badge")
		return
	}
	tone := "info"
	if ep.Invert {
		tone = "error"
	}
	w.badge(" "+ep.Label+" ", tone, "badge")
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
