package app

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func TestContentHintsShowPageBodyPathFocusAndMultiAction(t *testing.T) {
	a, d := demo(t)

	const (
		htmlPath = "/Users/unixsuperhero/hub/animations/clown-car-chips/index.html"
		mp4Path  = "/Users/unixsuperhero/hub/animations/clown-car-chips/demo.mp4"
		notePath = "/Users/unixsuperhero/.local/share/matomeru/notes/demo-note-full.md"
	)
	targetTask, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "ref98765",
		Fields: map[string]any{"title": "Referenced Target Task"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	note, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		ID:   "show-hints-note",
		Fields: map[string]any{
			"title": "Render Artifacts",
			"body":  "HTML: " + htmlPath + "\nVideo: " + mp4Path + "\nTask: task:" + targetTask.ID + " `go test ./...`",
			"path":  notePath,
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	openNote := func() {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:     views.CmdOpen,
			Args:   map[string]any{"ref": map[string]any{"kind": note.Kind, "id": note.ID}},
			Source: "test",
			Actor:  User,
		}})
	}

	// 1. Both Ledger and Classic Show layouts keep Body and Path Focusable with direct `y` copy.
	for _, layoutMode := range []string{views.ShowLayoutLedger, views.ShowLayoutClassic} {
		a.cfg.userSettings[SetShowLayout] = layoutMode
		a.reloadSettings()
		a.applySettings()
		openNote()

		root := a.View(a.State())
		bodyNode := root.Find("show.content.body")
		pathNode := root.Find("show.content.path")
		if bodyNode == nil || !bodyNode.Focusable {
			t.Fatalf("layout=%s: show.content.body must be focusable, got %+v", layoutMode, bodyNode)
		}
		if pathNode == nil || !pathNode.Focusable {
			t.Fatalf("layout=%s: show.content.path must be focusable, got %+v", layoutMode, pathNode)
		}

		// Focus Path and press y -> copies the path directly!
		a.Shell.State = withFocus(a.State(), "show.content.path")
		d.Keys("y")
		if a.rt.live.clipboard != notePath {
			t.Fatalf("layout=%s: y on show.content.path copied %q, want %q", layoutMode, a.rt.live.clipboard, notePath)
		}

		// Focus Body and press y -> copies the body directly!
		a.Shell.State = withFocus(a.State(), "show.content.body")
		d.Keys("y")
		if !strings.Contains(a.rt.live.clipboard, htmlPath) || !strings.Contains(a.rt.live.clipboard, mp4Path) {
			t.Fatalf("layout=%s: y on show.content.body copied %q, missing paths", layoutMode, a.rt.live.clipboard)
		}

		// Focus Hints (`g f`) displays visible inline badges on Body and Path (not invisible VStack title)!
		d.Keys("g", "f")
		if !a.focusHintsActive() {
			t.Fatalf("layout=%s: g f did not activate focus hints", layoutMode)
		}
		fTargets := a.panes.hints.session.Targets()
		bodyTgt, okBody := findFocusTargetByID(fTargets, "show.content.body")
		pathTgt, okPath := findFocusTargetByID(fTargets, "show.content.path")
		if !okBody || !okPath {
			t.Fatalf("layout=%s: focus hints missing Body (%v) or Path (%v): %+v", layoutMode, okBody, okPath, fTargets)
		}
		frame := d.Frame()
		if !strings.Contains(frame, "["+bodyTgt.Label+"]") || !strings.Contains(frame, "["+pathTgt.Label+"]") {
			t.Fatalf("layout=%s: focus hint badges for Body [%s] / Path [%s] not visible in rendered frame:\n%s",
				layoutMode, bodyTgt.Label, pathTgt.Label, frame)
		}
		d.Keys("esc")
	}

	// Restore default ledger layout and test `,` Content Hints on the Show page.
	a.cfg.userSettings[SetShowLayout] = views.ShowLayoutLedger
	a.reloadSettings()
	a.applySettings()
	openNote()
	d.Frame()

	// 2. Press `,` -> activates unmistakable Content Hints over Body, Path, record ID, and code span!
	d.Keys(",")
	if a.Mode() != ModeHints || !a.screenHintsActive() {
		t.Fatalf(", did not activate content hints overlay: mode=%q screen=%v status=%q", a.Mode(), a.screenHintsActive(), a.Status())
	}
	if !strings.Contains(a.Status(), "content hints") {
		t.Fatalf("expected 'content hints' in status line, got %q", a.Status())
	}
	cTargets := a.panes.hints.session.Targets()
	htmlTgt, hasHTML := findTargetByValueContains(cTargets, htmlPath)
	mp4Tgt, hasMP4 := findTargetByValueContains(cTargets, mp4Path)
	pathTgt, hasNotePath := findTargetByValueContains(cTargets, notePath)
	refTgt, hasRef := findTargetByValueContains(cTargets, "task:"+targetTask.ID)
	codeTgt, hasCode := findTargetByValueContains(cTargets, "go test ./...")
	if !hasHTML || !hasMP4 || !hasNotePath || !hasRef || !hasCode {
		t.Fatalf("content hints missing targets: html=%v mp4=%v notePath=%v ref=%v code=%v (all=%+v)",
			hasHTML, hasMP4, hasNotePath, hasRef, hasCode, cTargets)
	}

	// 3. Lowercase copies HTML path to clipboard.
	d.Keys(htmlTgt.Label)
	if a.Mode() != ModeNormal || a.rt.live.clipboard != htmlPath {
		t.Fatalf("lowercase content hint copied %q (mode=%q), want %q", a.rt.live.clipboard, a.Mode(), htmlPath)
	}

	// 4. `space` arms the action picker (`pick`) -> selecting a hint opens the 4-action picker modal!
	d.Keys(",", "space")
	if a.panes.hints == nil || a.panes.hints.session.ActiveAction() != hintsActionPick {
		t.Fatalf("space in ModeHints did not arm action picker: status=%q", a.Status())
	}
	mp4Tgt, _ = findTargetByValueContains(a.panes.hints.session.Targets(), mp4Path)
	d.Keys(mp4Tgt.Label)
	if a.Modal() != "hints.action.picker" {
		t.Fatalf("expected hints.action.picker after space + label, got modal=%q", a.Modal())
	}
	// Choose "Reveal in Finder" (4th item: j j j enter).
	d.Keys("j", "j", "j", "enter")
	wantReveal := systemOpener() + " " + handlers.Quote(filepath.Dir(mp4Path))
	if runtime.GOOS == "darwin" {
		wantReveal = "open -R " + handlers.Quote(mp4Path)
	}
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantReveal) {
		t.Fatalf("picker Reveal did not run %q; got %v", wantReveal, cmds)
	}

	// 5. `ctrl+<label>` on a Matomeru record ID opens that record's Show page directly!
	d.Keys(",")
	refTgt, _ = findTargetByValueContains(a.panes.hints.session.Targets(), "task:"+targetTask.ID)
	d.Keys("ctrl+" + refTgt.Label)
	if cur, ok := a.Showing(); !ok || cur != targetTask.Ref() {
		t.Fatalf("ctrl+<label> on record ID did not open Show page for %v: ok=%v cur=%v", targetTask.Ref(), ok, cur)
	}

	// 6. `,` works on 2 Work tables too (where `f` is filter-field, not hints!).
	d.Keys("2")
	d.Frame()
	if _, ok := a.Showing(); ok {
		d.Keys("H")
	}
	_ = pathTgt
	_ = codeTgt
}
