package turns

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeBuffer is a pane's buffer as tuios keeps it: logical lines laid out
// in rows width cells wide, each row captured without its trailing spaces
// (as capture-pane does, and as unwrap puts back), with trim.
type fakeBuffer struct {
	width int
	lines []string
	calls []string
}

func (b *fakeBuffer) rows() []string {
	var rows []string
	for _, l := range b.lines {
		if l == "" {
			rows = append(rows, "")
			continue
		}
		for len(l) > b.width {
			rows = append(rows, strings.TrimRight(l[:b.width], " "))
			l = l[b.width:]
		}
		rows = append(rows, strings.TrimRight(l, " "))
	}
	return rows
}

// rowOf is the 0-based row logical line i starts at.
func (b *fakeBuffer) rowOf(i int) int {
	saved := b.lines
	b.lines = saved[:i]
	n := len(b.rows())
	b.lines = saved
	return n
}

func (b *fakeBuffer) Capture(_ context.Context, start, end int, unwrap bool) ([]string, error) {
	b.calls = append(b.calls, fmt.Sprintf("%d-%d/%v", start, end, unwrap))
	all := b.rows()
	if unwrap {
		all = b.lines
	}
	for len(all) > 0 && strings.TrimSpace(all[len(all)-1]) == "" {
		all = all[:len(all)-1]
	}
	if start > len(all) {
		return nil, nil
	}
	return append([]string(nil), all[start-1:min(end, len(all))]...), nil
}

func TestShellTurnsAreCommandsOldestFirst(t *testing.T) {
	zero, one := 0, 1
	got := ShellTurns("w1", []Command{
		{Seq: 2, Cmdline: "go test", Exit: &one, Row: 10, At: 2000},
		{Seq: 1, Cmdline: "ls", Exit: &zero, Row: 3, At: 1000},
	}, "make")
	if len(got) != 3 || got[0].Prompt != "ls" || got[0].Command.End != 10 || got[1].Command.End != -1 ||
		got[1].State != StateError || got[0].State != StateDone || got[2].State != StateRunning || got[2].Prompt != "make" || got[2].ID != "shell:3" {
		t.Fatalf("turns %+v", got)
	}
}

// A command's output is the lines between its prompt and the next prompt,
// joined where the terminal wrapped them.
func TestShellOutputJoinsWrappedRows(t *testing.T) {
	long := strings.Repeat("abcdefghij", 5) // 50 cells: five rows at width 10
	b := &fakeBuffer{width: 10, lines: []string{
		"$ echo one", "one",
		"$ printf long", long, "short", "with  two  spaces and more text past the edge", "",
		"$ next",
	}}
	c := Command{Cmdline: "printf long", Row: b.rowOf(2), End: b.rowOf(7)}
	got, err := ShellOutput(context.Background(), b, c, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{long, "short", "with  two  spaces and more text past the edge"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("output\n%q\nwant\n%q", got, want)
	}
	// The last command reads to the end of the buffer.
	c = Command{Cmdline: "next", Row: b.rowOf(7), End: -1}
	if got, err := ShellOutput(context.Background(), b, c, 3); err != nil || len(got) != 0 {
		t.Fatalf("last command %q %v", got, err)
	}
}

// A prompt over two lines (a path line, then the line typed on) is not
// output.
func TestShellOutputSkipsATwoLinePrompt(t *testing.T) {
	b := &fakeBuffer{width: 80, lines: []string{"~/proj (main)", "❯ make lint", "ok", "~/proj (main)", "❯ "}}
	got, err := ShellOutput(context.Background(), b, Command{Cmdline: "make lint", Row: 0, End: 3}, 0)
	if err != nil || strings.Join(got, "|") != "ok" {
		t.Fatalf("output %q %v", got, err)
	}
}

func TestShellOutputLeftTheHistory(t *testing.T) {
	b := &fakeBuffer{width: 80}
	if _, err := ShellOutput(context.Background(), b, Command{Row: -1}, 0); err != ErrLeftHistory {
		t.Fatalf("row -1: %v", err)
	}
	if _, err := ShellOutput(context.Background(), b, Command{Row: 5, End: -1}, 0); err != ErrLeftHistory {
		t.Fatalf("a row past the buffer: %v", err)
	}
}

// A 50 000-line output comes a chunk a capture, never in one reply, and
// whole.
func TestShellOutputPagesAHugeOutput(t *testing.T) {
	lines := []string{"$ seq 50000"}
	for i := 1; i <= 50000; i++ {
		lines = append(lines, fmt.Sprintf("line %d %s", i, strings.Repeat("x", i%37)))
	}
	lines = append(lines, "$ ")
	b := &fakeBuffer{width: 30, lines: lines}
	end := b.rowOf(len(lines) - 1)
	got, err := ShellOutput(context.Background(), b, Command{Cmdline: "seq 50000", Row: 0, End: end}, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50000 || got[0] != lines[1] || got[49999] != lines[50000] {
		t.Fatalf("got %d lines, first %q last %q", len(got), got[0], got[len(got)-1])
	}
	for _, c := range b.calls {
		var s, e int
		var u bool
		fmt.Sscanf(strings.Replace(c, "/", " ", 1), "%d-%d %v", &s, &e, &u)
		if e-s+1 > 2000 {
			t.Fatalf("a capture of %s", c)
		}
	}
	if len(b.calls) < 25 {
		t.Fatalf("captures %d", len(b.calls))
	}
}
