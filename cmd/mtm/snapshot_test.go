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

// highlight is one run of reverse video in a frame: its stripped text and
// whether faint was on when it started (a dimmed highlight: an unfocused
// list's selected row, a page row beneath a modal).
type highlight struct {
	text   string
	dimmed bool
}

// highlights follows the SGR state along a raw frame, as ochazuke's
// story runner does: faint on via 2, off via 22, 0 or a bare reset;
// reverse on via 7, off via 27, 0 or a bare reset (and at a line's end).
func highlights(raw string) []highlight {
	var out []highlight
	var text strings.Builder
	faint, reverse, dimmed := false, false, false
	end := func() {
		if reverse {
			out = append(out, highlight{strings.TrimSpace(text.String()), dimmed})
		}
		reverse = false
		text.Reset()
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			end()
			faint = false
			continue
		}
		if raw[i] != 0x1b || i+1 >= len(raw) || raw[i+1] != '[' {
			if reverse {
				text.WriteByte(raw[i])
			}
			continue
		}
		j := i + 2
		for j < len(raw) && (raw[j] >= '0' && raw[j] <= '9' || raw[j] == ';' || raw[j] == ':') {
			j++
		}
		if j >= len(raw) || raw[j] != 'm' {
			continue
		}
		for _, p := range strings.Split(raw[i+2:j], ";") {
			switch p {
			case "", "0":
				end()
				faint = false
			case "2":
				faint = true
			case "22":
				faint = false
			case "7":
				if !reverse {
					reverse, dimmed = true, faint
				}
			case "27":
				end()
			}
		}
		i = j
	}
	end()
	return out
}

// highlighted is the text of a frame's full-strength and dimmed
// highlights.
func highlighted(raw string) (full, dimmed []string) {
	for _, h := range highlights(raw) {
		if h.dimmed {
			dimmed = append(dimmed, h.text)
		} else {
			full = append(full, h.text)
		}
	}
	return full, dimmed
}

// The work page draws projects and tasks; only the focused list's selected
// row stands out at full strength (reverse video), the unfocused list's
// selected row keeps a dimmed highlight (reverse under faint, ochazuke's
// dim-unfocused), and no row carries a cursor marker.
func TestWorkPageShowsOneSelectedRow(t *testing.T) {
	app := open(t)
	mono(t, app)
	check := func(want string, faded ...string) {
		t.Helper()
		raw := rawFrame(app)
		full, dimmed := highlighted(raw)
		if len(full) != 1 || !strings.Contains(full[0], want) {
			t.Fatalf("full-strength highlights %q, want one with %q:\n%s", full, want, ansi.Strip(raw))
		}
		if len(dimmed) != len(faded) {
			t.Fatalf("dimmed highlights %q, want %q:\n%s", dimmed, faded, ansi.Strip(raw))
		}
		for i, text := range faded {
			if !strings.Contains(dimmed[i], text) {
				t.Fatalf("dimmed highlights %q, want %q:\n%s", dimmed, faded, ansi.Strip(raw))
			}
		}
		for _, line := range strings.Split(ansi.Strip(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "> ") {
				t.Fatalf("a row carries a cursor marker: %q", line)
			}
		}
	}
	check("ochazuke") // ochazuke has no tasks: no other row to dim
	app.m.Update(keyMsg("j"))
	app.m.Update(keyMsg("enter")) // matomeru's tasks: the focus moves to them
	if !strings.Contains(frame(app), "write the brief") {
		t.Fatalf("tasks not shown:\n%s", frame(app))
	}
	check("write the brief", "◆ matomeru") // the projects list keeps its row, dimmed
}
