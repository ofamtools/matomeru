package contextitems

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// hardWrap breaks text into rows of at most width cells (ASCII), every row
// after the first starting with indent, mid-word where a word does not fit:
// what a program that wraps its own output (Claude Code, omp) prints.
func hardWrap(text string, width int, indent string) []string {
	var rows []string
	row := ""
	for _, word := range strings.Fields(text) {
		sep := " "
		if strings.TrimSpace(row) == "" {
			sep = ""
		}
		if len(row)+len(sep)+len(word) <= width {
			row += sep + word
			continue
		}
		if strings.TrimSpace(row) != "" && len(word) <= width-len(indent) {
			rows = append(rows, row)
			row = indent + word
			continue
		}
		// A word longer than a row is cut at the edge.
		row += sep
		for len(row)+len(word) > width {
			cut := width - len(row)
			rows = append(rows, row+word[:cut])
			row, word = indent, word[cut:]
		}
		row += word
	}
	return append(rows, row)
}

// box draws rows inside a box inner cells wide: "│ row │", padded, with
// a top and bottom border (omp's tool boxes).
func box(inner int, rows ...string) []string {
	out := []string{"╭" + strings.Repeat("─", inner+2) + "╮"}
	for _, r := range rows {
		out = append(out, "│ "+r+strings.Repeat(" ", inner-len(r))+" │")
	}
	return append(out, "╰"+strings.Repeat("─", inner+2)+"╯")
}

// gutter puts omp's one-cell left gutter before every row.
func gutter(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = " " + r
	}
	return out
}

// pad pads every row to width cells, as the emulator's frame does.
func pad(width int, rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r + strings.Repeat(" ", max(0, width-len([]rune(r))))
	}
	return out
}

// TestWrappedLinksJoin: a link split over rows, by the terminal or by the
// program's own wrap, is one span over every piece, its text joined with
// the padding between the pieces trimmed.
func TestWrappedLinksJoin(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "internal", "contextitems", "spans_wrapped_example.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pr := "https://github.com/unixsuperhero/matomeru/pull/1234/files"
	long := "https://example.com/" + strings.Repeat("a", 30) + "/" + strings.Repeat("b", 30) + "/tail?x=1"
	cases := []struct {
		name string
		rows []string
		opts SpanOptions
		want []string
	}{
		{
			name: "terminal soft wrap at the right edge",
			rows: pad(40, []string{"PR: " + pr[:36], pr[36:] + " next"}),
			opts: SpanOptions{Width: 40},
			want: []string{"pr " + pr + " @ 0:4-40 1:0-21"},
		},
		{
			name: "hard wrap with a 4-space indent",
			rows: hardWrap("Claude: the change is at "+pr+" for review", 50, "    "),
			want: []string{"pr " + pr + " @ 0:25-50 1:4-36"},
		},
		{
			name: "inside a │ … │ box",
			rows: box(40, hardWrap("see https://example.com/docs/guides/install?step=2", 40, "")...),
			want: []string{"link https://example.com/docs/guides/install?step=2 @ 1:6-42 2:2-12"},
		},
		{
			name: "omp: one-cell gutter, response wrapped to the text column",
			rows: pad(52, gutter(hardWrap("Opened the review at "+pr+" and left a note.", 50, ""))),
			opts: SpanOptions{Width: 52},
			want: []string{"pr " + pr + " @ 0:22-51 1:1-29"},
		},
		{
			name: "omp: one-cell gutter and a tool box",
			rows: pad(52, gutter(box(46, hardWrap("docs: https://github.com/unixsuperhero/omp/blob/main/README.md", 46, "")...))),
			opts: SpanOptions{Width: 52},
			want: []string{"link https://github.com/unixsuperhero/omp/blob/main/README.md @ 1:9-49 2:3-19"},
		},
		{
			// Captured from an omp pane, 2026-10-28: a screenshot path
			// wrapped inside a tool box.
			name: "omp capture: a path wrapped in a box",
			rows: []string{
				"╭─── 🟨 • Inspecting existing card styling · (178ms) ─────────────────────────╮",
				"│ /var/folders/fq/njpv8bjs42bf7c6t17qtwfpc0000gn/T/omp-sshots-15b7cfa4b69f9b2 │",
				"│ 6.png                                                                       │",
				"╰─────────────────────────────────────────────────────────────────────────────╯",
			},
			want: []string{"file /var/folders/fq/njpv8bjs42bf7c6t17qtwfpc0000gn/T/omp-sshots-15b7cfa4b69f9b26.png @ 1:2-77 2:2-7"},
		},
		{
			name: "a path split across rows",
			rows: hardWrap("edited "+file+" and ran the tests", len(dir)+30, "  "),
			want: []string{"file " + file + " @ 0:7-" + itoa(len(dir)+30) + " 1:2-" + itoa(2+len(file)-(len(dir)+23))},
		},
		{
			name: "a URL over three rows",
			rows: hardWrap("url: "+long, 40, "  "),
			want: []string{"link " + long + " @ 0:5-40 1:2-40 2:2-" + itoa(2+len(long)-35-38)},
		},
		{
			name: "unrelated text on the next row is not joined",
			rows: []string{"see https://example.com/docs/intro", "and then more text here"},
			want: []string{"link https://example.com/docs/intro @ 0:4-34"},
		},
		{
			name: "a blank row between is not joined",
			rows: []string{"see https://example.com/docs/", "", "intro/page"},
			want: []string{"link https://example.com/docs/ @ 0:4-29"},
		},
		{
			name: "a row that ends short of the margin is not joined",
			rows: []string{"see https://example.com/docs/", "intro/page", strings.Repeat("x ", 30)},
			want: []string{"link https://example.com/docs/ @ 0:4-29"},
		},
		{
			name: "a prompt on the next row is not joined",
			rows: []string{"end https://example.com/again", "$ "},
			want: []string{"link https://example.com/again @ 0:4-29"},
		},
		{
			name: "a new URL on the next row is its own",
			rows: []string{"a https://x.io/aaaaaaaa", "https://y.io/b"},
			want: []string{"link https://x.io/aaaaaaaa @ 0:2-23", "link https://y.io/b @ 1:0-14"},
		},
		{
			// omp, 2026-10-28: a command wrapped in its box, the second
			// path starting the next row.
			name: "a new path on the next row is its own",
			rows: box(len(dir)+17, "$ cp -p "+dir+"/init.lua", dir+"/b"),
			want: []string{"file " + dir + "/init.lua @ 1:10-" + itoa(10+len(dir)+9), "file " + dir + "/b @ 2:2-" + itoa(2+len(dir)+2)},
		},
		{
			name: "a box border does not continue a response row",
			rows: append([]string{"see https://example.com/docs/"}, box(27, "intro/page")...),
			want: []string{"link https://example.com/docs/ @ 0:4-29"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.opts.Source.Cwd = dir
			spans := ExtractSpans(c.rows, c.opts)
			got := spanKinds(spans)
			if !slices.Equal(got, c.want) {
				t.Fatalf("rows:\n%s\nspans:\n%s\nwant:\n%s", strings.Join(c.rows, "|\n"), strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
			for _, s := range spans {
				if strings.ContainsAny(s.Raw, " │\n") {
					t.Fatalf("raw %q keeps the padding", s.Raw)
				}
			}
		})
	}
}

// The joined text is what Extract finds too.
func TestExtractJoinsHardWraps(t *testing.T) {
	pr := "https://github.com/unixsuperhero/matomeru/pull/1234/files"
	text := strings.Join(hardWrap("Claude: the change is at "+pr+" for review", 50, "    "), "\n")
	if got := kinds(Extract(text, 0, Source{})); !slices.Equal(got, []string{"pr " + pr}) {
		t.Fatalf("items = %q", got)
	}
}

// OSC 8: a hyperlink's own URL is the item, over the cells of its text,
// across rows; a bare URL in its text is the label, not the target.
func TestHyperlinks(t *testing.T) {
	link := func(params, url, text string) string {
		return "\x1b]8;" + params + ";" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
	}
	cases := []struct {
		name string
		rows []string
		want []string
	}{
		{
			name: "the URL of a hyperlink, over its text",
			rows: []string{"go " + link("", "https://example.com/osc/target", "click here") + " now"},
			want: []string{"link https://example.com/osc/target @ 0:3-13"},
		},
		{
			name: "a hyperlink across two rows is one span",
			rows: []string{"x " + link("id=1", "https://x.io/long", "label pa"), "  " + link("id=1", "https://x.io/long", "rt two") + " y"},
			want: []string{"link https://x.io/long @ 0:2-10 1:2-8"},
		},
		{
			name: "a bare URL inside the text is the label",
			rows: []string{link("", "https://real.io/", "https://shown.io") + " https://other.io"},
			want: []string{"link https://real.io/ @ 0:0-16", "link https://other.io @ 0:17-33"},
		},
		{
			name: "BEL ends the sequence too",
			rows: []string{"\x1b]8;;https://bel.io/x\x07here\x1b]8;;\x07"},
			want: []string{"link https://bel.io/x @ 0:0-4"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := spanKinds(ExtractSpans(c.rows, SpanOptions{}))
			if !slices.Equal(got, c.want) {
				t.Fatalf("spans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}
