package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
	"ofam.dev/ochazuke/ui"
)

func TestRenderPerfProjectNavSingleBuildPerKeypress(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	// Open the Project Page workspace and focus project.nav.
	d.Keys("P")
	_ = d.Frame()
	if !strings.HasPrefix(a.Focus(), projectNavPrefix) {
		t.Fatalf("expected focus in project.nav, got %q", a.Focus())
	}

	// Step down/up across section boundaries in project.nav; each keypress plus
	// its subsequent screen render must invoke a.build(s) at most ONCE.
	for i, key := range []string{"j", "j", "j", "k", "k"} {
		prevFocus := a.Focus()
		before := a.viewMemo.builds
		d.Keys(key)
		_ = d.Frame()
		delta := a.viewMemo.builds - before
		if delta > 1 {
			t.Fatalf("step %d (%q: %s -> %s): expected <= 1 a.build(s), got %d", i, key, prevFocus, a.Focus(), delta)
		}
		if delta == 0 {
			t.Fatalf("step %d (%q: %s -> %s): expected 1 a.build(s), got 0", i, key, prevFocus, a.Focus())
		}
		if a.Focus() == prevFocus {
			t.Fatalf("step %d (%q): focus did not move from %q", i, key, prevFocus)
		}
		// Calling View again for the same settled state must hit the memo cache.
		after := a.viewMemo.builds
		_ = a.View(a.State())
		if a.viewMemo.builds != after {
			t.Fatalf("step %d (%q): redundant View(s) caused rebuild (%d -> %d)", i, key, after, a.viewMemo.builds)
		}
	}
}

// linearBindingsForReference is the uncached reference implementation of
// bindingsFor using direct linear scans and expr.Parse for parity verification.
func linearBindingsForReference(a *App, s core.State, chain []core.ContextFrame, modes, exclude []string, prefix string) []activeBinding {
	facts := make([]map[string]any, len(chain)+1)
	for i := len(chain) - 1; i >= 0; i-- {
		facts[i] = map[string]any{}
		for k, v := range facts[i+1] {
			facts[i][k] = v
		}
		for k, v := range chain[i].Facts {
			facts[i][k] = v
		}
	}
	topModalID := ""
	if n := len(s.Modals); n > 0 {
		topModalID = s.Modals[n-1].ID
	}
	env := func(level int) map[string]any {
		return map[string]any{
			"mode":  a.Engine.Mode(s),
			"scope": chain[level].Scope,
			"facts": facts[level],
			"modal": map[string]any{"open": len(s.Modals) > 0, "id": topModalID},
			"count": nil,
			"layer": nil,
		}
	}
	var out []activeBinding
	seen := map[string]bool{}
	add := func(level int, scope string) {
		for _, mode := range append(slices.Clone(modes), "") {
			for _, b := range a.bind.bindings {
				if b.Scope != scope || b.Mode != mode || b.Unbind || (b.Command == "" && b.Layer == nil) {
					continue
				}
				if b.Command != "" && a.bind.Keymap.Commands.HasAnyTag(b.Command, exclude) {
					continue
				}
				if cond, err := expr.Parse(b.When); err != nil || !cond.Eval(env(level)) {
					continue
				}
				keys := strings.Join(b.Keys, " ")
				if keys == "" {
					keys = "‹" + b.Class + "›"
				}
				if seen[keys] {
					continue
				}
				seen[keys] = true
				command := b.Command
				if b.Layer != nil {
					command = "layer." + b.Layer.Name
				}
				if len(b.Args) == 1 {
					for k, v := range b.Args {
						if k == "via" {
							continue
						}
						if str := fmt.Sprint(v); !strings.HasPrefix(str, "$") {
							command += " " + str
						}
					}
				}
				out = append(out, activeBinding{
					keys:       prefix + keys,
					command:    command,
					rawCommand: b.Command,
					args:       staticBindingArgs(b.Args),
					hint:       b.Hint,
					scope:      scope,
					mode:       mode,
					hidden:     b.Hidden || b.Class != "",
				})
			}
		}
	}
	for i, f := range chain {
		if f.Scope != "" {
			add(i, f.Scope)
		}
	}
	add(0, "")
	return out
}

func TestRenderPerfBindingsForParityAndCache(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	checkParity := func(label string, exclude []string, prefix string) {
		t.Helper()
		s := a.State()
		root := a.View(s)
		chain := ui.ContextChain(root, s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
		modes := a.bind.Keymap.ModeChain(a.Engine.Mode(s))

		got := a.bindingsFor(s, chain, modes, exclude, prefix)
		want := linearBindingsForReference(a, s, chain, modes, exclude, prefix)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: bindingsFor mismatch:\ngot  (%d): %+v\nwant (%d): %+v", label, len(got), got, len(want), want)
		}

		// Also verify hintLine matches when run with a freshly invalidated index.
		hintCached := a.hintLine(s, chain)
		a.viewMemo.binds = nil
		hintFresh := a.hintLine(s, chain)
		if hintCached != hintFresh {
			t.Fatalf("%s: hintLine mismatch:\ncached: %q\nfresh:  %q", label, hintCached, hintFresh)
		}
	}

	// 1. Panes rail
	_ = d.Frame()
	checkParity("panes-rail", nil, "")
	idx1 := a.compiledBindings()
	idx2 := a.compiledBindings()
	if idx1 != idx2 {
		t.Fatalf("expected compiledBindings to reuse cached index across calls")
	}

	// 2. Project workspace nav
	d.Keys("P")
	_ = d.Frame()
	checkParity("project-nav", nil, "")

	// 3. Leader-style exclude tags and prefix
	checkParity("leader-exclude", leader.ExcludeTags, "ctrl+a ")

	// 4. Work space
	d.Keys("2")
	_ = d.Frame()
	checkParity("work-space", nil, "")

	// 5. Rebuilding keymap invalidates the compiled index.
	if err := a.rebuildKeymap(); err != nil {
		t.Fatal(err)
	}
	idx3 := a.compiledBindings()
	if idx3 == idx1 {
		t.Fatalf("expected rebuildKeymap to invalidate compiledBindings index")
	}
	checkParity("post-rebuild", nil, "")
}

func TestRenderPerfBuildRailParity50Panes30Pages(t *testing.T) {
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

	// Create 5 root projects with nested child tasks (depth 2-3).
	roots := make([]data.Record, 5)
	tasks := make([]data.Record, 0, 15)
	for p := range 5 {
		roots[p] = mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Project %d", p+1),
			"workStatus": "active",
		})
		child := mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Subtask %d.1", p+1),
			"parent":     roots[p].Ref(),
			"workStatus": "active",
		})
		grandchild := mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Leaf %d.1.1", p+1),
			"parent":     child.Ref(),
			"workStatus": "todo",
		})
		tasks = append(tasks, roots[p], child, grandchild)
	}

	now := time.Now().UTC()
	panes := make([]data.Record, 50)
	for i := range 50 {
		fields := map[string]any{
			"title":      fmt.Sprintf("pane-%02d", i),
			"runStatus":  "running",
			"promptedAt": now.Add(-time.Duration(i) * time.Second).Format(data.TimeFormat),
		}
		if i%10 != 0 { // 5 panes intentionally unassigned
			fields["task"] = tasks[i%len(tasks)].Ref()
		}
		panes[i] = mk(data.KindPane, fields)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create 30 pages exercising unread, read, open question, answered question, and archived unread.
	for i := range 30 {
		paneID := panes[i].ID
		switch i % 5 {
		case 0:
			// Unread plain page -> attention = true
			if _, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
				Pane:     paneID,
				Title:    fmt.Sprintf("Unread Page %d", i),
				PageKind: "response",
				Root:     ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{{Kind: ui.KindText, Props: map[string]any{"text": "hi"}}}},
			}); err != nil {
				t.Fatalf("create unread page %d: %v", i, err)
			}
		case 1:
			// Read plain page -> attention = false
			res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
				Pane:     paneID,
				Title:    fmt.Sprintf("Read Page %d", i),
				PageKind: "response",
				Root:     ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{{Kind: ui.KindText, Props: map[string]any{"text": "done"}}}},
			})
			if err != nil {
				t.Fatalf("create read page %d: %v", i, err)
			}
			if _, err := a.Router.MarkViewed(res.ID, viewer); err != nil {
				t.Fatalf("mark viewed %d: %v", i, err)
			}
		case 2:
			// Viewed page WITH an unanswered question card -> attention = true
			res, err := a.Router.Ask(User, mcpui.AskArgs{
				Pane:    paneID,
				Title:   fmt.Sprintf("Open Question %d", i),
				Choices: []mcpui.Choice{{ID: "a", Label: "Option A"}, {ID: "b", Label: "Option B"}},
			})
			if err != nil {
				t.Fatalf("ask %d: %v", i, err)
			}
			if _, err := a.Router.MarkViewed(res.ID, viewer); err != nil {
				t.Fatalf("mark viewed open q %d: %v", i, err)
			}
		case 3:
			// Viewed page WITH an answered question card -> attention = false
			res, err := a.Router.Ask(User, mcpui.AskArgs{
				Pane:    paneID,
				Title:   fmt.Sprintf("Answered Question %d", i),
				Choices: []mcpui.Choice{{ID: "a", Label: "Option A"}, {ID: "b", Label: "Option B"}},
			})
			if err != nil {
				t.Fatalf("ask answered %d: %v", i, err)
			}
			if _, err := a.Router.SubmitAnswer(res.ID, res.CardID, map[string]any{"choice": "Option A"}, User); err != nil {
				t.Fatalf("answer %d: %v", i, err)
			}
			if _, err := a.Router.MarkViewed(res.ID, viewer); err != nil {
				t.Fatalf("mark viewed answered q %d: %v", i, err)
			}
		case 4:
			// Archived unread page -> attention = false
			res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
				Pane:     paneID,
				Title:    fmt.Sprintf("Archived Page %d", i),
				PageKind: "response",
				Root:     ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{{Kind: ui.KindText, Props: map[string]any{"text": "old"}}}},
			})
			if err != nil {
				t.Fatalf("create archived page %d: %v", i, err)
			}
			if _, _, err := store.Archive(res.Ref, User); err != nil {
				t.Fatalf("archive page %d: %v", i, err)
			}
		}
	}

	// Add telemetry needs_you on pane 40 and pending approval on pane 41.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			panes[40].ID: {PaneID: panes[40].ID, State: daemon.StateNeedsYou},
		},
		PendingApprovals: []*daemon.PendingApproval{
			{RequestID: "apr-1", PaneID: panes[41].ID, Tool: "bash"},
		},
	}, false)

	tbl := a.buildRail()
	if len(tbl.Rows) != len(panes) {
		t.Fatalf("rail row count = %d, want %d", len(tbl.Rows), len(panes))
	}

	paneByID := make(map[string]data.Record, len(panes))
	for _, p := range panes {
		paneByID[p.ID] = p
	}

	attnTrueCount := 0
	for _, row := range tbl.Rows {
		pane := paneByID[row.Ref.ID]
		wantProj, wantRef := a.resolvePaneProject(pane)
		if gotProj := data.Text(row.Cells["project"]); gotProj != wantProj {
			t.Fatalf("pane %s project = %q, want %q", pane.ID, gotProj, wantProj)
		}
		if wantRef != nil {
			if gotID, _ := row.Facts["projectId"].(string); gotID != wantRef.ID {
				t.Fatalf("pane %s projectId = %q, want %q", pane.ID, gotID, wantRef.ID)
			}
		} else if _, exists := row.Facts["projectId"]; exists {
			t.Fatalf("unassigned pane %s unexpectedly has projectId fact", pane.ID)
		}

		wantAttn := a.attention(pane.ID)
		if row.Unread != wantAttn {
			t.Fatalf("pane %s Unread = %v, want %v (via a.attention)", pane.ID, row.Unread, wantAttn)
		}
		if row.Unread {
			attnTrueCount++
		}
	}
	// Expect 6 unread (i%5==0) + 6 open questions (i%5==2) + pane 40 (needs_you) + pane 41 (approval) = 14.
	if attnTrueCount != 14 {
		t.Fatalf("attention pane count = %d, want 14", attnTrueCount)
	}
}

func TestRenderPerfContentFileCache(t *testing.T) {
	a, _ := demo(t)
	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "spec.md")
	if err := os.WriteFile(docPath, []byte("# Version 1\nBody one"), 0o644); err != nil {
		t.Fatal(err)
	}

	reads := 0
	a.opts.ReadFile = func(path string) ([]byte, error) {
		reads++
		return os.ReadFile(path)
	}

	doc, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindDocument,
		ID:   "cached-doc",
		Fields: map[string]any{
			"title":    "Cached Spec",
			"filePath": docPath,
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	schema := a.schema(data.KindDocument)

	for i := range 5 {
		got := a.resolveContentFiles(schema, *doc)
		if got["body"] != "# Version 1\nBody one" {
			t.Fatalf("call %d: unexpected body %q", i, got["body"])
		}
	}
	if reads != 1 {
		t.Fatalf("expected 1 disk read for unchanged file across 5 resolves, got %d", reads)
	}

	// Updating content/size invalidates the cache entry.
	if err := os.WriteFile(docPath, []byte("# Version 2\nUpdated body content!"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := a.resolveContentFiles(schema, *doc)
	if got["body"] != "# Version 2\nUpdated body content!" {
		t.Fatalf("expected updated content, got %q", got["body"])
	}
	if reads != 2 {
		t.Fatalf("expected 2 disk reads after file change, got %d", reads)
	}

	// Cache stays bounded at maxContentFileCacheEntries.
	for i := range maxContentFileCacheEntries + 10 {
		p := filepath.Join(tmpDir, fmt.Sprintf("f-%03d.md", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("file %d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.readContentFileCached(p); err != nil {
			t.Fatal(err)
		}
	}
	if a.viewMemo.fileCache == nil || len(a.viewMemo.fileCache.entries) > maxContentFileCacheEntries {
		t.Fatalf("expected fileCache size <= %d, got %d", maxContentFileCacheEntries, len(a.viewMemo.fileCache.entries))
	}
}
