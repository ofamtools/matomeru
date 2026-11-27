package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestCapturesPageViewEmptyAndFiltered(t *testing.T) {
	empty := CapturesPageView(CapturesPageSpec{})
	if empty.Scope != ScopeCaptures {
		t.Fatalf("scope = %q, want %q", empty.Scope, ScopeCaptures)
	}
	if empty.Facts["empty"] != true || empty.Facts["hasSelection"] != false {
		t.Fatalf("unexpected empty facts: %+v", empty.Facts)
	}
	list := empty.Find(CapturesListID)
	if list == nil || list.Kind != ui.KindText || !strings.Contains(data.Text(list.Props["text"]), "No captures yet") {
		t.Fatalf("unexpected empty list node: %+v", list)
	}

	filtered := CapturesPageView(CapturesPageSpec{FilterQuery: "missing"})
	if filtered.Facts["filtering"] != true {
		t.Fatalf("expected filtering=true, got %+v", filtered.Facts)
	}
	flist := filtered.Find(CapturesListID)
	if flist == nil || !strings.Contains(data.Text(flist.Props["text"]), "No captures match") {
		t.Fatalf("unexpected filtered empty list node: %+v", flist)
	}
}

func TestCapturesPageViewPopulatedRowsFactsAndPreview(t *testing.T) {
	now := time.Date(2026, 11, 27, 15, 30, 0, 0, time.UTC)
	spec := CapturesPageSpec{
		Rows: []data.CaptureRow{
			{
				ID:           "cap-1",
				Title:        "Refactor auth middleware",
				Body:         "Refactor auth middleware\nAdd rate limiting tests.",
				Status:       data.CaptureStatusSent,
				StatusLabel:  "Sent",
				StatusIcon:   "↗",
				StatusTone:   "state.done",
				Source:       data.CaptureSourceDictation,
				PaneID:       "pane-1",
				PaneTitle:    "claude-backend",
				ProjectID:    "proj-1",
				ProjectTitle: "Matomeru",
				Timestamp:    now,
				SentAt:       now,
				Ref:          core.Ref{Kind: data.KindCapture, ID: "cap-1"},
			},
			{
				ID:          "cap-2",
				Title:       "Check CI flakes",
				Body:        "Check CI flakes on darwin",
				Status:      data.CaptureStatusFailed,
				StatusLabel: "Failed",
				StatusIcon:  "✕",
				StatusTone:  "state.errored",
				Source:      data.CaptureSourceCompose,
				Error:       "pane disconnected",
				Timestamp:   now.Add(-24 * time.Hour),
				Ref:         core.Ref{Kind: data.KindCapture, ID: "cap-2"},
			},
		},
		SelectedIdx:      1,
		Total:            2,
		SentCount:        1,
		FailedCount:      1,
		PinnedAgentTitle: "claude-backend",
		PinnedCount:      2,
		Now:              now,
	}

	node := CapturesPageView(spec)
	if node.Facts["count"] != 2 || node.Facts["hasSelection"] != true || node.Facts["atBottom"] != true {
		t.Fatalf("unexpected facts: %+v", node.Facts)
	}
	if node.Facts["captureId"] != "cap-2" || node.Facts["captureStatus"] != data.CaptureStatusFailed || node.Facts["hasPinnedAgent"] != true {
		t.Fatalf("unexpected selection facts: %+v", node.Facts)
	}

	banner := node.Find(CapturesBannerID)
	if banner == nil || !strings.Contains(data.Text(banner.Props["text"]), "claude-backend (2 pinned)") {
		t.Fatalf("banner missing pinned agent info: %+v", banner)
	}
	tbl := node.Find(CapturesListID)
	if tbl == nil || tbl.Kind != ui.KindTable || tbl.Filter == nil || tbl.Filter.ID != CapturesListID {
		t.Fatalf("expected filterable table node, got %+v", tbl)
	}
	errNode := node.Find("captures.preview.err")
	if errNode == nil || !strings.Contains(data.Text(errNode.Props["text"]), "pane disconnected") {
		t.Fatalf("expected error detail in preview, got %+v", errNode)
	}
}
