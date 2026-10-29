package harness

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/drive"
)

// drive and view are data on a profile: a file entry sets them field by
// field over the built-in's, null removes the view, and a bad value is
// refused at load naming the field.
func TestDriveAndViewAreData(t *testing.T) {
	for _, h := range Builtins() {
		if h.DriveOf() != drive.Terminal || h.View != nil {
			t.Fatalf("%s: drive %q view %+v", h.Name, h.DriveOf(), h.View)
		}
	}
	merged, err := Merge(Builtins(), []byte(`[
		{"name": "claude", "drive": "exec", "view": {"open": "both"}},
		{"name": "omp", "view": {"split": 60}},
		{"name": "codex", "drive": "api", "view": {"open": "terminal", "split": 30}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := NewRegistry(merged); err != nil || len(r.List()) != 3 {
		t.Fatalf("registry: %v", err)
	}
	if merged[0].DriveOf() != drive.Exec || *merged[0].View != (View{Open: ViewBoth}) {
		t.Fatalf("claude: %q %+v", merged[0].Drive, merged[0].View)
	}
	if merged[1].DriveOf() != drive.Terminal || *merged[1].View != (View{Split: 60}) {
		t.Fatalf("omp: %q %+v", merged[1].Drive, merged[1].View)
	}
	if merged[2].DriveOf() != drive.API || *merged[2].View != (View{Open: ViewTerminal, Split: 30}) {
		t.Fatalf("codex: %q %+v", merged[2].Drive, merged[2].View)
	}
	// A second file entry merges by field into the first's view.
	again, err := Merge(merged, []byte(`[{"name": "claude", "view": {"split": 70}}, {"name": "codex", "view": null}]`))
	if err != nil || *again[0].View != (View{Open: ViewBoth, Split: 70}) || again[2].View != nil || *merged[0].View != (View{Open: ViewBoth}) {
		t.Fatalf("merge by field: %+v %+v (base %+v) %v", again[0].View, again[2].View, merged[0].View, err)
	}
	for body, want := range map[string]string{
		`[{"name": "claude", "drive": "pty"}]`:             `drive "pty" is not a drive`,
		`[{"name": "claude", "view": {"open": "split"}}]`:  `view.open: view "split" is not a view`,
		`[{"name": "claude", "view": {"split": 95}}]`:      `view.split 95 is outside 10..90`,
		`[{"name": "claude", "view": {"layout": "both"}}]`: `unknown field "layout"`,
	} {
		list, err := Merge(Builtins(), []byte(body))
		if err == nil {
			_, err = NewRegistry(list)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", body, err, want)
		}
	}
}
