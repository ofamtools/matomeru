package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func TestScaffoldProjectArchivePersistsWithoutOrphaning(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	dir := t.TempDir()
	executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": dir})
	executeA(t, a, "project", "archive", "", ochazuke.Arguments{"id": "matomeru"})
	for _, item := range a.projectList.Items() {
		if item.ID == "matomeru" {
			t.Fatal("archived project remains in the curated Projects list")
		}
	}
	other := openAt(t, path)
	p, err := other.work.Project("matomeru")
	if err != nil || !p.Archived || len(p.Trees) != 1 || p.Trees[0].Path != dir {
		t.Fatalf("reopened project lost archive or directories: %+v %v", p, err)
	}
	for _, id := range []string{"t1", "t2"} {
		task, err := other.work.Task(id)
		if err != nil || task.ProjectID != "matomeru" || task.Archived {
			t.Fatalf("project archive changed task %s: %+v %v", id, task, err)
		}
	}
	executeA(t, other, "project", "restore", "", ochazuke.Arguments{"id": "matomeru"})
	if err := other.projectList.Select("matomeru"); err != nil {
		t.Fatalf("restored project is not selectable: %v", err)
	}
}

func TestScaffoldPaneArchiveIsLocalAcrossListingsAndReopen(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openWith(t, Options{Data: path})
	f := a.panesd.(*fakePanes)
	a.setPanes(f.panes, nil)
	executeA(t, a, "pane", "archive", "", ochazuke.Arguments{"id": "w1"})
	if a.sectionOf("pane:matomeru/w1") >= 0 {
		t.Fatal("archived pane remains in the curated Panes lists")
	}
	a.setPanes(f.panes, nil)
	got := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if got["archived"] != true || len(f.panes) != 2 || len(f.killed) != 0 {
		t.Fatalf("archive changed daemon state or was overwritten: %v, killed %v", got, f.killed)
	}
	other := openWith(t, Options{Data: path})
	fresh := other.panesd.(*fakePanes)
	other.setPanes(fresh.panes, nil)
	got = executeA(t, other, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if got["archived"] != true {
		t.Fatalf("archive lost on reopen: %v", got)
	}
	executeA(t, other, "pane", "restore", "", ochazuke.Arguments{"id": "w1"})
	if other.sectionOf("pane:matomeru/w1") < 0 || len(fresh.killed) != 0 {
		t.Fatal("restore failed to reveal the existing daemon pane")
	}
}

func TestScaffoldWorkFormFollowsSelectionAndCommits(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	projectsOn(t, a, "matomeru")
	a.detail.work.show.Sync()
	if e := a.detail.work.show.Form(); e == nil || e.Values()["name"] != "matomeru" {
		t.Fatalf("project selection did not load the show form: %v", e)
	}
	executeA(t, a, "project", "open", "matomeru", nil)
	if err := a.taskList.Select("t1"); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.detail.work.show.Sync()
	e := a.detail.work.show.Form()
	if e == nil || e.Values()["title"] != "write the brief" {
		t.Fatalf("task selection did not replace the project form: %v", e)
	}
	frameWH(a, 160, 60)
	if err := a.r.SetFocus(e.ID() + "/title"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "i", "ctrl+a", "ctrl+k")
	typeText(t, a, "edited in Work")
	press(t, a, "esc")
	if task, err := a.work.Task("t1"); err != nil || task.Title != "edited in Work" {
		t.Fatalf("commit-on-change: %+v %v\n%s", task, err, frame(a))
	}
	if a.detail.work.show.Form() != e {
		t.Fatal("focusing the form changed its subject")
	}
	other := openAt(t, path)
	if task, err := other.work.Task("t1"); err != nil || task.Title != "edited in Work" {
		t.Fatalf("show-form edit did not persist: %+v %v", task, err)
	}
	if text := a.detail.work.relatedText(120); !strings.Contains(text, "History") || !strings.Contains(text, "task.set") {
		t.Fatalf("changed task lacks its history section:\n%s", text)
	}
	press(t, a, "u")
	if task, err := a.work.Task("t1"); err != nil || task.Title != "write the brief" {
		t.Fatalf("form undo: %+v %v", task, err)
	}
}

func TestScaffoldShowFormUsesRelatedSettingsAndCustomNoteView(t *testing.T) {
	t.Parallel()
	a := open(t)
	executeA(t, a, "note", "create", "", ochazuke.Arguments{"title": "stored note", "text": "note body", "about": "task:t1"})
	custom, err := a.r.AddList("work/custom-notes", []ochazuke.ListItem{{ID: "work/custom-notes/one", Label: "custom note renderer", Noun: "note"}}, ochazuke.Parent("work"))
	if err != nil {
		t.Fatal(err)
	}
	a.r.Define("note").RelatedView(custom.ID())
	projectsOn(t, a, "matomeru")
	executeA(t, a, "project", "open", "matomeru", nil)
	if err := a.taskList.Select("t1"); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.detail.work.show.Sync()
	executeA(t, a, "task", "set", "", ochazuke.Arguments{"id": "t1", "props": map[string]any{"title": "task with history"}})
	text := a.detail.work.relatedText(120)
	if strings.Count(text, "custom note renderer") != 1 || strings.Contains(text, "stored note") || strings.LastIndex(text, "History") <= strings.Index(text, "custom note renderer") {
		t.Fatalf("custom view or last history section lost:\n%s", text)
	}
	form := a.detail.work.show.Form()
	if err := custom.Focus(); err != nil {
		t.Fatal(err)
	}
	a.detail.work.show.Sync()
	if a.detail.work.show.Form() != form || form.Values()["title"] != "task with history" {
		t.Fatal("entering the embedded note view changed the form's task")
	}
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
	setting(t, a, settingRelatedHidden, []string{"note.*", "ochazuke.*"})
	if text := a.detail.work.relatedText(120); strings.Contains(text, "custom note renderer") || strings.Contains(text, "History") {
		t.Fatalf("explicit hidden patterns were bypassed:\n%s", text)
	}
	setting(t, a, settingRelatedHidden, []string{})
	setting(t, a, ochazuke.RelatedDetailSetting, false)
	if text := a.detail.work.relatedText(120); text != "" {
		t.Fatalf("disabled stack still draws: %s", text)
	}
	setting(t, a, ochazuke.RelatedDetailSetting, true)
	setting(t, a, settingRelatedEmpty, ochazuke.RelatedEmptyHeader)
	if text := a.detail.work.relatedText(120); !strings.Contains(text, "custom note renderer") {
		t.Fatalf("reenabling stack lost the custom note view:\n%s", text)
	}
}

func TestScaffoldFormOptOutKeepsCuratedViews(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[on], func(t *testing.T) {
			t.Parallel()
			a, f := panesApp(t)
			setting(t, a, ochazuke.DetailShowFormSetting, on)
			if err := a.selectPaneRow("pane:matomeru/w1"); err != nil {
				t.Fatal(err)
			}
			a.detail.panes.show.Sync()
			a.pv.tab = tabScreen
			a.preview = preview{Pane: "pane:matomeru/w1", Text: "preserved screen"}
			if text := frameWH(a, 160, 60); !strings.Contains(text, "preserved screen") {
				t.Fatalf("screen lost with form=%v:\n%s", on, text)
			}
			if (a.detail.panes.show.Form() != nil) != on {
				t.Fatalf("pane form opt-out ignored: %v", on)
			}
			executeA(t, a, "pane", "attach", "pane:matomeru/w1", nil)
			if frameWH(a, 160, 60); a.r.Terminal(paneTermID) == nil || len(f.streams) == 0 {
				t.Fatal("show form replaced the live terminal")
			}
			pressAll(t, a, "ctrl+a", "[")
			if frameWH(a, 160, 60); a.r.FocusID() != copyID || a.r.Terminal(paneTermID) == nil {
				t.Fatal("copy mode lost the live terminal")
			}
			pressAll(t, a, "esc")
			projectsOn(t, a, "matomeru")
			a.detail.work.show.Sync()
			if (a.detail.work.show.Form() != nil) != on {
				t.Fatalf("Work form opt-out ignored: %v", on)
			}
			if text := frameWH(a, 160, 60); !strings.Contains(text, "matomeru") || !strings.Contains(text, "projects") {
				t.Fatalf("opt-out lost Projects or Work detail:\n%s", text)
			}
		})
	}
}

func TestScaffoldPanesTurnsRemainAboveTheForm(t *testing.T) {
	t.Parallel()
	a, _, _ := claudeTurnsApp(t)
	useTurnStyle(t, a, turnsStyleTree)
	for _, on := range []bool{false, true} {
		setting(t, a, ochazuke.DetailShowFormSetting, on)
		a.detail.panes.show.Sync()
		text := frameWH(a, 160, 60)
		if !strings.Contains(text, "fix the flaky test") || (a.detail.panes.show.Form() != nil) != on {
			t.Fatalf("turns and form do not coexist (on=%v):\n%s", on, text)
		}
	}
}
