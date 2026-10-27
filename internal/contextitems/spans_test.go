package contextitems

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// spanKinds is "kind text @parts" per span.
func spanKinds(spans []Span) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		var b strings.Builder
		b.WriteString(s.Item.Kind + " " + s.Item.Text + " @")
		for _, p := range s.Parts {
			b.WriteString(" " + itoa(p.Row) + ":" + itoa(p.Col) + "-" + itoa(p.End))
		}
		out = append(out, b.String())
	}
	return out
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestExtractSpans(t *testing.T) {
	dir := t.TempDir()
	url := "https://github.com/unixsuperhero/matomeru/pull/1234/files"
	cases := []struct {
		name string
		rows []string
		opts SpanOptions
		want []string
	}{
		{
			name: "single row items",
			rows: []string{"see https://example.com/x and ./main.go now"},
			want: []string{
				"link https://example.com/x @ 0:4-25",
				"file " + filepath.Join(dir, "main.go") + " @ 0:30-39",
			},
		},
		{
			name: "URL soft-wrapped across two rows by width",
			rows: []string{"PR: " + url[:36], url[36:], "next line"},
			opts: SpanOptions{Width: 40},
			want: []string{"pr " + url + " @ 0:4-40 1:0-" + itoa(len(url)-36)},
		},
		{
			name: "URL soft-wrapped by the terminal's wrap flag",
			rows: []string{"x https://a.io/ab", "cd done"},
			opts: SpanOptions{Wrapped: []bool{true, false}},
			want: []string{"link https://a.io/abcd @ 0:2-17 1:0-2"},
		},
		{
			name: "URL hard-wrapped by a program is repaired",
			rows: []string{"url: https://example.com/a/", "  b/c"},
			want: []string{"link https://example.com/a/b/c @ 0:5-27 1:2-5"},
		},
		{
			name: "wide runes before an item count two cells",
			rows: []string{"日本 https://a.io é https://b.io"},
			want: []string{"link https://a.io @ 0:5-17", "link https://b.io @ 0:20-32"},
		},
		{
			name: "overlapping matches: the URL claims its path, PR and hex",
			rows: []string{"https://github.com/o/r/pull/1/commits/1ba025f9 and o/r#2 1ba025f"},
			want: []string{
				"pr https://github.com/o/r/pull/1/commits/1ba025f9 @ 0:0-46",
				"pr o/r#2 @ 0:51-56",
				"commit 1ba025f @ 0:57-64",
			},
		},
		{
			name: "same text twice is two spans in reading order",
			rows: []string{"a https://x.io/", "b https://x.io and https://x.io/"},
			want: []string{
				"link https://x.io/ @ 0:2-15",
				"link https://x.io @ 1:2-14",
				"link https://x.io/ @ 1:19-32",
			},
		},
		{
			name: "terminal controls are stripped before columns",
			rows: []string{"\x1b[31mred\x1b[0m https://a.io"},
			want: []string{"link https://a.io @ 0:4-16"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.opts.Source.Cwd = dir
			got := spanKinds(ExtractSpans(c.rows, c.opts))
			if !slices.Equal(got, c.want) {
				t.Fatalf("spans:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

func TestSpansOfTheSameItemShareAKey(t *testing.T) {
	spans := ExtractSpans([]string{"https://x.io/ https://x.io https://y.io"}, SpanOptions{})
	if len(spans) != 3 || spans[0].Key != spans[1].Key || spans[0].Key == spans[2].Key {
		t.Fatalf("keys = %q", []string{spans[0].Key, spans[1].Key, spans[2].Key})
	}
	if spans[0].Raw != "https://x.io/" || spans[1].Raw != "https://x.io" {
		t.Fatalf("raw = %q %q", spans[0].Raw, spans[1].Raw)
	}
}

// The spans name the items Extract finds over the same screen.
func TestSpansFindWhatExtractFinds(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		"see https://example.com/docs?page=2 and https://example.com/docs?page=2.",
		"opened https://github.com/unixsuperhero/matomeru/pull/42",
		"and unixsuperhero/ochazuke#7 for the fix, main.go too",
		"edited " + file + ":12:3 and ./main.go",
		"Switched to a new branch 'lane/context'",
		"[lane/context 1ba025f] context: declare the table",
		"window 9332c352-fd31-49a0-bb93-6b18ed187d8d, origin/main",
	}
	src := Source{Cwd: dir}
	want := kinds(Extract(strings.Join(rows, "\n"), 30, src))
	slices.Sort(want)
	var got []string
	for _, s := range ExtractSpans(rows, SpanOptions{Width: 30, Source: src}) {
		got = append(got, s.Item.Kind+" "+s.Item.Text)
	}
	slices.Sort(got)
	got = slices.Compact(got)
	if !slices.Equal(got, want) {
		t.Fatalf("spans:\n%s\nextract:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A bare word is a path only when it exists; the check is the Stat given.
func TestSpansCheckBarePathsThroughStat(t *testing.T) {
	asked := 0
	exists := func(string) (fs.FileInfo, error) { asked++; return fakeFile{}, nil }
	missing := func(string) (fs.FileInfo, error) { asked++; return nil, fs.ErrNotExist }
	rows := []string{"edit main.go"}
	if got := spanKinds(ExtractSpans(rows, SpanOptions{Source: Source{Cwd: "/w"}, Stat: exists})); !slices.Equal(got, []string{"file /w/main.go @ 0:5-12"}) {
		t.Fatalf("existing = %q", got)
	}
	if got := ExtractSpans(rows, SpanOptions{Source: Source{Cwd: "/w"}, Stat: missing}); len(got) != 0 {
		t.Fatalf("missing = %q", spanKinds(got))
	}
	if asked == 0 {
		t.Fatal("Stat was not used")
	}
	stat := CachedStat()
	a, errA := stat(os.TempDir())
	b, errB := stat(os.TempDir())
	if errA != nil || errB != nil || a != b {
		t.Fatalf("cached = %v %v, %v %v", a, errA, b, errB)
	}
}

type fakeFile struct{ fs.FileInfo }

func (fakeFile) IsDir() bool { return false }
