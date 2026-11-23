package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

type wave3FakeHost struct {
	mu       sync.Mutex
	sessions map[string]ptyhost.Frame
	writes   map[string][][]byte
}

func newWave3FakeHost() *wave3FakeHost {
	return &wave3FakeHost{
		sessions: map[string]ptyhost.Frame{},
		writes:   map[string][][]byte{},
	}
}

func (h *wave3FakeHost) setLines(id string, lines []string, rev uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[id] = ptyhost.Frame{
		Cols:     100,
		Rows:     max(len(lines), 10),
		Revision: rev,
		Lines:    slices.Clone(lines),
		Plain:    slices.Clone(lines),
	}
}

func (h *wave3FakeHost) Open(id, _, _ string, cols, rows int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[id]; !ok {
		h.sessions[id] = ptyhost.Frame{Cols: cols, Rows: rows, Revision: 1, Lines: []string{""}, Plain: []string{""}}
	}
	return nil
}

func (h *wave3FakeHost) Write(id string, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := append([]byte(nil), data...)
	h.writes[id] = append(h.writes[id], cp)
	return nil
}

func (h *wave3FakeHost) Resize(string, int, int) error { return nil }

func (h *wave3FakeHost) Snapshot(id string) (ptyhost.Frame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.sessions[id]
	return f, ok
}

func (h *wave3FakeHost) Close(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
	return nil
}

func (h *wave3FakeHost) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions = map[string]ptyhost.Frame{}
}

func (h *wave3FakeHost) allWrites(id string) [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]byte, len(h.writes[id]))
	copy(out, h.writes[id])
	return out
}

func (h *wave3FakeHost) joinedWrites(id string) string {
	var b bytes.Buffer
	for _, w := range h.allWrites(id) {
		b.Write(w)
	}
	return b.String()
}

// 1. Stable project-pane rail grouping + panes.group setting + panes.group.toggle
func TestWave3StableProjectPaneRailGrouping(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	_, client := startTestDaemon(t, store)

	projA, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "AlphaProject", "workStatus": "active"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	subA, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "AlphaSubtask", "workStatus": "active", "parent": projA.Ref()}}, User)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "BetaProject", "workStatus": "active"}}, User)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	p1, _, _ := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "alpha-newest", "task": subA.Ref(), "runStatus": "running", "promptedAt": now.Format(data.TimeFormat),
	}}, User)
	p2, _, _ := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "alpha-older", "task": projA.Ref(), "runStatus": "idle", "promptedAt": now.Add(-2 * time.Minute).Format(data.TimeFormat),
	}}, User)
	p3, _, _ := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "beta-pane", "task": projB.Ref(), "runStatus": "running", "promptedAt": now.Add(-5 * time.Minute).Format(data.TimeFormat),
	}}, User)
	p4, _, _ := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "loose-pane", "runStatus": "done", "promptedAt": now.Add(-10 * time.Minute).Format(data.TimeFormat),
	}}, User)

	a, err := New(store, Options{Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	if got := a.panesGroupMode(); got != PanesGroupProjectPane {
		t.Fatalf("default panes.group = %q, want %q", got, PanesGroupProjectPane)
	}

	// Verify project headers (no session middle layer!) and stable promptedAt order.
	items := a.rail().Items()
	var labels []string
	for _, it := range items {
		if it.Header {
			labels = append(labels, "H:"+it.Group)
		} else {
			labels = append(labels, "P:"+it.Row.Ref.ID)
		}
	}
	wantLabels := []string{
		"H:AlphaProject", "P:" + p1.ID, "P:" + p2.ID,
		"H:BetaProject", "P:" + p3.ID,
		"H:Unassigned", "P:" + p4.ID,
	}
	if !slices.Equal(labels, wantLabels) {
		t.Fatalf("rail items = %v, want %v", labels, wantLabels)
	}

	// Send hook telemetry: needs_you on p4 (oldest!), running on p3, idle on p2, done on p1.
	ctx := context.Background()
	for _, ev := range []daemon.HookEvent{
		{PaneID: p4.ID, Harness: "claude", Kind: daemon.HookKindState, State: daemon.StateNeedsYou, Summary: "needs approval"},
		{PaneID: p3.ID, Harness: "claude", Kind: daemon.HookKindActivity, State: daemon.StateRunning, Tool: "Read"},
		{PaneID: p2.ID, Harness: "claude", Kind: daemon.HookKindState, State: daemon.StateIdle},
		{PaneID: p1.ID, Harness: "claude", Kind: daemon.HookKindState, State: daemon.StateDone},
	} {
		if _, err := client.HookEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	a.WatchCheck()
	wake(d)

	// CRITICAL invariant: panes NEVER reorder out of promptedAt order when needs_you or output arrives!
	var afterLabels []string
	for _, it := range a.rail().Items() {
		if it.Header {
			afterLabels = append(afterLabels, "H:"+it.Group)
		} else {
			afterLabels = append(afterLabels, "P:"+it.Row.Ref.ID)
		}
	}
	if !slices.Equal(afterLabels, wantLabels) {
		t.Fatalf("telemetry reordered rail: got %v, want %v", afterLabels, wantLabels)
	}

	// Verify clear status indicator symbols render for all 4 states.
	frame := d.Frame()
	for _, want := range []string{"AlphaProject", "BetaProject", "Unassigned", "✓ done", "○ idle", "▶ running", "◆ needs_you"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("rail frame missing %q:\n%s", want, frame)
		}
	}

	// Toggle to flat via panes.group.toggle (g p).
	d.Keys("g", "p")
	if a.panesGroupMode() != PanesGroupFlat {
		t.Fatalf("after g p, panes.group = %q, want flat", a.panesGroupMode())
	}
	for _, it := range a.rail().Items() {
		if it.Header {
			t.Fatalf("flat mode should not have group headers, got %+v", it)
		}
	}
	if got := []string{a.rail().Rows[0].Ref.ID, a.rail().Rows[1].Ref.ID, a.rail().Rows[2].Ref.ID, a.rail().Rows[3].Ref.ID}; !slices.Equal(got, []string{p1.ID, p2.ID, p3.ID, p4.ID}) {
		t.Fatalf("flat rail order = %v", got)
	}

	// Toggle back to project-pane.
	d.Keys("g", "p")
	if a.panesGroupMode() != PanesGroupProjectPane || !a.rail().Items()[0].Header {
		t.Fatalf("second g p did not restore project-pane grouping")
	}

	// Verify opt-out via settings.json ("panes.group": "flat").
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, settingsFileName), []byte(`{"panes.group": "flat"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	flatApp, err := New(store, Options{ConfigDir: cfgDir, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flatApp.Close() })
	if flatApp.panesGroupMode() != PanesGroupFlat || flatApp.rail().GroupField != "" {
		t.Fatalf("settings.json opt-out failed: mode=%q groupField=%q", flatApp.panesGroupMode(), flatApp.rail().GroupField)
	}
}

// 2. Project header overview + filterable/sortable/groupable Context Items table on right of 1 Panes
func TestWave3ProjectHeaderOverviewAndContextTable(t *testing.T) {
	a, d := demo(t)
	pane := titled(t, a, data.KindPane, "claude: wave 5B")
	a.harvestPrompt(pane, "Check https://example.com/spec and app/wave3_rail.go:42 and ofam/matomeru#101")

	// Move cursor up from first pane to the "matomeru-v2" Project header row.
	d.Keys("k")
	groupName, projRef, onProj := a.selectedRailProject()
	if !onProj || groupName != "matomeru-v2" || projRef == nil {
		t.Fatalf("k did not land on matomeru-v2 project header: onProj=%v group=%q ref=%v", onProj, groupName, projRef)
	}

	frame := d.Frame()
	for _, want := range []string{"Project · matomeru-v2", "Tasks (", "Notes (", "Links (", "Context Items", "https://example.com/spec", "app/wave3_rail.go:42", "ofam/matomeru#101"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("project overview missing %q:\n%s", want, frame)
		}
	}

	// Press enter on project header -> focuses project.context.table on the right.
	d.Keys("enter")
	if a.Focus() != projectContextTable {
		t.Fatalf("enter on project header did not focus %s: got %q", projectContextTable, a.Focus())
	}

	// Filter Context Items table with /.
	d.Keys("/")
	d.Type("example.com")
	d.Keys("enter")
	ctxTbl := a.nav.tables[projectContextTable]
	if ctxTbl == nil || len(ctxTbl.Filtered()) != 1 {
		t.Fatalf("context table filter did not narrow to 1 item: %+v", ctxTbl)
	}

	// Star, yank, and attach the focused context item.
	d.Keys("s")
	if !strings.Contains(a.Status(), "starred") {
		t.Fatalf("s did not star context item: status=%q", a.Status())
	}
	d.Keys("y")
	if a.Register(core.RegUnnamed) != "https://example.com/spec" {
		t.Fatalf("y did not yank context item: reg=%q", a.Register(core.RegUnnamed))
	}
	d.Keys("a")
	rels, err := a.Store.Related(*projRef, data.RelContext, data.Outgoing)
	if err != nil || len(rels) != 1 || rels[0].To.ID != "https://example.com/spec" {
		t.Fatalf("a did not attach context item via RelContext: rels=%+v err=%v", rels, err)
	}

	// Clear filter and verify sort (o) and group (g b).
	d.Keys("/", "esc", "o")
	if a.nav.tables[projectContextTable].SortField == "" {
		t.Fatal("o did not sort project context table")
	}
	d.Keys("g", "b")
	d.Type("kind")
	d.Keys("enter")
	if a.nav.tables[projectContextTable].GroupField != "kind" {
		t.Fatalf("g b did not group project context table by kind: got %q", a.nav.tables[projectContextTable].GroupField)
	}
}

// 3. High-signal Context Items harvesting (prompts, response pages, screen frames) + Context Picker
func TestWave3ContextHarvestingAndPicker(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	host := newWave3FakeHost()
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "ctx-pane", "cwd": t.TempDir(), "runStatus": "running",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	// Visible screen frame with OSC-8 link, URL, PR ref, file path, plus noisy bare SHA & branch name.
	host.setLines(pane.ID, []string{
		"See \x1b]8;;https://docs.ofam.dev/guide\x07Guide\x1b]8;;\x07 and ofam/ochazuke#77",
		"Edited src/handler.go:88 on branch feat/auth commit deadbeef1234",
	}, 1)

	// Agent official Response page with URL and file reference.
	router := mcpui.New(store)
	if _, err := router.CreatePage(seedAgent, mcpui.CreatePageArgs{
		Pane:     pane.ID,
		Title:    "Response: plan",
		Slug:     "resp",
		PageKind: "response",
		Root: ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{
			"markdown": "Updated docs/design.md and opened https://github.com/ofam/matomeru/pull/99",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Host: host, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Render once so liveScreen snapshots and harvests the visible terminal frame.
	_ = d.Frame()
	// Also harvest a user prompt.
	a.harvestPrompt(*pane, "Check https://prompt.example.org/issue and README.md")

	all := a.allContextItems()
	vals := make([]string, len(all))
	for i, it := range all {
		vals[i] = it.Value
	}
	for _, want := range []string{
		"https://docs.ofam.dev/guide",
		"ofam/ochazuke#77",
		"src/handler.go:88",
		"docs/design.md",
		"https://github.com/ofam/matomeru/pull/99",
		"https://prompt.example.org/issue",
		"README.md",
	} {
		if !slices.Contains(vals, want) {
			t.Fatalf("harvested context items missing %q; got %v", want, vals)
		}
	}
	// Noise must be excluded: bare SHAs and uncontextualized git branches.
	for _, noise := range []string{"deadbeef1234", "feat/auth"} {
		if slices.Contains(vals, noise) {
			t.Fatalf("harvested context items should not contain noise %q; got %v", noise, vals)
		}
	}

	// Open searchable Context Picker with g c.
	d.Keys("g", "c")
	if a.Modal() != "context" || a.Mode() != ModePicker {
		t.Fatalf("g c did not open context picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Test y (yank when query empty).
	topItem := a.activePicker("context").Model().Items[0].Title
	d.Keys("y")
	if a.Modal() != "" || a.Register(core.RegUnnamed) != topItem {
		t.Fatalf("y in context picker: modal=%q reg=%q want=%q", a.Modal(), a.Register(core.RegUnnamed), topItem)
	}

	// Test a (attach via RelContext to focused pane).
	d.Keys("g", "c", "a")
	rels, err := store.Related(pane.Ref(), data.RelContext, data.Outgoing)
	if err != nil || len(rels) != 1 || rels[0].To.ID != topItem {
		t.Fatalf("a in context picker did not attach via RelContext: rels=%+v err=%v", rels, err)
	}

	// Test search + enter (opens via handlers -> queues system-open run for URL).
	d.Keys("g", "c", "/")
	d.Type("docs.ofam.dev")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("enter in context picker left modal %q open", a.Modal())
	}
	runsRes, _ := store.Query(data.Query{Kind: data.KindRun})
	if !slices.ContainsFunc(runsRes.Items, func(r data.Record) bool {
		return strings.Contains(r.Text("command"), "https://docs.ofam.dev/guide")
	}) {
		t.Fatalf("enter in context picker did not invoke handler for URL: status=%q runs=%+v", a.Status(), runsRes.Items)
	}
}

// 4. Live terminal hints (ctrl+a f and f) using ochazuke/widgets/hints
func TestWave3LiveTerminalHints(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	host := newWave3FakeHost()
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "hints-pane", "cwd": t.TempDir(), "runStatus": "running",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	host.setLines(pane.ID, []string{
		"Open https://ofam.dev/matomeru or edit app/view.go:10 or see ofam/matomeru#55",
	}, 1)

	a, err := New(store, Options{Host: host, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Trigger pane.hints via `f` from 1 Panes rail.
	d.Keys("f")
	if a.Mode() != ModeHints || a.panes.hints == nil || !a.panes.hints.session.Active() {
		t.Fatalf("f did not start ModeHints: mode=%q status=%q", a.Mode(), a.Status())
	}
	targets := a.panes.hints.session.Targets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 hint targets, got %d: %+v", len(targets), targets)
	}
	// Overlay renders label badges over the dimmed terminal view.
	hintFrame := d.Frame()
	if !strings.Contains(hintFrame, "["+targets[0].Label+"]") || !strings.Contains(hintFrame, "HINTS") {
		t.Fatalf("hints overlay badges not rendered:\n%s", hintFrame)
	}
	// Visible matches are recorded into the pane's context items.
	if len(a.paneContextItems(pane.ID)) < 3 {
		t.Fatalf("pane.hints did not record visible matches into context items: %+v", a.paneContextItems(pane.ID))
	}

	// 1. Lowercase label copies target to clipboard + registers.
	d.Keys(targets[0].Label)
	if a.Mode() != ModeNormal || a.Register(core.RegUnnamed) != targets[0].Value || a.rt.live.clipboard != targets[0].Value {
		t.Fatalf("lowercase hint: mode=%q reg=%q clip=%q want=%q", a.Mode(), a.Register(core.RegUnnamed), a.rt.live.clipboard, targets[0].Value)
	}

	// 2. Enter live terminal, trigger via ctrl+a f, then UPPERCASE label inserts into pane!
	d.Keys("enter", "enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("failed to enter ModeTerminal: mode=%q", a.Mode())
	}
	d.Keys("ctrl+a", "f")
	if a.Mode() != ModeHints || a.panes.hints == nil {
		t.Fatalf("ctrl+a f in ModeTerminal did not activate hints: mode=%q status=%q", a.Mode(), a.Status())
	}
	upper := strings.ToUpper(targets[1].Label)
	d.Keys(upper)
	if a.Mode() != ModeTerminal {
		t.Fatalf("after uppercase hint, expected return to ModeTerminal, got %q", a.Mode())
	}
	if got := host.joinedWrites(pane.ID); !strings.Contains(got, targets[1].Value) {
		t.Fatalf("uppercase hint did not insert %q into pane; writes=%q", targets[1].Value, got)
	}

	// 3. ctrl+label opens target via handlers!
	d.Keys("ctrl+a", "f")
	d.Keys("ctrl+" + targets[0].Label)
	runsRes, _ := store.Query(data.Query{Kind: data.KindRun})
	if !slices.ContainsFunc(runsRes.Items, func(r data.Record) bool {
		return strings.Contains(r.Text("command"), targets[0].Value)
	}) {
		t.Fatalf("ctrl+label did not open %q via handlers: status=%q", targets[0].Value, a.Status())
	}

	// 4. esc and q cancel hint mode cleanly.
	d.Keys("ctrl+a", "f", "esc")
	if a.Mode() != ModeTerminal || a.panes.hints != nil {
		t.Fatalf("esc did not cancel hints cleanly: mode=%q hints=%v", a.Mode(), a.panes.hints)
	}
	d.Keys("ctrl+a", "f", "q")
	if a.Mode() != ModeTerminal || a.panes.hints != nil {
		t.Fatalf("q did not cancel hints cleanly: mode=%q hints=%v", a.Mode(), a.panes.hints)
	}
}

// 5. Prompt composer box (send, queue, interrupt, drafts) + async @file / $skill / /command completion
func TestWave3PromptComposerModesDraftsAndAsyncCompletion(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")

	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "pkg", "router.go"), []byte("package pkg\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	script(t, binDir, "o-ai", `
if [ "$3" = "ai.skill" ]; then
  echo '{"items":[{"id":"tdd-loop","fields":{"name":"tdd-loop","description":"Test-driven workflow"}}]}'
elif [ "$3" = "ai.agent" ]; then
  echo '{"items":[{"id":"reviewer","fields":{"name":"reviewer","description":"Code review agent"}}]}'
else
  echo '{"items":[]}'
fi
`)

	cfgDir := t.TempDir()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	_, client := startTestDaemon(t, store)
	host := newWave3FakeHost()
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "comp-pane", "cwd": workDir, "runStatus": "running",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	host.setLines(pane.ID, []string{"ready"}, 1)

	a, err := New(store, Options{
		ConfigDir:     cfgDir,
		Daemon:        client,
		Host:          host,
		ExtensionDirs: []string{binDir},
		Watch:         time.Hour,
		FS:            smokeFS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.panes.interruptDelay = 15 * time.Millisecond
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Open composer with `c` on 1 Panes, wait for async @file and o-ai discovery.
	d.Keys("c")
	if a.Modal() != "composer" || a.Mode() != ModeComposer {
		t.Fatalf("c did not open prompt composer: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	a.WaitBackground()
	wake(d)

	// Test async @file completion + multi-line drafting + $skill completion.
	d.Type("Inspect @rout")
	cm := a.activeComposer()
	if cm == nil || !cm.compOpen || !strings.Contains(d.Frame(), "@pkg/router.go") {
		t.Fatalf("@file completion did not populate: comp=%+v\n%s", cm, d.Frame())
	}
	d.Keys("tab") // accepts @pkg/router.go
	d.Keys("enter")
	d.Type("using $tdd")
	if !cm.compOpen || !strings.Contains(d.Frame(), "$tdd-loop") {
		t.Fatalf("$skill completion did not populate from o-ai: items=%+v\n%s", cm.compItems, d.Frame())
	}
	d.Keys("tab") // accepts $tdd-loop
	if !strings.Contains(cm.in.Value(), "@pkg/router.go") || !strings.Contains(cm.in.Value(), "\n") || !strings.Contains(cm.in.Value(), "$tdd-loop") {
		t.Fatalf("composer value = %q", cm.in.Value())
	}

	// Test /command completion too, then save as draft via esc!
	d.Type("/comp")
	if !cm.compOpen || !strings.Contains(d.Frame(), "/compact") {
		t.Fatalf("/command completion did not populate: items=%+v", cm.compItems)
	}
	d.Keys("esc") // closes completion popup first
	d.Keys("esc") // closes composer and saves draft to drafts.json
	if a.Modal() != "" {
		t.Fatalf("esc did not close composer: modal=%q", a.Modal())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, draftsFileName)); err != nil {
		t.Fatalf("composer draft not persisted to drafts.json: %v", err)
	}

	// Reopen composer and restore saved draft via ctrl+p, then deliver in `send` mode (ctrl+s).
	d.Keys("c", "ctrl+p")
	if cm := a.activeComposer(); cm == nil || !strings.Contains(cm.in.Value(), "@pkg/router.go") {
		t.Fatalf("ctrl+p did not restore composer draft: cm=%+v", cm)
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close composer: modal=%q", a.Modal())
	}
	sent := host.joinedWrites(pane.ID)
	if !strings.Contains(sent, "\x1b[200~") || !strings.Contains(sent, "@pkg/router.go") || !strings.Contains(sent, "\x1b[201~\r") {
		t.Fatalf("send mode did not bracketed-paste + Enter: %q", sent)
	}

	// Test `queue` mode: while daemon reports running, prompt is queued; when idle arrives, it auto-delivers!
	ctx := context.Background()
	if _, err := client.HookEvent(ctx, daemon.HookEvent{PaneID: pane.ID, Harness: "claude", Kind: daemon.HookKindState, State: daemon.StateRunning}); err != nil {
		t.Fatal(err)
	}
	a.refreshTelemetry()

	d.Keys("c", "shift+tab")
	if cm := a.activeComposer(); cm == nil || cm.delivery != ComposerModeQueue {
		t.Fatalf("shift+tab did not switch to queue mode: got %+v", cm)
	}
	d.Type("queued-followup-prompt")
	d.Keys("ctrl+s")
	if a.queuedPromptCount(pane.ID) != 1 {
		t.Fatalf("queue mode did not hold prompt while running: count=%d", a.queuedPromptCount(pane.ID))
	}
	if strings.Contains(host.joinedWrites(pane.ID), "queued-followup-prompt") {
		t.Fatal("queued prompt delivered prematurely while pane was running")
	}

	// Now daemon reports pane idle -> WatchCheck + wake delivers queued prompt!
	if _, err := client.HookEvent(ctx, daemon.HookEvent{PaneID: pane.ID, Harness: "claude", Kind: daemon.HookKindState, State: daemon.StateIdle}); err != nil {
		t.Fatal(err)
	}
	a.WatchCheck()
	wake(d)
	if a.queuedPromptCount(pane.ID) != 0 {
		t.Fatalf("queued prompt was not flushed on idle: count=%d", a.queuedPromptCount(pane.ID))
	}
	if !strings.Contains(host.joinedWrites(pane.ID), "\x1b[200~queued-followup-prompt\x1b[201~\r") {
		t.Fatalf("flushed queued prompt not written via bracketed paste: %q", host.joinedWrites(pane.ID))
	}

	// Test `interrupt` mode: sends Esc (\x1b), brief pause, then bracketed paste!
	writesBefore := len(host.allWrites(pane.ID))
	d.Keys("c", "shift+tab", "shift+tab")
	if cm := a.activeComposer(); cm == nil || cm.delivery != ComposerModeInterrupt {
		t.Fatalf("two shift+tabs did not switch to interrupt mode: got %+v", cm)
	}
	d.Type("urgent-interrupt-prompt")
	d.Keys("ctrl+s")
	a.WaitBackground()
	wake(d)

	newWrites := host.allWrites(pane.ID)[writesBefore:]
	if len(newWrites) < 2 || !bytes.Equal(newWrites[0], []byte{0x1b}) || !strings.Contains(string(newWrites[1]), "\x1b[200~urgent-interrupt-prompt\x1b[201~\r") {
		t.Fatalf("interrupt mode writes = %q, want Esc followed by bracketed paste", newWrites)
	}
}

// 6. Multi-question Questions TOC page UI for batched ui.ask
func TestWave3BatchedQuestionsUI(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "ask-pane", "runStatus": "waiting",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	router := mcpui.New(store)
	askRes, err := router.Ask(seedAgent, mcpui.AskArgs{
		Pane:  pane.ID,
		ID:    "batch-deploy",
		Title: "Deploy Checklist",
		Questions: []mcpui.AskQuestion{
			{
				ID:    "q-env",
				Title: "Target environment?",
				Choices: []mcpui.Choice{
					{ID: "staging", Label: "Staging"},
					{ID: "prod", Label: "Production"},
				},
			},
			{
				ID:    "q-rollout",
				Title: "Rollout strategy?",
				Choices: []mcpui.Choice{
					{ID: "canary", Label: "Canary"},
					{ID: "bluegreen", Label: "Blue-Green"},
				},
			},
			{
				ID:    "q-note",
				Title: "Release note?",
				Form: &mcpui.AskForm{
					Fields: []core.ParamSpec{{Name: "note", Type: core.ParamString, Required: true}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(askRes.CardIDs) != 3 {
		t.Fatalf("expected 3 batched cards, got %v", askRes.CardIDs)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 40)

	// Pane has attention dot for unread batched questions.
	if !a.attention(pane.ID) {
		t.Fatal("pane should have attention for unread batched questions")
	}

	// Open Questions page.
	d.Keys("enter", "j", "enter")
	if a.Focus() != "q-env" {
		t.Fatalf("opening batched Questions did not focus first question q-env: got %q", a.Focus())
	}
	frame := d.Frame()
	for _, want := range []string{"Deploy Checklist (0/3 answered, 0 staged)", "Target environment?", "Rollout strategy?", "Submit All (0/3 ready)"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("batched Questions page missing %q:\n%s", want, frame)
		}
	}

	// Navigate between questions with n / p and j / k.
	d.Keys("n")
	if a.Focus() != "q-rollout" {
		t.Fatalf("n did not advance to q-rollout: got %q", a.Focus())
	}
	d.Keys("p")
	if a.Focus() != "q-env" {
		t.Fatalf("p did not go back to q-env: got %q", a.Focus())
	}

	// Answer Q1 -> stages answer and advances focus to Q2!
	d.Keys("a", "space", "ctrl+s")
	if a.Modal() != "" || a.Focus() != "q-rollout" {
		t.Fatalf("staging Q1: modal=%q focus=%q status=%q", a.Modal(), a.Focus(), a.Status())
	}
	if !strings.Contains(d.Frame(), "staged") {
		t.Fatalf("Questions page does not show staged badge after Q1:\n%s", d.Frame())
	}

	// Answer Q2 inside modal (first l picks canary, second l picks bluegreen), then step directly to Q3 with ctrl+n, answer Q3, and Submit All with ctrl+s!
	d.Keys("a", "l", "l", "ctrl+n") // picks bluegreen (second choice) and steps to Q3
	if am := a.activeAnswerForm(); a.Modal() != "answer" || am == nil || am.cardID != "q-note" {
		t.Fatalf("ctrl+n in batch answer modal did not move to q-note: modal=%q answer=%+v", a.Modal(), am)
	}
	d.Keys("i")
	d.Type("v2.3.0")
	d.Keys("ctrl+s") // all 3 answered -> atomic SubmitAnswers!
	if a.Modal() != "" {
		t.Fatalf("final ctrl+s did not submit all and close modal: modal=%q status=%q", a.Modal(), a.Status())
	}

	listRes, err := router.ListPages(mcpui.ListPagesArgs{Pane: pane.ID})
	if err != nil || len(listRes.Pages) != 1 {
		t.Fatalf("ListPages: %+v err=%v", listRes, err)
	}
	qPage := listRes.Pages[0]
	if qPage.Unread {
		t.Fatal("Questions page still unread after SubmitAnswers")
	}
	gotAnswers := map[string]string{}
	for _, q := range qPage.Questions {
		if !q.Answered {
			t.Fatalf("question %q still unanswered after SubmitAnswers", q.CardID)
		}
		for k, v := range q.Answer {
			gotAnswers[q.CardID+"."+k] = fmt.Sprint(v)
		}
	}
	if gotAnswers["q-env.choice"] != "staging" || gotAnswers["q-rollout.choice"] != "bluegreen" || gotAnswers["q-note.note"] != "v2.3.0" {
		t.Fatalf("unexpected submitted batch answers: %v", gotAnswers)
	}
	if a.attention(pane.ID) {
		t.Fatal("pane attention dot did not clear after all batched questions were answered")
	}
}

func TestWave3BindingsCheckZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	specs := CommandSpecs()
	for _, want := range []string{
		CmdPanesGroupToggle,
		CmdContextPick,
		CmdPaneHints,
		CmdPaneCompose,
		CmdQuestionNext,
		CmdQuestionPrev,
		CmdQuestionSubmitAll,
	} {
		if !slices.ContainsFunc(specs, func(s core.CommandSpec) bool { return s.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}
	// Quick sanity check on contextitems helper usage.
	_ = contextitems.KindURL
}
