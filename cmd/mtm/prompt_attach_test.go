package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/harness"
)

// fakeClipboard stands in for the OS clipboard: an image is written to
// the path the box asks for, as the real reader does.
type fakeClipboard struct {
	image bool
	files []string
	text  string
	err   error
	asked []string // the image paths asked for
}

func (c *fakeClipboard) Read(_ context.Context, imagePath string) (clipboardContent, error) {
	c.asked = append(c.asked, imagePath)
	switch {
	case c.err != nil:
		return clipboardContent{}, c.err
	case len(c.files) > 0:
		return clipboardContent{Files: c.files}, nil
	case c.image:
		if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
			return clipboardContent{}, err
		}
		return clipboardContent{Image: imagePath}, nil
	}
	return clipboardContent{Text: c.text}, nil
}

func clipApp(t *testing.T) (*App, *fakePanes, *fakeClipboard) {
	t.Helper()
	app, f := promptApp(t)
	clip := &fakeClipboard{}
	app.pb.clip = clip
	return app, f, clip
}

// ctrl+v with an image on the clipboard saves it under the data
// directory, writes a chip at the cursor and counts it on the rule.
func TestPromptPasteImageInsertsAChip(t *testing.T) {
	t.Parallel()
	app, _, clip := clipApp(t)
	clip.image = true
	typeText(t, app, "look at  please")
	app.pb.input.Cursor = len("look at ")
	pressSettle(t, app, "ctrl+v")
	if got := string(app.pb.input.Text); got != "look at [image 1] please" {
		t.Fatalf("text %q", got)
	}
	if app.r.Focus != promptID || app.r.BaseMode() != "insert" {
		t.Fatalf("focus %s %s", app.r.Focus, app.r.BaseMode())
	}
	live := app.pb.live()
	if len(live) != 1 || live[0].path != clip.asked[0] {
		t.Fatalf("attachments %+v, asked %q", live, clip.asked)
	}
	want := filepath.Join(filepath.Dir(app.dataPath), "attachments", "w1")
	if filepath.Dir(live[0].path) != want || filepath.Ext(live[0].path) != ".png" {
		t.Fatalf("saved at %s, want under %s", live[0].path, want)
	}
	if _, err := os.Stat(live[0].path); err != nil {
		t.Fatal(err)
	}
	// An attach is a success: a notice naming the chip and the file, not
	// the error line (owner, 2026-10-28: it read "Error: attached ...").
	wantNotice := "attached [image 1] (" + filepath.Base(live[0].path) + ")"
	if app.m.Status != "" || !slices.Contains(app.m.Notices, wantNotice) {
		t.Fatalf("status %q notices %q, want notice %q", app.m.Status, app.m.Notices, wantNotice)
	}
	if f := frame(app); strings.Contains(f, "Error:") || !strings.Contains(f, "Notice: attached [image 1]") {
		t.Fatalf("an attach reads as an error or not at all:\n%s", f)
	}
	if r := boxRule(t, app); !strings.Contains(r, "1 image") {
		t.Fatalf("rule %q", r)
	}
	pressSettle(t, app, "ctrl+v")
	if got := string(app.pb.input.Text); !strings.Contains(got, "[image 2]") || len(app.pb.live()) != 2 {
		t.Fatalf("second: %q %+v", got, app.pb.live())
	}
	if r := boxRule(t, app); !strings.Contains(r, "2 images") {
		t.Fatalf("rule %q", r)
	}
}

// Files copied in Finder: an image file is attached by its own path,
// any other file goes in as @path (quoted with a space).
func TestPromptPasteFiles(t *testing.T) {
	t.Parallel()
	app, _, clip := clipApp(t)
	clip.files = []string{"/shots/a.PNG", "/src/main.go", "/My Docs/notes.txt"}
	pressSettle(t, app, "ctrl+v")
	if got := string(app.pb.input.Text); got != `[image 1] @/src/main.go @"/My Docs/notes.txt" ` {
		t.Fatalf("text %q", got)
	}
	if live := app.pb.live(); len(live) != 1 || live[0].path != "/shots/a.PNG" {
		t.Fatalf("attachments %+v", live)
	}
	if app.m.Status != "" || !slices.Contains(app.m.Notices, "attached [image 1]") {
		t.Fatalf("status %q notices %q", app.m.Status, app.m.Notices)
	}
}

// Text on the clipboard is a plain paste; an empty one is a plain status
// line and an unreadable one the error line, never a modal, and the box
// keeps its text.
func TestPromptPasteTextEmptyAndUnreadable(t *testing.T) {
	t.Parallel()
	app, _, clip := clipApp(t)
	clip.text = "hello there"
	pressSettle(t, app, "ctrl+v")
	if got := string(app.pb.input.Text); got != "hello there" || len(app.pb.live()) != 0 {
		t.Fatalf("text %q", got)
	}
	clip.text = ""
	pressSettle(t, app, "ctrl+v")
	if !strings.Contains(app.m.Info, "the clipboard is empty") || string(app.pb.input.Text) != "hello there" {
		t.Fatalf("empty: %q %q", app.m.Info, string(app.pb.input.Text))
	}
	if fr := frame(app); app.m.Status != "" || strings.Contains(fr, "Error:") || !strings.Contains(fr, "the clipboard is empty") {
		t.Fatalf("an empty clipboard is plain, not the error line: status %q\n%s", app.m.Status, fr)
	}
	clip.err = errors.New("osascript: execution error")
	pressSettle(t, app, "ctrl+v")
	if !strings.Contains(app.m.Status, "could not read the clipboard: osascript: execution error") || app.r.Focus != promptID {
		t.Fatalf("unreadable: %q focus %s", app.m.Status, app.r.Focus)
	}
	if m := app.r.TopModal(); m != "" {
		t.Fatalf("a modal opened: %s", m)
	}
}

// Deleting a chip detaches its image; undo brings it back; detach
// removes one by number.
func TestPromptAttachmentRemoval(t *testing.T) {
	t.Parallel()
	app, _, clip := clipApp(t)
	clip.image = true
	pressSettle(t, app, "ctrl+v")
	pressSettle(t, app, "ctrl+v")
	if len(app.pb.live()) != 2 {
		t.Fatalf("attachments %+v", app.pb.live())
	}
	press(t, app, "ctrl+w", "ctrl+w") // kills "] " back to "[image 2"…
	text := string(app.pb.input.Text)
	if strings.Contains(text, "[image 2]") {
		t.Fatalf("still has the chip: %q", text)
	}
	if live := app.pb.live(); len(live) != 1 || live[0].n != 1 {
		t.Fatalf("after delete: %+v (%q)", live, text)
	}
	if err := app.pb.input.Replace(0, len(app.pb.input.Text), "[image 1] and [image 2] "); err != nil {
		t.Fatal(err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "detach", Noun: promptNoun, Args: ochazuke.Arguments{"n": 1}}); err != nil {
		t.Fatal(err)
	}
	if got := string(app.pb.input.Text); got != "and [image 2] " || len(app.pb.live()) != 1 {
		t.Fatalf("detach 1: %q %+v", got, app.pb.live())
	}
	if _, err := app.r.Execute(ochazuke.Request{Verb: "detach", Noun: promptNoun}); err != nil {
		t.Fatal(err)
	}
	if got := string(app.pb.input.Text); got != "and " || len(app.pb.live()) != 0 {
		t.Fatalf("detach the only one: %q", got)
	}
}

// Send: Claude Code (and omp) read an @ mention of the path at submit, so
// each chip goes out as @/abs/path in the one paste; the attachment is
// kept as a file context item of the pane, and the box's list empties.
func TestPromptSendWritesMentionsAndRecordsContext(t *testing.T) {
	t.Parallel()
	app, f, clip := clipApp(t)
	clip.image = true
	typeText(t, app, "what is")
	pressSettle(t, app, "ctrl+v")
	typeText(t, app, "showing?")
	path := app.pb.live()[0].path
	pressSettle(t, app, "enter")
	want := "submit w1 what is @" + path + " showing?"
	if strings.Join(f.prompts, "|") != want {
		t.Fatalf("calls %q, want %q", f.prompts, want)
	}
	if len(app.pb.attachments) != 0 || len(app.pb.input.Text) != 0 {
		t.Fatalf("after send: %+v %q", app.pb.attachments, string(app.pb.input.Text))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file went: %v", err)
	}
	items, err := app.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.Kind == "file" && it.Text == path && it.Window == "w1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no file context item for %s: %+v", path, items)
	}
}

// Queue mode always writes @ mentions, whatever the harness: tuios types
// the prompt later as one paste. Interrupt mode does the same for a
// harness that takes mentions.
func TestPromptQueueAndInterruptCarryMentions(t *testing.T) {
	t.Parallel()
	app, f, clip := clipApp(t)
	clip.image = true
	f.panes[0].AgentState = "working"
	reload(t, app)
	press(t, app, "ctrl+t") // queue
	pressSettle(t, app, "ctrl+v")
	typeText(t, app, "after this")
	path := app.pb.live()[0].path
	pressSettle(t, app, "enter")
	if want := "queue w1 @" + path + " after this"; strings.Join(f.prompts, "|") != want {
		t.Fatalf("queue %q, want %q", f.prompts, want)
	}
	f.prompts = nil
	press(t, app, "i")      // the send left the box in normal mode
	press(t, app, "ctrl+t") // interrupt
	pressSettle(t, app, "ctrl+v")
	path = app.pb.live()[0].path
	pressSettle(t, app, "enter")
	if want := "interrupt w1|submit w1 @" + path; strings.Join(f.prompts, "|") != want {
		t.Fatalf("interrupt %q, want %q", f.prompts, want)
	}
}

// A harness that attaches a pasted path (codex's attach: paste) gets each
// path as a paste of its own before the text, chips out of the text; its
// queued prompt still carries @ mentions.
func TestPromptPasteHarnessPastesPathsFirst(t *testing.T) {
	t.Parallel()
	app, f, clip := clipApp(t)
	reg, err := harness.NewRegistry([]harness.Harness{{Name: "claude", Command: []string{"claude"}, Attach: harness.AttachPaste}})
	if err != nil {
		t.Fatal(err)
	}
	app.harnesses = reg
	clip.image = true
	typeText(t, app, "compare")
	pressSettle(t, app, "ctrl+v")
	pressSettle(t, app, "ctrl+v")
	live := app.pb.live()
	pressSettle(t, app, "enter")
	want := "paste w1 " + live[0].path + "|paste w1 " + live[1].path + "|submit w1 compare"
	if strings.Join(f.prompts, "|") != want {
		t.Fatalf("calls %q, want %q", f.prompts, want)
	}
	f.prompts = nil
	press(t, app, "i")      // the send left the box in normal mode
	press(t, app, "ctrl+t") // queue
	pressSettle(t, app, "ctrl+v")
	path := app.pb.live()[0].path
	pressSettle(t, app, "enter")
	if want := "queue w1 @" + path; strings.Join(f.prompts, "|") != want {
		t.Fatalf("queue %q, want %q", f.prompts, want)
	}
}

func TestPromptOutgoingSpacesMentions(t *testing.T) {
	t.Parallel()
	live := []promptAttachment{{n: 1, path: "/a.png"}, {n: 2, path: "/b c.png"}}
	for _, c := range []struct{ in, mention, strip string }{
		{"see[image 1]now", "see @/a.png now", "seenow"},
		{"[image 1] [image 2]", `@/a.png @"/b c.png"`, " "},
		{"[image 3] stays", "[image 3] stays", "[image 3] stays"},
	} {
		if got := promptOutgoing(c.in, live, true); got != c.mention {
			t.Errorf("%q mentions: %q, want %q", c.in, got, c.mention)
		}
		if got := promptOutgoing(c.in, live, false); got != c.strip {
			t.Errorf("%q stripped: %q, want %q", c.in, got, c.strip)
		}
	}
}

// ctrl+v is hinted in insert mode and its verbs are registered (the
// palette and ? help list them); normal mode's ctrl+v stays the editor's.
func TestPromptPasteKeysAndVerbs(t *testing.T) {
	t.Parallel()
	app, _, _ := clipApp(t)
	if hints := hintKeys(app); !slices.Contains(hints, "ctrl+v paste/attach") {
		t.Fatalf("footer %q", hints)
	}
	for _, verb := range []string{"paste", "detach"} {
		if _, ok := app.r.Operations[app.r.CanonicalKey(promptNoun+"."+verb)]; !ok {
			t.Errorf("no %s.%s verb", promptNoun, verb)
		}
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	// Normal mode's ctrl+v is the editor's visual block, not a paste.
	press(t, app, "esc")
	app.Update(keyMsg("ctrl+v"))
	if len(app.pb.attachments) != 0 || app.pb.input.Mode == "insert" {
		t.Fatalf("normal ctrl+v pasted: %+v mode %s", app.pb.attachments, app.pb.input.Mode)
	}
}
