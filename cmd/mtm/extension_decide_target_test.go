package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A decision key on a focused row of a mounted decision page runs the verb
// with that row's question id as its target, never another noun's id.
func TestExtensionRowVerbTargetsTheFocusedRow(t *testing.T) {
	t.Parallel()
	catalog, err := os.ReadFile("testdata/o-questions-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := fakeExtension(t, string(catalog), `"decision list") cat '`+mustAbs(t, "testdata/o-questions-decision-rows.json")+`' ;;
"question decide"|"question accept"|"question undecide"|"question list"|"question decide"|"decision decide"|"decision accept"|"decision undecide") echo '{"ok":true}' ;;`)
	path := seeded(t)
	out := mustCLI(t, openAt(t, path), "extension", "register", "question", "--bin", bin)
	if !strings.Contains(out, `"name": "question"`) {
		t.Fatalf("register answered:\n%s", out)
	}
	a := openAt(t, path)
	var page string
	for _, x := range a.exts {
		for _, v := range x.views {
			if strings.HasSuffix(v.Page(), "/decision") {
				page = v.Page()
			}
		}
	}
	openExtPage(t, a, page)
	id := page + "/questions/question/q-f7a7a215efd0defb"
	if err := a.r.SetFocus(id); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"1": `question decide --target question/q-f7a7a215efd0defb --json {"choice":1} --yes`,
		"a": `question accept --target question/q-f7a7a215efd0defb --json {} --yes`,
		"x": `question undecide --target question/q-f7a7a215efd0defb --json {} --yes`,
	} {
		pressJob(t, a, key)
		var got bool
		for _, c := range calls(t, log) {
			got = got || strings.HasSuffix(c, "|"+want)
		}
		if !got {
			t.Errorf("key %s: want %q; calls %v", key, want, calls(t, log))
		}
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
