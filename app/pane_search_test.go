package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/core"
)

func TestPaneSearchBindingsAndLeaderParity(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("default bindings have errors: %+v", rep.Errors)
	}

	a, d := demo(t)

	// F in ModeNormal opens global pane search in filter mode.
	d.Keys("F")
	if a.Modal() != paneSearchModalID {
		t.Fatalf("F did not open %s modal (got %q)", paneSearchModalID, a.Modal())
	}
	if a.Mode() != ModePickerFilter {
		t.Fatalf("F mode = %q, want %q", a.Mode(), ModePickerFilter)
	}
	d.Keys("esc", "esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("failed to close pane search modal (still %q)", a.Modal())
	}

	// ctrl+a F inside ModeTerminal mirrors F via the general leader fallback.
	d.Keys("enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal, got %q", a.Mode())
	}
	d.Keys("ctrl+a", "F")
	if a.Modal() != paneSearchModalID {
		t.Fatalf("ctrl+a F in ModeTerminal did not open %s (modal=%q mode=%q)", paneSearchModalID, a.Modal(), a.Mode())
	}
	d.Keys("esc", "esc", "esc")
	d.Keys("ctrl+a", "d")

	// ctrl+a F inside ModePage mirrors F too.
	selPane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	pagesRes, err := a.Store.Query(data.Query{Kind: data.KindPage})
	if err != nil {
		t.Fatal(err)
	}
	targetPageID := ""
	for _, pg := range pagesRes.Items {
		if pRef, has := pg.RefField("pane"); has && pRef.ID == selPane.ID && pg.Text("pageKind") != "screen" {
			targetPageID = pg.ID
			break
		}
	}
	if targetPageID == "" {
		t.Fatal("expected a non-screen ui.page on selected demo pane")
	}
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		d.Keys("l")
	}
	for range 12 {
		if a.Focus() == tocPrefix+targetPageID {
			break
		}
		d.Keys("j")
	}
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("expected ModePage (focus=%q), got %q", a.Focus(), a.Mode())
	}
	d.Keys("ctrl+a", "F")
	if a.Modal() != paneSearchModalID {
		t.Fatalf("ctrl+a F in ModePage did not open %s (modal=%q mode=%q)", paneSearchModalID, a.Modal(), a.Mode())
	}
}

func TestPaneSearchSourcesJumpAndOptOutSettings(t *testing.T) {
	newTestApp := func(t *testing.T, settings map[string]any) (*App, *Driver, map[string]data.Record) {
		t.Helper()
		store, err := data.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })

		transcriptPath := filepath.Join(t.TempDir(), "session.jsonl")
		if err := os.WriteFile(transcriptPath, []byte(
			`{"type":"assistant","message":{"role":"assistant","content":"transcript secret word: KRYPTONITE_LOG"}}`+"\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}

		pScroll, _, _ := store.Create(data.CreateInput{
			Kind:   data.KindPane,
			Fields: map[string]any{"title": "scroll-pane", "harness": "zsh", "cwd": "/tmp/scroll"},
		}, User)
		pRecords, _, _ := store.Create(data.CreateInput{
			Kind:   data.KindPane,
			Fields: map[string]any{"title": "records-pane", "harness": "claude", "cwd": "/tmp/records", "transcriptPath": transcriptPath},
		}, User)
		pArch, _, _ := store.Create(data.CreateInput{
			Kind:   data.KindPane,
			Fields: map[string]any{"title": "archived-pane", "harness": "codex", "cwd": "/tmp/arch"},
		}, User)
		_, _, _ = store.Archive(pArch.Ref(), User)

		_, _, _ = store.Create(data.CreateInput{
			Kind:   data.KindQuestion,
			Fields: map[string]any{"prompt": "Deploy QUASAR_QA now?", "answer": "Approved", "pane": pRecords.Ref()},
		}, User)
		_, _, _ = store.Create(data.CreateInput{
			Kind: data.KindPage,
			Fields: map[string]any{
				"title": "Metrics Page",
				"pane":  pRecords.Ref(),
				"root": map[string]any{
					"kind":  "markdown",
					"props": map[string]any{"markdown": "NEBULA_PAGE latency graph"},
				},
			},
		}, User)
		_, _, _ = store.Create(data.CreateInput{
			Kind:   data.KindContext,
			Fields: map[string]any{"title": "RFC", "value": "https://example.com/PULSAR_CTX", "pane": pRecords.Ref()},
		}, User)

		a, err := New(store, Options{})
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range settings {
			a.cfg.userSettings[k] = v
		}
		if len(settings) > 0 {
			a.reloadSettings()
		}
		a.rt.live.frames[pScroll.ID] = ptyhost.Frame{
			ID:         pScroll.ID,
			Revision:   3,
			Scrollback: []string{"echo starting", "SOLARIS_SCROLLBACK_TOKEN ready"},
			Plain:      []string{"$ "},
		}
		d := NewDriver(a, 130, 36)
		return a, d, map[string]data.Record{
			"scroll":   *pScroll,
			"records":  *pRecords,
			"archived": *pArch,
		}
	}

	t.Run("search across scrollback, SQLite records, and transcripts + jump on enter", func(t *testing.T) {
		a, d, panes := newTestApp(t, nil)

		// Jump to Work space first to verify confirming a match jumps back to 1 Panes.
		d.Keys("2")
		if a.Space() != SpaceWork {
			t.Fatalf("space = %q, want work", a.Space())
		}

		// 1. Live scrollback match.
		d.Keys("F")
		d.Type("SOLARIS_SCROLLBACK_TOKEN")
		pm := a.activePicker(paneSearchModalID).Model()
		if got := pm.Filtered(); len(got) != 1 || got[0].ID != panes["scroll"].ID {
			t.Fatalf("scrollback filtered = %+v, want %s", got, panes["scroll"].ID)
		}
		if frame := d.Frame(); !strings.Contains(frame, "[scrollback]") || !strings.Contains(frame, "SOLARIS_SCROLLBACK_TOKEN") {
			t.Fatalf("preview missing scrollback excerpt:\n%s", frame)
		}

		// Confirm jumps to 1 Panes and selects scroll-pane.
		d.Keys("enter")
		if a.Modal() != "" || a.Space() != SpacePanes {
			t.Fatalf("after enter: modal=%q space=%q", a.Modal(), a.Space())
		}
		if sel, ok := a.SelectedPane(); !ok || sel.ID != panes["scroll"].ID {
			t.Fatalf("selected pane = %v (%v), want %s", sel, ok, panes["scroll"].ID)
		}

		// 2. SQLite records (KindQuestion, KindPage, KindContext) + transcript match.
		for _, token := range []string{"QUASAR_QA", "NEBULA_PAGE", "PULSAR_CTX", "KRYPTONITE_LOG"} {
			d.Keys("F")
			d.Type(token)
			got := a.activePicker(paneSearchModalID).Model().Filtered()
			if len(got) != 1 || got[0].ID != panes["records"].ID {
				t.Fatalf("query %q matched %+v, want %s", token, got, panes["records"].ID)
			}
			// Yank top matched snippet from normal picker mode.
			if token == "QUASAR_QA" {
				d.Keys("esc", "y")
				if !strings.Contains(a.Register(core.RegClipboard), "QUASAR_QA") {
					t.Fatalf("yanked clipboard = %q, want QUASAR_QA", a.Register(core.RegClipboard))
				}
				continue
			}
			d.Keys("esc", "esc", "esc")
		}

		// 3. Secondary binding `s` cycles source filter and `o` attaches live terminal (in ModePicker).
		d.Keys("F", "esc", "s") // leave filter -> cycle source all -> scrollback
		if !strings.Contains(a.Status(), string(panesearch.SourceScrollback)) {
			t.Fatalf("s did not cycle source: status=%q", a.Status())
		}
		d.Keys("o")
		if a.Modal() != "" || a.Mode() != ModeTerminal {
			t.Fatalf("o did not attach live terminal: modal=%q mode=%q", a.Modal(), a.Mode())
		}
	})

	t.Run("opt-out search.panesIncludeTranscripts on vs off", func(t *testing.T) {
		aOff, dOff, _ := newTestApp(t, map[string]any{SetSearchPanesIncludeTranscripts: false})
		dOff.Keys("F")
		dOff.Type("KRYPTONITE_LOG")
		if got := aOff.activePicker(paneSearchModalID).Model().Filtered(); len(got) != 0 {
			t.Fatalf("with search.panesIncludeTranscripts=false, matched %+v, want 0", got)
		}

		aOn, dOn, panes := newTestApp(t, map[string]any{SetSearchPanesIncludeTranscripts: true})
		dOn.Keys("F")
		dOn.Type("KRYPTONITE_LOG")
		if got := aOn.activePicker(paneSearchModalID).Model().Filtered(); len(got) != 1 || got[0].ID != panes["records"].ID {
			t.Fatalf("with search.panesIncludeTranscripts=true, matched %+v, want %s", got, panes["records"].ID)
		}
	})

	t.Run("opt-out search.panesIncludeArchived on vs off", func(t *testing.T) {
		aOff, dOff, panes := newTestApp(t, map[string]any{SetSearchPanesIncludeArchived: false})
		dOff.Keys("F")
		dOff.Type("archived-pane")
		if got := aOff.activePicker(paneSearchModalID).Model().Filtered(); len(got) != 0 {
			t.Fatalf("with search.panesIncludeArchived=false, matched %+v, want 0", got)
		}
		// Pressing `a` in normal picker mode toggles archived panes on.
		dOff.Keys("esc", "a")
		if got := aOff.activePicker(paneSearchModalID).Model().Filtered(); len(got) != 1 || got[0].ID != panes["archived"].ID {
			t.Fatalf("after `a` toggle, matched %+v, want %s", got, panes["archived"].ID)
		}

		aOn, dOn, panesOn := newTestApp(t, map[string]any{SetSearchPanesIncludeArchived: true})
		dOn.Keys("F")
		dOn.Type("archived-pane")
		if got := aOn.activePicker(paneSearchModalID).Model().Filtered(); len(got) != 1 || got[0].ID != panesOn["archived"].ID {
			t.Fatalf("with search.panesIncludeArchived=true, matched %+v, want %s", got, panesOn["archived"].ID)
		}
	})
}
