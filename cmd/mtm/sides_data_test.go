package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func sideConfig(t *testing.T, a *App, raw string) {
	t.Helper()
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.configDir, sidesFile), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if errs := a.reloadSides(); len(errs) > 0 {
		t.Fatalf("reload: %v", errs)
	}
}

func TestSidesMergeSourcesAndDisable(t *testing.T) {
	t.Parallel()
	patches := []sidePatch{}
	if err := json.Unmarshal([]byte(`[{"name":"term","disabled":true},{"name":"tool","key":"G","title":"My tool"},{"name":"scratch","command":["{shell}"]}]`), &patches); err != nil {
		t.Fatal(err)
	}
	exts := []*extension{{name: "fake", bin: "/fake", doc: ocli.CatalogDoc{Sides: []ocli.CatalogSide{
		{Name: "tool", Title: "Tool", Key: "g", Command: []string{"{bin}", "{dir}"}},
		{Name: "vim", Command: []string{"bad"}},
	}}}}
	rows, notices, err := mergeSideRows(exts, patches)
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0], "built-in") {
		t.Fatalf("%+v %v %v", rows, notices, err)
	}
	names := []string{}
	for _, row := range rows {
		names = append(names, row.Name)
	}
	if !slices.Equal(names, []string{"vim", "tool", "scratch"}) {
		t.Fatal(names)
	}
	if rows[0].Launch != "vim-session" || rows[1].Title != "My tool" || rows[1].Bin != "/fake" || rows[1].Key != "G" || rows[1].Source != "user" || rows[2].Launch != "command" {
		t.Fatalf("%+v", rows)
	}
	// Later extensions win by name; a bad extension contributes no rows.
	exts = append(exts, &extension{name: "later", bin: "/later", doc: ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: "tool", Command: []string{"later"}}}}}, &extension{name: "bad", doc: ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: "bad/name", Command: []string{"bad"}}}}})
	rows, notices, err = mergeSideRows(exts, nil)
	if err != nil || len(notices) != 2 || rows[2].Bin != "/later" {
		t.Fatalf("%+v %v %v", rows, notices, err)
	}
}

func TestSidesBadConfigKeepsRows(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`[{"name":"new","unknown":1}]`, `[{"name":"new"}]`, `[{"name":"a/b","disabled":true}]`, `[{"name":"new","command":["{wat}"]}]`, `[] []`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			before := slices.Clone(a.sideRows)
			if err := os.MkdirAll(a.configDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(a.configDir, sidesFile), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if len(a.reloadSides()) == 0 || !reflect.DeepEqual(a.sideRows, before) {
				t.Fatalf("bad config replaced rows: %+v", a.sideRows)
			}
		})
	}
}

func runSideVerb(t *testing.T, a *App, noun, verb string, args ochazuke.Arguments) any {
	t.Helper()
	out, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: verb, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	for _, job := range a.r.Jobs() {
		if job.Error != "" {
			t.Fatal(job.Error)
		}
	}
	return out
}

func TestSidesUserAndExtensionLaunchCurrentContextAndRestart(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	a.cli = true
	parent := driver.panes.panes[0]
	cwd := t.TempDir()
	driver.panes.panes[0].Cwd = cwd
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: parent.ID, Env: map[string]string{"SIDE_ENV": "a b"}}); err != nil {
		t.Fatal(err)
	}
	a.mountExtension("fake", extEntry{Bin: "/fake bin", Catalog: ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: "tool", Title: "Side tool", Key: "g", Command: []string{"{bin}", "{dir}", "{pane}", "{session}", "{project}", "{shell}"}}}}})
	sideConfig(t, a, `[{"name":"scratch","title":"Scratch","command":["scratch","{dir}"]}]`)
	list := runSideVerb(t, a, "side", "list", nil).([]sideRow)
	if len(list) != 4 {
		t.Fatalf("list: %+v", list)
	}
	for _, name := range []string{"tool", "scratch"} {
		runSideVerb(t, a, "side", "show", ochazuke.Arguments{"name": name, "pane": parent.ID})
	}
	if len(driver.launches) != 2 {
		t.Fatalf("launches: %v", driver.launches)
	}
	want := []string{"env", "SIDE_ENV=a b", "/fake bin", cwd, parent.ID, parent.Session, "matomeru", a.getenv("SHELL")}
	if want[len(want)-1] == "" {
		want[len(want)-1] = "/bin/sh"
	}
	got := driver.launches[0].Window
	if !slices.Equal(got.Command, want) || got.Cwd != cwd || got.Session != parent.Session || got.Workspace != parent.Workspace {
		t.Fatalf("launch %+v; want argv %q", got, want)
	}
	links, err := a.work.Companions()
	if err != nil || len(links) != 2 {
		t.Fatalf("%+v %v", links, err)
	}
	next := t.TempDir()
	driver.panes.panes[0].Cwd = next
	old := driver.launches[0].ID
	runSideVerb(t, a, "side", "restart", ochazuke.Arguments{"name": "tool", "pane": parent.ID})
	if len(driver.launches) != 3 || driver.launches[2].Cwd != next || !slices.Equal(driver.panes.killed, []string{old}) {
		t.Fatalf("restart: %+v %v", driver.launches, driver.panes.killed)
	}
	links, err = a.work.Companions()
	if err != nil || len(links) != 2 || slices.ContainsFunc(links, func(c work.Companion) bool { return c.Window == old }) {
		t.Fatalf("restart links: %+v %v", links, err)
	}
}

// This end-to-end model walk dispatches real leader rows, draws frames,
// lists and hides the side, then reconciles its program's exit. No daemon
// process, shell program or owner's session is used.
func TestSidesLeaderWalkFrames(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	mono(t, a)
	sideConfig(t, a, `[{"name":"tool","title":"Side tool","hint":"tool","key":"g","command":["fake-side","{dir}"]}]`)
	parent := driver.panes.panes[0]
	cmd := key(t, a, "enter")
	driver.panes.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"PARENT"}}
	runOnce(a, cmd)
	if _, err := a.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	leaderKey(t, a, "ctrl+a", "g")
	if a.r.Focus != sideTermID || a.attached.SideFront != "tool" || len(driver.launches) != 1 {
		t.Fatalf("leader open: %s %+v %v", a.r.Focus, a.attached, driver.launches)
	}
	if got := a.keyForRequest(paneTermID, "terminal.side", ochazuke.Arguments{"kind": "tool"}); got != "ctrl+a g" {
		t.Fatalf("hint key: %q", got)
	}
	frames := map[string]string{"open": plainView(a)}
	if !strings.Contains(frames["open"], "Side tool") {
		t.Fatal("side title missing")
	}
	leaderKey(t, a, "ctrl+a", "g")
	if a.r.Focus != paneTermID || len(driver.launches) != 1 {
		t.Fatal("second key failed to return to parent")
	}
	leaderKey(t, a, "ctrl+a", "w")
	choices := a.r.Chooser(sideChooser).Matches()
	if len(choices) != 1 || choices[0].Label != "Side tool · "+parent.label() {
		t.Fatalf("chooser: %+v", choices)
	}
	frames["chooser"] = plainView(a)
	press(t, a, "enter")
	leaderKey(t, a, "ctrl+a", "d")
	if a.r.Terminal(sideTermID) != nil || len(driver.panes.killed) != 0 {
		t.Fatal("d did not hide the side while keeping the program")
	}
	frames["hidden"] = plainView(a)
	// A removed row must not orphan an open companion; chooser still reaches it.
	sideConfig(t, a, `[{"name":"tool","disabled":true}]`)
	leaderKey(t, a, "ctrl+a", "w")
	if len(a.r.Chooser(sideChooser).Matches()) != 1 {
		t.Fatal("disabled companion disappeared")
	}
	press(t, a, "enter")
	driver.panes.mu.Lock()
	driver.panes.panes = driver.panes.panes[:len(driver.panes.panes)-1]
	driver.panes.mu.Unlock()
	a.setPanes(driver.panes.panes, nil)
	if a.r.Terminal(sideTermID) != nil || a.r.Focus != paneTermID {
		t.Fatal("exit failed to close the side")
	}
	if dir := os.Getenv("MTM_SIDE_ARTIFACTS"); dir != "" {
		for name, frame := range frames {
			if err := os.WriteFile(filepath.Join(dir, "side-"+name+".txt"), []byte(frame), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSidesUnknownLaunchAndLeaderClash(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	sideConfig(t, a, `[{"name":"future","title":"Future","launch":"future-launch","key":"w"}]`)
	if slices.ContainsFunc(a.r.Noun("terminal").Bindings["leader_pending"], func(b ochazuke.Binding) bool { return b.Keys == "w" && b.Request.Key() != "panes.sides" }) {
		t.Fatal("side shadowed leader w")
	}
	runSideVerb(t, a, "side", "show", ochazuke.Arguments{"name": "future", "pane": driver.panes.panes[0].ID})
	if len(driver.launches) != 0 {
		t.Fatal("unknown launcher ran a command")
	}
	if _, ok := a.sideDefinition("future"); !ok {
		t.Fatal("unknown launcher lost the record")
	}
}

func TestSidesKeyOverridesAndConfigReload(t *testing.T) {
	t.Parallel()
	a := open(t)
	sideConfig(t, a, `[{"name":"scratch","key":"g","title":"Scratch","command":["scratch"]}]`)
	path := a.r.Config().Path(ochazuke.BindingsFile)
	if err := os.WriteFile(path, []byte(`[{"noun":"terminal","mode":"leader_pending","keys":"g","unbind":true},{"noun":"terminal","mode":"leader_pending","keys":"G","request":{"noun":"terminal","verb":"side","args":{"kind":"scratch"}}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if problems := a.r.ReloadUserBindings(); len(problems) > 0 {
		t.Fatal(problems)
	}
	if key := a.sideKey("scratch"); key != "G" {
		t.Fatalf("user key %q", key)
	}
	// Rebuilding defaults must keep the override; removing it later must
	// reveal the new default, without resurrecting the previous key.
	sideConfig(t, a, `[{"name":"scratch","key":"x","title":"Scratch","command":["scratch"]}]`)
	if !slices.ContainsFunc(a.r.Noun("terminal").Bindings["leader_pending"], func(b ochazuke.Binding) bool {
		return b.Keys == "G" && b.Request.Key() == a.r.CanonicalKey("terminal.side") && ochazuke.StringArg(b.Request.Args, "kind", "") == "scratch"
	}) {
		t.Fatal("refresh lost the user's G row")
	}
	if err := os.WriteFile(path, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if problems, err := a.r.ReloadConfig(); err != nil || len(problems) > 0 {
		t.Fatalf("%v %v", problems, err)
	}
	if key := a.sideKey("scratch"); key != "x" {
		t.Fatalf("after override removed: %q", key)
	}
	for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
		if b.Keys == "g" && !b.Unbind {
			t.Fatalf("stale default restored: %+v", b)
		}
	}
}

func TestSidesRestartKeepsFrontSideFocused(t *testing.T) {
	t.Parallel()
	a, driver := shellApp(t)
	sideConfig(t, a, `[{"name":"tool","title":"Side tool","key":"g","command":["fake-side","{dir}"]}]`)
	cmd := key(t, a, "enter")
	driver.panes.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"PARENT"}}
	runOnce(a, cmd)
	leaderKey(t, a, "ctrl+a", "g")
	before := a.companionPanes[a.sides[a.attached.ID].Windows["tool"]]
	driver.panes.panes[0].Cwd = t.TempDir()
	runSideVerb(t, a, "side", "restart", nil) // no name: the front side
	if a.r.Focus != sideTermID || a.sides[a.attached.ID].Windows["tool"] == before.ID || len(driver.launches) != 2 || driver.launches[1].Cwd != driver.panes.panes[0].Cwd {
		t.Fatalf("restart: focus %s, sides %+v, launches %+v", a.r.Focus, a.sides, driver.launches)
	}
}

func TestSidesReconcileThreeKindsAndLeaderCollision(t *testing.T) {
	t.Parallel()
	a, f := sideViewApp(t)
	sideConfig(t, a, `[{"name":"tool","title":"Side tool","key":"ctrl+b","command":["fake"]}]`)
	extra := f.panes[len(f.panes)-1]
	extra.ID = "custom-side"
	f.panes = append(f.panes, extra)
	if err := a.work.PutCompanion(work.Companion{Window: extra.ID, Parent: a.attached.ID, Kind: "tool"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(f.panes, nil)
	for _, name := range []string{"vim", "term", "tool", "vim", "term"} {
		if err := a.showSide(a.attached, name); err != nil {
			t.Fatal(err)
		}
	}
	leaderKey(t, a, "ctrl+a", "d")
	if a.attached.SideFront != "vim" {
		t.Fatalf("previous side %q", a.attached.SideFront)
	}
	leaderKey(t, a, "ctrl+a", "d")
	if a.attached.SideFront != "tool" {
		t.Fatalf("next previous %q", a.attached.SideFront)
	}
	if err := a.r.Settings().Set(ochazuke.TerminalLeaderSetting, "ctrl+b"); err != nil {
		t.Fatal(err)
	}
	if key := a.sideKey("tool"); key != "" {
		t.Fatalf("side key %q shadows the leader's self-send", key)
	}
	if !slices.ContainsFunc(a.m.Notices, func(s string) bool { return strings.Contains(s, "side tool: leader key ctrl+b skipped") }) {
		t.Fatal("missing collision notice")
	}
}

func TestSidesExtensionRefreshAndRemoval(t *testing.T) {
	t.Parallel()
	a := open(t)
	doc := ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: "tool", Title: "Side tool", Key: "g", Command: []string{"{bin}"}}}}
	reg := a.extRegistry()
	if err := reg.write("fake", extEntry{Bin: "/fake", Catalog: doc}); err != nil {
		t.Fatal(err)
	}
	a.syncExtensions()
	if row, ok := a.sideDefinition("tool"); !ok || row.Bin != "/fake" || a.sideKey("tool") != "g" {
		t.Fatalf("mount %+v %v", row, ok)
	}
	doc.Sides[0].Title = "New tool"
	doc.Sides[0].Key = "x"
	if err := reg.write("fake", extEntry{Bin: "/new-fake", Catalog: doc}); err != nil {
		t.Fatal(err)
	}
	a.syncExtensions()
	if row, ok := a.sideDefinition("tool"); !ok || row.Title != "New tool" || row.Bin != "/new-fake" || a.sideKey("tool") != "x" {
		t.Fatalf("refresh %+v %v", row, ok)
	}
	if err := reg.remove("fake"); err != nil {
		t.Fatal(err)
	}
	a.syncExtensions()
	if _, ok := a.sideDefinition("tool"); ok || a.sideKey("tool") != "" {
		t.Fatal("removed extension kept its side")
	}
}

func TestSidesLeaderWordsFollowUserRequests(t *testing.T) {
	t.Parallel()
	a := open(t)
	sideConfig(t, a, `[{"name":"scratch","key":"g","title":"Scratch","command":["scratch"]}]`)
	path := a.r.Config().Path(ochazuke.BindingsFile)
	if err := os.WriteFile(path, []byte(`[{"noun":"terminal","mode":"leader_pending","keys":"v","request":{"noun":"terminal","verb":"zoom"}},{"noun":"terminal","mode":"leader_pending","keys":"G","request":{"noun":"terminal","verb":"side","args":{"kind":"scratch"}}}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if problems := a.r.ReloadUserBindings(); len(problems) > 0 {
		t.Fatal(problems)
	}
	runSideVerb(t, a, "terminal", "keys", nil)
	var zoom, side bool
	for _, item := range a.r.List(leaderList).Items() {
		if item.Cells[1] == "v" {
			zoom = strings.Contains(item.Cells[2], "Zoom") && !strings.Contains(item.Cells[2], "vim")
		}
		if item.Cells[1] == "G" {
			side = strings.Contains(item.Cells[2], "Scratch")
		}
	}
	if !zoom || !side {
		t.Fatalf("modal did not follow user requests: zoom=%v side=%v", zoom, side)
	}
}

func TestSidesHintsKeepTitlesAndFollowReload(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.extRegistry().write("git", extEntry{Bin: "/fake-git", Catalog: ocli.CatalogDoc{Sides: []ocli.CatalogSide{{Name: "diff", Title: "Git working tree diff", Key: "g", Command: []string{"{bin}"}}}}}); err != nil {
		t.Fatal(err)
	}
	a.syncExtensions()
	check := func(want string) {
		t.Helper()
		var hint, summary string
		for _, b := range a.r.Noun("terminal").Bindings["leader_pending"] {
			if b.Request.Key() == a.r.CanonicalKey("terminal.side") && ochazuke.StringArg(b.Request.Args, "kind", "") == "diff" {
				hint = b.Hint
				if !strings.Contains(b.Help, "Git working tree diff") {
					t.Fatalf("help lost title: %q", b.Help)
				}
			}
		}
		for _, b := range a.r.Noun("terminal").Bindings["terminal"] {
			if b.Keys == a.r.TerminalLeader() {
				summary = b.Hint
			}
		}
		if hint != want || !strings.Contains(summary, "g "+want+" · ") || strings.Contains(summary, "Git working tree diff") {
			t.Fatalf("hint=%q summary=%q; want %q", hint, summary, want)
		}
		rows := runSideVerb(t, a, "side", "list", nil).([]sideRow)
		if row := rows[len(rows)-1]; row.Title != "Git working tree diff" {
			t.Fatalf("list lost title: %+v", row)
		}
		runSideVerb(t, a, "terminal", "keys", nil)
		if !strings.Contains(plainView(a), "Git working tree diff") {
			t.Fatal("keys modal lost title")
		}
		press(t, a, "esc")
	}
	check("diff")
	sideConfig(t, a, `[{"name":"diff","hint":"changes"}]`)
	check("changes")
	sideConfig(t, a, `[{"name":"diff","hint":""}]`)
	check("diff")
}
