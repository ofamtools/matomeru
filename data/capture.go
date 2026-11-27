package data

import (
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// Capture status and source values for KindCapture records.
const (
	CaptureStatusCaptured = "captured"
	CaptureStatusSent     = "sent"
	CaptureStatusDraft    = "draft"
	CaptureStatusFailed   = "failed"
	CaptureStatusArchived = "archived"

	CaptureSourceCompose   = "compose"
	CaptureSourceDictation = "dictation"

	captureTitleMaxRunes = 72
)

// CaptureSources lists valid capture origins.
var CaptureSources = []string{CaptureSourceCompose, CaptureSourceDictation}

// CaptureRow is the pure view-model row projected for the Captures Review page.
type CaptureRow struct {
	ID           string
	Title        string
	Body         string
	Preview      string
	Status       string
	StatusLabel  string
	StatusIcon   string
	StatusTone   string
	Source       string
	PaneID       string
	PaneTitle    string
	ProjectID    string
	ProjectTitle string
	Timestamp    time.Time
	SentAt       time.Time
	Error        string
	Archived     bool
	Ref          core.Ref
}

func captureStatusColumn() StatusColumn {
	return StatusColumn{
		Name:            "captureStatus",
		Title:           "Status",
		Default:         CaptureStatusCaptured,
		PrimaryRowStyle: true,
		Values: []StatusValue{
			{Name: CaptureStatusCaptured, Label: "Captured", Tone: "accent", RowTone: "row.active", Icon: "●", IconTone: "accent", Position: 0, Starts: true, Open: true},
			{Name: CaptureStatusSent, Label: "Sent", Tone: "state.done", RowTone: "row.dim", Icon: "↗", IconTone: "state.done", Position: 1, Ends: true},
			{Name: CaptureStatusDraft, Label: "Draft", Tone: "muted", RowTone: "row.muted", Icon: "○", IconTone: "muted", Position: 2, Open: true},
			{Name: CaptureStatusFailed, Label: "Failed", Tone: "state.errored", RowTone: "row.error", Icon: "✕", IconTone: "state.errored", Position: 3, Open: true},
			{Name: CaptureStatusArchived, Label: "Archived", Tone: "muted", RowTone: "row.dim", Icon: "·", IconTone: "muted", Position: 4, Ends: true, Archives: true},
		},
	}
}

func captureSchema() Schema {
	status := captureStatusColumn()
	return Schema{
		Kind:         KindCapture,
		Title:        "Capture",
		Plural:       "Captures",
		SummaryField: "title",
		Fields: []FieldSpec{
			str("title", "short title derived from the captured text"),
			strFmt("body", "captured dictation or composed prompt text", ShowFormatMarkdown),
			statusField(status, "delivery lifecycle of this capture (captured, sent, draft, failed, archived)"),
			{Name: "source", Type: core.ParamString, Enum: CaptureSources, Default: CaptureSourceCompose, Description: "how the capture was entered (compose or dictation)"},
			refTo("pane", KindPane, "target agent pane the capture was sent to"),
			refTo("project", KindTask, "active project when captured"),
			str("sentAt", "when the capture was last sent to an agent pane (RFC 3339)"),
			str("error", "delivery error detail when captureStatus is failed"),
			{Name: "attachments", Type: core.ParamList, ShowFormat: ShowFormatList, Description: "attached files, images, URLs, or context references"},
		},
		StatusColumns:  []StatusColumn{status},
		DefaultColumns: []string{"title", "captureStatus", "pane", "project", "source", "sentAt"},
		Relations:      universalRelations(),
		FactFields:     []string{"captureStatus", "source"},
		BadgeRules: []BadgeRule{
			{When: `fields.source == "dictation"`, Text: "dictation", Tone: "info"},
			{When: `fields.captureStatus == "failed"`, Text: "failed", Tone: "error"},
		},
	}
}

// CaptureTitleFromBody derives a concise single-line title from captured body text.
func CaptureTitleFromBody(body string) string {
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimLeft(line, "#*- ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rs := []rune(line)
		if len(rs) > captureTitleMaxRunes {
			return strings.TrimSpace(string(rs[:captureTitleMaxRunes-1])) + "…"
		}
		return line
	}
	return "Untitled capture"
}

// CapturePreview returns a single-line snippet of body for the review table.
func CapturePreview(body string, maxRunes int) string {
	flat := strings.Join(strings.Fields(body), " ")
	if maxRunes <= 0 {
		return flat
	}
	rs := []rune(flat)
	if len(rs) <= maxRunes {
		return flat
	}
	return strings.TrimSpace(string(rs[:maxRunes-1])) + "…"
}

// SortCapturesNewestFirst orders captures newest-first by UpdatedAt (falling back to CreatedAt).
func SortCapturesNewestFirst(records []Record) {
	slices.SortStableFunc(records, func(a, b Record) int {
		at, bt := captureSortTime(a), captureSortTime(b)
		if cmp := bt.Compare(at); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func captureSortTime(rec Record) time.Time {
	if !rec.UpdatedAt.IsZero() {
		return rec.UpdatedAt
	}
	return rec.CreatedAt
}

// SortPinnedPanes filters to pinned panes and sorts them deterministically:
// newest pinnedAt first, then newest promptedAt, then CreatedAt, then ID.
func SortPinnedPanes(records []Record) []Record {
	var pinned []Record
	for _, rec := range records {
		if rec.Bool("pinned") {
			pinned = append(pinned, rec)
		}
	}
	slices.SortStableFunc(pinned, func(a, b Record) int {
		if cmp := b.Time("pinnedAt").Compare(a.Time("pinnedAt")); cmp != 0 {
			return cmp
		}
		if cmp := b.Time("promptedAt").Compare(a.Time("promptedAt")); cmp != 0 {
			return cmp
		}
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return pinned
}

// IsPaneRef reports whether ref addresses a pane record.
func IsPaneRef(ref core.Ref) bool { return ref.Kind == KindPane && ref.ID != "" }

// IsTaskRef reports whether ref addresses a task record.
func IsTaskRef(ref core.Ref) bool { return ref.Kind == KindTask && ref.ID != "" }

// IsCaptureRef reports whether ref addresses a capture record.
func IsCaptureRef(ref core.Ref) bool { return ref.Kind == KindCapture && ref.ID != "" }
