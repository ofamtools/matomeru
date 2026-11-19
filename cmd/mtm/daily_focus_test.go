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
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(10 * time.Hour) // the store stamps done with the real clock: the fake day is the real UTC day
	a.loc, a.clock = time.UTC, func() time.Time { return now }
	if err := a.r.Settings().Set(settingFocusSelector, focusSelectorPicker); err != nil {
		t.Fatal(err)
	}
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
// focus selector (focus.selector outline, the default) opens over the
// live pane, the task outline in the focus-set mode.
func TestFocusPickerFromLive(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	press(t, a, "enter")
	if a.r.CurrentMode().Name != "terminal" {
		t.Fatalf("not live: %s", a.r.CurrentMode().Name)
	}
	press(t, a, "ctrl+a", "#")
	if a.r.TopModal() != outlineModal || a.r.CurrentMode().Name != focusSetMode {
		t.Fatalf("ctrl+a # opened %q in %s", a.r.TopModal(), a.r.CurrentMode().Name)
	}
}

// # opens the task outline in the focus-set mode, nested as O shows it:
// enter on a task focuses it (the row gains the focus mark) and again
// unfocuses it; the outline's editing keys do nothing there; esc closes.
func TestFocusSelectorOutline(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "#")
	if a.r.TopModal() != outlineModal || a.r.CurrentMode().Name != focusSetMode {
		t.Fatalf("# opened %q in %s", a.r.TopModal(), a.r.CurrentMode().Name)
	}
	if f := frame(a); !strings.Contains(f, focusSelectorTitle) {
		t.Fatalf("title:\n%s", f)
	}
	// k walks up from "no project" (the all view's last branch) into the
	// tasks above it: the outline's motions answer in the mode.
	for i := 0; i < 20 && a.r.Instances[a.r.FocusID()].Noun != outlineTaskNoun; i++ {
		press(t, a, "k")
	}
	row := a.r.FocusID()
	if a.r.Instances[row].Noun != outlineTaskNoun {
		t.Fatalf("no task row reached: %s", row)
	}
	thing := "task:" + taskOf(row)
	listed := func() bool { _, ok, _ := a.focusAt(a.focusToday(), thing); return ok }
	press(t, a, "enter")
	if !listed() {
		t.Fatalf("enter did not focus %s", thing)
	}
	marked := func() bool {
		n, _ := a.outline.tree.Selected()
		return strings.Contains(strings.Join(n.Cells, " "), focusGlyph)
	}
	if !marked() {
		t.Fatal("no focus mark on the focused task")
	}
	setting(t, a, settingTasksFocusGlyph, false)
	if marked() {
		t.Fatal("tasks.focus-glyph off kept the mark")
	}
	setting(t, a, settingTasksFocusGlyph, true)
	before, _ := json.Marshal(tasksOf(t, a, ""))
	all := func() string {
		b, _ := json.Marshal(tasksOf(t, a, ""))
		return string(b)
	}
	for _, key := range []string{"x", "s", "i", "o", "O", "a", "n", "tab", "shift+tab", "J", "K", "d", "d", "y", "y", "p", "A", "X", "m", "C", "R", "b", "F", "v", "V", "d"} {
		press(t, a, key)
		if a.r.TopModal() != outlineModal || a.r.CurrentMode().Name != focusSetMode {
			t.Fatalf("%s left %q in %s", key, a.r.TopModal(), a.r.CurrentMode().Name)
		}
	}
	if got := all(); got != string(before) {
		t.Fatalf("an editing key changed a task:\n%s\n%s", before, got)
	}
	press(t, a, "enter")
	if listed() {
		t.Fatalf("enter again did not unfocus %s", thing)
	}
	press(t, a, "esc")
	if m := a.r.TopModal(); m != "" {
		t.Fatalf("esc left %q open", m)
	}
}
