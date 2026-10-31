package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

// cardsApp is claudeTurnsApp (the cards style is the default) with turns
// 2 to 7 appended, at 120 by 30.
func cardsApp(t *testing.T) (*App, *fakePanes, string) {
	t.Helper()
	app, f, log := claudeTurnsApp(t)
	moreClaudeTurns(t, log, 2, 8)
	reload(t, app)
	app.m.Width, app.m.Height = 120, 30
	frame(app)
	return app, f, log
}

// appKey sends a key through the App's Update (its page hooks run, as
// in the real loop) and returns what it scheduled.
func appKey(t *testing.T, a *App, k string) tea.Cmd {
	t.Helper()
	_, cmd := a.Update(keyMsg(k))
	if a.m.Status != "" {
		t.Fatalf("key %q: %s", k, a.m.Status)
	}
	return cmd
}

// selectedCard is the shown timeline's selected card id.
func selectedCard(a *App) string { return a.pv.cards.SelectedID() }

// A Claude pane's turns are cards: the whole prompt as the header, a meta
// line, and, opened, the steers, the thinking and the tool call folded
// (the settings' defaults) and the answer as sections.
func TestTurnCardsFromTheSessionLog(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	app.m.Width, app.m.Height = 120, 50
	cards := app.pv.cards.Cards()
	if len(cards) != 1 || cards[0].Header != "fix the flaky test" || cards[0].Meta != "Oct 27 06:00 · done · 1 steer · 1 tool call · 4.0s" && !strings.Contains(cards[0].Meta, "done · 1 steer · 1 tool call · 4.0s") {
		t.Fatalf("cards %+v", cards)
	}
	if app.pv.cards.IsOpen(cards[0].ID) {
		t.Fatal("a finished turn starts closed")
	}
	fr := frame(app)
	if !strings.Contains(fr, "╭") || !strings.Contains(fr, "▸ fix the flaky test") || !strings.Contains(fr, "done · 1 steer · 1 tool call · 4.0s") {
		t.Fatalf("closed card:\n%s", fr)
	}
	press(t, app, "}")
	settle(app, key(t, app, "l"))
	fr = frame(app)
	for _, want := range []string{"▾ 1 steers · 1 steer", "only the cmd/mtm package", "▸ 2 thinking · 1 line", "▸ 3 tool Bash · go test -race ./...", "▾ 4 answer", "Fixed: the test now fakes the clock."} {
		if !strings.Contains(fr, want) {
			t.Fatalf("opened card lacks %q:\n%s", want, fr)
		}
	}
	if strings.Contains(fr, "the test races on the clock") || strings.Contains(fr, "input:") {
		t.Fatalf("folded sections show their text:\n%s", fr)
	}
	// z 2 unfolds the thinking; the settings fold it again, open the
	// tools and hide the thinking.
	press(t, app, "z", "2")
	if fr = frame(app); !strings.Contains(fr, "the test races on the clock") {
		t.Fatalf("z 2:\n%s", fr)
	}
	for name, value := range map[string]string{settingTurnsThinking: "false", settingTurnsToolsOpen: "true"} {
		if _, err := app.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": name, "value": value}}); err != nil {
			t.Fatal(err)
		}
	}
	fr = frame(app)
	if strings.Contains(fr, "thinking") || !strings.Contains(fr, "▾ 2 tool Bash") || !strings.Contains(fr, "input:") || !strings.Contains(fr, "ok cmd/mtm") {
		t.Fatalf("after the settings:\n%s", fr)
	}
	// y and Y copy from a card as from a tree row.
	sys := &fakeSystem{}
	app.cx.sys = sys
	settle(app, key(t, app, "y"))
	settle(app, key(t, app, "Y"))
	if len(sys.copied) != 2 || sys.copied[0] != "Fixed: the test now **fakes** the clock." || !strings.HasPrefix(sys.copied[1], "fix the flaky test\n\nonly the cmd/mtm package") {
		t.Fatalf("copied %q", sys.copied)
	}
}

// Follow: the cards open at the newest, a turn appended at the bottom
// shows and is selected, the pane row keeping the focus or a card having
// it. Hold: after k, or J/K scrolling up, an appended turn moves neither
// the window nor the selection. G follows again.
func TestTurnCardsFollowHoldAndG(t *testing.T) {
	t.Parallel()
	app, _, log := cardsApp(t)
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 7") || selectedCard(app) != turnCardID("w1", "p7") || !app.pv.cards.Following() {
		t.Fatalf("opened on %s following %v:\n%s", selectedCard(app), app.pv.cards.Following(), got)
	}
	moreClaudeTurns(t, log, 8, 9)
	reload(t, app)
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 8") || selectedCard(app) != turnCardID("w1", "p8") {
		t.Fatalf("follow from the pane row: %s\n%s", selectedCard(app), got)
	}
	press(t, app, "}")
	if app.r.Focus != turnCardID("w1", "p8") {
		t.Fatalf("} focused %s", where(app))
	}
	moreClaudeTurns(t, log, 9, 10)
	reload(t, app)
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 9") || app.r.Focus != turnCardID("w1", "p9") {
		t.Fatalf("follow from a card: %s\n%s", where(app), got)
	}
	// k holds.
	press(t, app, "k")
	held := turnsRegion(t, app)
	if app.pv.cards.Following() || app.r.Focus != turnCardID("w1", "p8") {
		t.Fatalf("k: %s following %v", where(app), app.pv.cards.Following())
	}
	moreClaudeTurns(t, log, 10, 11)
	reload(t, app)
	if got := turnsRegion(t, app); got != held || app.r.Focus != turnCardID("w1", "p8") {
		t.Fatalf("an append moved the held cards (%s):\n%s\nwas:\n%s", where(app), got, held)
	}
	// G follows again.
	press(t, app, "G")
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 10") || app.r.Focus != turnCardID("w1", "p10") || !app.pv.cards.Following() {
		t.Fatalf("G: %s\n%s", where(app), got)
	}
	// K from the pane row scrolls up and holds too; J back to the end
	// follows again.
	press(t, app, "esc")
	if inst := app.r.Instances[app.r.Focus]; inst == nil || inst.Noun != "pane" {
		t.Fatalf("esc focused %s", where(app))
	}
	press(t, app, "K", "K", "K")
	held = turnsRegion(t, app)
	if app.pv.cards.Following() || strings.Contains(held, "prompt number 10") && strings.Contains(held, "╰") && strings.HasSuffix(strings.TrimSpace(held), "╯") {
		t.Fatalf("K from the pane row: following %v\n%s", app.pv.cards.Following(), held)
	}
	moreClaudeTurns(t, log, 11, 12)
	reload(t, app)
	if got := turnsRegion(t, app); got != held {
		t.Fatalf("an append moved the cards K held:\n%s\nwas:\n%s", got, held)
	}
	// j down to the newest card and its end (reaching the bottom) follows
	// again. (J scrolling back to the end leaves the selection where it
	// was, so the timeline does not follow: ochazuke follows only with the
	// last card selected.) A press scrolls half the window at most
	// (ochazuke R3.14), so the end can take one j more than the card.
	press(t, app, "}")
	for range 8 {
		press(t, app, "j")
		frame(app)
		if app.pv.cards.Following() {
			break
		}
	}
	frame(app) // following is judged when the cards are drawn
	moreClaudeTurns(t, log, 12, 13)
	reload(t, app)
	if got := turnsRegion(t, app); !strings.Contains(got, "prompt number 12") || !app.pv.cards.Following() || app.r.Focus != turnCardID("w1", "p12") {
		t.Fatalf("j back to the newest does not follow (%s):\n%s", where(app), got)
	}
}

// A growing answer of the running turn keeps its bottom in view while
// following: the running card starts open, its newest line on screen.
func TestTurnCardsAGrowingAnswerKeepsItsBottomInView(t *testing.T) {
	t.Parallel()
	app, f, log := cardsApp(t)
	stream := func(n int) string {
		var lines []string
		for i := 1; i <= n; i++ {
			lines = append(lines, fmt.Sprintf("stream line %d", i))
		}
		return strings.Join(lines, "\n\n")
	}
	writeLog(t, log, logPrompt(t, "run", "2026-10-27T12:00:00Z", "a long running turn"),
		logAssistant(t, "s1", "2026-10-27T12:00:01Z", "tool_use", map[string]any{"type": "text", "text": stream(3)}))
	f.panes[0].AgentState = "working"
	reload(t, app)
	id := turnCardID("w1", "run")
	if !app.pv.cards.IsOpen(id) || selectedCard(app) != id {
		t.Fatalf("the running card: open %v selected %s", app.pv.cards.IsOpen(id), selectedCard(app))
	}
	if got := turnsRegion(t, app); !strings.Contains(got, "stream line 3") || !strings.Contains(got, "●") {
		t.Fatalf("running card:\n%s", got)
	}
	for _, n := range []int{12, 30} {
		writeLog(t, log, logAssistant(t, fmt.Sprintf("s%d", n), "2026-10-27T12:00:02Z", "tool_use", map[string]any{"type": "text", "text": stream(n)}))
		reload(t, app)
		got := turnsRegion(t, app)
		last := strings.Split(strings.TrimRight(got, " \n"), "\n")
		if !strings.Contains(got, fmt.Sprintf("stream line %d", n)) || !strings.Contains(last[len(last)-2], "╯") && !strings.Contains(last[len(last)-1], "╯") {
			t.Fatalf("after %d lines the bottom is out of view:\n%s", n, got)
		}
	}
}

// Each pane keeps its own cards: a held place, an open card and the
// selection survive going to another pane and back, and another tab.
func TestTurnCardsKeepTheirPlacePerPane(t *testing.T) {
	t.Parallel()
	app, _, _ := cardsApp(t)
	press(t, app, "}", "k", "k", "l", "J")
	held, sel := turnsRegion(t, app), app.r.Focus
	press(t, app, "esc", "j")
	settle(app, follow(app))
	frame(app)
	press(t, app, "k")
	settle(app, follow(app))
	// The focus is on the pane row: the card is drawn unfocused.
	if got := turnsRegion(t, app); got != unfocusedCorners(held) || selectedCard(app) != sel {
		t.Fatalf("back on the pane: %s\n%s\nwas:\n%s", selectedCard(app), got, held)
	}
	settle(app, key(t, app, "]"))
	frame(app)
	settle(app, key(t, app, "["))
	if got := turnsRegion(t, app); got != unfocusedCorners(held) || selectedCard(app) != sel {
		t.Fatalf("back on the tab: %s\n%s\nwas:\n%s", selectedCard(app), got, held)
	}
}

// An omp pane's turns are cards too, following its current branch.
func TestTurnCardsFromAnOmpSession(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "omp", "idle", "01a1-s1", "/w/proj"
	dir := filepath.Join(app.ompSessions, "-w-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := func(text string, steer bool) map[string]any {
		m := map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": text}}}
		if steer {
			m["steering"] = true
		}
		return m
	}
	writeLog(t, filepath.Join(dir, "2026-10-27T10-00-00-000Z_01a1-s1.jsonl"),
		logRecord(t, map[string]any{"type": "session", "version": 3, "id": "01a1-s1", "cwd": "/w/proj"}),
		ompRecord(t, "message", "u1", "", "2026-10-27T10:00:01Z", user("plan the release", false)),
		ompRecord(t, "message", "s1", "u1", "2026-10-27T10:00:02Z", user("skip the changelog", true)),
		ompRecord(t, "message", "a1", "s1", "2026-10-27T10:00:03Z", map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "Planned."}}, "stopReason": "stop"}),
		ompRecord(t, "message", "u2", "a1", "2026-10-27T10:00:04Z", user("abandoned", false)),
		ompRecord(t, "message", "u3", "a1", "2026-10-27T10:00:05Z", user("ship it", false)),
	)
	reload(t, app)
	settle(app, key(t, app, "["))
	cards := app.pv.cards.Cards()
	if len(cards) != 2 || cards[0].Header != "plan the release" || !strings.Contains(cards[0].Meta, "1 steer") || cards[1].Header != "ship it" {
		t.Fatalf("cards %+v", cards)
	}
	if fr := frame(app); strings.Contains(fr, "abandoned") || !strings.Contains(fr, "ship it") {
		t.Fatalf("frame:\n%s", fr)
	}
}

// A shell's commands are cards; opening one reads its output, by l and by
// the timeline's own keys alike.
func TestTurnCardsOfAShell(t *testing.T) {
	t.Parallel()
	app, f := shellTurnsApp(t, 3)
	app.m.Width, app.m.Height = 120, 40
	cards := app.pv.cards.Cards()
	if len(cards) != 3 || cards[0].Header != "make old" || !strings.Contains(cards[0].Meta, "exit 1") || f.captures != 0 {
		t.Fatalf("cards %+v captures %d", cards, f.captures)
	}
	frame(app)
	press(t, app, "}", "k") // ls
	settle(app, key(t, app, "l"))
	if fr := frame(app); !strings.Contains(fr, "a.txt") || !strings.Contains(fr, "b.txt") {
		t.Fatalf("ls output:\n%s", fr)
	}
	press(t, app, "k")
	settle(app, appKey(t, app, "space")) // make old, opened by the timeline's toggle
	if fr := frame(app); !strings.Contains(fr, "output left the history") {
		t.Fatalf("make old:\n%s", fr)
	}
}

// turns.style switches the tab live, the focus staying on the same turn.
func TestTurnsStyleSwitchesLive(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	frame(app)
	press(t, app, "}")
	if app.r.Focus != turnCardID("w1", "u1") {
		t.Fatalf("} focused %s", where(app))
	}
	useTurnStyle(t, app, turnsStyleTree)
	if fr := frame(app); app.r.Focus != "turn:u1" || !strings.Contains(fr, "fix the flaky test · 1 steer") {
		t.Fatalf("tree: %s\n%s", where(app), fr)
	}
	useTurnStyle(t, app, turnsStyleCards)
	if fr := frame(app); app.r.Focus != turnCardID("w1", "u1") || !strings.Contains(fr, "╭") || app.pv.turns.List().Len() != 0 {
		t.Fatalf("cards: %s tree rows %d\n%s", where(app), app.pv.turns.List().Len(), fr)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": settingTurnsStyle, "value": "compact"}}); err == nil {
		t.Fatal("turns.style took a value it does not offer")
	}
}

// A journal turn's card: opening it marks it reviewed (its meta loses
// "new"), and the answer wraps without cutting.
func TestJournalTurnCards(t *testing.T) {
	t.Parallel()
	app := wrapAppStyle(t, strings.Repeat("lorem ipsum dolor sit amet ", 12)+"ENDWORD", turnsStyleCards)
	turns, _ := app.hist.Turns("w1")
	if turns[0].ReviewedAt != 0 {
		t.Fatal("the first turn was reviewed before it opened")
	}
	// wrapApp's } l focused the last card (following) and opened it.
	press(t, app, "k", "l")
	fr := plainView(app)
	if turns, _ = app.hist.Turns("w1"); turns[0].ReviewedAt == 0 {
		t.Fatalf("opening the card did not review it:\n%s", fr)
	}
	if !strings.Contains(fr, "ENDWORD") || strings.Contains(fr, "lorem…") {
		t.Fatalf("the answer is cut:\n%s", fr)
	}
	if c := app.pv.cards.Cards()[0]; strings.Contains(c.Meta, "new") || c.Header != "prompt 1\nwith a second line" {
		t.Fatalf("card %+v", c)
	}
}

// Golden frames of an opened journal card (prose, an indented code line,
// a blank line, a long URL, Japanese) at 80 and 120 columns; -update
// rewrites them.
func TestOpenedTurnCardFrames(t *testing.T) {
	t.Parallel()
	answer := "This answer is a long paragraph of prose that the agent wrote, which should wrap onto several rows at the panel width instead of being cut off with an ellipsis at the edge.\n" +
		"    func indented() { return someLongFunctionName(withArguments, andMoreArguments, andEvenMoreArguments) }\n" +
		"\n" +
		"See https://example.com/a/very/long/path/that/does/not/have/any/spaces/in/it/at/all/so/it/must/hard/break/somewhere.html\n" +
		"日本語の文章も正しく折り返されるべきです。全角文字は二つのセルを占めるので、幅の計算に注意が必要です。"
	app := wrapAppStyle(t, answer, turnsStyleCards)
	app.loc = time.UTC
	press(t, app, "k", "l") // the first card, whose answer this is
	for _, width := range []int{80, 120} {
		app.m.Width, app.m.Height = width, 34
		got := plainView(app)
		file := filepath.Join("testdata", fmt.Sprintf("turn-cards-%d.golden", width))
		if *updateGolden {
			if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("%d columns differ from %s:\n%s", width, file, got)
		}
	}
}
