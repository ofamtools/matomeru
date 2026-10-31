package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func TestNoteMakerCLIAndFilters(t *testing.T) {
	t.Parallel()
	a, _ := cliApp(t)
	actorPane(t, a, "w7", "t1", "codex")
	a.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "matomeru"})
	var thing map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, a, "note", "create", "--json", `{"text":"agent note example","task":"t1"}`)), &thing); err != nil {
		t.Fatal(err)
	}
	n, err := a.work.Note(thing["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	want := work.Actor{Kind: work.ActorAgent, Harness: "codex", Pane: "w7", Session: "matomeru", Task: "t1", Model: "m1"}
	if n.Maker() != want || thing["made-by"] != work.ActorAgent {
		t.Fatalf("agent maker: %+v, %v", n.Maker(), thing)
	}
	a.getenv = envOf(nil)
	mustCLI(t, a, "note", "create", "--json", `{"text":"owner note example"}`)
	if err := a.work.PutNote(work.Note{ID: "legacy", Text: "unknown note example", CreatedBy: work.Actor{Kind: work.ActorUnknown}}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"mine", "agents", "all"} {
		out := mustCLI(t, a, "note", "list", "--all", "--filter", filter)
		for _, maker := range []string{"owner", "agent", "unknown"} {
			visible := filter == "all" || filter == "mine" && maker == "owner" || filter == "agents" && maker == "agent"
			if strings.Contains(out, maker+" note example") != visible {
				t.Fatalf("%s/%s: %s", filter, maker, out)
			}
		}
	}
}

func TestNoteMakerTUIQuickNote(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.getenv = envOf(map[string]string{actorEnv: "agent", "TUIOS_PANE_ID": "w7"})
	press(t, a, "N")
	typeText(t, a, "owner typed this")
	press(t, a, "esc", "esc")
	notes, err := a.work.Notes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Text == "owner typed this" {
			if n.MadeBy() != work.ActorOwner {
				t.Fatalf("maker: %+v", n)
			}
			return
		}
	}
	t.Fatal("quick note not stored")
}

func noteFilterRows(t *testing.T, a *App) {
	t.Helper()
	for _, maker := range []string{work.ActorOwner, work.ActorAgent, work.ActorUnknown} {
		if err := a.work.PutNote(work.Note{ID: "maker-" + maker, ProjectID: "matomeru", TaskID: "t1", Text: maker + " note example", CreatedBy: work.Actor{Kind: maker}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
}

func assertNoteFilterFrame(t *testing.T, a *App, filter string) {
	t.Helper()
	a.m.Width, a.m.Height = 140, 35
	f := ansi.Strip(frameView(a).Content)
	for _, maker := range []string{"owner", "agent", "unknown"} {
		visible := filter == "all" || filter == "mine" && maker == "owner" || filter == "agents" && maker == "agent"
		if strings.Contains(f, maker+" note example") != visible {
			t.Fatalf("%s/%s:\n%s", filter, maker, f)
		}
	}
	if !strings.Contains(f, "["+filter+"]") {
		t.Fatalf("no filter strip:\n%s", f)
	}
}

func TestNoteMakerFiltersPageAndModal(t *testing.T) {
	t.Parallel()
	a := open(t)
	noteFilterRows(t, a)
	notesOn(t, a, "maker-owner")
	for _, name := range []string{"mine", "agents", "all"} {
		press(t, a, "z", "]")
		assertNoteFilterFrame(t, a, name)
	}
	press(t, a, "z", "[")
	assertNoteFilterFrame(t, a, "agents")
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	assertNoteFilterFrame(t, a, "agents")
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "outline", "matomeru", nil)
	if err := a.outline.tree.Select(outlineNode("t1")); err != nil {
		t.Fatal(err)
	}
	if err := a.syncOutlineNotes(false); err != nil {
		t.Fatal(err)
	}
	if err := a.outline.tree.Focus(); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 140, 35
	frameView(a)
	press(t, a, "ctrl+w", "w")
	if a.r.Focus != outlineNotesList+":maker-owner" && a.r.Focus != outlineNotesList+":maker-agent" {
		t.Fatalf("pane focus: %s", a.r.Focus)
	}
	for _, name := range []string{"mine", "agents", "all"} {
		press(t, a, "z", "]")
		assertNoteFilterFrame(t, a, name)
	}
	press(t, a, "z", "[")
	assertNoteFilterFrame(t, a, "agents")
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	assertNoteFilterFrame(t, a, "agents")
	// The same noun's standard verbs resolve the modal row's record key.
	row, _ := a.outline.notes.Selected()
	executeA(t, a, "note", "rename", row.ID, ochazuke.Arguments{"name": "renamed in modal"})
	n, err := a.work.Note("maker-agent")
	if err != nil || n.Title != "renamed in modal" {
		t.Fatalf("rename: %+v %v", n, err)
	}
	// Selected task changes update the pane without carrying over its rows.
	if err := a.outline.tree.Select(outlineNode("t2")); err != nil {
		t.Fatal(err)
	}
	if err := a.syncOutlineNotes(false); err != nil {
		t.Fatal(err)
	}
	if a.outline.notes.Len() != 0 {
		t.Fatal("notes from previous task remain")
	}
	if err := a.r.Settings().Set(settingTaskNotesPane, false); err != nil {
		t.Fatal(err)
	}
	press(t, a, "k")
	if a.r.List(outlineNotesList) != nil {
		t.Fatal("opt-out left notes pane")
	}
}

func TestNoteMakerModalView(t *testing.T) {
	t.Parallel()
	a := open(t)
	noteFilterRows(t, a)
	if _, err := a.work.AddNote("matomeru", "t2", "last task note"); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "outline", "matomeru", nil)
	if err := a.outline.tree.Select(outlineNode("t2")); err != nil {
		t.Fatal(err)
	}
	if err := a.outline.tree.Focus(); err != nil {
		t.Fatal(err)
	}
	if err := a.syncOutlineNotes(false); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 140, 35
	frameView(a)
	press(t, a, "ctrl+w", "w", "enter")
	if a.r.TopModal() != "" || a.r.Page != notesPage || a.noteDocNote == "" {
		t.Fatalf("view left modal or no note: modal=%s page=%s note=%s", a.r.TopModal(), a.r.Page, a.noteDocNote)
	}
}

func TestNoteMakerUndoRetainsProvenance(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{work.ActorUnknown, work.ActorAgent} {
		t.Run(kind, func(t *testing.T) {
			a := open(t)
			n := work.Note{ID: "restore", ProjectID: "matomeru", Text: "restore this", CreatedBy: work.Actor{Kind: kind}}
			if err := a.work.PutNote(n); err != nil {
				t.Fatal(err)
			}
			notesOn(t, a, n.ID)
			executeA(t, a, "note", "delete", n.ID, nil)
			executeA(t, a, "history", "undo", "", nil)
			restored, err := a.work.Note(n.ID)
			if err != nil || restored.MadeBy() != kind {
				t.Fatalf("restored: %+v %v", restored, err)
			}
		})
	}
}
