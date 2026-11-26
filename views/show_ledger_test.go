package views

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestShowLedgerLayoutFoldTalliesAndOptOut(t *testing.T) {
	w := build(t)

	// 1. Full 58-line body renders inline in ledger mode without truncation.
	var bodyLines []string
	for i := 1; i <= 58; i++ {
		bodyLines = append(bodyLines, fmt.Sprintf("- LEDGER_LINE_%02d: ~/proj/matomeru/views/show_ledger.go:%d", i, i))
	}
	longNote, _, err := w.store.Create(data.CreateInput{
		Kind: data.KindNote,
		ID:   "ledger-note",
		Fields: map[string]any{
			"title": "Ledger outline verification note",
			"body":  strings.Join(bodyLines, "\n"),
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Relate(data.RelateInput{From: longNote.Ref(), Kind: data.RelAbout, To: w.project.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	opts := DefaultShowOptions()
	opts.HideEmptyRelations = true
	opts.Width = 120

	root, err := ShowViewWith(w.store, longNote.Ref(), "show.header", opts)
	if err != nil {
		t.Fatal(err)
	}
	if root.Facts["showLayout"] != ShowLayoutLedger {
		t.Fatalf("expected ledger layout at width 120, got %v", root.Facts["showLayout"])
	}
	hdr := root.Find("show.header")
	if hdr == nil || hdr.Border {
		t.Fatalf("expected borderless show.header in ledger layout, got %+v", hdr)
	}
	bodyPanel := root.Find("show.content.body")
	if bodyPanel == nil || bodyPanel.Border || !bodyPanel.Focusable {
		t.Fatalf("expected borderless focusable show.content.body, got %+v", bodyPanel)
	}
	if raw, _ := bodyPanel.Facts["copy"].(string); !strings.Contains(raw, "LEDGER_LINE_01") || !strings.Contains(raw, "LEDGER_LINE_58") {
		t.Fatalf("body copy fact truncated or missing lines: len=%d", len(raw))
	}
	// Empty relation sections collapse into ONE focusable fold toggle row.
	fold := root.Find("show.fold.empty")
	if fold == nil || !fold.Focusable || fold.Facts["kind"] != "fold" || fold.Facts["expanded"] != false {
		t.Fatalf("expected collapsed focusable show.fold.empty, got %+v", fold)
	}
	if root.Find("show.sec.rel.tag.out") != nil {
		t.Fatal("collapsed empty fold should hide individual empty relation rows")
	}

	// Expanding the fold reveals the empty relation rows.
	opts.EmptyFoldExpanded = true
	expanded, err := ShowViewWith(w.store, longNote.Ref(), "show.fold.empty", opts)
	if err != nil {
		t.Fatal(err)
	}
	if f := expanded.Find("show.fold.empty"); f == nil || f.Facts["expanded"] != true {
		t.Fatalf("expected expanded show.fold.empty, got %+v", f)
	}
	if expanded.Find("show.sec.rel.tag.out") == nil {
		t.Fatal("expanded empty fold should reveal empty relation rows")
	}

	// Narrow width (< 100 cols) automatically switches from ledger to stacked.
	narrowOpts := DefaultShowOptions()
	narrowOpts.HideEmptyRelations = true
	narrowOpts.Width = 84
	narrow, err := ShowViewWith(w.store, longNote.Ref(), "show.header", narrowOpts)
	if err != nil {
		t.Fatal(err)
	}
	if narrow.Facts["showLayout"] != ShowLayoutStacked {
		t.Fatalf("expected auto-stacked layout below 100 cols, got %v", narrow.Facts["showLayout"])
	}
	frame, _ := ui.Render(narrow, ui.DefaultDarkTheme(), "show.header", 84, 36)
	plain := stripANSI(frame.ANSI)
	if !strings.Contains(plain, "1 PROPS") || !strings.Contains(plain, "LEDGER_LINE_01") {
		t.Fatalf("narrow stacked render missing expected content:\n%s", plain)
	}

	// Classic opt-out restores the legacy 2-column split with show.props.table.
	classicOpts := DefaultShowOptions()
	classicOpts.Layout = ShowLayoutClassic
	classic, err := ShowViewWith(w.store, longNote.Ref(), "show.header", classicOpts)
	if err != nil {
		t.Fatal(err)
	}
	if classic.Find("show.body") == nil || classic.Find("show.props.table") == nil {
		t.Fatal("show.layout=classic opt-out must restore legacy 2-column split and show.props.table")
	}
}

func TestShowLedgerCatalogOnlyCustomNoun(t *testing.T) {
	w := build(t)
	custom := data.Schema{
		Kind:         "runbook",
		Title:        "Runbook",
		Plural:       "Runbooks",
		SummaryField: "title",
		Fields: []data.FieldSpec{
			{Name: "title", Type: core.ParamString, Role: data.FieldRoleTitle},
			{Name: "host", Type: core.ParamString, Role: data.FieldRoleProp},
			{Name: "script", Type: core.ParamPath, Role: data.FieldRoleProp},
			{Name: "steps", Type: core.ParamString, Role: data.FieldRoleBody},
			{Name: "secretNote", Type: core.ParamString, Role: data.FieldRoleHidden},
		},
	}
	w.store.Registry().Register(custom)
	rec, _, err := w.store.Create(data.CreateInput{
		Kind: "runbook",
		ID:   "rb1",
		Fields: map[string]any{
			"title":      "Deploy edge nodes",
			"host":       "vps2.internal",
			"script":     "~/bin/deploy.sh",
			"steps":      "1. Verify checks\n2. Roll out canary",
			"secretNote": "should stay hidden",
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}

	root, err := ShowViewWith(w.store, rec.Ref(), "show.header", DefaultShowOptions())
	if err != nil {
		t.Fatal(err)
	}
	if root.Find("show.content.steps") == nil {
		t.Fatal("Role=body field on catalog-only noun should render as show.content.steps")
	}
	if root.Find("show.prop.secretNote") != nil || root.Find("show.content.secretNote") != nil {
		t.Fatal("Role=hidden field should be omitted from Show page")
	}
	pathNode := root.Find("show.prop.script")
	if pathNode == nil || pathNode.Facts["tokenType"] != "path" || pathNode.Facts["copy"] != "~/bin/deploy.sh" {
		t.Fatalf("expected focusable path prop token with copy fact, got %+v", pathNode)
	}
	frame, _ := ui.Render(root, ui.DefaultDarkTheme(), "show.header", 120, 36)
	plain := stripANSI(frame.ANSI)
	if !strings.Contains(plain, "Deploy edge nodes") || !strings.Contains(plain, "vps2.internal") || !strings.Contains(plain, "Roll out canary") {
		t.Fatalf("rendered catalog-only noun Show page missing expected content:\n%s", plain)
	}
}

func TestShowLedgerStarredRowAlignsTitlesOnOneLine(t *testing.T) {
	w := build(t)
	n1, _, err := w.store.Create(data.CreateInput{
		Kind:   data.KindNote,
		ID:     "nstar1",
		Fields: map[string]any{"title": "Starred note alpha", "task": w.project.Ref()},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Star(n1.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Create(data.CreateInput{
		Kind:   data.KindNote,
		ID:     "nplain2",
		Fields: map[string]any{"title": "Unstarred note beta", "task": w.project.Ref()},
	}, owner); err != nil {
		t.Fatal(err)
	}

	root, err := ShowViewWith(w.store, w.project.Ref(), "show.header", DefaultShowOptions())
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := ui.Render(root, ui.DefaultDarkTheme(), "show.header", 120, 40)
	plain := stripANSI(frame.ANSI)
	var starCol, plainCol int = -1, -1
	for _, ln := range strings.Split(plain, "\n") {
		if idx := strings.Index(ln, "Starred note alpha"); idx >= 0 {
			if !strings.Contains(ln, "★") {
				t.Fatalf("starred note line missing ★ indicator: %q", ln)
			}
			starCol = len([]rune(ln[:idx]))
		}
		if idx := strings.Index(ln, "Unstarred note beta"); idx >= 0 {
			plainCol = len([]rune(ln[:idx]))
		}
	}
	if starCol < 0 || plainCol < 0 {
		t.Fatalf("expected both notes in rendered Show page:\n%s", plain)
	}
	if starCol != plainCol {
		t.Fatalf("expected consistent left alignment between starred (%d) and unstarred (%d) titles:\n%s", starCol, plainCol, plain)
	}
}
