package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Captures Review Page scope and node IDs.
const (
	ScopeCaptures    = "captures.review"
	CapturesPageID   = "captures.page"
	CapturesListID   = "captures.list"
	CapturesBannerID = "captures.banner"
	CapturesDetailID = "captures.preview"
	CapturesHintID   = "captures.hint"

	CapturesHintText = "enter/o edit · s send to pinned · S pick agent & send · y yank · d archive · n new · / filter"
)

// CapturesPageSpec is the pure data contract for CapturesPageView.
type CapturesPageSpec struct {
	Rows             []data.CaptureRow
	SelectedIdx      int
	Total            int
	CapturedCount    int
	SentCount        int
	FailedCount      int
	FilterQuery      string
	PinnedAgentTitle string
	PinnedCount      int
	ScopeLabel       string
	Now              time.Time
}

// CapturesPageView renders the persistent Captures Review log page purely from spec.
func CapturesPageView(spec CapturesPageSpec) ui.ViewNode {
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	selected := clampCaptureIndex(spec.SelectedIdx, len(spec.Rows))
	facts := capturePageFacts(spec, selected, filtering)

	children := []ui.ViewNode{capturesBanner(spec)}
	if filtering {
		children = append(children, fixed(text("captures.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children,
		capturesTable(spec, selected, filtering),
		capturesPreview(spec, selected),
		fixed(text(CapturesHintID, " "+CapturesHintText, "muted"), 1),
	)
	return ui.ViewNode{
		ID:       CapturesPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeCaptures,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

func clampCaptureIndex(idx, n int) int {
	if n == 0 || idx < 0 {
		return -1
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func capturePageFacts(spec CapturesPageSpec, selected int, filtering bool) map[string]any {
	facts := map[string]any{
		"count":          len(spec.Rows),
		"total":          spec.Total,
		"empty":          len(spec.Rows) == 0,
		"filtering":      filtering,
		"hasFilter":      filtering,
		"hasSelection":   selected >= 0,
		"atTop":          selected <= 0,
		"atBottom":       len(spec.Rows) == 0 || selected >= len(spec.Rows)-1,
		"captureId":      "",
		"captureStatus":  "",
		"archived":       false,
		"hasBody":        false,
		"hasPinnedAgent": strings.TrimSpace(spec.PinnedAgentTitle) != "",
		"scopeLabel":     strings.TrimSpace(spec.ScopeLabel),
	}
	if selected >= 0 {
		row := spec.Rows[selected]
		facts["captureId"] = row.ID
		facts["captureStatus"] = row.Status
		facts["archived"] = row.Archived
		facts["hasBody"] = strings.TrimSpace(row.Body) != ""
	}
	return facts
}

func capturesBanner(spec CapturesPageSpec) ui.ViewNode {
	var line spanLine
	line.write(" ✦ Captures ", "accent")
	if sc := strings.TrimSpace(spec.ScopeLabel); sc != "" {
		line.write("["+sc+"] ", "accent")
	}
	line.write(fmt.Sprintf("%d total", spec.Total), "")
	if spec.CapturedCount > 0 {
		line.write(fmt.Sprintf(" · ● %d captured", spec.CapturedCount), "accent")
	}
	if spec.SentCount > 0 {
		line.write(fmt.Sprintf(" · ↗ %d sent", spec.SentCount), "state.done")
	}
	if spec.FailedCount > 0 {
		line.write(fmt.Sprintf(" · ✕ %d failed", spec.FailedCount), "state.errored")
	}
	if strings.TrimSpace(spec.PinnedAgentTitle) != "" {
		suffix := ""
		if spec.PinnedCount > 1 {
			suffix = fmt.Sprintf(" (%d pinned)", spec.PinnedCount)
		}
		line.write("   ★ "+spec.PinnedAgentTitle+suffix, "accent")
	} else {
		line.write("   ○ no pinned agent (p on a pane pins)", "muted")
	}
	return fixed(ui.ViewNode{
		ID:    CapturesBannerID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

type captureColumn struct {
	Title string
	Cell  func(row data.CaptureRow, now time.Time) (string, string)
}

var captureColumns = []captureColumn{
	{"Status", captureStatusCell},
	{"Capture", captureTitleCell},
	{"Agent", captureAgentCell},
	{"Project", captureProjectCell},
	{"When", captureTimeCell},
}

func captureStatusCell(r data.CaptureRow, _ time.Time) (string, string) {
	icon := r.StatusIcon
	if icon == "" {
		icon = "●"
	}
	label := r.StatusLabel
	if label == "" {
		label = r.Status
	}
	return icon + " " + label, r.StatusTone
}

func captureTitleCell(r data.CaptureRow, _ time.Time) (string, string) {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = data.CaptureTitleFromBody(r.Body)
	}
	tone := ""
	if r.Archived {
		tone = "muted"
	}
	return title, tone
}

func captureAgentCell(r data.CaptureRow, _ time.Time) (string, string) {
	if strings.TrimSpace(r.PaneTitle) == "" {
		return "—", "muted"
	}
	return "▣ " + r.PaneTitle, "info"
}

func captureProjectCell(r data.CaptureRow, _ time.Time) (string, string) {
	if strings.TrimSpace(r.ProjectTitle) == "" {
		return "—", "muted"
	}
	return r.ProjectTitle, ""
}

func captureTimeCell(r data.CaptureRow, now time.Time) (string, string) {
	ts := r.Timestamp
	if ts.IsZero() {
		return "—", "muted"
	}
	return formatCaptureTime(ts, now), "muted"
}

func formatCaptureTime(ts, now time.Time) string {
	if !now.IsZero() && ts.Local().YearDay() == now.Local().YearDay() && ts.Local().Year() == now.Local().Year() {
		return ts.Local().Format("15:04")
	}
	return ts.Local().Format("Jan 02 15:04")
}

func capturesTable(spec CapturesPageSpec, selected int, filtering bool) ui.ViewNode {
	titles := make([]any, len(captureColumns))
	for i, col := range captureColumns {
		titles[i] = col.Title
	}
	rows := make([]any, 0, len(spec.Rows))
	for _, r := range spec.Rows {
		cells := make([]any, len(captureColumns))
		tones := make([]string, len(captureColumns))
		for i, col := range captureColumns {
			cells[i], tones[i] = col.Cell(r, spec.Now)
		}
		rows = append(rows, ui.TableRowSpec{Cells: cells, CellTones: tones})
	}
	tbl := ui.ViewNode{
		ID:        CapturesListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: CapturesListID, Prefix: "captures ", Fields: []string{"title", "status", "agent", "project", "source"}},
		Box:       layout.Constraints{Flex: 1, MinH: 6},
		Props: map[string]any{
			"columns":  titles,
			"rows":     rows,
			"selected": selected,
		},
	}
	if len(rows) == 0 {
		empty := "No captures yet · press g i or n to dictate or compose a capture"
		if filtering {
			empty = "No captures match the filter."
		}
		tbl.Kind = ui.KindText
		tbl.Tone = "muted"
		tbl.Props = map[string]any{"text": " " + empty}
	}
	return tbl
}

func capturesPreview(spec CapturesPageSpec, selected int) ui.ViewNode {
	if selected < 0 || selected >= len(spec.Rows) {
		return ui.ViewNode{
			ID:     CapturesDetailID,
			Kind:   ui.KindPanel,
			Title:  "Preview",
			Border: true,
			Box:    layout.Constraints{FixedH: 6},
			Children: []ui.ViewNode{
				text("captures.preview.empty", " Select a capture to inspect its full body.", "muted"),
			},
		}
	}
	row := spec.Rows[selected]
	meta := capturePreviewMeta(row, spec.Now)
	body := strings.TrimSpace(row.Body)
	if body == "" {
		body = "(empty body)"
	}
	children := []ui.ViewNode{
		fixed(text("captures.preview.meta", " "+meta, "muted"), 1),
		text("captures.preview.body", body, ""),
	}
	if strings.TrimSpace(row.Error) != "" {
		children = append(children, fixed(text("captures.preview.err", " Error: "+row.Error, "state.errored"), 1))
	}
	return ui.ViewNode{
		ID:       CapturesDetailID,
		Kind:     ui.KindPanel,
		Title:    "Capture Preview · " + captureTitleOnly(row),
		Border:   true,
		Box:      layout.Constraints{FixedH: 9},
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: children}},
	}
}

func captureTitleOnly(row data.CaptureRow) string {
	if t := strings.TrimSpace(row.Title); t != "" {
		return t
	}
	return data.CaptureTitleFromBody(row.Body)
}

func capturePreviewMeta(row data.CaptureRow, now time.Time) string {
	parts := []string{"Status " + row.Status}
	if row.Source != "" {
		parts = append(parts, "Source "+row.Source)
	}
	if row.PaneTitle != "" {
		parts = append(parts, "Agent "+row.PaneTitle)
	}
	if row.ProjectTitle != "" {
		parts = append(parts, "Project "+row.ProjectTitle)
	}
	if !row.SentAt.IsZero() {
		parts = append(parts, "Sent "+formatCaptureTime(row.SentAt, now))
	}
	return strings.Join(parts, " · ")
}
