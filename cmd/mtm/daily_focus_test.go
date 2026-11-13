package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// focusLabels is the focus list as focus-item list answers it.
func focusLabels(t *testing.T, a *App, args ...string) []string {
	t.Helper()
	var page struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, a, append([]string{"focus-item", "list"}, args...)...)), &page); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range page.Items {
		out = append(out, it.Label)
	}
	return out
}

// # opens the focus picker: enter focuses the highlighted task and stays
// open, a typed title that matches nothing focuses a new task, and
// focus-item list answers the three in order. = on a task row toggles its
// focus. A task done today stays in today's list; with the clock a day
// on, focus.carry unfinished keeps the unfinished ones and none shows none.
func TestFocusPickerAndDayChange(t *testing.T) {
	t.Parallel()
	a := open(t)
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	a.loc, a.clock = time.UTC, func() time.Time { return now }
	press(t, a, "#")
	ch := a.r.Chooser(focusPickerName)
	if ch == nil {
		t.Fatalf("# opened %q", a.r.TopModal())
	}
	rows := ch.Matches()
	if len(rows) < 2 {
		t.Fatalf("picker rows %v", rows)
	}
	first, second := rows[0].Label, rows[1].Label
	press(t, a, "enter")
	if a.r.Chooser(focusPickerName) == nil {
		t.Fatal("the picker closed after a pick")
	}
	if got := ch.Matches()[0].Label; got != focusGlyph+" "+first {
		t.Fatalf("first row after the pick %q, want %q marked", got, first)
	}
	press(t, a, "down", "enter")
	typeText(t, a, "brand new focus zq")
	press(t, a, "enter")
	if len(ch.Matches()) == 0 {
		t.Fatal("the new task is not in the picker")
	}
	press(t, a, "esc", "q")
	if m := a.r.TopModal(); m != "" {
		t.Fatalf("modal %q still open", m)
	}
	title := func(label string) string {
		_, rest, _ := strings.Cut(label, "] ")
		name, _, _ := strings.Cut(rest, " · ")
		return strings.TrimSuffix(name, " (unassociated)")
	}
	if got, want := focusLabels(t, a), []string{title(first), title(second), "brand new focus zq"}; !slices.Equal(got, want) {
		t.Fatalf("focus %q, want %q", got, want)
	}

	// + on a task row of the Work page toggles its focus.
	a.all = true
	if err := a.refreshTasks(); err != nil || len(a.tasks) == 0 {
		t.Fatalf("no tasks on Work: %v", err)
	}
	if err := a.taskList.Select(a.tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
	row := a.r.Instances[a.r.FocusID()]
	if row == nil || row.Noun != "task" {
		t.Fatalf("focus on %v", row)
	}
	listed := func() bool { _, ok, _ := a.focusAt(a.focusToday(), "task:"+row.ID); return ok }
	was := listed()
	press(t, a, "=")
	if listed() == was {
		t.Fatalf("+ left task %s focused=%v", row.ID, was)
	}
	press(t, a, "=")
	if listed() != was {
		t.Fatalf("+ twice changed task %s", row.ID)
	}

	// The first is done today: it stays; tomorrow it drops off.
	var focused struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(mustCLI(t, a, "task", "list", "--filter", "focus")), &focused); err != nil {
		t.Fatal(err)
	}
	if len(focused.Items) != 3 {
		t.Fatalf("task list --filter focus: %v", focused.Items)
	}
	mustCLI(t, a, "task", "done", focused.Items[0].ID)
	if got := focusLabels(t, a); len(got) != 3 {
		t.Fatalf("a focus finished today left today's list: %q", got)
	}
	now = now.Add(24 * time.Hour)
	if got, want := focusLabels(t, a), []string{title(second), "brand new focus zq"}; !slices.Equal(got, want) {
		t.Fatalf("carry unfinished: %q, want %q", got, want)
	}
	if err := a.r.Settings().Set(settingFocusCarry, "none"); err != nil {
		t.Fatal(err)
	}
	if got := focusLabels(t, a); len(got) != 0 {
		t.Fatalf("carry none: %q", got)
	}
}

// ctrl+a # in live view falls through to # (leader.fall-through): the
// focus picker opens over the live pane.
func TestFocusPickerFromLive(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	press(t, a, "enter")
	if a.r.CurrentMode().Name != "terminal" {
		t.Fatalf("not live: %s", a.r.CurrentMode().Name)
	}
	press(t, a, "ctrl+a", "#")
	if a.r.Chooser(focusPickerName) == nil {
		t.Fatalf("ctrl+a # opened %q", a.r.TopModal())
	}
}
