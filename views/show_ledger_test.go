package views

import (
	"fmt"
	"slices"
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

	root, err := loadShowWith(w.store, longNote.Ref(), "show.header", opts)
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
	expanded, err := loadShowWith(w.store, longNote.Ref(), "show.fold.empty", opts)
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
	narrow, err := loadShowWith(w.store, longNote.Ref(), "show.header", narrowOpts)
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
	classic, err := loadShowWith(w.store, longNote.Ref(), "show.header", classicOpts)
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

	root, err := loadShowWith(w.store, rec.Ref(), "show.header", DefaultShowOptions())
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

	root, err := loadShowWith(w.store, w.project.Ref(), "show.header", DefaultShowOptions())
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

// propsFieldNames are the record fields the ledger Props region lists.
func propsFieldNames(root ui.ViewNode) []string {
	var out []string
	if props := root.Find("show.props"); props != nil {
		for _, row := range props.Children {
			if name, ok := row.Facts["field"].(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

func TestShowHeaderAndPropsShowEachFieldOnce(t *testing.T) {
	w := build(t)
	lane, _, err := w.store.Create(data.CreateInput{Kind: data.KindLane, ID: "lane1", Fields: map[string]any{
		"title": "Polish the Show page", "laneStatus": "running", "summary": "dedupe props", "task": w.project.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []data.Record{w.project, w.sub2, *lane, w.about, w.note, w.pane, w.run} {
		schema := w.schema(rec.Kind)
		for _, layout := range []string{ShowLayoutLedger, ShowLayoutClassic} {
			opts := DefaultShowOptions()
			opts.Layout, opts.Width = layout, 120
			root, err := loadShowWith(w.store, rec.Ref(), "show.header", opts)
			if err != nil {
				t.Fatal(err)
			}
			var listed []string
			if layout == ShowLayoutLedger {
				listed = propsFieldNames(root)
			} else {
				for _, row := range root.Find("show.props.table").Props["rows"].([]any) {
					listed = append(listed, row.([]any)[0].(string))
				}
			}
			header := headerFields(schema, opts)
			for _, name := range listed {
				if header[name] || header[fieldName(schema, name)] {
					t.Errorf("%s %s: field %q is in the header and again in Props", layout, data.RefText(rec.Ref()), name)
				}
			}
			hdr := root.Find("show.header")
			if sum, _ := hdr.Props["summary"].(string); sum != "" {
				t.Errorf("%s %s: header summary %q repeats a field Props lists", layout, data.RefText(rec.Ref()), sum)
			}
		}
	}

	// Opt-out: KeepHeaderFieldsInProps lists the status column again.
	opts := DefaultShowOptions()
	opts.KeepHeaderFieldsInProps = true
	root, err := loadShowWith(w.store, lane.Ref(), "show.header", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(propsFieldNames(root), "laneStatus") {
		t.Fatalf("KeepHeaderFieldsInProps should list laneStatus in Props, got %v", propsFieldNames(root))
	}
}

// fieldName maps a classic props row title back to its field name.
func fieldName(schema data.Schema, title string) string {
	for _, f := range schema.Fields {
		if fieldTitle(schema, f.Name) == title {
			return f.Name
		}
	}
	return title
}

func TestShowLongRelationTitlesStayOnOneRow(t *testing.T) {
	w := build(t)
	long := strings.Repeat("dictated owner voice note about lanes panes and relation rows ", 4) // 248 chars
	multi := "first line of the note\nsecond line\twith a tab\n\n  third line"
	for id, title := range map[string]string{"long1": long, "multi1": multi} {
		if _, _, err := w.store.Create(data.CreateInput{Kind: data.KindTask, ID: id, Fields: map[string]any{
			"title": title, "parent": w.sub2.Ref(), "priority": "P1", "workStatus": "active"}}, owner); err != nil {
			t.Fatal(err)
		}
	}
	for _, size := range [][2]int{{80, 24}, {120, 36}} {
		cols, rows := size[0], size[1]
		opts := DefaultShowOptions()
		opts.Width = cols
		root, err := loadShowWith(w.store, w.sub2.Ref(), "show.header", opts)
		if err != nil {
			t.Fatal(err)
		}
		conn := root.Find("show.connections")
		var longID string
		var walk func(n *ui.ViewNode)
		walk = func(n *ui.ViewNode) {
			if n.Ref != nil && n.Ref.ID != "" && n.Kind == ui.KindText {
				line, _ := n.Props["text"].(string)
				if strings.ContainsAny(line, "\n\t\r") || runeLen(line) > cols-showRowGutter || n.Props["wrap"] != false {
					t.Errorf("%dx%d: relation row %q is not one clean unwrapped row (len %d)", cols, rows, line, runeLen(line))
				}
				if n.Ref.ID == "long1" {
					longID = n.ID
					if !strings.Contains(line, "…") || !strings.HasSuffix(line, "long1") {
						t.Errorf("%dx%d: long title row should end in …  and keep its id: %q", cols, rows, line)
					}
				}
				if n.Ref.ID == "multi1" && !strings.Contains(line, "first line of the note second line with a tab third line") {
					t.Errorf("%dx%d: multi-line title should collapse to one line: %q", cols, rows, line)
				}
			}
			for i := range n.Children {
				walk(&n.Children[i])
			}
		}
		walk(conn)
		if longID == "" {
			t.Fatalf("%dx%d: no relation row for long1", cols, rows)
		}
		// Drawn with the long row focused, it occupies one terminal row.
		frame, _ := ui.Render(root, ui.DefaultDarkTheme(), longID, cols, rows)
		found := false
		for _, line := range strings.Split(stripANSI(frame.ANSI), "\n") {
			if strings.Contains(line, "dictated owner voice") {
				found = true
				if !strings.Contains(line, "long1") {
					t.Errorf("%dx%d: long relation row wrapped:\n%s", cols, rows, stripANSI(frame.ANSI))
				}
			}
		}
		if !found {
			t.Errorf("%dx%d: long relation row not drawn:\n%s", cols, rows, stripANSI(frame.ANSI))
		}
	}
}
