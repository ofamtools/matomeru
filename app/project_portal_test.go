package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestProjectPortalOpenAndStayLiveFromTerminal(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":            "Portal Alpha",
		"taskStatus":       "in_progress",
		"defaultDir":       "/tmp/portal-alpha",
		"projectTheme":     "tokyo-night",
		"defaultContextMd": "Always preserve strict data-from-behavior boundaries.",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":     "alpha-agent",
		"harness":   "codex",
		"runStatus": "running",
		"task":      core.Ref{Kind: data.KindTask, ID: proj.ID},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 36)

	// Move onto the pane row in 1 Panes rail and press P in normal mode: opens Project Portal Tab.
	d.Keys("j")
	d.Keys("P")
	wantTab := projectTabID(proj.ID)
	if a.Space() != wantTab {
		t.Fatalf("P in normal mode: space = %q, want %q", a.Space(), wantTab)
	}
	if a.Mode() != ModeNormal {
		t.Fatalf("P in normal mode: mode = %q, want %q", a.Mode(), ModeNormal)
	}

	// Home dashboard includes Portal Settings & Default Context prose.
	frame := d.Frame()
	if !strings.Contains(frame, "PORTAL SETTINGS") || !strings.Contains(frame, "Always preserve strict data-from-behavior") {
		t.Fatalf("expected Portal Settings & defaultContextMd in Home dashboard:\n%s", frame)
	}

	// Enter live terminal on 1 Panes, then press ctrl+a P -> Project Portal Tab staying LIVE!
	d.Keys("1")
	d.Keys("enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal after enter on pane, got %q", a.Mode())
	}
	d.Keys("ctrl+a", "P")
	if a.Space() != wantTab {
		t.Fatalf("ctrl+a P: space = %q, want %q", a.Space(), wantTab)
	}
	if a.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a P from live terminal must stay in ModeTerminal, got %q", a.Mode())
	}
	if got := a.projectCtrl().childPaneByProject[proj.ID]; got != pane.ID {
		t.Fatalf("ctrl+a P did not attach child pane %q in portal, got %q", pane.ID, got)
	}
}

func TestProjectPortalOnPOptOut(t *testing.T) {
	a, d := configured(t, t.TempDir())
	a.cfg.userSettings[SetProjectPortalOnP] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("2")
	d.Keys("P")
	if strings.HasPrefix(a.Space(), projectTabID("")) {
		t.Fatalf("expected opt-out %s=false to skip project portal tab, got space %q", SetProjectPortalOnP, a.Space())
	}
}

func TestProjectDefaultContextBundleAndEdit(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":            "Context Lab",
		"defaultDir":       "/tmp/context-lab",
		"defaultContextMd": "Keep modules small and pure.",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	ctxRec, _, err := store.Create(data.CreateInput{Kind: data.KindContext, Fields: map[string]any{
		"title":       "Architecture Spec",
		"contextKind": "doc",
		"value":       "~/hub/docs/arch.md",
		"official":    true,
		"task":        core.Ref{Kind: data.KindTask, ID: proj.ID},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = store.Star(ctxRec.Ref(), User)

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}

	bundle := a.buildProjectDefaultContext(proj.ID)
	for _, want := range []string{
		"# Project Default Context: Context Lab",
		"/tmp/context-lab",
		"Keep modules small and pure.",
		"Architecture Spec",
	} {
		if !strings.Contains(bundle, want) {
			t.Errorf("default context bundle missing %q:\n%s", want, bundle)
		}
	}
}

func TestFloatingPagedNoteComposerMultiPageMarkAndDispatch(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Deck Project",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	t1, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":  "First Feature",
		"parent": core.Ref{Kind: data.KindTask, ID: proj.ID},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":  "Second Feature",
		"parent": core.Ref{Kind: data.KindTask, ID: proj.ID},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(a, 120, 36)

	// Open Note Deck from 2 Work -> creates Page 1 with section header + deep link.
	d.Keys("2")
	d.Keys("alt+n")
	if a.Modal() != noteDeckModalID {
		t.Fatalf("alt+n: modal = %q, want %q", a.Modal(), noteDeckModalID)
	}
	if a.Mode() != ModeNoteDeck {
		t.Fatalf("alt+n: mode = %q, want %q", a.Mode(), ModeNoteDeck)
	}
	d.Type("Check boundary contract")
	frame1 := d.Frame()
	if !strings.Contains(frame1, "Page 1/1") || !strings.Contains(frame1, "mtm://") || !strings.Contains(frame1, "Check boundary contract") {
		t.Fatalf("expected rendered markdown preview + deep link + body:\n%s", frame1)
	}
	// Mark page 1, save & close.
	d.Keys("ctrl+m", "esc")
	if a.Modal() != "" {
		t.Fatalf("esc did not close note deck: %q", a.Modal())
	}

	// Move down and press alt+n again -> adds Page 2!
	d.Keys("j", "alt+n")
	if a.Modal() != noteDeckModalID {
		t.Fatalf("second alt+n did not open note deck: %q", a.Modal())
	}
	d.Type("Follow up on tests")
	if !strings.Contains(d.Frame(), "Page 2/2") {
		t.Fatalf("expected Page 2/2 in note deck:\n%s", d.Frame())
	}

	// Flip back with ctrl+p and mark all with ctrl+a.
	d.Keys("ctrl+p")
	if !strings.Contains(d.Frame(), "Page 1/2") {
		t.Fatalf("ctrl+p did not flip to Page 1/2:\n%s", d.Frame())
	}
	d.Keys("ctrl+a")
	if got := a.noteDeck.MarkedCount(); got != 2 {
		t.Fatalf("ctrl+a marked count = %d, want 2", got)
	}
	compiled, _ := a.noteDeck.CompileMarkedMarkdown("")
	if !strings.Contains(compiled, "Check boundary contract") || !strings.Contains(compiled, "Follow up on tests") {
		t.Fatalf("compiled marked markdown missing page bodies:\n%s", compiled)
	}
	d.Keys("esc")
	_, _ = t1, t2
}

func TestTaskThreadNoteStyling(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	taskRec, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Styled Thread Task"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	note, _, err := store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{
		"title":      "Agent proposal",
		"body":       "Agent reply body",
		"authorKind": "agent",
		"task":       core.Ref{Kind: data.KindTask, ID: taskRec.ID},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	a.trees.expandedThreads[taskRec.ID] = true
	nodes := []tree.Node{{ID: taskRec.ID, Label: "Styled Thread Task"}}
	a.attachTaskThreadNodes(nodes, map[string][]data.Record{taskRec.ID: {*note}})
	if len(nodes[0].Children) != 1 {
		t.Fatalf("expected 1 attached thread note node, got %d", len(nodes[0].Children))
	}
	child := nodes[0].Children[0]
	if child.Gutter != "│" || child.Icon != "◆" {
		t.Fatalf("expected polished thread gutter and agent glyph, got gutter=%q icon=%q", child.Gutter, child.Icon)
	}
	if len(child.Badges) == 0 || child.Badges[0].Text != "● new" {
		t.Fatalf("expected unread agent badge, got %+v", child.Badges)
	}
}
