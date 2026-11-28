package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

const (
	// showRowGutter is the cells a Show row leaves free on the right: the
	// scroll column's bar and one cell of air.
	showRowGutter = 2
	// showRowMinTitle is the narrowest a row's title gets before the row
	// drops its optional summary columns to make room.
	showRowMinTitle = 16
	// showRowFallbackTitle is a row title's limit when the page width is
	// unknown (opts.Width 0).
	showRowFallbackTitle = 64
)

// oneLine is s on a single line: every run of whitespace (newlines, tabs,
// blank lines of a dictated title) becomes one space, then the text is cut
// to limit cells ending in "…". limit <= 0 keeps the whole line.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); limit > 0 && len(r) > limit {
		if limit == 1 {
			return "…"
		}
		return string(r[:limit-1]) + "…"
	}
	return s
}

// rowBudget is how many cells a row's variable text may take once its fixed
// parts (used cells) are laid out, at the page width; fallback when the width
// is unknown. It never goes below floor, so a very narrow page still shows a
// readable stub (the row is drawn unwrapped and clipped past that).
func rowBudget(width, used, fallback, floor int) int {
	if width <= 0 {
		return fallback
	}
	return max(width-showRowGutter-used, floor)
}

// runeLen is the cell count the Show presenters assume for s.
func runeLen(s string) int { return len([]rune(s)) }

// headerFields names the fields the Show header already presents, so the
// Props region does not repeat them: the title (summary) field and every
// status column (drawn as header badges). opts.KeepHeaderFieldsInProps opts
// out (nil set).
func headerFields(schema data.Schema, opts ShowOptions) map[string]bool {
	if opts.KeepHeaderFieldsInProps {
		return nil
	}
	out := map[string]bool{}
	if schema.SummaryField != "" {
		out[schema.SummaryField] = true
	}
	for _, col := range schema.StatusColumns {
		out[col.Name] = true
	}
	return out
}

// propsOmitted is the set of fields Props leaves out: those shown in full in
// content regions plus those the header already shows.
func propsOmitted(rendered, header map[string]bool) map[string]bool {
	out := make(map[string]bool, len(rendered)+len(header))
	for k, v := range rendered {
		out[k] = v
	}
	for k, v := range header {
		out[k] = v
	}
	return out
}

// headerSummary is the one-line summary under a Show title, skipping fields
// already rendered in full. A field Props lists (propsLists) belongs to Props,
// where it is a focusable, copyable row, so the header leaves it there unless
// opts.KeepHeaderFieldsInProps asks for both.
func headerSummary(schema data.Schema, rec data.Record, rendered map[string]bool, opts ShowOptions, propsLists func(string) bool) string {
	for _, name := range []string{"description", "summary", "body", "command", "filePath", "cwd", "cron", "outcomeStatus"} {
		if name == schema.SummaryField || rendered[name] {
			continue
		}
		if !opts.KeepHeaderFieldsInProps && propsLists(name) {
			continue
		}
		if s := oneLine(rec.Text(name), 120); s != "" {
			return s
		}
	}
	return ""
}

// ledgerPropsLists reports whether the ledger Props region lists a field:
// every field but the hidden-role ones.
func ledgerPropsLists(schema data.Schema) func(string) bool {
	return func(name string) bool {
		for _, f := range schema.Fields {
			if f.Name == name {
				return f.EffectiveRole(schema.SummaryField) != data.FieldRoleHidden
			}
		}
		return true
	}
}

// classicPropsLists: the classic props table lists every field.
func classicPropsLists(string) bool { return true }

// headerMetaLines are the ledger header's badge and region-index rows: one
// row when both fit the width, else the index drops to a row of its own.
func headerMetaLines(badges, regions []string, width int) []ui.ViewNode {
	badgeText := " " + strings.Join(badges, "  ·  ")
	indexText := strings.Join(regions, " · ")
	muted := func(id, s string) ui.ViewNode {
		return ui.ViewNode{ID: id, Kind: ui.KindText, Props: map[string]any{
			"text": s, "spans": []ui.ToneSpan{{From: 0, Tone: "muted"}},
		}}
	}
	joined := badgeText + "    " + indexText
	if width <= 0 || runeLen(joined) <= width-showRowGutter {
		return []ui.ViewNode{muted("show.header.meta", joined)}
	}
	return []ui.ViewNode{muted("show.header.meta", badgeText), muted("show.header.index", " "+indexText)}
}

// fitRefTitle is a Props reference row's target title on one line, cut to
// what is left after the margin (prefixLen), the label column and the arrow,
// and the raw ref text when the row shows one.
func fitRefTitle(title string, target core.Ref, prefixLen int, opts ShowOptions) string {
	used := prefixLen + 17
	if !opts.DeclarativePresentations {
		used += runeLen(data.RefText(target)) + 3
	}
	return oneLine(title, rowBudget(opts.Width, used, 88, 12))
}

// fitEntryRow fits one Connections row: the title on one line in the cells
// left after the fixed parts (margin, marker, glyph, gaps) and the suffix.
// The summary columns (cols) are optional and give way before the title is
// cut below showRowMinTitle. It returns the title and the suffix to draw.
func fitEntryRow(title, cols, id string, fixedLen, width int) (string, string) {
	if cols != "" {
		withCols := rowBudget(width, fixedLen+runeLen(cols)+2+runeLen(id), showRowFallbackTitle, 8)
		if withCols >= min(showRowMinTitle, runeLen(oneLine(title, 0))) {
			return oneLine(title, withCols), cols + "  " + id
		}
	}
	return oneLine(title, rowBudget(width, fixedLen+runeLen(id), showRowFallbackTitle, 8)), id
}

// ledgerMetaLine is the Props Created · Updated · Revision row; on a page too
// narrow for the timestamps it keeps the relative ages alone, so it never wraps.
func ledgerMetaLine(pref string, rec data.Record, now time.Time, width int) string {
	created, updated := formatRelativeAge(rec.CreatedAt, now), formatRelativeAge(rec.UpdatedAt, now)
	line := fmt.Sprintf("%sCreated %s (%s) · Updated %s (%s) · Revision %d", pref,
		rec.CreatedAt.Local().Format(timeLayout), created, rec.UpdatedAt.Local().Format(timeLayout), updated, rec.ChangeSetNumber)
	if width > 0 && runeLen(line) > width-showRowGutter {
		line = fmt.Sprintf("%sCreated %s · Updated %s · Revision %d", pref, created, updated, rec.ChangeSetNumber)
	}
	return line
}
