package main

import (
	"reflect"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// reload lists the fake's panes again, as a daemon event or the refresh
// timer does.
func reload(t *testing.T, a *App) {
	t.Helper()
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
}

// A window that starts as a shell and later runs an agent stays one row:
// the agent's row (glyph, harness, state), never a shell row beside an
// agent row, and every pane verb (attach, preview, rename, move, kill, the
// leader keys) still answers on it.
func TestAShellThatRunsAnAgentBecomesOneAgentRow(t *testing.T) {
	app, f := panesApp(t)
	app.renamer = &fakeRenamer{panes: f}
	f.panes[1].Name = "Terminal 0e90e60d"
	reload(t, app)
	row := "pane:ochazuke/w2"
	if n := countRows(app, "pane"); n != 2 || !strings.Contains(cells(app, row), "shell") {
		t.Fatalf("before: %d pane rows, %q", n, cells(app, row))
	}
	f.panes[1].Harness, f.panes[1].AgentState = "claude", "needs_input"
	reload(t, app)
	if n := countRows(app, "pane"); n != 2 {
		t.Fatalf("after: %d pane rows, want 2: %+v", n, app.paneList.Items())
	}
	if got := cells(app, row); !strings.Contains(got, "? claude") || !strings.Contains(got, "| needs input") {
		t.Fatalf("agent row cells %q", got)
	}
	// Needs input sorts first and the header counts it; w jumps to it.
	if fr := frame(app); !strings.Contains(fr, "1 need input · w next") {
		t.Fatalf("header:\n%s", fr)
	}
	if items := app.paneList.Items(); items[2].ID != row {
		t.Fatalf("needs input is not first: %+v", items)
	}
	press(t, app, "w")
	if app.r.Focus != row {
		t.Fatalf("w: focus %s", app.r.Focus)
	}
	// Every terminal verb answers on the agent row.
	want := []string{"enter pane.attach", "d d pane.kill", "r pane.rename", "m pane.move", "n pane.new"}
	listed := map[string]bool{}
	for _, b := range app.r.ListBindings(row) {
		listed[b.Keys+" "+b.Request.Key()] = true
	}
	for _, w := range want {
		if !listed[w] {
			t.Fatalf("agent row lacks %q", w)
		}
	}
	settle(app, follow(app))
	if app.preview.Pane != row {
		t.Fatalf("preview %+v", app.preview)
	}
	executeA(t, app, "pane", "rename", row, ochazuke.Arguments{"name": "fixer"})
	settle(app, app.r.PendingCommands())
	if f.panes[1].Name != "fixer" || !strings.Contains(cells(app, row), "? fixer") {
		t.Fatalf("rename: %q %q", f.panes[1].Name, cells(app, row))
	}
	key(t, app, "enter")
	if app.attached.ID != "w2" || app.r.Focus != paneTermID {
		t.Fatalf("attach: %s %s", app.attached.ID, app.r.Focus)
	}
	press(t, app, "ctrl+a", "z")
	if !app.pv.zoom {
		t.Fatal("zoom on the agent row")
	}
	press(t, app, "ctrl+a", "d")
	if app.r.Focus != row {
		t.Fatalf("detach: focus %s", app.r.Focus)
	}
	executeA(t, app, "pane", "move", row, ochazuke.Arguments{"project": "matomeru"})
	if o, _ := app.work.PaneOwner("w2", ""); o.ProjectID != "matomeru" {
		t.Fatalf("move: owner %+v", o)
	}
	press(t, app, "d", "d")
	settle(app, key(t, app, "y"))
	if !reflect.DeepEqual(f.killed, []string{"w2"}) {
		t.Fatalf("kill: %v", f.killed)
	}
}

// m opens the project chooser (no hex ids), without the pane's current
// owner; picking a project moves the pane.
func TestMoveUsesTheProjectChooser(t *testing.T) {
	app, _ := panesApp(t)
	key(t, app, "m")
	c := app.choosers.Get(moveChooser)
	if c == nil {
		t.Fatal("m opened no chooser")
	}
	var labels []string
	for _, r := range c.Matches() {
		labels = append(labels, r.Label)
	}
	// The pane is unassociated, so "unassociated" is not offered.
	if strings.Join(labels, ",") != "ochazuke,matomeru" {
		t.Fatalf("choices %v", labels)
	}
	typeText(t, app, "mato")
	settle(app, key(t, app, "enter"))
	if o, _ := app.work.PaneOwner("w1", ""); o.ProjectID != "matomeru" {
		t.Fatalf("owner %+v", o)
	}
	if app.r.TopModal() != "" {
		t.Fatalf("chooser still open: %s", app.r.TopModal())
	}
}

// Session rows have their own verbs: enter attaches the first pane, r
// renames, x kills the session after a confirm.
func TestSessionRowVerbs(t *testing.T) {
	app, f := panesApp(t)
	app.renamer = &fakeRenamer{panes: f}
	press(t, app, "k")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "session" {
		t.Fatalf("k: focus %s", app.r.Focus)
	}
	session := app.r.Focus
	executeA(t, app, "session", "rename", session, ochazuke.Arguments{"name": "mtm"})
	settle(app, app.r.PendingCommands())
	if f.panes[0].Session != "mtm" {
		t.Fatalf("rename: %+v", f.panes)
	}
	if err := app.pv.tree.Select("session:mtm"); err != nil {
		t.Fatal(err)
	}
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	key(t, app, "enter")
	if app.attached.ID != "w1" {
		t.Fatalf("enter on a session attached %q", app.attached.ID)
	}
	press(t, app, "ctrl+a", "d")
	if err := app.pv.tree.Select("session:mtm"); err != nil {
		t.Fatal(err)
	}
	if err := app.paneList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, app, "d", "d")
	if prompt, _ := confirmPrompt(t, app); prompt != "Kill session mtm (1) and its panes?" {
		t.Fatalf("confirm %q", prompt)
	}
	settle(app, key(t, app, "y"))
	if !reflect.DeepEqual(f.killed, []string{"session:mtm"}) {
		t.Fatalf("killed %v", f.killed)
	}
}

// The tabs: ] walks Screen, Activity, Transcript, Turns; Activity and
// Transcript load off the loop; a shell has none.
func TestDetailTabs(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness = "claude"
	reload(t, app)
	settle(app, key(t, app, "]"))
	if app.pv.tab != tabActivity || !strings.Contains(frame(app), "Read  main.go") || !strings.Contains(frame(app), "Screen [Activity]") {
		t.Fatalf("activity:\n%s", frame(app))
	}
	settle(app, key(t, app, "]"))
	if !strings.Contains(frame(app), "fix the tests") {
		t.Fatalf("transcript:\n%s", frame(app))
	}
	press(t, app, "j", "j")
	settle(app, follow(app))
	if !strings.Contains(frame(app), "a shell has no agent transcript") {
		t.Fatalf("shell transcript:\n%s", frame(app))
	}
	settle(app, key(t, app, "["))
	settle(app, key(t, app, "["))
	if app.pv.tab != tabScreen {
		t.Fatalf("tab %d", app.pv.tab)
	}
}

// Turns: one row per turn, oldest first, collapsed; a listing that sees
// the agent start working records a turn with the owner at that moment,
// one that sees it stop captures its screen; opening a turn marks it
// reviewed in the store.
func TestTurnsRecordOpenAndMarkReviewed(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState = "claude", "idle"
	reload(t, app)
	launch, err := app.work.AddReview(work.Review{Session: "matomeru", Window: "w1", Pane: "matomeru/claude", ProjectID: "matomeru"})
	if err != nil {
		t.Fatal(err)
	}
	turn := func(state, screen string) {
		f.panes[0].AgentState = state
		f.screens["w1"] = screen
		reload(t, app)
	}
	turn("working", "first answer")
	turn("idle", "first answer")
	// The second prompt goes in while the pane belongs to ochazuke; a later
	// move does not change whose turn it was.
	executeA(t, app, "pane", "associate", "pane:matomeru/w1", ochazuke.Arguments{"project": "ochazuke"})
	turn("working", "second answer")
	executeA(t, app, "pane", "associate", "pane:matomeru/w1", ochazuke.Arguments{"project": "matomeru"})
	turn("idle", "second answer")
	turns, err := app.paneTurns("w1")
	if err != nil || len(turns) != 2 || turns[0][0].ID != launch.ID || turns[0][0].TurnID == "" || turns[1][0].ProjectID != "ochazuke" {
		t.Fatalf("turns %+v %v", turns, err)
	}
	if turns[0][0].Output != "first answer" || turns[1][0].Output != "second answer" {
		t.Fatalf("outputs %q %q", turns[0][0].Output, turns[1][0].Output)
	}
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	if app.pv.tab != tabTurns {
		t.Fatalf("tab %d", app.pv.tab)
	}
	fr := frame(app)
	if !strings.Contains(fr, "turn 1") || !strings.Contains(fr, "turn 2 ·") || strings.Contains(fr, "first answer") || strings.Index(fr, "turn 1") > strings.Index(fr, "turn 2") {
		t.Fatalf("turns tab:\n%s", fr)
	}
	press(t, app, "}")
	if !strings.HasPrefix(app.r.Focus, "turn:") {
		t.Fatalf("} from the tree: focus %s", app.r.Focus)
	}
	press(t, app, "l")
	if fr := frame(app); !strings.Contains(fr, "first answer") {
		t.Fatalf("opened turn:\n%s", fr)
	}
	if got, _ := app.work.Review(launch.ID); got.ReviewedAt == 0 {
		t.Fatal("opening the turn did not mark it reviewed")
	}
	// esc goes back up to the pane list, from a turn and from a turn's line.
	press(t, app, "esc")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("esc from a turn: focus %s", app.r.Focus)
	}
	press(t, app, "}", "j")
	if !strings.HasPrefix(app.r.Focus, "turn:") || !strings.Contains(app.r.Focus, "/") {
		t.Fatalf("j into the turn's lines: focus %s", app.r.Focus)
	}
	press(t, app, "esc")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("esc from a turn line: focus %s", app.r.Focus)
	}
}

// Narrow, the detail stacks under the tree; both widths draw the tree,
// the tabs and the counts.
func TestPanesRenderAt80And120(t *testing.T) {
	app, _ := panesApp(t)
	settle(app, follow(app))
	for _, width := range []int{80, 120} {
		app.m.Width, app.m.Height = width, 24
		fr := plainView(app)
		for _, want := range []string{"panes (2)", "claude", "[Screen]", "claude is thinking"} {
			if !strings.Contains(fr, want) {
				t.Fatalf("%d: frame lacks %q:\n%s", width, want, fr)
			}
		}
	}
}

func plainView(a *App) string { return xansi.Strip(a.m.View().Content) }

func countRows(a *App, noun string) int {
	n := 0
	for _, it := range a.paneList.Items() {
		if it.Noun == noun {
			n++
		}
	}
	return n
}

func cells(a *App, id string) string {
	for _, it := range a.paneList.Items() {
		if it.ID == id {
			return strings.Join(it.Cells, " | ")
		}
	}
	return ""
}
