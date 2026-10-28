package contextitems

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Cell is the run of screen cells a part of a span covers: one row, from
// column Col up to (not including) End. Columns count terminal cells, so a
// wide rune takes two.
type Cell struct {
	Row, Col, End int
}

// Span is one occurrence of an item on the screen.
type Span struct {
	// Item is the item as Extract would return it (a path resolved
	// against the source's folder and cleaned).
	Item Item
	// Raw is the text as it reads on the screen (./main.go, a URL with
	// its wrap and the padding between its pieces removed), or a
	// hyperlink's target.
	Raw string
	// Key is the same for every occurrence of the same item (kind and
	// normalized text, the rule Extract dedupes by): group by it, or hand
	// it to hints.Target.Key so the same item gets the same label.
	Key string
	// Parts are the cells the occurrence covers, one per row, top to
	// bottom: an item soft-wrapped across rows has several.
	Parts []Cell
}

// SpanOptions are the screen and where it was printed.
type SpanOptions struct {
	// Width is the pane's columns, for joining soft-wrapped rows as Join
	// does (0: none). Wrapped, when given, replaces that rule: Wrapped[i]
	// says row i continues on row i+1 (a terminal's soft-wrap flag).
	Width   int
	Wrapped []bool
	// Source fills the items' source fields; paths resolve against
	// Source.Cwd.
	Source Source
	// Stat checks a path on disk, as classifyPath needs to decide whether
	// a bare word (main.go, src/app.go) names a file. Nil means os.Stat.
	// On the UI loop pass CachedStat(), so a screen redrawn with the same
	// paths costs no system calls.
	Stat StatFunc
}

// ExtractSpans finds the items on a screen, given as its rows (one string
// per row, plain text or with terminal controls, which are stripped), and
// where each occurrence is drawn. It uses the same patterns and overlap
// rules as Extract over the same joined text, so the items are the ones
// Extract finds; unlike Extract it returns every occurrence (grouped by
// Span.Key), in reading order. A prompt or command source entry is not an
// item here: a screen has no single entry.
func ExtractSpans(rows []string, opts SpanOptions) []Span {
	stat := opts.Stat
	if stat == nil {
		stat = os.Stat
	}
	lines := make([]mline, len(rows))
	linkAt := map[[2]int]string{}
	for r, row := range rows {
		plain, links := stripLinks(row)
		lines[r] = screenLine(r, plain)
		for i, l := range links {
			if l != "" {
				c := lines[r].at[i]
				linkAt[[2]int{c.Row, c.Col}] = l
			}
		}
	}
	text, at := joinLines(lines, opts.Width, opts.Wrapped, opts.Source.Cwd, stat)
	// A hyperlink names its own target: its cells are its, and a pattern
	// matching the text drawn in it (a bare URL as its label) is dropped.
	links := hyperlinks(text, at, linkAt)
	var claimed []span
	out := make([]Span, 0, len(links))
	starts := make([]int, 0, len(links))
	for _, l := range links {
		m, raw := hyperlinkFound(l.target, stat)
		claimed = append(claimed, span{l.start, l.end})
		out = append(out, Span{
			Item:  newItem(m.kind, m.value, m.exists, opts.Source),
			Raw:   raw,
			Key:   itemKey(m.kind, m.value),
			Parts: parts(l.at),
		})
		starts = append(starts, l.start)
	}
	for _, m := range find(text, opts.Source.Cwd, stat) {
		value := strings.TrimSpace(m.value)
		if value == "" || overlaps(claimed, [2]int{m.start, m.end}) {
			continue
		}
		out = append(out, Span{
			Item:  newItem(m.kind, value, m.exists, opts.Source),
			Raw:   text[m.start:m.end],
			Key:   itemKey(m.kind, value),
			Parts: parts(at[m.start:m.end]),
		})
		starts = append(starts, m.start)
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return starts[a] - starts[b] })
	sorted := make([]Span, len(out))
	for i, j := range order {
		sorted[i] = out[j]
	}
	return sorted
}

// screenLine is row r with the cell of every byte.
func screenLine(r int, s string) mline {
	at := make([]Cell, 0, len(s))
	col := 0
	for i, ru := range s {
		w := ansi.StringWidth(string(ru))
		n := utf8.RuneLen(ru)
		if ru == utf8.RuneError {
			_, n = utf8.DecodeRuneInString(s[i:])
		}
		for range n {
			at = append(at, Cell{Row: r, Col: col, End: col + w})
		}
		col += w
	}
	return mline{s: s, at: at}
}

// parts folds the cells of a match's bytes into one run per row.
func parts(at []Cell) []Cell {
	var out []Cell
	for _, c := range at {
		if c.Row < 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Row == c.Row {
			out[n-1].Col = min(out[n-1].Col, c.Col)
			out[n-1].End = max(out[n-1].End, c.End)
			continue
		}
		out = append(out, c)
	}
	return out
}

// CachedStat is os.Stat remembering each path's answer for the life of the
// returned function (safe for concurrent use). A file created after the
// first look stays unknown until a new cache is made, so make one per
// hints session, not per process.
func CachedStat() StatFunc {
	type answer struct {
		info fs.FileInfo
		err  error
	}
	var mu sync.Mutex
	seen := map[string]answer{}
	return func(path string) (fs.FileInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		if a, ok := seen[path]; ok {
			return a.info, a.err
		}
		info, err := os.Stat(path)
		seen[path] = answer{info, err}
		return info, err
	}
}
