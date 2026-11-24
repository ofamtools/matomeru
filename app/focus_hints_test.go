package app

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/hints"
)

func findFocusTargetByID(targets []hints.Target, id string) (hints.Target, bool) {
	for _, t := range targets {
		if t.ID == id {
			return t, true
		}
	}
	return hints.Target{}, false
}

func findFocusTargetByValueContains(targets []hints.Target, sub string) (hints.Target, bool) {
	for _, t := range targets {
		if strings.Contains(t.Value, sub) {
			return t, true
		}
	}
	return hints.Target{}, false
}

func TestFocusHintsInlineAndDrilldown(t *testing.T) {
	for _, compactTOC := range []bool{true, false} {
		modeName := "compactTOC=true"
		if !compactTOC {
			modeName = "compactTOC=false"
		}
		t.Run(modeName, func(t *testing.T) {
			a, d := demo(t)
			a.cfg.userSettings[SetCompactTOC] = compactTOC
			a.reloadSettings()
			a.applySettings()
			d.Keys("1")

			initialFocus := a.Focus()
			d.Keys("g", "f")
			if !a.focusHintsActive() {
				t.Fatalf("g f did not activate focus hints")
			}

			// Default hints.focusStyle="inline": NO floating focus.hints.overlay modal.
			root := a.View(a.State())
			if ov := root.Find("focus.hints.overlay"); ov != nil {
				t.Fatalf("default inline focus hints unexpectedly rendered focus.hints.overlay")
			}

			targets := a.panes.hints.session.Targets()
			// Level 1 must label space tabs (1 Panes, 2 Work, ...) and top-level rails (Panes, Pages, Screen).
			tabPanes, ok := findFocusTargetByID(targets, "space.panes")
			if !ok || tabPanes.Value != "1 Panes" {
				t.Fatalf("missing space.panes tab target, got %+v", targets)
			}
			tabWork, ok := findFocusTargetByID(targets, "space.work")
			if !ok || tabWork.Value != "2 Work" {
				t.Fatalf("missing space.work tab target, got %+v", targets)
			}
			railPanel, ok := findFocusTargetByID(targets, "panes.rail")
			if !ok || railPanel.Value != "Panes" {
				t.Fatalf("missing panes.rail panel target, got %+v", targets)
			}
			tocPanel, ok := findFocusTargetByID(targets, "site.toc")
			if !ok || tocPanel.Value != "Pages" {
				t.Fatalf("missing site.toc panel target, got %+v", targets)
			}
			contentPanel, ok := findFocusTargetByID(targets, "site.content")
			if !ok || !strings.Contains(contentPanel.Value, "Screen") {
				t.Fatalf("missing site.content panel target, got %+v", targets)
			}

			// Individual page items inside Pages must NOT be labeled at Level 1.
			if _, hasLeaf := findFocusTargetByID(targets, "site.toc.screen"); hasLeaf {
				t.Fatalf("Level 1 drilldown should not label individual TOC items yet: %+v", targets)
			}
			// Never expose raw internal IDs in target names.
			for _, tgt := range targets {
				disp := focusTargetDisplayName(tgt)
				for _, raw := range []string{"panes.rail.table", "site.toc.screen", "site.screen", "focus ·"} {
					if strings.Contains(disp, raw) {
						t.Fatalf("target %+v leaked raw internal ID %q in %q", tgt, raw, disp)
					}
				}
			}

			// Verify badges render inline on screen right next to real titles.
			frame1 := d.Frame()
			for _, want := range []string{
				"[" + tabPanes.Label + "] 1 Panes",
				"[" + tabWork.Label + "] 2 Work",
				"[" + railPanel.Label + "] Panes",
				"[" + tocPanel.Label + "] Pages",
				"[" + contentPanel.Label + "] " + contentPanel.Value,
			} {
				if !strings.Contains(frame1, want) {
					t.Fatalf("Level 1 frame missing inline badge %q:\n%s", want, frame1)
				}
			}

			// 1. Picking the Pages rail drills down into Level 2.
			d.Type(tocPanel.Label)
			if a.Mode() != ModeHints || !a.focusHintsActive() || len(a.panes.hints.focusStack) != 2 {
				t.Fatalf("picking Pages did not drill into Level 2: mode=%q stack=%d", a.Mode(), len(a.panes.hints.focusStack))
			}
			lvl2Targets := a.panes.hints.session.Targets()
			if len(lvl2Targets) < 2 {
				t.Fatalf("expected >= 2 page targets inside Pages, got %+v", lvl2Targets)
			}
			tocScreen, ok := findFocusTargetByID(lvl2Targets, "site.toc.screen")
			if !ok || tocScreen.Kind != "Pages" || tocScreen.Value != "Screen" {
				t.Fatalf("Level 2 Screen target = %+v (ok=%v), want Kind=Pages Value=Screen", tocScreen, ok)
			}
			// Verify no raw glyphs or raw IDs leak into Level 2 display names.
			for _, tgt := range lvl2Targets {
				if strings.ContainsAny(tgt.Value, "◆●▣≡") {
					t.Fatalf("Level 2 target value still contains leading glyph: %+v", tgt)
				}
				if strings.HasPrefix(tgt.Value, "site.toc.") {
					t.Fatalf("Level 2 target value leaked raw ID: %+v", tgt)
				}
			}
			// Level 2 frame badges items inside Pages and does NOT badge outer tabs or Panes rail.
			frame2 := d.Frame()
			if strings.Contains(frame2, "["+tabPanes.Label+"] 1 Panes") {
				t.Fatalf("Level 2 frame unexpectedly kept Level 1 tab badge:\n%s", frame2)
			}
			if !strings.Contains(frame2, "["+tocScreen.Label+"]") {
				t.Fatalf("Level 2 frame missing inline badge [%s] on Screen item:\n%s", tocScreen.Label, frame2)
			}

			// 2. Backspace in Level 2 (empty prefix) steps back out to Level 1.
			d.Keys("backspace")
			if !a.focusHintsActive() || len(a.panes.hints.focusStack) != 1 {
				t.Fatalf("backspace in Level 2 did not return to Level 1: active=%v stack=%d", a.focusHintsActive(), len(a.panes.hints.focusStack))
			}

			// 3. Re-enter Level 2 on Pages and press enter to focus the first child immediately.
			d.Type(tocPanel.Label)
			if len(a.panes.hints.focusStack) != 2 {
				t.Fatalf("failed to re-enter Level 2")
			}
			firstLvl2 := a.panes.hints.session.Targets()[0]
			d.Keys("enter")
			if a.Mode() != ModeNormal || a.panes.hints != nil {
				t.Fatalf("enter in Level 2 did not exit hints: mode=%q", a.Mode())
			}
			if a.Focus() != firstLvl2.ID {
				t.Fatalf("enter in Level 2 focused %q, want %q", a.Focus(), firstLvl2.ID)
			}
			if strings.Contains(a.Status(), firstLvl2.ID) || !strings.Contains(a.Status(), "focused Pages:") {
				t.Fatalf("status after enter = %q, want clean rendered name without raw ID", a.Status())
			}

			// 4. Drill into Pages and pick a non-Screen page target directly.
			d.Keys("g", "f")
			tocPanel, _ = findFocusTargetByID(a.panes.hints.session.Targets(), "site.toc")
			d.Type(tocPanel.Label)
			var otherPage hints.Target
			for _, tgt := range a.panes.hints.session.Targets() {
				if tgt.ID != "site.toc.screen" {
					otherPage = tgt
					break
				}
			}
			if otherPage.ID == "" {
				t.Fatalf("expected another TOC page besides Screen")
			}
			d.Type(otherPage.Label)
			if a.Mode() != ModeNormal || a.Focus() != otherPage.ID {
				t.Fatalf("picking %+v landed on mode=%q focus=%q", otherPage, a.Mode(), a.Focus())
			}
			if strings.Contains(a.Status(), otherPage.ID) || !strings.Contains(a.Status(), otherPage.Value) {
				t.Fatalf("status = %q, want rendered title %q without raw ID %q", a.Status(), otherPage.Value, otherPage.ID)
			}

			// 5. Picking a single-focus rail (Panes) focuses panes.rail.table immediately.
			d.Keys("g", "f")
			railPanel, _ = findFocusTargetByID(a.panes.hints.session.Targets(), "panes.rail")
			d.Type(railPanel.Label)
			if a.Mode() != ModeNormal || a.Focus() != railTable {
				t.Fatalf("picking Panes rail landed on mode=%q focus=%q, want %q", a.Mode(), a.Focus(), railTable)
			}
			if !strings.Contains(a.Status(), "focused Panes") || strings.Contains(a.Status(), railTable) {
				t.Fatalf("status = %q, want 'focused Panes' without raw ID", a.Status())
			}

			// 6. esc cancels cleanly and strips all inline [label] badges from the frame.
			d.Keys("g", "f")
			badged := d.Frame()
			d.Keys("esc")
			if a.Mode() != ModeNormal || a.panes.hints != nil || a.Focus() != initialFocus {
				t.Fatalf("esc did not restore state: mode=%q focus=%q want=%q", a.Mode(), a.Focus(), initialFocus)
			}
			clean := d.Frame()
			if clean == badged || strings.Contains(clean, "["+railPanel.Label+"] Panes") {
				t.Fatalf("esc left stale focus hint badges on screen:\n%s", clean)
			}

			// 7. Picking a space tab (2 Work) switches to SpaceWork cleanly.
			d.Keys("g", "f")
			tabWork, _ = findFocusTargetByID(a.panes.hints.session.Targets(), "space.work")
			d.Type(tabWork.Label)
			if a.Mode() != ModeNormal || a.State().ActivePage != SpaceWork {
				t.Fatalf("picking 2 Work tab landed on mode=%q space=%q, want %q", a.Mode(), a.State().ActivePage, SpaceWork)
			}
		})
	}
}

func TestFocusHintsFlatOptOutAndStyleSettings(t *testing.T) {
	t.Run("hints.focusDrilldown=false shows flat leaf hints directly", func(t *testing.T) {
		a, d := demo(t)
		a.cfg.userSettings[SetFocusHintsDrilldown] = false
		a.reloadSettings()
		a.applySettings()
		d.Keys("1")

		d.Keys("g", "f")
		if !a.focusHintsActive() {
			t.Fatal("g f did not activate flat focus hints")
		}
		targets := a.panes.hints.session.Targets()
		railTgt, ok := findFocusTargetByID(targets, railTable)
		if !ok || railTgt.Value != "Panes" || railTgt.Kind != "" {
			t.Fatalf("flat rail target = %+v (ok=%v), want Value=Panes Kind=''", railTgt, ok)
		}
		tocScreen, ok := findFocusTargetByID(targets, "site.toc.screen")
		if !ok || tocScreen.Kind != "Pages" || tocScreen.Value != "Screen" {
			t.Fatalf("flat site.toc.screen target = %+v (ok=%v), want Kind=Pages Value=Screen", tocScreen, ok)
		}
		screenTgt, ok := findFocusTargetByID(targets, siteScreen)
		if !ok || !strings.Contains(screenTgt.Value, "Screen") || screenTgt.Kind != "" {
			t.Fatalf("flat site.screen target = %+v (ok=%v), want Value containing Screen", screenTgt, ok)
		}

		frame := d.Frame()
		if !strings.Contains(frame, "["+railTgt.Label+"] Panes") {
			t.Fatalf("flat frame missing inline badge on Panes panel:\n%s", frame)
		}
		if !strings.Contains(frame, "["+tocScreen.Label+"]") {
			t.Fatalf("flat frame missing inline badge on Pages Screen row:\n%s", frame)
		}
		if !strings.Contains(frame, "["+screenTgt.Label+"] "+screenTgt.Value) {
			t.Fatalf("flat frame missing inline badge on Screen panel:\n%s", frame)
		}

		// Jump straight to site.screen in one keystroke.
		d.Type(screenTgt.Label)
		if a.Mode() != ModeNormal || a.Focus() != siteScreen {
			t.Fatalf("flat jump to screen landed on mode=%q focus=%q", a.Mode(), a.Focus())
		}
		if strings.Contains(a.Status(), siteScreen) || !strings.Contains(a.Status(), "focused Screen") {
			t.Fatalf("flat jump status = %q, want clean rendered name without raw ID", a.Status())
		}
	})

	t.Run("hints.focusStyle=overlay renders overlay with clean rendered titles and no inline badges", func(t *testing.T) {
		a, d := demo(t)
		a.cfg.userSettings[SetFocusHintsStyle] = FocusHintsOverlay
		a.reloadSettings()
		a.applySettings()
		d.Keys("1")

		d.Keys("g", "f")
		root := a.View(a.State())
		ov := root.Find("focus.hints.overlay")
		if ov == nil || ov.Kind != ui.KindOverlay {
			t.Fatalf("hints.focusStyle=overlay did not render focus.hints.overlay")
		}
		railPanel := root.Find("panes.rail")
		if railPanel != nil && strings.HasPrefix(railPanel.Title, "[") {
			t.Fatalf("hints.focusStyle=overlay unexpectedly stamped inline badge onto panes.rail title: %q", railPanel.Title)
		}
		frame := d.Frame()
		if !strings.Contains(frame, "Hints") {
			t.Fatalf("overlay frame missing Hints title:\n%s", frame)
		}
		if strings.Contains(frame, "focus ·") || strings.Contains(frame, "panes.rail.table") || strings.Contains(frame, "site.screen") {
			t.Fatalf("overlay leaked raw internal target IDs:\n%s", frame)
		}
		if _, ok := findFocusTargetByValueContains(a.panes.hints.session.Targets(), "Panes"); !ok {
			t.Fatalf("overlay targets missing Panes")
		}
		d.Keys("esc")
	})

	t.Run("hints.focusStyle=both renders both inline badges and overlay", func(t *testing.T) {
		a, d := demo(t)
		a.cfg.userSettings[SetFocusHintsStyle] = FocusHintsBoth
		a.reloadSettings()
		a.applySettings()
		d.Keys("1")

		d.Keys("g", "f")
		root := a.View(a.State())
		if root.Find("focus.hints.overlay") == nil {
			t.Fatal("hints.focusStyle=both missing focus.hints.overlay")
		}
		railPanel := root.Find("panes.rail")
		if railPanel == nil || !strings.HasPrefix(railPanel.Title, "[") {
			t.Fatalf("hints.focusStyle=both missing inline badge on panes.rail: %+v", railPanel)
		}
		d.Keys("esc")
	})
}
