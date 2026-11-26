package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

func TestUniversalFilterableAcrossProjectTasksOutlineWorkTreeNavAndBindings(t *testing.T) {
	a, d := demo(t)
	proj, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Filter Project"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	parentTask, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Auth Migration", "parent": proj.Ref(), "workStatus": "doing"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "PKCE Handshake", "parent": parentTask.Ref(), "workStatus": "todo"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Unrelated Billing", "parent": proj.Ref(), "workStatus": "done"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Project Tab -> Tasks tree (`project.tasks.tree`) filters in place with key:value + child match.
	pc := a.projectCtrl()
	pc.openTabs = append(pc.openTabs, proj.ID)
	pc.setSection(proj.ID, "tasks")
	d.Keys("6")
	a.Shell.State = withFocus(a.State(), projectOutlineTree)
	_ = d.Frame()
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("Project Tab Tasks / did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("status:todo pkce")
	d.Keys("enter")
	if a.Mode() != ModeNormal {
		t.Fatalf("enter after filter did not return to ModeNormal: mode=%q", a.Mode())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Auth Migration") || !strings.Contains(frame, "PKCE Handshake") || strings.Contains(frame, "Unrelated Billing") {
		t.Fatalf("Project Tab Tasks filter did not keep parent+child and hide unrelated:\n%s", frame)
	}

	// 2. Project Left Nav (`project.nav`) filters in place.
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("project.nav / did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("lanes")
	d.Keys("enter")
	if nf := (projectNavFilterable{a: a, projKey: proj.ID}); nf.FilterQuery() != "lanes" {
		t.Fatalf("project.nav filter query = %q, want lanes", nf.FilterQuery())
	}
	d.Keys("/", "esc")

	// 3. 2 Work tree filters in place as a tree by default (filter.inPlaceTree=true).
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTree
	a.cfg.userSettings[SetFilterInPlaceTree] = true
	a.reloadSettings()
	a.applySettings()
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected 2 Work in tree mode")
	}
	d.Keys("/")
	d.Type("pkce")
	d.Keys("enter")
	if !a.inWorkTree(a.State()) {
		t.Fatal("filter.inPlaceTree=true should keep 2 Work in tree mode while filtered")
	}
	if tm := a.trees.workModel(data.KindTask); !tm.IsFiltered() || len(tm.Flatten()) == 0 {
		t.Fatalf("expected work tree filtered with matching rows, query=%q rows=%d", tm.FilterQuery(), len(tm.Flatten()))
	}
	d.Keys("/", "esc")

	// 4. T Outline modal filters directly inside ModeOutline.
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline modal: modal=%q", a.Modal())
	}
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("/ in outline modal did not enter ModeFilter: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Type("pkce")
	d.Keys("enter")
	if a.Mode() != ModeOutline {
		t.Fatalf("enter in outline filter did not return to ModeOutline: mode=%q", a.Mode())
	}
	if tm := a.trees.outlineModel(); tm.FilterQuery() != "pkce" || len(tm.Flatten()) == 0 {
		t.Fatalf("outline tree did not filter for pkce: query=%q rows=%d", tm.FilterQuery(), len(tm.Flatten()))
	}
	d.Keys("q")

	// 5. B Bindings modal respects configured fuzzy match mode and supports multi-word "group toggle".
	a.cfg.userSettings[SetMatchMode] = string(traits.MatchFuzzy)
	a.reloadSettings()
	a.applySettings()
	d.Keys("B")
	if a.Modal() != "bindings" || a.bind.bindEd.FilterMode() != traits.MatchFuzzy {
		t.Fatalf("B modal mode=%q want fuzzy", a.bind.bindEd.FilterMode())
	}
	d.Keys("/")
	d.Type("group toggle")
	d.Keys("enter")
	if got := len(a.bind.bindEd.Filtered()); got < 4 {
		t.Fatalf("B search 'group toggle' matched %d rows, want >= 4", got)
	}
	_ = core.Ref{}
}

func TestFilterBarBottomLeft(t *testing.T) {
	a, d := demo(t)
	proj, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Very Long Project Workspace Name For Breadcrumb Testing"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	pc := a.projectCtrl()
	pc.openTabs = append(pc.openTabs, proj.ID)
	pc.setSection(proj.ID, "home")
	d.Keys("6")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	_ = d.Frame()

	// 1. Project Page project.nav in ModeFilter: bottom line is anchored at col 0
	// and the status breadcrumb does not contain the inline /query.
	d.Keys("/")
	if a.Mode() != ModeFilter {
		t.Fatalf("expected ModeFilter, got %q", a.Mode())
	}
	d.Type("lanes")
	frame := d.Frame()
	lines := strings.Split(frame, "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected short frame (%d lines):\n%s", len(lines), frame)
	}
	statusLine := lines[len(lines)-2]
	bottomLine := lines[len(lines)-1]
	if !strings.HasPrefix(bottomLine, " nav /lanes█  [substring]") {
		t.Fatalf("bottom line not anchored at col 0:\nstatus: %q\nbottom: %q", statusLine, bottomLine)
	}
	if !strings.Contains(bottomLine, "enter keep · esc clear · tab mode") {
		t.Fatalf("bottom line missing filter hints: %q", bottomLine)
	}
	if strings.Contains(statusLine, "/lanes") || strings.Contains(statusLine, "nav /") {
		t.Fatalf("status line should not duplicate inline filter while bottom-left bar is active:\nstatus: %q", statusLine)
	}

	// Cursor motion (ctrl+a) redraws the bottom-left bar via viewKey.
	d.Keys("ctrl+a")
	lines = strings.Split(d.Frame(), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], " nav /█lanes  [substring]") {
		t.Fatalf("ctrl+a did not redraw cursor in filter bar: %q", lines[len(lines)-1])
	}

	// Narrow width clips cleanly without out-of-bounds ToneSpans.
	clippedNode, ok := a.filterBarNode(a.State(), 18)
	if !ok {
		t.Fatal("filterBarNode unexpectedly inactive")
	}
	clippedText, _ := clippedNode.Props["text"].(string)
	maxRunes := len([]rune(clippedText))
	if maxRunes != 18 {
		t.Fatalf("clipped width = %d runes, want 18 (%q)", maxRunes, clippedText)
	}
	if spans, _ := clippedNode.Props["spans"].([]ui.ToneSpan); len(spans) > 0 {
		for _, sp := range spans {
			if sp.From < 0 || sp.From >= sp.To || sp.To > maxRunes {
				t.Fatalf("out-of-bounds span %+v for maxRunes=%d (%q)", sp, maxRunes, clippedText)
			}
		}
	}

	// Keeping the filter in Normal mode shows inline status and restores normal hints.
	d.Keys("enter")
	if a.Mode() != ModeNormal {
		t.Fatalf("expected ModeNormal after enter, got %q", a.Mode())
	}
	lines = strings.Split(d.Frame(), "\n")
	if !strings.Contains(lines[len(lines)-2], "nav /lanes (") {
		t.Fatalf("kept filter missing from status line: %q", lines[len(lines)-2])
	}
	if strings.HasPrefix(lines[len(lines)-1], " nav /lanes") {
		t.Fatalf("bottom line should restore normal hints after enter: %q", lines[len(lines)-1])
	}
	d.Keys("/", "esc")

	// 2. Invalid regex in ModeFilter renders "invalid regex" inside the bottom-left bar.
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	d.Keys("/", "tab", "tab")
	d.Type("[")
	lines = strings.Split(d.Frame(), "\n")
	bottomLine = lines[len(lines)-1]
	if !strings.HasPrefix(bottomLine, " nav /[█  [regex]") || !strings.Contains(bottomLine, "invalid regex") {
		t.Fatalf("invalid regex not rendered cleanly in bottom-left filter bar: %q", bottomLine)
	}
	d.Keys("esc")

	// 3. Opt-out ui.filterBarBottomLeft=false restores the legacy inline status filter.
	a.cfg.userSettings[SetFilterBarBottomLeft] = false
	a.reloadSettings()
	a.applySettings()
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")
	d.Keys("/")
	d.Type("la")
	lines = strings.Split(d.Frame(), "\n")
	statusLine = lines[len(lines)-2]
	bottomLine = lines[len(lines)-1]
	if !strings.Contains(statusLine, "nav /la█ (") {
		t.Fatalf("opt-out did not restore inline filter in status line: %q", statusLine)
	}
	if strings.HasPrefix(bottomLine, " nav /la█") {
		t.Fatalf("opt-out should not render bottom-left filter bar: %q", bottomLine)
	}
	d.Keys("esc")
}
