package main

import (
	"reflect"
	"testing"

	"ofam.dev/ochazuke"
)

// The nouns with a prop that always holds a path get the library's free
// verbs; the opener they reach is the runtime's, faked here.
func TestPathPropsGetFreeVerbs(t *testing.T) {
	t.Parallel()
	app := openWith(t, Options{Data: seeded(t)})
	var ran [][]string
	app.r.SetOpener(ochazuke.Opener{GOOS: "darwin", Start: func(name string, args ...string) error {
		ran = append(ran, append([]string{name}, args...))
		return nil
	}})
	for _, noun := range []string{"project", "saved", "pane", "vim"} {
		for _, verb := range []string{"reveal", "yank-path", "open", "open-with"} {
			if _, ok := app.r.Operations.Find(ochazuke.Key(noun + "." + verb)); !ok {
				t.Errorf("%s.%s missing", noun, verb)
			}
		}
	}
	if err := app.cx.sys.Open(nil, "/tmp/x"); err != nil {
		t.Fatal(err)
	}
	if err := app.cx.sys.Reveal(nil, "/tmp/x"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"open", "--", "/tmp/x"}, {"open", "-R", "--", "/tmp/x"}}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
}
