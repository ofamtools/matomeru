package handlers

import (
	"slices"
	"testing"
)

func names(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Name
	}
	return out
}

func TestDefaultRulesMatch(t *testing.T) {
	r := Default()
	r.IsDir = func(p string) bool { return p == "/work/repo" }
	for target, want := range map[string][]string{
		"/data/sales.csv":           {"o-csv2"},
		"/data/SALES.TSV":           {"o-csv2"},
		"notes/log.jsonl":           {"o-json2"},
		"/work/repo/README.md":      {MarkdownReader, SideVim},
		"/work/repo":                {PathPicker, NewPane},
		"/somewhere/else/":          {PathPicker, NewPane},
		"https://ofam.dev/a/b?c=d":  {SystemOpen},
		"http://localhost:8080":     {SystemOpen},
		"https://ofam.dev/guide.md": {SystemOpen}, // a URL is never matched by a file glob
		"/attachments/shot.png":     {SystemOpen},
		"/attachments/PHOTO.JPG":    {SystemOpen},
		"/attachments/pic.jpeg":     {SystemOpen},
		"/attachments/anim.gif":     {SystemOpen},
		"/attachments/clip.webp":    {SystemOpen},
		"/attachments/diagram.svg":  {SystemOpen},
		"/bin/ls":                   nil,
		"":                          nil,
	} {
		if got := names(r.Match(target)); !slices.Equal(got, want) {
			t.Errorf("Match(%q) = %v, want %v", target, got, want)
		}
	}
}

func TestLoadJSONCMergesAndOrders(t *testing.T) {
	r := Default()
	r.IsDir = func(string) bool { return false }
	err := r.LoadJSONC([]byte(`[
		// my own reader wins over the built-in one
		{"name": "glow", "match": "*.md", "command": "glow -p", "priority": 200},
		/* the built-in editor handler, moved up */
		{"name": "side-vim", "match": "*.md", "priority": 150},
		{"name": "mpv", "match": "*.mkv", "command": "mpv {target} --fs",},
	]`))
	if err != nil {
		t.Fatal(err)
	}
	got := r.Match("/docs/My Plan.md")
	if !slices.Equal(names(got), []string{"glow", SideVim, MarkdownReader}) {
		t.Fatalf("order = %v", names(got))
	}
	if got[0].Kind != KindExec || got[0].CommandLine("/docs/My Plan.md") != "glow -p '/docs/My Plan.md'" {
		t.Fatalf("glow = %+v, command %q", got[0], got[0].CommandLine("/docs/My Plan.md"))
	}
	if got[1].Kind != KindBuiltin || len(r.Rules()) != len(DefaultRules())+2 {
		t.Fatalf("side-vim was not replaced in place: %+v (%d rules)", got[1], len(r.Rules()))
	}
	if cmd := r.Match("a.mkv")[0].CommandLine("it's.mkv"); cmd != `mpv 'it'\''s.mkv' --fs` {
		t.Fatalf("command = %q", cmd)
	}
	// The object form, and what is refused.
	if err := r.LoadJSONC([]byte(`{"rules": [{"name": "zathura", "match": "*.pdf", "command": "zathura"}]}`)); err != nil || len(r.Match("x.pdf")) != 1 {
		t.Fatalf("object form: %v", err)
	}
	before := len(r.Rules())
	for _, bad := range []string{`[{"name": "x"}]`, `[{"name": "x", "match": "*.x", "kind": "exec"}]`, `[{"name": "x", "match": "*.x", "kind": "nope"}]`, `{nope`} {
		if err := r.LoadJSONC([]byte(bad)); err == nil {
			t.Errorf("LoadJSONC(%s) did not fail", bad)
		}
	}
	if len(r.Rules()) != before {
		t.Fatal("a file that could not be read changed the rules")
	}
}
