package main

import (
	"errors"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func taskImageApp(t *testing.T) (*App, *fakeClipboard, string) {
	t.Helper()
	a := open(t)
	clip := &fakeClipboard{}
	a.pb.clip = clip
	press(t, a, "j", "O", "o")
	return a, clip, taskOf(a.outline.tree.Editing())
}

func TestTaskImagesSaveReopenRemoveAndUndo(t *testing.T) {
	t.Parallel()
	a, clip, id := taskImageApp(t)
	typeText(t, a, "look at ")
	clip.files = []string{"/shots/a.PNG", "/My Shots/b.webp"}
	pressSettle(t, a, "ctrl+v")
	if got := string(a.r.Input(outlineTree + "/line").Text); got != "look at [image 1] [image 2] " {
		t.Fatalf("task input %q", got)
	}
	press(t, a, "esc")
	want := `look at @/shots/a.PNG @"/My Shots/b.webp"`
	if got := stored(t, a, id).Title; got != want {
		t.Fatalf("saved %q, want %q", got, want)
	}
	press(t, a, "esc", "O")
	if got := frame(a); !strings.Contains(got, "look at [image 1] [image 2]") {
		t.Fatalf("reopened task:\n%s", got)
	}
	if err := a.outline.tree.Select(outlineNode(id)); err != nil {
		t.Fatal(err)
	}
	if err := a.outline.tree.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "i")
	if _, err := a.r.Execute(ochazuke.Request{Noun: outlineModal, Verb: "detach", Args: ochazuke.Arguments{"n": 1}}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc")
	if got := stored(t, a, id).Title; got != `look at @"/My Shots/b.webp"` {
		t.Fatalf("removed image still saved: %q", got)
	}
	press(t, a, "u")
	if got := stored(t, a, id).Title; got != want {
		t.Fatalf("undo lost an attachment: %q", got)
	}
}

func TestTaskImagesLateReadCannotEnterAnotherEditor(t *testing.T) {
	t.Parallel()
	for _, destination := range []string{"next task", "same task", "reopened modal"} {
		t.Run(destination, func(t *testing.T) {
			t.Parallel()
			a, clip, id := taskImageApp(t)
			typeText(t, a, "original")
			clip.image = true
			_, cmd := a.Update(keyMsg("ctrl+v"))
			press(t, a, "esc")
			switch destination {
			case "next task":
				press(t, a, "o")
				typeText(t, a, "next task")
			case "same task":
				press(t, a, "i")
			case "reopened modal":
				press(t, a, "esc", "O")
				if err := a.outline.tree.Select(outlineNode(id)); err != nil {
					t.Fatal(err)
				}
				if err := a.outline.tree.Focus(); err != nil {
					t.Fatal(err)
				}
				press(t, a, "i")
			}
			in := a.r.Input(outlineTree + "/line")
			before := string(in.Text)
			settleJobs(t, a, cmd)
			if got := string(in.Text); got != before {
				t.Fatalf("late read changed %s: %q to %q", destination, before, got)
			}
			press(t, a, "esc")
			if got := stored(t, a, id).Title; got != "original" {
				t.Fatalf("late read changed saved source: %q", got)
			}
		})
	}
}

func TestTaskImagesChooserCancelAndOptOut(t *testing.T) {
	t.Parallel()
	a, clip, id := taskImageApp(t)
	clip.files = []string{"/a.png", "/b.jpg"}
	pressSettle(t, a, "ctrl+v")
	if _, err := a.r.Execute(ochazuke.Request{Noun: outlineModal, Verb: "detach"}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc", "esc")
	want := "@/a.png @/b.jpg"
	if got := stored(t, a, id).Title; got != want {
		t.Fatalf("cancelled chooser removed an image: %q", got)
	}
	press(t, a, "esc")
	if _, err := a.r.Settings().SetSession(settingTaskImages, false); err != nil {
		t.Fatal(err)
	}
	press(t, a, "O")
	if got := frame(a); !strings.Contains(got, want) || strings.Contains(got, "[image 1]") {
		t.Fatalf("opt-out did not preserve raw mentions:\n%s", got)
	}
	press(t, a, "o")
	typeText(t, a, "ordinary task")
	before := len(clip.asked)
	pressSettle(t, a, "ctrl+v")
	if len(clip.asked) != before {
		t.Fatal("opt-out read the clipboard")
	}
	if got := string(a.r.Input(outlineTree + "/line").Text); got != "ordinary task" {
		t.Fatalf("opt-out changed task: %q", got)
	}
}

func TestTaskImagesPreserveTextAndClipboardErrors(t *testing.T) {
	t.Parallel()
	a, clip, id := taskImageApp(t)
	typeText(t, a, "literal [image 1] ")
	clip.files = []string{"/a.gif", "/notes.txt"}
	pressSettle(t, a, "ctrl+v")
	clip.files = nil
	clip.text = "ordinary 日本語"
	pressSettle(t, a, "ctrl+v")
	clip.text = ""
	pressSettle(t, a, "ctrl+v")
	before := string(a.r.Input(outlineTree + "/line").Text)
	clip.err = errors.New("clipboard unavailable")
	pressSettle(t, a, "ctrl+v")
	if got := string(a.r.Input(outlineTree + "/line").Text); got != before {
		t.Fatalf("failed read changed text: %q to %q", before, got)
	}
	if !strings.Contains(a.m.Status, "clipboard unavailable") {
		t.Fatalf("missing clipboard error: %q", a.m.Status)
	}
	a.m.Status = ""
	press(t, a, "esc")
	if got := stored(t, a, id).Title; got != "literal [image 1] @/a.gif @/notes.txt ordinary 日本語" {
		t.Fatalf("ordinary text changed: %q", got)
	}
	press(t, a, "esc", "O")
	if got := frame(a); !strings.Contains(got, "literal [image 1] [image 2]") {
		t.Fatalf("literal chip collision after reopen:\n%s", got)
	}
}

func TestTaskImagesQuotedPathsSurviveReopen(t *testing.T) {
	t.Parallel()
	a, clip, id := taskImageApp(t)
	clip.files = []string{`/shots/a"b.png`, `/shots/a\b c.png`}
	pressSettle(t, a, "ctrl+v")
	press(t, a, "esc")
	want := `@"/shots/a\"b.png" @"/shots/a\\b c.png"`
	if got := stored(t, a, id).Title; got != want {
		t.Fatalf("quoted paths saved as %q", got)
	}
	press(t, a, "esc", "O")
	if err := a.outline.tree.Select(outlineNode(id)); err != nil {
		t.Fatal(err)
	}
	if err := a.outline.tree.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "i", "right")
	typeText(t, a, " checked")
	press(t, a, "esc")
	if got := stored(t, a, id).Title; got != want+" checked" {
		t.Fatalf("reopened edit changed image paths: %q", got)
	}
	if got := frame(a); !strings.Contains(got, "[image 1] [image 2] checked") {
		t.Fatalf("quoted image chips missing:\n%s", got)
	}
}
