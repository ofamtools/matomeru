package views

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
)

func TestResolveRecordStyleLanesAndOptOut(t *testing.T) {
	reg := data.DefaultSchemas()
	laneSchema, ok := reg.Get(data.KindLane)
	if !ok {
		t.Fatal("missing lane schema")
	}

	cases := []struct {
		status   string
		rowTone  string
		cellTone string
		icon     string
		iconTone string
	}{
		{"planned", "row.muted", "state.planned", "○", "state.planned"},
		{"running", "row.active", "state.working", "▶", "state.working"},
		{"review", "row.attention", "state.review", "◉", "state.review"},
		{"merged", "row.dim", "state.done.dim", "✓", "state.done.dim"},
		{"done", "row.dim", "state.done.dim", "✓", "state.done.dim"},
		{"parked", "row.muted", "muted", "⏸", "muted"},
		{"cancelled", "row.cancelled", "state.cancelled", "✕", "state.cancelled"},
	}

	for _, tc := range cases {
		rec := data.Record{
			Kind:          data.KindLane,
			ID:            "l-" + tc.status,
			Fields:        map[string]any{"title": "Lane " + tc.status, "laneStatus": tc.status},
			ArchiveStatus: data.ArchiveActive,
			StarStatus:    data.StarUnstarred,
		}
		st := ResolveRecordStyle(laneSchema, rec, DefaultStyleOptions())
		if st.RowTone != tc.rowTone || st.Icon != tc.icon || st.IconTone != tc.iconTone {
			t.Errorf("lane %s: got row=%q icon=%q iconTone=%q, want row=%q icon=%q iconTone=%q",
				tc.status, st.RowTone, st.Icon, st.IconTone, tc.rowTone, tc.icon, tc.iconTone)
		}
		if st.CellTones["laneStatus"] != tc.cellTone || st.CellIcons["laneStatus"] != tc.icon || st.IconTones["laneStatus"] != tc.iconTone {
			t.Errorf("lane %s cells: tones=%v icons=%v iconTones=%v", tc.status, st.CellTones, st.CellIcons, st.IconTones)
		}
	}

	running := data.Record{
		Kind:          data.KindLane,
		ID:            "l-run",
		Fields:        map[string]any{"title": "Running lane", "laneStatus": "running"},
		ArchiveStatus: data.ArchiveActive,
		StarStatus:    data.StarUnstarred,
	}
	// Opt-out RowSemanticStyling: tones cleared, icons preserved.
	noRow := ResolveRecordStyle(laneSchema, running, StyleOptions{RowSemanticStyling: false, StatusIcons: true})
	if noRow.RowTone != "" || len(noRow.CellTones) != 0 || len(noRow.IconTones) != 0 || noRow.Icon != "▶" || noRow.CellIcons["laneStatus"] != "▶" {
		t.Fatalf("RowSemanticStyling=false got %+v", noRow)
	}
	// Opt-out StatusIcons: icons cleared, tones preserved.
	noIcons := ResolveRecordStyle(laneSchema, running, StyleOptions{RowSemanticStyling: true, StatusIcons: false})
	if noIcons.Icon != "" || len(noIcons.CellIcons) != 0 || len(noIcons.IconTones) != 0 || noIcons.RowTone != "row.active" || noIcons.CellTones["laneStatus"] != "state.working" {
		t.Fatalf("StatusIcons=false got %+v", noIcons)
	}
	// Archived overrides RowTone to row.dim when RowSemanticStyling is enabled.
	archived := running
	archived.ArchiveStatus = data.ArchiveArchived
	archStyle := ResolveRecordStyle(laneSchema, archived, DefaultStyleOptions())
	if !archStyle.Archived || archStyle.RowTone != "row.dim" {
		t.Fatalf("archived lane style = %+v, want row.dim", archStyle)
	}
}

func TestStyleRulesBadgeRulesAndFactFields(t *testing.T) {
	reg := data.DefaultSchemas()

	// Task StyleRules: P0 -> error, P1 -> warning.
	taskSchema, _ := reg.Get(data.KindTask)
	p0 := data.Record{Kind: data.KindTask, ID: "t0", Fields: map[string]any{"title": "Urgent", "workStatus": "active", "priority": "P0"}}
	p1 := data.Record{Kind: data.KindTask, ID: "t1", Fields: map[string]any{"title": "High", "workStatus": "todo", "priority": "P1"}}
	if st := ResolveRecordStyle(taskSchema, p0, DefaultStyleOptions()); st.CellTones["priority"] != "error" || st.RowTone != "row.active" {
		t.Fatalf("P0 style = %+v", st)
	}
	if st := ResolveRecordStyle(taskSchema, p1, DefaultStyleOptions()); st.CellTones["priority"] != "warning" || st.RowTone != "row.muted" {
		t.Fatalf("P1 style = %+v", st)
	}

	// Schedule FactFields, BadgeRules, StyleRules.
	schedSchema, _ := reg.Get(data.KindSchedule)
	activeSched := data.Record{Kind: data.KindSchedule, ID: "s1", Fields: map[string]any{"title": "Daily"}}
	pausedSched := data.Record{Kind: data.KindSchedule, ID: "s2", Fields: map[string]any{"title": "Nightly", "paused": true}}
	if facts := RecordFacts(schedSchema, activeSched, time.Now()); facts["paused"] != false {
		t.Fatalf("active schedule paused fact = %#v, want false", facts["paused"])
	}
	if facts := RecordFacts(schedSchema, pausedSched, time.Now()); facts["paused"] != true {
		t.Fatalf("paused schedule paused fact = %#v, want true", facts["paused"])
	}
	if st := ResolveRecordStyle(schedSchema, pausedSched, DefaultStyleOptions()); st.RowTone != "row.muted" {
		t.Fatalf("paused schedule RowTone = %q, want row.muted", st.RowTone)
	}
	if st := ResolveRecordStyle(schedSchema, activeSched, DefaultStyleOptions()); st.RowTone != "" {
		t.Fatalf("active schedule RowTone = %q, want empty", st.RowTone)
	}
	badges := StatusBadges(schedSchema, pausedSched)
	foundPaused := false
	for _, b := range badges {
		if b.Text == "paused" && b.Tone == "warning" {
			foundPaused = true
		}
	}
	if !foundPaused {
		t.Fatalf("paused schedule badges = %+v, want paused warning badge", badges)
	}
	for _, b := range StatusBadges(schedSchema, activeSched) {
		if b.Text == "paused" {
			t.Fatalf("active schedule should not have paused badge: %+v", b)
		}
	}

	// Run StyleRule: exitCode != 0 && exitCode != null -> error.
	runSchema, _ := reg.Get(data.KindRun)
	okRun := data.Record{Kind: data.KindRun, ID: "r0", Fields: map[string]any{"title": "ok", "runStatus": "done", "exitCode": 0}}
	nilRun := data.Record{Kind: data.KindRun, ID: "rn", Fields: map[string]any{"title": "queued", "runStatus": "queued"}}
	badRun := data.Record{Kind: data.KindRun, ID: "r1", Fields: map[string]any{"title": "fail", "runStatus": "failed", "exitCode": 2}}
	if st := ResolveRecordStyle(runSchema, okRun, DefaultStyleOptions()); st.CellTones["exitCode"] != "" {
		t.Fatalf("exitCode=0 should not error: %+v", st.CellTones)
	}
	if st := ResolveRecordStyle(runSchema, nilRun, DefaultStyleOptions()); st.CellTones["exitCode"] != "" {
		t.Fatalf("nil exitCode should not error: %+v", st.CellTones)
	}
	if st := ResolveRecordStyle(runSchema, badRun, DefaultStyleOptions()); st.CellTones["exitCode"] != "error" {
		t.Fatalf("exitCode=2 style = %+v, want error", st.CellTones)
	}

	// PickerFromRecordsWith respects StatusIcons flag.
	pmOn := PickerFromRecordsWith(reg, "Tasks", []data.Record{p0}, DefaultStyleOptions())
	if items := pmOn.Filtered(); len(items) != 1 || !strings.HasPrefix(items[0].Title, "▶ ") {
		t.Fatalf("picker with StatusIcons=true: %+v", items)
	}
	pmOff := PickerFromRecordsWith(reg, "Tasks", []data.Record{p0}, StyleOptions{RowSemanticStyling: true, StatusIcons: false})
	if items := pmOff.Filtered(); len(items) != 1 || strings.HasPrefix(items[0].Title, "▶ ") {
		t.Fatalf("picker with StatusIcons=false: %+v", items)
	}
}

// TestNoPerNounViewConditionals scans every non-test Go file in views/ and
// asserts that generic presenter files contain zero references to concrete
// noun kinds and that no file in views/ introduces per-noun conditionals or
// hardcoded extension kinds.
func TestNoPerNounViewConditionals(t *testing.T) {
	forbiddenIdents := map[string]bool{
		"KindTask":     true,
		"KindLane":     true,
		"KindPane":     true,
		"KindPage":     true,
		"KindSchedule": true,
	}
	// Dedicated single-widget helpers (lane progress bar & pane site) legitimately
	// reference their own widget noun when querying or scoping; generic presenters
	// (views.go, show.go, style.go, outline.go) must have ZERO references to any
	// forbidden Kind* symbol, and NO file may reference KindSchedule or "df.culprit".
	widgetOwnKinds := map[string]map[string]bool{
		"lane.go": {"KindLane": true},
		"site.go": {"KindPane": true, "KindPage": true},
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		allowed := widgetOwnKinds[name]
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if forbiddenIdents[x.Name] && !allowed[x.Name] {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d: forbidden noun reference %q in views/", name, pos.Line, x.Name)
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING && strings.Contains(x.Value, "df.culprit") {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d: forbidden hardcoded extension kind %s in views/", name, pos.Line, x.Value)
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no non-test Go files scanned in views/")
	}
}
