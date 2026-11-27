package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

func TestProjectPortalLeftNavRightNeverSkipsTOC(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	now := time.Now().UTC()
	proj := mk(data.KindTask, map[string]any{
		"title":      "Portal Focus Project",
		"workStatus": "active",
		"defaultDir": "/tmp/portal-focus",
	})
	leafPane := mk(data.KindPane, map[string]any{
		"title":      "claude: leaf-worker",
		"harness":    "claude",
		"task":       proj.Ref(),
		"cwd":        "/tmp/portal-focus",
		"runStatus":  "running",
		"promptedAt": now.Format(data.TimeFormat),
	})
	parentPane := mk(data.KindPane, map[string]any{
		"title":      "claude: parent-worker",
		"harness":    "claude",
		"task":       proj.Ref(),
		"cwd":        "/tmp/portal-focus",
		"runStatus":  "running",
		"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
	})
	sidePane := mk(data.KindPane, map[string]any{
		"title":      "shell: side-logs",
		"parentPane": parentPane.Ref(),
		"role":       data.RoleSideTerm,
		"cwd":        "/tmp/portal-focus",
		"runStatus":  "idle",
		"promptedAt": now.Add(-2 * time.Minute).Format(data.TimeFormat),
	})
	leafPage := mk(data.KindPage, map[string]any{
		"title":    "Leaf Design",
		"slug":     "leaf-design",
		"pageKind": "plan",
		"pinned":   true,
		"pane":     leafPane.Ref(),
		"task":     proj.Ref(),
		"root": ui.ViewNode{
			ID:        "leaf.page.card",
			Kind:      ui.KindCard,
			Focusable: true,
			Title:     "Leaf Spec Card",
			Props:     map[string]any{"summary": "Details inside leaf page"},
		},
	})
	otherPage := mk(data.KindPage, map[string]any{
		"title":    "Parent Plan",
		"slug":     "parent-plan",
		"pageKind": "plan",
		"pinned":   true,
		"pane":     parentPane.Ref(),
		"task":     proj.Ref(),
		"root":     ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": "Parent plan body"}},
	})

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 165, 36)

	// Open the Project Portal tab for proj.
	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.viewMemo.gen++

	// Expand Panes section in project.nav with l (preserving expand-section behavior).
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"panes")
	a.viewMemo.gen++
	d.Keys("l")
	if a.Focus() != projectNavPrefix+"panes" {
		t.Fatalf("l on collapsed panes section should stay on section header, got %q", a.Focus())
	}

	leafNodeID := projectNavNodePrefix + "panes." + leafPane.ID
	parentNodeID := projectNavNodePrefix + "panes." + parentPane.ID
	sideNodeID := projectNavNodePrefix + "panes." + sidePane.ID

	// 1. Leaf pane node in project.nav (live previewing pane site with site.toc + site.screen):
	//    Seed a stale lastFocus["site.toc"] from another pane to verify post-commit resolution!
	a.nav.lastFocus["site.toc"] = tocPrefix + otherPage.ID
	a.Shell.State = withFocus(a.State(), leafNodeID)
	a.viewMemo.gen++
	_ = d.Frame()

	// Press l -> MUST land on site.toc (NOT skip to site.screen / ModeTerminal!).
	d.Keys("l")
	if a.Mode() != ModeNormal {
		t.Fatalf("l from project.nav pane node entered mode=%q, want %q (focus=%q)", a.Mode(), ModeNormal, a.Focus())
	}
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("l from project.nav pane node skipped site.toc: focus=%q, want prefix %q", a.Focus(), tocPrefix)
	}
	if a.activeChildPaneID(a.State()) != leafPane.ID {
		t.Fatalf("l from project.nav pane node did not commit child pane: got %q want %q", a.activeChildPaneID(a.State()), leafPane.ID)
	}

	// 2. From site.toc.screen, press l -> lands on site.screen (ModeTerminal).
	a.Shell.State = withFocus(a.State(), tocPrefix+views.ScreenPageID)
	a.viewMemo.gen++
	d.Keys("l")
	if a.Focus() != siteScreen {
		t.Fatalf("l from site.toc.screen focus=%q, want %q", a.Focus(), siteScreen)
	}

	// Detach to ModeNormal on site.screen, then h -> site.toc, then h -> project.nav!
	if a.Mode() == ModeTerminal {
		d.Keys("ctrl+a", "d")
	}
	// If detachFocusTarget already moved to site.toc, put focus on site.screen to test site.content -> site.toc -> project.nav explicitly.
	a.Shell.State = withMode(withFocus(a.State(), siteScreen), ModeNormal)
	a.viewMemo.gen++
	d.Keys("h")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("h from site.screen did not land on site.toc: focus=%q", a.Focus())
	}
	d.Keys("h")
	if a.Focus() != leafNodeID {
		t.Fatalf("second h from site.toc did not return to project.nav node %q: got %q", leafNodeID, a.Focus())
	}

	// 3. Non-screen TOC page: project.nav -> l -> site.toc -> select leafPage -> l -> site.content -> h -> site.toc!
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("re-entering site.toc with l failed: focus=%q", a.Focus())
	}
	a.Shell.State = withFocus(a.State(), tocPrefix+leafPage.ID)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("l")
	if a.Focus() != "leaf.page.card" && a.Focus() != views.PageScrollID {
		t.Fatalf("l from non-screen TOC entry did not land in site.content: focus=%q", a.Focus())
	}
	d.Keys("h")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("h from site.content did not return to site.toc: focus=%q", a.Focus())
	}
	d.Keys("h")
	if a.Focus() != leafNodeID {
		t.Fatalf("h from site.toc did not return to project.nav node %q: got %q", leafNodeID, a.Focus())
	}

	// 4. Expandable parent pane node: first l expands child sidePane (staying on parentNodeID);
	//    second l (now already expanded) MUST land on site.toc!
	a.Shell.State = withFocus(a.State(), parentNodeID)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("l")
	if a.Focus() != parentNodeID || findNode(a, sideNodeID) == nil {
		t.Fatalf("first l on collapsed parent pane should expand sidePane: focus=%q sideNode=%v", a.Focus(), findNode(a, sideNodeID) != nil)
	}
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) || a.Mode() != ModeNormal {
		t.Fatalf("second l on already-expanded parent pane skipped site.toc: focus=%q mode=%q", a.Focus(), a.Mode())
	}

	// 5. Already-open child pane while focus is on expanded project.nav.panes header:
	//    l MUST land on site.toc, and h MUST return to project.nav!
	a.projectCtrl().childPaneByProject[proj.ID] = leafPane.ID
	delete(a.projectCtrl().childPaneReturnFocus, proj.ID)
	a.Shell.State = withMode(withFocus(a.State(), projectNavPrefix+"panes"), ModeNormal)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) || a.Mode() != ModeNormal {
		t.Fatalf("l from project.nav.panes with open child pane skipped site.toc: focus=%q mode=%q", a.Focus(), a.Mode())
	}
	d.Keys("h")
	if a.Focus() != projectNavPrefix+"panes" {
		t.Fatalf("h from site.toc did not return to project.nav.panes: focus=%q", a.Focus())
	}
}
