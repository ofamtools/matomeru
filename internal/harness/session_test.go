package harness

import (
	"strings"
	"testing"
)

// The built-ins carry their session sources; a file's session object
// merges by field, null removes it, and a source that ties no record to
// a pane is refused.
func TestSessionSources(t *testing.T) {
	reg, err := NewRegistry(Builtins())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "omp"} {
		if h, _ := reg.Get(name); h.Session == nil {
			t.Fatalf("%s has no session source", name)
		}
	}
	if h, _ := reg.Get("codex"); h.Session != nil {
		t.Fatalf("codex has a session source: %+v", h.Session)
	}
	merged, err := Merge(Builtins(), []byte(`[{"name": "claude", "session": {"files": "/x/*.json"}}, {"name": "omp", "session": null}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(merged); err != nil {
		t.Fatal(err)
	}
	if s := merged[0].Session; s.Files != "/x/*.json" || s.Session != "sessionId" || Builtins()[0].Session.Files == "/x/*.json" {
		t.Fatalf("merge: %+v", s)
	}
	if merged[1].Session != nil {
		t.Fatalf("null kept omp's source: %+v", merged[1].Session)
	}
	for in, want := range map[string]string{
		`{"files": "/x/*.json", "session": "id", "transcript": "/t/{session}"}`:        "need pid",
		`{"files": "/x/*.json", "session": "id", "transcript": "/t", "pid": "pid"}`:    "needs start",
		`{"files": "/x/{tty}", "session": "id"}`:                                       "give path",
		`{"files": "/x/{tty}", "path": "2", "format": "lines", "sessionPattern": "x"}`: "with a group",
		`{"files": "/x/{tty}", "path": "two", "format": "lines", "session": "1"}`:      "not a line number",
		`{"files": "", "path": "p", "session": "s"}`:                                   "files is empty",
	} {
		list, err := Merge(Builtins(), []byte(`[{"name": "x", "command": ["x"], "session": `+in+`}]`))
		if err == nil {
			_, err = NewRegistry(list)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", in, err, want)
		}
	}
}
