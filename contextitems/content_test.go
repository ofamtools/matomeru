package contextitems

import (
	"testing"
)

func TestExtractContentMatchesPathsURLsRefsAndCode(t *testing.T) {
	lines := []string{
		"Body: /Users/unixsuperhero/hub/animations/clown-car-chips/index.html and /Users/unixsuperhero/hub/demo.mp4",
		"Path: /Users/unixsuperhero/.local/share/matomeru/notes/demo.md",
		"See https://ofam.dev/docs and task:abc12345 and mtm://note/xyz999",
		"Run `go test ./...` or check org/repo#42",
	}
	matches := ExtractContentMatches(lines)

	want := map[string]string{
		"/Users/unixsuperhero/hub/animations/clown-car-chips/index.html": KindFile,
		"/Users/unixsuperhero/hub/demo.mp4":                              KindFile,
		"/Users/unixsuperhero/.local/share/matomeru/notes/demo.md":       KindFile,
		"https://ofam.dev/docs":                                          KindURL,
		"task:abc12345":                                                  KindRef,
		"mtm://note/xyz999":                                              KindRef,
		"go test ./...":                                                  KindCode,
		"org/repo#42":                                                    KindPR,
	}
	got := map[string]string{}
	for _, m := range matches {
		got[m.Value] = m.Kind
	}
	for val, wantKind := range want {
		if gotKind, ok := got[val]; !ok || gotKind != wantKind {
			t.Errorf("match %q = %q (found=%v), want kind %q (all=%+v)", val, gotKind, ok, wantKind, matches)
		}
	}
}
