package turns

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A shell pane's turns are its commands: tuios reads its shell's OSC 133
// marks and keeps the last 200 finished commands (command-history), each
// with the history row of its prompt. A turn's prompt is the command line
// and its response the output: the lines from the prompt's row to the next
// command's prompt row. The output is read only when a turn is opened or
// copied (ShellOutput), a chunk at a time.

// ShellTurns are a pane's commands as turns, oldest first. cmds come as
// tuios lists them (newest first); each command's End is the next one's
// prompt row. running, when the shell runs a command now, is one more
// turn at the end, still running.
func ShellTurns(session string, cmds []Command, running string) []Turn {
	sorted := slices.Clone(cmds)
	slices.SortFunc(sorted, func(a, b Command) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	out := make([]Turn, 0, len(sorted)+1)
	for i, c := range sorted {
		c.End = -1
		if i+1 < len(sorted) {
			c.End = sorted[i+1].Row
		}
		cmd := c
		state := StateDone
		if c.Exit != nil && *c.Exit != 0 {
			state = StateError
		}
		out = append(out, Turn{
			ID: fmt.Sprintf("shell:%d", c.Seq), Source: SourceShell, Session: session, Prompt: c.Cmdline,
			StartedAt: time.UnixMilli(c.At), Duration: time.Duration(c.DurationMS) * time.Millisecond,
			State: state, Exit: c.Exit, Command: &cmd,
		})
	}
	if strings.TrimSpace(running) != "" {
		next := uint64(1)
		if len(sorted) > 0 {
			next = sorted[len(sorted)-1].Seq + 1
		}
		out = append(out, Turn{ID: fmt.Sprintf("shell:%d", next), Source: SourceShell, Session: session, Prompt: running, State: StateRunning})
	}
	return out
}

// Capturer reads a pane's buffer (its history, then its screen) as
// capture-pane does: lines start to end, 1-based and inclusive, with the
// blank lines at the buffer's end dropped (trim). With unwrap the rows the
// terminal soft-wrapped come back joined and start and end count the
// joined lines; without it they count rows. Past the end it returns fewer
// lines, or none.
type Capturer interface {
	Capture(ctx context.Context, start, end int, unwrap bool) ([]string, error)
}

// ErrLeftHistory says a command's prompt row has left the pane's history.
var ErrLeftHistory = errors.New("output left the history")

// DefaultChunk is how many lines ShellOutput asks for in one capture.
const DefaultChunk = 2000

// ShellOutput reads a command's output, unwrapped, from its prompt's row
// to the next prompt's row (c.End; -1 to the end of the buffer), chunk
// lines a capture (DefaultChunk when 0), so a huge output never comes in
// one reply.
//
// tuios numbers command rows as rows, while an unwrapped capture counts
// joined lines, so the reader walks both from the top of the buffer and
// lines them up: each joined line is the rows it is made of laid end to
// end (with the spaces a capture trims from a wrapped row's end). The
// lines of the joined line holding the prompt row, and any prompt lines
// after it up to the one holding the command line, are not output.
func ShellOutput(ctx context.Context, cap Capturer, c Command, chunk int) ([]string, error) {
	if c.Row < 0 {
		return nil, ErrLeftHistory
	}
	if chunk <= 0 {
		chunk = DefaultChunk
	}
	rows := &captureStream{cap: cap, chunk: chunk}
	lines := &captureStream{cap: cap, chunk: chunk, unwrap: true}
	var out []string
	promptLine := ""
	row := 0 // the next row, 0-based as tuios numbers them
	inside := false
	for {
		line, ok, err := lines.next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		n, err := rowsOf(ctx, rows, line)
		if err != nil {
			return nil, err
		}
		row += n
		last := row - 1
		if c.End >= 0 && last >= c.End {
			break
		}
		switch {
		case inside:
			out = append(out, line)
		case last >= c.Row:
			inside, promptLine = true, line
		}
	}
	if !inside {
		return nil, ErrLeftHistory
	}
	out = skipPrompt(out, promptLine, c.Cmdline)
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out, nil
}

// skipPrompt drops the rest of a prompt that spans lines: when the
// prompt's own line does not hold the command line, the first of the next
// few lines that does is the prompt's last.
func skipPrompt(out []string, promptLine, cmdline string) []string {
	want := strings.TrimSpace(strings.SplitN(cmdline, "\n", 2)[0])
	if want == "" || strings.Contains(promptLine, want) {
		return out
	}
	for i := 0; i < min(4, len(out)); i++ {
		if strings.Contains(out[i], want) {
			return out[i+1:]
		}
	}
	return out
}

// rowsOf consumes the rows the joined line is made of and says how many.
// A row the line does not continue with ends it (the walk stays in step
// with the rows it could match).
func rowsOf(ctx context.Context, rows *captureStream, line string) (int, error) {
	rest := line
	n := 0
	for {
		r, ok, err := rows.next(ctx)
		if err != nil {
			return n, err
		}
		if !ok {
			return max(n, 1), nil
		}
		n++
		if !strings.HasPrefix(rest, r) {
			return n, nil // out of step: the line ends here
		}
		rest = rest[len(r):]
		if strings.TrimSpace(rest) == "" {
			return n, nil
		}
		// The next row goes on after the spaces the capture trimmed from
		// this one's end, unless it starts with spaces of its own.
		peek, ok, err := rows.peek(ctx)
		if err != nil {
			return n, err
		}
		if !ok {
			return n, nil
		}
		pad := len(rest) - len(strings.TrimLeft(rest, " "))
		own := len(peek) - len(strings.TrimLeft(peek, " "))
		if skip := pad - own; skip > 0 {
			rest = rest[skip:]
		}
		if peek == "" || !strings.HasPrefix(rest, peek) {
			return n, nil
		}
	}
}

// captureStream reads a buffer a line at a time, a chunk a capture.
type captureStream struct {
	cap    Capturer
	chunk  int
	unwrap bool
	next1  int // the 1-based line the next capture starts at
	buf    []string
	at     int
	eof    bool
	calls  int // captures made, for tests
}

func (s *captureStream) fill(ctx context.Context) error {
	if s.at < len(s.buf) || s.eof {
		return nil
	}
	if s.next1 == 0 {
		s.next1 = 1
	}
	got, err := s.cap.Capture(ctx, s.next1, s.next1+s.chunk-1, s.unwrap)
	s.calls++
	if err != nil {
		return err
	}
	s.buf, s.at = got, 0
	s.next1 += len(got)
	s.eof = len(got) < s.chunk
	return nil
}

func (s *captureStream) peek(ctx context.Context) (string, bool, error) {
	if err := s.fill(ctx); err != nil {
		return "", false, err
	}
	if s.at >= len(s.buf) {
		return "", false, nil
	}
	return s.buf[s.at], true, nil
}

func (s *captureStream) next(ctx context.Context) (string, bool, error) {
	line, ok, err := s.peek(ctx)
	if ok {
		s.at++
	}
	return line, ok, err
}
