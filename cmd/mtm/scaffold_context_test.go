package main

import (
	"fmt"
	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/ochazuke"
	"strings"
	"testing"
)

func TestScaffoldContextNewFormAndUnassociatedSave(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	press(t, a, "n")
	e := a.r.RecordEditor(ochazuke.ArgsFormID)
	if e == nil {
		t.Fatalf("new did not ask for a record:\n%s", frame(a))
	}
	for _, name := range []string{"text", "type", "project"} {
		if a.r.Instances[ochazuke.ArgsFormID+"/"+name] == nil {
			t.Fatalf("new form lacks %s", name)
		}
	}
	typeText(t, a, "unassociated context")
	press(t, a, "enter")
	items, err := a.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Text == "unassociated context" {
			if it.ProjectID != "" || it.TaskID != "" {
				t.Fatalf("unassociated save gained an owner: %+v", it)
			}
			return
		}
	}
	t.Fatalf("new form did not save: %+v\n%s", items, frame(a))
}

func TestScaffoldContextNewFormUsesListScope(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	if err := a.cx.list.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "n")
	e := a.r.RecordEditor(ochazuke.ArgsFormID)
	if e == nil || e.Values()["project"] != "matomeru" {
		t.Fatalf("scoped new: form %v\n%s", e, frame(a))
	}
}

func TestScaffoldContextGeneratedEditAndShowOwner(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	selectContext(t, a, "/tmp/matomeru/main.go")
	row := a.r.Instances[a.r.FocusID()]
	id := savedRowKey(row)
	press(t, a, "i")
	e := editing(a)
	if e == nil {
		t.Fatalf("generated edit did not open:\n%s", frame(a))
	}
	for _, name := range []string{"text", "type", "project", "task"} {
		if a.r.Instances[editFormID+"/"+name] == nil {
			t.Fatalf("edit form lacks %s", name)
		}
	}
	if err := a.r.SetFocus(editFormID + "/text"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "i", "ctrl+a", "ctrl+k")
	typeText(t, a, "https://example.com/edited")
	press(t, a, "esc", "esc")
	got, err := a.contexts.Get(id)
	if err != nil || got.Text != "https://example.com/edited" {
		t.Fatalf("edited context: %+v %v", got, err)
	}
	if err := a.r.Pop(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenIndex(contextNoun, id); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); !strings.Contains(fr, "project") || !strings.Contains(fr, "matomeru") {
		t.Fatalf("show hides owner:\n%s", fr)
	}
}

func TestScaffoldContextAddExplicitProjectWinsOverCallingPane(t *testing.T) {
	t.Parallel()
	a, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	var result extracted
	cliJSON(t, a, &result, contextNoun, "add", "--text", "https://example.com/explicit", "--kind", "link", "--project", "ochazuke")
	if len(result.Items) != 1 {
		t.Fatalf("add: %+v", result)
	}
	got, err := a.contexts.Get(result.Items[0].ID)
	if err != nil || got.ProjectID != "ochazuke" || got.TaskID != "" {
		t.Fatalf("explicit project: %+v %v", got, err)
	}
}

func TestScaffoldContextCuratedPageWindowsPinnedLegacyRows(t *testing.T) {
	t.Parallel()
	a := open(t)
	bulkRows(t, a, 20000, 0)
	for i, owner := range []contextitems.Owner{{ProjectID: "matomeru"}, {TaskID: "t1"}, {ProjectID: "ochazuke"}} {
		_, _, err := a.contexts.Merge([]contextitems.Item{{Kind: "link", Text: fmt.Sprintf("https://example.com/scoped-%d", i)}}, owner, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
	}
	reads := a.db.FullReads(contextitems.Table.Name)
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	if err := a.r.OpenPage(contextPage); err != nil {
		t.Fatal(err)
	}
	_ = frame(a)
	if !a.cx.list.Windowed() || a.cx.list.WindowTotal() != 2 {
		t.Fatalf("scoped window: %v %d", a.cx.list.Windowed(), a.cx.list.WindowTotal())
	}
	if got := a.db.FullReads(contextitems.Table.Name) - reads; got != 0 {
		t.Fatalf("opening Context made %d full reads", got)
	}
}
