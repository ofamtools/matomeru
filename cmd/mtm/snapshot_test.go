package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// monoFrame renders one 80x24 frame in the mono theme. It fails on a
// colour escape (mono emits only bold, faint and the like) and returns the
// cells with each line's trailing blanks trimmed.
func monoFrame(t *testing.T, a *App) string {
	t.Helper()
	a.m.Width, a.m.Height = 80, 24
	raw := a.m.View().Content
	if c := regexp.MustCompile(`\x1b\[[0-9;]*(3[0-9]|4[0-9]|9[0-7]|10[0-7])[;m]`).FindString(raw); c != "" {
		t.Fatalf("colour escape %q in the mono frame", c)
	}
	lines := strings.Split(ansi.Strip(raw), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// mono switches the app to the mono theme.
func mono(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Verb: "set", Noun: "appearance", Args: ochazuke.Arguments{"theme": "mono"}}); err != nil {
		t.Fatal(err)
	}
}

// The panes page with a fake pane attached and focused: the detail is a
// focused (heavy) panel titled with the pane's session/name.
const attachedFrame = `  ◆ Matomeru                                                         TERMINAL
   Work  › Panes  Review
  ────────────────────────────────────────────────────────────────────────────
  panes (2)
  > matomeru/claude
    ochazuke/shell



  ╭━ matomeru/claude ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╮
  ┃ $ claude                                                                 ┃
  ┃ > thinking about the brief                                               ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ┃                                                                          ┃
  ╰━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╯
  jobs: #1 panes.refresh done
  ────────────────────────────────────────────────────────────────────────────
  ctrl+a leader: d detaches, ctrl+a sends ctrl+a · <paste> terminal.paste: Pa…
`

// The same page with a preview and the focus on the list: a plain panel.
const previewFrame = `  ◆ Matomeru                                                           NORMAL
   Work  › Panes  Review
  ────────────────────────────────────────────────────────────────────────────
  panes (2)
  > matomeru/claude
    ochazuke/shell



  ╭─ matomeru/claude · preview ──────────────────────────────────────────────╮
  │ claude is thinking                                                       │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  │                                                                          │
  ╰──────────────────────────────────────────────────────────────────────────╯
  jobs: #2 pane.preview done · #1 panes.refresh done
  ────────────────────────────────────────────────────────────────────────────
  ctrl+c app.quit: Exit the application · enter pane.attach: Attach the pane …
`

func TestPanesDetailSnapshots(t *testing.T) {
	app, f := panesApp(t)
	mono(t, app)
	cmd := key(t, app, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"$ claude", "> thinking about the brief"}, Hidden: true}
	runOnce(app, cmd)
	if got := monoFrame(t, app); got != attachedFrame {
		t.Fatalf("attached frame:\n%s\nwant:\n%s", got, attachedFrame)
	}
	if !app.r.Terminal(paneTermID).Focused() {
		t.Fatal("the terminal is not focused")
	}

	app, _ = panesApp(t)
	mono(t, app)
	settle(app, key(t, app, "p"))
	if got := monoFrame(t, app); got != previewFrame {
		t.Fatalf("preview frame:\n%s\nwant:\n%s", got, previewFrame)
	}
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus = %s", app.r.Focus)
	}
}
