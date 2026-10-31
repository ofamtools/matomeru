package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
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
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
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
	// Needs input sorts first and the header counts it; W jumps to it.
	if fr := frame(app); !strings.Contains(fr, "1 need you · W next") {
		t.Fatalf("header:\n%s", fr)
	}
	if items := app.paneList.Items(); items[2].ID != row {
		t.Fatalf("needs input is not first: %+v", items)
	}
	press(t, app, "W")
	if app.r.Focus != row {
		t.Fatalf("W: focus %s", app.r.Focus)
	}
	// Every terminal verb answers on the agent row.
	want := []string{"enter pane.attach", "d d pane.kill", "r pane.rename", "m pane.choose-move", "n pane.new"}
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
	executeA(t, app, "pane", "associate", row, ochazuke.Arguments{"project": "matomeru"})
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
	t.Parallel()
	app, _ := panesApp(t)
	key(t, app, "m")
	c := app.r.Chooser(moveChooser)
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
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
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

// The tabs: ] walks Turns, Screen, Activity, Transcript; Activity and
// Transcript load off the loop; a shell has none.
func TestDetailTabs(t *testing.T) {
	t.Parallel()
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

// capturedTurns journals turns into a database as the capture hooks do
// while mtm is closed: pane w1 of session matomeru, owned by matomeru
// when the first prompt went in and by ochazuke for the second.
func capturedTurns(t *testing.T, path string, answers ...string) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	j, err := history.Open(db, journalOwner(w))
	if err != nil {
		t.Fatal(err)
	}
	owners := []string{"matomeru", "ochazuke"}
	at := int64(1_790_000_000_000)
	for i, answer := range answers {
		if err := w.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: owners[i%2]}); err != nil {
			t.Fatal(err)
		}
		turn := history.Turn{ID: fmt.Sprint("t", i), Session: "matomeru", Window: "w1", Harness: "claude-code", ConversationID: "c",
			Prompt: fmt.Sprintf("prompt %d\nwith a second line", i+1), StartedAt: at + int64(i)*60_000}
		if _, err := j.Capture(turn); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Complete(turn, answer, turn.StartedAt+30_000); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
}

// Turns: with mtm closed the agent finished two turns; opening mtm shows
// both, oldest first, collapsed, each labelled by its prompt; opening one
// shows the whole prompt and the whole answer (no 12-line cut) and marks
// it reviewed; each keeps its prompt-time owner after the pane moved.
func TestTurnsFromTheJournal(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	var long []string
	for i := 1; i <= 30; i++ {
		long = append(long, fmt.Sprintf("answer line %d", i))
	}
	capturedTurns(t, path, strings.Join(long, "\n"), "second answer")
	app := openWith(t, Options{Data: path})
	f := app.panesd.(*fakePanes)
	f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
	reload(t, app)
	turns, err := app.hist.Turns("w1")
	if err != nil || len(turns) != 2 || turns[0].ProjectID != "matomeru" || turns[1].ProjectID != "ochazuke" {
		t.Fatalf("turns %+v %v", turns, err)
	}
	useTurnStyle(t, app, turnsStyleTree)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	if app.pv.tab != tabTurns {
		t.Fatalf("tab %d", app.pv.tab)
	}
	app.m.Width, app.m.Height = 120, 60
	fr := frame(app)
	if !strings.Contains(fr, "prompt 1 · 1 answer ·") || !strings.Contains(fr, "prompt 2 · 1 answer ·") || !strings.Contains(fr, "· new") ||
		strings.Contains(fr, "answer line 1") || strings.Index(fr, "prompt 1") > strings.Index(fr, "prompt 2") {
		t.Fatalf("turns tab:\n%s", fr)
	}
	press(t, app, "}")
	if !strings.HasPrefix(app.r.Focus, "turn:") {
		t.Fatalf("} from the tree: focus %s", app.r.Focus)
	}
	press(t, app, "g", "g", "l") // } lands on the newest turn (F9); open the first
	fr = frame(app)
	for _, want := range []string{"with a second line", "answer line 1"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("opened turn lacks %q:\n%s", want, fr)
		}
	}
	// Every answer line is a row of the tree; the tree scrolls to the last.
	var rows []string
	for _, it := range app.pv.turns.List().Items() {
		rows = append(rows, it.Label)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "answer line 30") {
		t.Fatalf("rows:\n%s", strings.Join(rows, "\n"))
	}
	for range 34 {
		press(t, app, "j")
	}
	if fr := frame(app); !strings.Contains(fr, "answer line 30") {
		t.Fatalf("j to the turn's end:\n%s", fr)
	}
	for range 34 {
		press(t, app, "k")
	}
	if turns, _ := app.hist.Turns("w1"); turns[0].ReviewedAt == 0 || turns[1].ReviewedAt != 0 {
		t.Fatalf("marks %+v", turns)
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
	t.Parallel()
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

func plainView(a *App) string {
	return xansi.Strip(frameView(a).Content)
}

func countRows(a *App, noun string) int {
	n := 0
	for _, it := range sectionItems(a) {
		if it.Noun == noun {
			n++
		}
	}
	return n
}

func cells(a *App, id string) string {
	for _, it := range sectionItems(a) {
		if it.ID == id {
			return strings.Join(it.Cells, " | ")
		}
	}
	return ""
}

// UX review 2 F22: one pane is "1 pane", not "1 panes".
func TestPluralCounts(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{0: "0 panes", 1: "1 pane", 2: "2 panes"} {
		if got := plural(n, "pane"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
	app, _ := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	press(t, app, "k") // the claude pane's session row
	if app.r.Focus != "session:matomeru" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	settle(app, follow(app))
	if fr := frame(app); !strings.Contains(fr, "1 pane ·") || strings.Contains(fr, "1 panes") {
		t.Fatalf("session summary:\n%s", fr)
	}
}

// UX review 2 F22: in the project tree a session row says "session", so a
// session named after its project does not read as the project twice.
func TestNestedSessionRowsSaySession(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	if got := cells(app, "session:matomeru"); !strings.Contains(got, "▾ session matomeru (1)") {
		t.Fatalf("session cells %q", got)
	}
	if got := cells(app, "project:"); strings.Contains(got, "session") {
		t.Fatalf("project cells %q", got)
	}
}

// With the Turns tab showing, l on the pane row (the key esc from a turn
// leaves the owner on) moves into the turns, never to the hidden screen:
// no copy mode, no Screen tab, no attach. On the Activity tab l does
// nothing; on the Screen tab it is still copy mode.
func TestLOnAPaneRowWithTheTurnsTabFocusesTheTurns(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	capturedTurns(t, path, "first answer", "second answer")
	app := openWith(t, Options{Data: path})
	f := app.panesd.(*fakePanes)
	f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
	reload(t, app)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	app.m.Width, app.m.Height = 120, 40
	frame(app)
	press(t, app, "}", "l") // open the first turn
	frame(app)
	press(t, app, "esc")
	frame(app)
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("esc from a turn: focus %s", app.r.Focus)
	}
	for range 2 {
		press(t, app, "l")
		frame(app)
		if !onTurn(app) || app.pv.tab != tabTurns || app.copy.active ||
			app.r.Terminal(paneTermID) != nil || app.r.Mode.Name != "normal" {
			t.Fatalf("l on the pane row with Turns: focus %s tab %d copy %v mode %s", app.r.Focus, app.pv.tab, app.copy.active, app.r.Mode.Name)
		}
		press(t, app, "esc")
		frame(app)
	}
	settle(app, key(t, app, "]")) // Screen
	settle(app, key(t, app, "]")) // Activity
	frame(app)
	press(t, app, "l")
	if app.r.Focus != "pane:matomeru/w1" || app.pv.tab != tabActivity || app.copy.active {
		t.Fatalf("l on the pane row with Activity: focus %s tab %d copy %v", app.r.Focus, app.pv.tab, app.copy.active)
	}
}

// The owner's rule: an attached terminal the focus has left stays
// attached, but moving into the detail goes back into it only on the
// Screen tab. On Turns (or Activity, Transcript) the detail draws that
// tab, and }, tab and l reach its content, never terminal mode.
func TestAnAttachedTerminalIsReenteredOnlyFromTheScreenTab(t *testing.T) {
	t.Parallel()
	row := "pane:matomeru/w1"
	attachedThenLeft := func(t *testing.T) *App {
		path := seeded(t)
		capturedTurns(t, path, "first answer", "second answer")
		app := openWith(t, Options{Data: path})
		f := app.panesd.(*fakePanes)
		f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
		reload(t, app)
		app.m.Width, app.m.Height = 120, 40
		frame(app)
		settle(app, key(t, app, "enter"))
		frame(app)
		if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
			t.Fatalf("enter attaches: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
		}
		// The focus leaves the terminal without detaching (a click on the row).
		if err := app.r.SetFocus(row); err != nil {
			t.Fatal(err)
		}
		frame(app)
		return app
	}
	for _, keys := range [][]string{{"}"}, {"tab"}, {"l"}} {
		app := attachedThenLeft(t)
		settle(app, key(t, app, "[")) // Screen wraps back to Turns
		settle(app, follow(app))
		fr := frame(app)
		if !strings.Contains(fr, "prompt 1") || strings.Contains(fr, "· live ·") {
			t.Fatalf("Turns with a terminal attached draws the turns:\n%s", fr)
		}
		press(t, app, keys...)
		frame(app)
		if !onTurn(app) || app.r.Mode.Name != "normal" || app.r.Terminal(paneTermID) == nil {
			t.Fatalf("%v on Turns: focus %s mode %s attached %v", keys, app.r.Focus, app.r.Mode.Name, app.r.Terminal(paneTermID) != nil)
		}
	}
	// Screen keeps today's behaviour: } goes back into the live terminal.
	app := attachedThenLeft(t)
	press(t, app, "}")
	if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("} on Screen: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
}
