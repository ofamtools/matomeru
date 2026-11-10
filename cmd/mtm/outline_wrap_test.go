package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestOutlineWrapAlignsNestedTitle(t *testing.T) {
	t.Parallel()
	a := pinned(t)
	setting(t, a, settingTasksColumns, []string{})
	task := stored(t, a, "t2")
	task.ParentID = "t1"
	task.Title = "testing-wrap-title continuation tail"
	if err := a.work.PutTask(task); err != nil {
		t.Fatal(err)
	}
	press(t, a, "T")
	focusRow(t, a, outlineNode("t2"))
	if a.r.TopModal() != outlineModal {
		t.Fatalf("T opened %q, want the outline", a.r.TopModal())
	}
	// Use the app's registered modal renderer, with its real columns and
	// glyph cells. The project and parent task put this task at depth two.
	render := func(width int) []string {
		t.Helper()
		block := a.m.ModalViews[outlineModal].Render(a.r, a.r.Instances[outlineModal], width, 24)
		t.Logf("outline body at width %d:\n%s", width, block.Text)
		return strings.Split(ansi.Strip(block.Text), "\n")
	}
	lines := render(40)
	first := frameLine(lines, "testing-wrap-title")
	if first < 0 || first+1 >= len(lines) {
		t.Fatalf("missing wrapped title:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[first], "      [ ] testing-wrap-title") {
		t.Fatalf("nested status prefix missing: %q", lines[first])
	}
	if got := strings.TrimRight(lines[first+1], " "); got != "          continuation tail" {
		t.Fatalf("continuation %q, want ten spaces then continuation tail", got)
	}
	for _, tc := range []struct {
		line string
		text string
	}{
		{lines[first], "testing-wrap-title"},
		{lines[first+1], "continuation tail"},
	} {
		at := strings.Index(tc.line, tc.text)
		if at < 0 || ansi.StringWidth(tc.line[:at]) != 10 {
			t.Fatalf("%q is not at cell 10 in %q", tc.text, tc.line)
		}
	}
	if wide := strings.Join(render(80), "\n"); !strings.Contains(wide, "testing-wrap-title continuation tail") {
		t.Fatalf("wide outline lost title text:\n%s", wide)
	}
	setting(t, a, "tree.wrap", false)
	if clipped := strings.Join(render(40), "\n"); strings.Contains(clipped, "continuation tail") || !strings.Contains(clipped, "testing-wrap-title") {
		t.Fatalf("tree.wrap=false did not restore the clipped row:\n%s", clipped)
	}
	if got := stored(t, a, "t2"); got.Title != "testing-wrap-title continuation tail" || got.ParentID != "t1" {
		t.Fatalf("display wrapping changed the stored task: %+v", got)
	}
}
