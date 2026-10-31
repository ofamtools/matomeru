package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
)

// altO is alt+o as the terminal reports it.
var altO = tea.KeyPressMsg{Code: 'o', Mod: tea.ModAlt}

// promptContextApp is promptApp with a file and a link seen in the box's
// pane, and a commit in no project.
func promptContextApp(t *testing.T) (*App, string) {
	t.Helper()
	app, f := promptApp(t)
	file := filepath.Join(t.TempDir(), "main.go")
	for _, it := range []contextitems.Item{
		{Kind: "file", Text: file, Window: f.panes[0].ID},
		{Kind: "link", Text: "https://example.com/notes", Window: f.panes[0].ID},
		{Kind: "commit", Text: "1ba025f", Window: "elsewhere"},
	} {
		if _, _, err := app.contexts.Merge([]contextitems.Item{it}, contextitems.Owner{}, 1); err != nil {
			t.Fatal(err)
		}
	}
	return app, file
}

// openBoxPicker presses alt+o in the box and checks the picker opened.
func openBoxPicker(t *testing.T, app *App) {
	t.Helper()
	app.Update(altO)
	if app.r.TopModal() != contextPicker {
		t.Fatalf("alt+o opened %q (status %q)", app.r.TopModal(), app.m.Status)
	}
}

// alt+o opens the context picker over the box's pane, from insert and
// normal mode; enter writes a file as @path at the cursor, mid-line too,
// and the box keeps the focus in insert mode.
func TestPromptBoxAltOInsertsAFileAtTheCursor(t *testing.T) {
	t.Parallel()
	app, file := promptContextApp(t)
	typeText(t, app, "read  now")
	app.pb.input.Cursor = len("read ")
	openBoxPicker(t, app)
	if texts := pickerTexts(t, app); !slices.Contains(texts, file) || !slices.Contains(texts, "1ba025f") {
		t.Fatalf("rows %q", texts)
	}
	typeText(t, app, "main.go")
	settle(app, key(t, app, "enter"))
	want := "read @" + file + " now"
	if got := string(app.pb.input.Text); got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
	if app.r.Focus != promptID || app.r.BaseMode() != "insert" || app.pb.input.Cursor != len([]rune("read @"+file)) {
		t.Fatalf("focus %s mode %s cursor %d", app.r.Focus, app.r.BaseMode(), app.pb.input.Cursor)
	}
	// From normal mode too: it writes at the cursor and leaves insert on.
	press(t, app, "esc")
	if app.r.BaseMode() != "normal" {
		t.Fatalf("esc: mode %s", app.r.BaseMode())
	}
	openBoxPicker(t, app)
	typeText(t, app, "1ba0")
	settle(app, key(t, app, "enter"))
	if got := string(app.pb.input.Text); !strings.Contains(got, "1ba025f") || app.r.BaseMode() != "insert" || app.r.Focus != promptID {
		t.Fatalf("normal mode: %q focus %s mode %s", got, app.r.Focus, app.r.BaseMode())
	}
}

// A link is written as its text, with no @.
func TestPromptBoxAltOInsertsALinksText(t *testing.T) {
	t.Parallel()
	app, _ := promptContextApp(t)
	typeText(t, app, "see ")
	openBoxPicker(t, app)
	typeText(t, app, "notes")
	settle(app, key(t, app, "enter"))
	if got := string(app.pb.input.Text); got != "see https://example.com/notes" {
		t.Fatalf("text %q", got)
	}
}

// esc closes the picker and leaves the box as it was.
func TestPromptBoxAltOEscLeavesTheText(t *testing.T) {
	t.Parallel()
	app, _ := promptContextApp(t)
	typeText(t, app, "keep me")
	openBoxPicker(t, app)
	typeText(t, app, "notes")
	press(t, app, "esc")
	if app.r.TopModal() == contextPicker || app.r.Focus != promptID {
		t.Fatalf("esc: modal %q focus %s", app.r.TopModal(), app.r.Focus)
	}
	if got := string(app.pb.input.Text); got != "keep me" {
		t.Fatalf("text %q", got)
	}
}

// The footer names the key, and the verb is the palette's (and pick's
// into=prompt reaches the same picker).
func TestPromptBoxContextHintAndVerb(t *testing.T) {
	t.Parallel()
	app, _ := promptContextApp(t)
	if hints := hintKeys(app); !slices.Contains(hints, "alt+o context") {
		t.Fatalf("footer %q", hints)
	}
	if _, ok := app.r.Operations[app.r.CanonicalKey(promptNoun+".context")]; !ok {
		t.Fatal("no turn-prompt.context verb")
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "pick", Noun: contextNoun, Args: ochazuke.Arguments{"into": "prompt"}}); err != nil || app.r.TopModal() != contextPicker {
		t.Fatalf("pick into=prompt: %v, modal %q", err, app.r.TopModal())
	}
}

// The picker's own keys work from the box: ctrl+o opens the item and the
// box's text is left alone.
func TestPromptBoxAltOPickerKeepsItsKeys(t *testing.T) {
	t.Parallel()
	app, _ := promptContextApp(t)
	sys := &fakeSystem{}
	app.cx.sys = sys
	typeText(t, app, "x")
	openBoxPicker(t, app)
	typeText(t, app, "notes")
	settle(app, key(t, app, "ctrl+o"))
	if !slices.Equal(sys.opened, []string{"https://example.com/notes"}) || string(app.pb.input.Text) != "x" {
		t.Fatalf("ctrl+o opened %v, text %q", sys.opened, string(app.pb.input.Text))
	}
}
