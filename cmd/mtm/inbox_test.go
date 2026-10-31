package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/work"
)

// inboxApp is the seeded app with a fixed inbox clock and a fake opener.
func inboxApp(t *testing.T) (*App, *fakeSystem, *int64) {
	t.Helper()
	a := open(t)
	a.inbox.after = nil // tests deliver inboxDue themselves
	clock := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC).UnixMilli()
	a.inbox.items.SetClock(func() int64 { return clock })
	a.loc = time.UTC
	sys := &fakeSystem{}
	a.inbox.sys = sys
	return a, sys, &clock
}

// cliJSON runs a CLI command and decodes its JSON answer into v.
func cliJSON(t *testing.T, a *App, v any, args ...string) {
	t.Helper()
	out := mustCLI(t, a, args...)
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("mtm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

type addAnswer struct {
	ID        string `json:"id"`
	New       bool   `json:"new"`
	Source    string `json:"source"`
	URL       string `json:"url"`
	Why       string `json:"why"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Read      bool   `json:"read"`
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Notice    string `json:"notice"`
}

// The contract outside tools push through: add prints the item with new
// set when a row was added; the same source and URL (or --key) refreshes
// that row instead, so a poll that runs again never duplicates.
func TestCLIActionItemAddIsIdempotent(t *testing.T) {
	t.Parallel()
	a, _, _ := inboxApp(t)
	var first, again, keyed, rekeyed addAnswer
	cliJSON(t, a, &first, "action-item", "add", "--source", "gh", "--url", "https://github.com/o/r/pull/7", "--why", "review requested", "--title", "Fix the store", "--json")
	if first.ID == "" || !first.New || first.Source != "gh" || first.State != "open" || first.Read {
		t.Fatalf("first add: %+v", first)
	}
	cliJSON(t, a, &again, "action-item", "add", "--source", "gh", "--url", "https://github.com/o/r/pull/7", "--why", "re-review requested")
	if again.ID != first.ID || again.New || again.Why != "re-review requested" || again.Title != "Fix the store" {
		t.Fatalf("repeat add: %+v", again)
	}
	cliJSON(t, a, &keyed, "action-item", "add", "--source", "ci", "--key", "build-1", "--url", "https://ci/run/1", "--why", "CI failed")
	cliJSON(t, a, &rekeyed, "action-item", "add", "--source", "ci", "--key", "build-1", "--url", "https://ci/run/2")
	if rekeyed.ID != keyed.ID || rekeyed.New || rekeyed.URL != "https://ci/run/2" {
		t.Fatalf("keyed add: %+v then %+v", keyed, rekeyed)
	}
	items, err := a.inbox.items.All()
	if err != nil || len(items) != 2 {
		t.Fatalf("items %d, %v", len(items), err)
	}
	var count actionitems.Counts
	cliJSON(t, a, &count, "action-item", "count")
	if count.Open != 2 || count.Unread != 2 {
		t.Fatalf("count %+v", count)
	}
}

// Record first: an owner that cannot be found is left out with a notice,
// and an item naming none is unassociated; a task brings its project.
func TestCLIActionItemAddRecordsWhateverItIsGiven(t *testing.T) {
	t.Parallel()
	a, _, _ := inboxApp(t)
	var lost, bare, onTask, byName addAnswer
	cliJSON(t, a, &lost, "action-item", "add", "--why", "look", "--project", "nope")
	if !lost.New || lost.ProjectID != "" || !strings.Contains(lost.Notice, "unknown project nope") {
		t.Fatalf("unknown project: %+v", lost)
	}
	cliJSON(t, a, &bare, "action-item", "add")
	if !bare.New || bare.Source != actionitems.ManualSource {
		t.Fatalf("bare: %+v", bare)
	}
	cliJSON(t, a, &onTask, "action-item", "add", "--task", "t1", "--why", "blocked")
	if onTask.TaskID != "t1" || onTask.ProjectID != "matomeru" {
		t.Fatalf("task: %+v", onTask)
	}
	cliJSON(t, a, &byName, "action-item", "add", "--project", "Ochazuke", "--why", "release")
	if byName.ProjectID != "ochazuke" {
		t.Fatalf("project by name: %+v", byName)
	}
	var rows []actionThing
	cliJSON(t, a, &rows, "action-item", "list", "--project", "matomeru")
	if len(rows) != 1 || rows[0].ID != onTask.ID {
		t.Fatalf("list --project: %+v", rows)
	}
}

// actionThing is an action item as its props (the standard verbs' answer).
type actionThing struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	Read         bool   `json:"read"`
	Archived     bool   `json:"archived"`
	SnoozedUntil int64  `json:"snoozed-until"`
	Project      string `json:"project"`
	Task         string `json:"task"`
	Pane         string `json:"pane"`
	Title        string `json:"title"`
}

// The item's verbs over the CLI: read, unread and toggle-read are set of
// read, snooze sets snoozed-until, archive and restore are standard,
// associate's old forms run set, the reverse relations too.
func TestCLIActionItemVerbs(t *testing.T) {
	t.Parallel()
	a, _, clock := inboxApp(t)
	var it addAnswer
	cliJSON(t, a, &it, "action-item", "add", "--source", "gh", "--url", "https://x/1", "--why", "review requested", "--title", "PR 1")
	var got actionThing
	cliJSON(t, a, &got, "action-item", "read", it.ID)
	if !got.Read {
		t.Fatalf("read: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "unread", it.ID)
	if got.Read {
		t.Fatalf("unread: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "snooze", it.ID, "--for", "2h")
	if got.State != "snoozed" || got.SnoozedUntil != *clock+2*3600*1000 {
		t.Fatalf("snooze: %+v", got)
	}
	var open []any
	cliJSON(t, a, &open, "action-item", "list")
	if len(open) != 0 {
		t.Fatalf("snoozed item listed as open: %v", open)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "restore", it.ID)
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "archive", it.ID)
	if got.State != "archived" || !got.Read {
		t.Fatalf("archive: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "restore", it.ID)
	if got.State != "open" {
		t.Fatalf("restore: %+v", got)
	}
	// Associate with a task, then the reverse from the task's side.
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "associate", it.ID, "--task", "t2")
	if got.Task != "t2" || got.Project != "matomeru" {
		t.Fatalf("associate: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "action-item", "associate", it.ID, "--scratch")
	if got.Task != "" || got.Project != "" {
		t.Fatalf("scratch: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "task", "associate", "--target", "t1", "--action-item", it.ID)
	if got.Task != "t1" || got.Project != "matomeru" {
		t.Fatalf("task.associate --action-item: %+v", got)
	}
	got = actionThing{}
	cliJSON(t, a, &got, "project", "associate", "--target", "ochazuke", "--action-item", it.ID)
	if got.Project != "ochazuke" || got.Task != "" {
		t.Fatalf("project.associate --action-item: %+v", got)
	}
	// Promote to a task under t1: a subtask; the item is archived and
	// points at it.
	var promoted struct {
		actionItemJSON
		Task work.Task `json:"task"`
	}
	cliJSON(t, a, &promoted, "action-item", "promote-task", it.ID, "--parent", "t1")
	if promoted.Task.ParentID != "t1" || promoted.Task.Title != "PR 1" || promoted.PromotedTaskID != promoted.Task.ID || promoted.State != "archived" {
		t.Fatalf("promote-task: %+v", promoted)
	}
	if _, _, err := cli(a, "", "action-item", "promote-todo", it.ID); err == nil {
		t.Fatal("promoted twice")
	}
	if _, _, err := cli(a, "", "action-item", "delete", it.ID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.inbox.items.Get(it.ID); err == nil {
		t.Fatal("delete kept the row")
	}
}

// seedInbox adds an unread item with a URL in matomeru and a read one
// with no owner.
func seedInbox(t *testing.T, a *App, clock *int64) (actionitems.Item, actionitems.Item) {
	t.Helper()
	pr, _, err := a.inbox.items.Add(actionitems.Spec{Source: "gh", URL: "https://github.com/o/r/pull/9", Title: "Lift the store", Why: "review requested", ProjectID: "matomeru"})
	if err != nil {
		t.Fatal(err)
	}
	*clock += 1000
	ping, _, err := a.inbox.items.Add(actionitems.Spec{Source: "slack", Why: "mentioned", Title: "standup notes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.inbox.items.SetRead(ping.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshInbox(); err != nil {
		t.Fatal(err)
	}
	return pr, ping
}

// I opens the merged page (Panes, titled Inbox) on its queue; the Items
// page's title row counts unread/open; the rows go unread first; u, x, X,
// enter, s, t, m and d d act on the row.
func TestInboxPageKeys(t *testing.T) {
	t.Parallel()
	a, sys, clock := inboxApp(t)
	pr, ping := seedInbox(t, a, clock)
	press(t, a, "I")
	if a.r.Page != panesPage || a.r.Instances[a.r.Focus] == nil || a.r.Instances[a.r.Focus].Parent != queueTreeID+"/list" {
		t.Fatalf("I: page %s focus %s", a.r.Page, a.r.Focus)
	}
	toPage(t, a, inboxPage)
	if a.r.Instances[a.r.Focus] == nil || a.r.Instances[a.r.Focus].Parent != inboxListID {
		t.Fatalf("Items: focus %s", a.r.Focus)
	}
	f := frameAt(a, 120)
	for _, want := range []string{"inbox 1/2", "● Lift the store", "· standup notes", "review requested", "matomeru", "unassociated"} {
		if !strings.Contains(f, want) {
			t.Fatalf("inbox lacks %q:\n%s", want, f)
		}
	}
	t.Logf("inbox, 120 columns:\n%s", f)
	if sel, _ := a.inbox.list.Selected(); sel.ID != pr.ID {
		t.Fatalf("first row %s, want the unread %s", sel.ID, pr.ID)
	}

	press(t, a, "u")
	if it, _ := a.inbox.items.Get(pr.ID); !it.Read() {
		t.Fatal("u did not mark read")
	}
	if !strings.Contains(frameAt(a, 120), "inbox 0/2") {
		t.Fatal("title count did not follow")
	}
	press(t, a, "u")
	if it, _ := a.inbox.items.Get(pr.ID); it.Read() {
		t.Fatal("u again did not mark unread")
	}

	settle(a, key(t, a, "enter"))
	if len(sys.opened) != 1 || sys.opened[0] != pr.URL {
		t.Fatalf("enter opened %v", sys.opened)
	}
	if it, _ := a.inbox.items.Get(pr.ID); !it.Read() {
		t.Fatal("open did not mark read")
	}

	if err := a.inbox.list.Select(pr.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "x")
	if it, _ := a.inbox.items.Get(pr.ID); it.State(*clock) != actionitems.StateArchived {
		t.Fatal("x did not archive")
	}
	if err := a.inbox.list.Select(pr.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "X")
	if it, _ := a.inbox.items.Get(pr.ID); it.State(*clock) != actionitems.StateOpen {
		t.Fatal("X did not restore")
	}

	// s asks how long; enter takes the first choice, an hour.
	if err := a.inbox.list.Select(ping.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "s")
	if a.r.TopModal() == "" {
		t.Fatal("s did not ask")
	}
	press(t, a, "enter")
	if it, _ := a.inbox.items.Get(ping.ID); it.State(*clock) != actionitems.StateSnoozed || it.SnoozedUntil != *clock+3600*1000 {
		t.Fatalf("snooze: %+v", it)
	}
	// The snooze ends: the timer's reload brings the item back.
	*clock += 2 * 3600 * 1000
	a.Update(inboxDue{})
	if !strings.Contains(frameAt(a, 120), "· standup notes") {
		t.Fatalf("woken item not back:\n%s", frameAt(a, 120))
	}

	// m moves the item to a project with the chooser.
	if err := a.inbox.list.Select(ping.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	if a.r.TopModal() == "" {
		t.Fatal("m did not ask")
	}
	typeText(t, a, "ochazuke")
	press(t, a, "enter")
	if it, _ := a.inbox.items.Get(ping.ID); it.ProjectID != "ochazuke" {
		t.Fatalf("m: %+v", it)
	}

	// t promotes to a todo in the item's project.
	if err := a.inbox.list.Select(pr.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "t")
	it, _ := a.inbox.items.Get(pr.ID)
	task, err := a.work.Task(it.PromotedTodoID)
	if err != nil || task.ProjectID != "matomeru" || task.ParentID != "" || task.Title != "Lift the store" || it.State(*clock) != actionitems.StateArchived {
		t.Fatalf("t: item %+v task %+v %v", it, task, err)
	}

	// d d asks; y deletes.
	if err := a.inbox.list.Select(ping.ID); err != nil {
		t.Fatal(err)
	}
	press(t, a, "d", "d")
	if a.r.TopModal() != "confirm" {
		t.Fatalf("d d opened %q", a.r.TopModal())
	}
	press(t, a, "y")
	if _, err := a.inbox.items.Get(ping.ID); err == nil {
		t.Fatal("d d y kept the item")
	}
}

// An item another process adds shows on the timer's reload, with the
// title row's count; nothing open shows no count.
func TestInboxReloadsWhatOthersAdd(t *testing.T) {
	t.Parallel()
	a, _, _ := inboxApp(t)
	if strings.Contains(frameAt(a, 120), "inbox ") {
		t.Fatal("count with nothing open")
	}
	other, err := actionitems.Open(a.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Add(actionitems.Spec{Source: "gh", Why: "CI failed", Title: "build 12"}); err != nil {
		t.Fatal(err)
	}
	a.Update(inboxDue{})
	toPage(t, a, inboxPage)
	f := frameAt(a, 120)
	if !strings.Contains(f, "inbox 1/1") || !strings.Contains(f, "● build 12") {
		t.Fatalf("reload:\n%s", f)
	}
}

func TestSnoozeParsing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{"90m": 90 * time.Minute, "4h": 4 * time.Hour, "2d": 48 * time.Hour, "1w": 7 * 24 * time.Hour} {
		if d, err := parseSpan(in); err != nil || d != want {
			t.Fatalf("%s: %v %v", in, d, err)
		}
	}
	if _, err := parseSpan("soon"); err == nil {
		t.Fatal("soon parsed")
	}
	for in, want := range map[string]time.Time{
		"tomorrow":             time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC),
		"2026-11-02":           time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
		"2026-11-02 14:30":     time.Date(2026, 11, 2, 14, 30, 0, 0, time.UTC),
		"2026-11-02T14:30:00Z": time.Date(2026, 11, 2, 14, 30, 0, 0, time.UTC),
	} {
		if at, err := parseUntil(in, now); err != nil || !at.Equal(want) {
			t.Fatalf("%s: %v %v", in, at, err)
		}
	}
	rows := snoozeChoices(now)
	if len(rows) != 4 || rows[3].Data != time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339) {
		t.Fatalf("choices %+v", rows)
	}
}
