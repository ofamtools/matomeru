package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
)

// The inbox: action items, everything that needs the user, from any
// source, on one page. Outside tools push items in with
//
//	mtm action-item add --source gh --url URL --why "review requested" [--title T] [--key K] [--project P|--dir D|--task T|--pane W]
//
// which is idempotent on the source and key (else the source and URL), so
// a poll that runs again refreshes its row instead of adding one. The
// result is the item as JSON with "new" set when a row was added. Nothing
// is required: an item that names no owner is unassociated.
//
// The page lists every item: open unread first, then open read, snoozed,
// archived. The title row counts the open items and the unread ones among
// them (inbox U/O). The rows reload when the page opens and every
// inboxEvery, so items another process adds show up, and a snooze that
// ends brings its item back.

const (
	actionItemNoun = "action-item"
	inboxPage      = "inbox"
	inboxTitle     = "Items"
	inboxListID    = "inbox/list"
	inboxDetailID  = "inbox/detail"
	inboxEvery     = 5 * time.Second
)

var inboxColumns = []ochazuke.Column{
	{Title: "item", Min: 16},
	{Title: "why", Priority: 2},
	{Title: "source", Priority: 3},
	{Title: "for", Priority: 1},
	{Title: "when", Align: ochazuke.AlignRight, Priority: 4},
}

// inboxState is the page's state: the store, its list and detail, the
// opener and the counts the title row shows.
type inboxState struct {
	items  *actionitems.Store
	list   *ochazuke.List
	detail *linkedDetail
	sys    systemActions
	counts actionitems.Counts
	sig    string             // what the rows were last built from; a reload that finds it unchanged sets nothing
	all    []actionitems.Item // the items the rows were built from; the Panes page's top list reads them
	// after schedules the reload timer (tea.Tick); tests set it nil and
	// deliver inboxDue themselves.
	after func(time.Duration, tea.Msg) tea.Cmd
}

// inboxDue is the inbox's reload timer.
type inboxDue struct{}

// inboxIDParam is the id parameter every one-item verb takes.
var inboxIDParam = ochazuke.Parameter{Type: "string", Noun: actionItemNoun, Source: actionItemNoun + ".list", Description: "The item id; omitted, the selected row"}

// inboxOwnerParams are the association parameters add and associate take.
var inboxOwnerParams = map[string]ochazuke.Parameter{
	"project": {Type: "string", Noun: "project", Source: "projects.list", Free: true, Description: "The project id or name; one not found is left out with a notice"},
	"dir":     {Type: "string", Description: "A directory: the project holding it"},
	"task":    {Type: "string", Noun: "task", Description: "The task id (its project follows)"},
	"pane":    {Type: "string", Noun: "pane", Description: "The pane's window id (its owner follows when no project or task is named)"},
	"session": {Type: "string", Description: "The pane's session"},
}

func (a *App) defineInbox() {
	a.inbox.sys = osSystem{}
	a.inbox.after = func(d time.Duration, msg tea.Msg) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return msg })
	}
	n := actionItemNoun
	// Props and the store first: the standard verbs (get, list, create,
	// set, delete, watch, move, duplicate, archive, restore) come from them
	// (action_item_store.go); show, read, unread and toggle-read are
	// aliases; snooze and associate run set; the rest are mtm's own.
	a.r.Define(n).Props(actionItemProps()...).Store(a.actionItemStore()).Columns(inboxColumns...).
		Verb("add", a.addActionItem, "Record an action item; the same source and --key (else --url) refreshes its row instead of adding one. Prints the item; new is true when a row was added").
		Verb("count", a.countActionItems, "Count the open action items and the unread ones among them").Fetch().
		Verb("open", a.openActionItem, "Open the item's URL in the browser and mark it read").Positional("id").
		Verb("snooze", a.snoozeActionItem, "Hide the item until --until (a time) or for --for (90m, 4h, 2d, 1w), the standard set of snoozed-until; omitted, a chooser asks").Positional("id").Many().
		Verb("promote-todo", a.promoteTodo, "Promote the item to a todo: a top-level task of its project (else the pin, else unassociated); the item is archived and points at it").Positional("id").
		Verb("promote-task", a.promoteTask, "Promote the item to a task: a subtask of --parent, else of the item's task, else a top-level task like promote-todo").Positional("id").
		Verb("associate", a.associateActionItem, "Choose the project to move the item to (omit the owner); --project, --task, --dir, --pane and --scratch are the deprecated forms of set").Positional("id").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the item to (most recent first, unassociated last); the choice runs action-item.move").Many().
		Mode("normal").
		Bind("enter", ochazuke.Do(n+".open")).
		Bind("u", ochazuke.Do(n+".toggle-read").Hinted("read/unread")).
		Bind("x", ochazuke.Do(n+".archive")).
		Bind("X", ochazuke.Do(n+".restore")).
		Bind("s", ochazuke.Do(n+".snooze")).
		Bind("t", ochazuke.Do(n+".promote-todo").Hinted("to todo")).
		Bind("c", ochazuke.Do(n+".promote-task").Hinted("to task")).
		Bind("m", ochazuke.Do(n+".choose-move")).
		Bind("d d", ochazuke.Do(n+".delete"))
	// show is get under its old name; read, unread and toggle-read are the
	// standard set of read (read shares the prop's name as its set).
	for _, alias := range []ochazuke.ActionAlias{
		{Alias: n + ".show", Action: n + ".get", Note: "the standard get: every prop of the item"},
		{Alias: n + ".read", Action: n + ".set", Args: ochazuke.Arguments{"prop": "read", "value": true}, Note: "the standard set of read"},
		{Alias: n + ".unread", Action: n + ".set", Args: ochazuke.Arguments{"prop": "read", "value": false}, Note: "the standard set of read, off"},
		{Alias: n + ".toggle-read", Action: n + ".set", Args: ochazuke.Arguments{"prop": "read", "toggle": true}, Note: "the standard set of read, flipped"},
	} {
		if err := a.r.AddActionAlias(alias); err != nil {
			panic(err)
		}
	}
	// I opens the inbox from anywhere: the Panes page, titled Inbox, whose
	// top list is the open queue (panes_sections.go). A row to the
	// framework's page.open; page.open inbox still opens every item.
	a.r.Define("app").Mode("normal").Bind("I", ochazuke.Do("ochazuke.page.open", ochazuke.Arguments{"id": panesPage}).Hinted("inbox"))

	add := map[string]ochazuke.Parameter{
		"source": {Type: "string", Description: "The tool or service: gh, slack, ci, agent, … (default manual)"},
		"url":    {Type: "string", Description: "What open opens; with the source, the de-duplication key when --key is not given"},
		"key":    {Type: "string", Description: "The de-duplication key, unique per source"},
		"title":  {Type: "string", Description: "The thing: a PR title, a message's first line"},
		"why":    {Type: "string", Description: "What it wants of you: review requested, CI failed, mentioned"},
		"text":   {Type: "string", Description: "Details"},
		"kind":   {Type: "string", Description: "The item's type: review-request, mention, ci-failed, …"},
		"extra":  {Type: "object", Description: "Any JSON the source wants kept with the item"},
		"reopen": {Type: "boolean", Description: "Make an archived or snoozed duplicate open and unread again"},
	}
	for k, v := range inboxOwnerParams {
		add[k] = v
	}
	a.parameters(n+".add", add)
	a.parameters(n+".open", map[string]ochazuke.Parameter{"id": inboxIDParam})
	a.parameters(n+".snooze", map[string]ochazuke.Parameter{"id": inboxIDParam,
		"until": {Type: "string", Description: "When it comes back: RFC 3339, 2006-01-02 15:04, 2006-01-02 or tomorrow"},
		"for":   {Type: "string", Description: "How long: 90m, 4h, 2d, 1w"}})
	promote := map[string]ochazuke.Parameter{"id": inboxIDParam,
		"title":   {Type: "string", Description: "The task's title (default the item's)"},
		"project": {Type: "string", Noun: "project", Source: "projects.list", Free: true, Description: "The project to add it to (id or name)"}}
	a.parameters(n+".promote-todo", promote)
	promoteTask := map[string]ochazuke.Parameter{"parent": {Type: "string", Noun: "task", Description: "The task to add it under"}}
	for k, v := range promote {
		promoteTask[k] = v
	}
	a.parameters(n+".promote-task", promoteTask)
	associate := map[string]ochazuke.Parameter{"id": inboxIDParam, "scratch": {Type: "boolean", Description: "Deprecated: action-item move ID --to \"\" (unassociate it)"}}
	for k, v := range inboxOwnerParams {
		v.Description = "Deprecated (action-item set): " + v.Description
		associate[k] = v
	}
	a.parameters(n+".associate", associate)
	a.ownedBy(n, inboxListID)
}

// inboxRelateParam is the action item a task, pane or project verb relates.
var inboxRelateParam = ochazuke.Parameter{Type: "string", Noun: actionItemNoun, Source: actionItemNoun + ".list", Description: "An action item to associate with this one"}

func (a *App) addInboxPage() error {
	a.r.Instance(inboxPage, ochazuke.Kind("page"))
	var err error
	if a.inbox.list, err = a.r.AddList(inboxListID, nil, ochazuke.Parent(inboxPage), ochazuke.Columns(inboxColumns...)); err != nil {
		return err
	}
	if a.inbox.detail, err = a.addLinkedDetail(inboxDetailID, inboxPage, func() *ochazuke.List { return a.inbox.list }, a.inboxSubject); err != nil {
		return err
	}
	// Items: every action item in every state. The Panes page, titled
	// Inbox, holds the open queue; this page stays for the archived and
	// snoozed items and the whole list.
	if _, err = a.r.AddPage(inboxPage, inboxTitle, a.renderInbox, ochazuke.Focus(inboxListID)); err != nil {
		return err
	}
	return a.refreshInbox()
}

// inboxTick starts the reload timer.
func (a *App) inboxTick() tea.Cmd {
	if a.inbox == nil || a.inbox.after == nil {
		return nil
	}
	return a.inbox.after(inboxEvery, inboxDue{})
}

// inboxUpdate reloads the rows on the timer and when the page opens.
func (a *App) inboxUpdate(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(inboxDue); !ok {
		return nil
	}
	if err := a.reloadInbox(); err != nil {
		a.r.UI().Error(err.Error())
	}
	return a.inboxTick()
}

// reloadInbox rebuilds the rows only when the items changed (or a snooze
// ended) since the last build.
func (a *App) reloadInbox() error {
	items, err := a.inbox.items.All()
	if err != nil {
		return err
	}
	if inboxSig(items, a.inbox.items.Now()) == a.inbox.sig {
		return nil
	}
	return a.setInboxRows(items)
}

func inboxSig(items []actionitems.Item, now int64) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.ID + ":" + strconv.FormatInt(it.Updated, 10) + ":" + it.State(now) + ";")
	}
	return b.String()
}

// refreshInbox reloads the rows and the title row's counts.
func (a *App) refreshInbox() error {
	if a.inbox == nil || a.inbox.list == nil {
		return nil
	}
	items, err := a.inbox.items.All()
	if err != nil {
		return err
	}
	return a.setInboxRows(items)
}

func (a *App) setInboxRows(items []actionitems.Item) error {
	now := a.inbox.items.Now()
	a.inbox.sig = inboxSig(items, now)
	rows := make([]ochazuke.ListItem, 0, len(items))
	var c actionitems.Counts
	for _, it := range items {
		if it.State(now) == actionitems.StateOpen {
			c.Open++
			if !it.Read() {
				c.Unread++
			}
		}
		row := a.inboxRow(it, now)
		rows = append(rows, ochazuke.ListItem{ID: row.ID, Noun: actionItemNoun, Label: row.Label, Cells: row.Cells, Data: it})
	}
	a.inbox.counts = c
	a.inbox.all = items
	a.refreshTitle()
	if err := a.inbox.list.SetItems(rows); err != nil {
		return err
	}
	// The Panes page's top list is these items (panes_sections.go).
	return a.rebuildPanes()
}

// inboxGlyph marks a row's state: ● unread, · read, z snoozed, ✓ archived.
func inboxGlyph(it actionitems.Item, now int64) string {
	switch it.State(now) {
	case actionitems.StateArchived:
		return "✓"
	case actionitems.StateSnoozed:
		return "z"
	}
	if it.Read() {
		return "·"
	}
	return "●"
}

func (a *App) inboxRow(it actionitems.Item, now int64) ocli.Row {
	return ocli.Row{Noun: actionItemNoun, ID: it.ID, Label: it.Label(), Data: itemView(it, now), Cells: []string{
		inboxGlyph(it, now) + " " + it.Label(), it.Why, it.Source, a.inboxOwner(it), a.inboxWhen(it, now)}}
}

// inboxOwner is what the item is for: its task, else its project, else its
// pane, else unassociated.
func (a *App) inboxOwner(it actionitems.Item) string {
	if it.TaskID != "" {
		if t, err := a.work.Task(it.TaskID); err == nil {
			return a.ownerLabel(t.ProjectID) + " › " + t.Title
		}
		return "task " + it.TaskID
	}
	if it.ProjectID != "" {
		return a.ownerLabel(it.ProjectID)
	}
	if it.Window != "" {
		return "pane " + it.Window
	}
	return "unassociated"
}

// inboxWhen is the row's time: when a snooze ends, else how long ago the
// item arrived.
func (a *App) inboxWhen(it actionitems.Item, now int64) string {
	if it.State(now) == actionitems.StateSnoozed {
		return "until " + time.UnixMilli(it.SnoozedUntil).In(a.location()).Format("Jan 2 15:04")
	}
	return ago(time.Duration(now-it.Created) * time.Millisecond)
}

// ago is a duration as the list shows it: now, 5m, 3h, 2d.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// inboxCountLabel is the title row's count: "inbox U/O", "" with nothing open.
func (a *App) inboxCountLabel() string {
	if a.inbox == nil || a.inbox.counts.Open == 0 {
		return ""
	}
	return fmt.Sprintf("inbox %d/%d", a.inbox.counts.Unread, a.inbox.counts.Open)
}

func (a *App) renderInbox(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	return a.withLinkedDetail(a.inbox.detail, width, height, func(w, h int) ochazuke.Block {
		s := a.m.Styles()
		c := a.inbox.counts
		head := fmt.Sprintf("inbox · %d unread · %d open · %d in all", c.Unread, c.Open, a.inbox.list.Len())
		lines := []string{s.Muted.Render(ansi.Truncate(head, w, "…"))}
		body := max(1, h-1)
		if a.inbox.list.Len() == 0 {
			lines = append(lines, a.fill("Nothing needs you. Tools add items with mtm action-item add --source S --url U --why W.", body, w))
		} else {
			lines = append(lines, a.view(a.inbox.list, w, body))
		}
		return ochazuke.Text(strings.Join(lines, "\n"))
	})
}

// inboxSubject is the detail's item (its id), text and links.
func (a *App) inboxSubject() (string, string, []link) {
	item, ok := a.inbox.list.Selected()
	if !ok {
		return "", "", nil
	}
	it, ok := item.Data.(actionitems.Item)
	if !ok {
		return "", "", nil
	}
	var links []link
	if l, ok := a.projectLink(cmp.Or(it.ProjectID, a.taskProject(it.TaskID))); ok {
		links = append(links, l)
	}
	if l, ok := a.paneLink(it.Window); ok {
		links = append(links, l)
	}
	return it.ID, a.inboxFacts(it), links
}

func (a *App) taskProject(id string) string {
	if id == "" {
		return ""
	}
	t, err := a.work.Task(id)
	if err != nil {
		return ""
	}
	return t.ProjectID
}

func (a *App) inboxFacts(it actionitems.Item) string {
	now := a.inbox.items.Now()
	state := it.State(now)
	if state == actionitems.StateOpen {
		state += map[bool]string{true: ", read", false: ", unread"}[it.Read()]
	}
	snoozed := ""
	if it.SnoozedUntil > 0 {
		snoozed = stamp(it.SnoozedUntil)
	}
	promoted := ""
	if id := it.Promoted(); id != "" {
		promoted = id
		if t, err := a.work.Task(id); err == nil {
			promoted = t.Title + " (" + a.ownerLabel(t.ProjectID) + ")"
		}
	}
	text := it.Label() + "\n\n" + facts(
		"why", it.Why,
		"state", state,
		"source", it.Source,
		"kind", it.Kind,
		"url", it.URL,
		"for", a.inboxOwner(it),
		"pane", strings.TrimSpace(it.Session+" "+it.Window),
		"arrived", stamp(it.Created),
		"updated", stamp(it.Updated),
		"read", stamp(it.ReadAt),
		"snoozed", snoozed,
		"archived", stamp(it.ArchivedAt),
		"promoted", promoted,
		"key", it.Key,
	)
	if it.Text != "" {
		text += "\n\n" + it.Text
	}
	return text
}

// actionItemAt is the item a verb acts on: --id, else the targeted row.
func (a *App) actionItemAt(ctx ochazuke.Ctx) (actionitems.Item, error) {
	id := ochazuke.StringArg(ctx.Args, "id", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == actionItemNoun {
		// The row's item: the Inbox page's rows are its id; a row of the
		// Panes page's top list may hold its pane's row id instead.
		id = ctx.Instance.ID
		if it, ok := ctx.Instance.Data.(actionitems.Item); ok {
			id = it.ID
		}
	}
	if id == "" {
		return actionitems.Item{}, errors.New("specify an action item id or select an inbox row")
	}
	return a.inbox.items.Get(id)
}

// inboxChanged reloads the rows after a change and answers the item.
func (a *App) inboxChanged(it actionitems.Item, err error) ochazuke.Result {
	if err == nil {
		err = a.refreshInbox()
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(itemView(it, a.inbox.items.Now()))
}

// actionOwner resolves the association arguments. Record first: an owner
// that cannot be found is left out with a notice, never an error.
func (a *App) actionOwner(args ochazuke.Arguments) (actionitems.Owner, string, error) {
	var o actionitems.Owner
	var notices []string
	if task := ochazuke.StringArg(args, "task", ""); task != "" {
		t, err := a.work.Task(task)
		switch {
		case err == nil:
			o.TaskID, o.ProjectID = t.ID, t.ProjectID
		case isNotFound(err):
			notices = append(notices, "unknown task "+task)
		default:
			return o, "", err
		}
	}
	if project := ochazuke.StringArg(args, "project", ""); project != "" && o.ProjectID == "" && o.TaskID == "" {
		if id, ok := a.projectNamed(project); ok {
			o.ProjectID = id
		} else {
			notices = append(notices, "unknown project "+project)
		}
	}
	if dir := ochazuke.StringArg(args, "dir", ""); dir != "" && o.ProjectID == "" && o.TaskID == "" {
		if id := a.pathProject(dir); id != "" {
			o.ProjectID = id
		} else {
			notices = append(notices, "no project holds "+dir)
		}
	}
	if pane := ochazuke.StringArg(args, "pane", ""); pane != "" {
		o.Window, o.Session = pane, ochazuke.StringArg(args, "session", "")
		if o.ProjectID == "" && o.TaskID == "" {
			owner, err := a.work.PaneOwner(pane, "")
			if err == nil {
				o.ProjectID, o.TaskID = owner.ProjectID, owner.TaskID
			} else if !isNotFound(err) {
				return o, "", err
			}
		}
	}
	notice := ""
	if len(notices) > 0 {
		notice = strings.Join(notices, "; ") + "; recorded without it"
	}
	return o, notice, nil
}

// projectNamed is a project by id, else by name (ignoring case).
func (a *App) projectNamed(s string) (string, bool) {
	if _, ok := a.project(s); ok {
		return s, true
	}
	for _, p := range a.projects {
		if strings.EqualFold(projectName(p), s) || strings.EqualFold(p.Name, s) {
			return p.ID, true
		}
	}
	return "", false
}

// addedItem is action-item.add's answer: the item and whether it is new.
type addedItem struct {
	actionItemJSON
	New    bool   `json:"new"`
	Notice string `json:"notice,omitempty"`
}

func (a *App) addActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	o, notice, err := a.actionOwner(ctx.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	arg := func(k string) string { return ochazuke.StringArg(ctx.Args, k, "") }
	reopen, _ := ctx.Args["reopen"].(bool)
	extra, _ := ctx.Args["extra"].(map[string]any)
	it, created, err := a.inbox.items.Add(actionitems.Spec{Source: arg("source"), URL: arg("url"), Key: arg("key"), Title: arg("title"), Why: arg("why"),
		Text: arg("text"), Kind: arg("kind"), ProjectID: o.ProjectID, TaskID: o.TaskID, Window: o.Window, Session: o.Session, Extra: extra, Reopen: reopen})
	if err == nil {
		err = a.refreshInbox()
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	if notice != "" {
		ctx.Notify(notice)
	}
	return ochazuke.Done(addedItem{actionItemJSON: itemView(it, a.inbox.items.Now()), New: created, Notice: notice})
}

func (a *App) countActionItems(ochazuke.Ctx) ochazuke.Result {
	c, err := a.inbox.items.Counts()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(c)
}

func (a *App) openActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if it, err = a.inbox.items.SetRead(it.ID, true); err == nil {
		err = a.refreshInbox()
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	if p, ok := a.listedPane(it.Window); ok && it.URL == "" {
		// A pane's signal opens its pane: attached live on Panes.
		return a.openItemPane(ctx, p)
	}
	if it.URL == "" {
		return ochazuke.Failed(fmt.Errorf("%s has no URL to open; it is marked read", it.Label()))
	}
	sys, url := a.inbox.sys, it.URL
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return url, sys.Open(c, url)
	})
}

// snoozeChoices are the snooze chooser's rows: a label and its end.
func snoozeChoices(now time.Time) []ochazuke.ChooserRow {
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, t.Location()) }
	monday := day(now.AddDate(0, 0, 1))
	for monday.Weekday() != time.Monday {
		monday = monday.AddDate(0, 0, 1)
	}
	rows := []struct {
		label string
		at    time.Time
	}{
		{"1 hour", now.Add(time.Hour)},
		{"4 hours", now.Add(4 * time.Hour)},
		{"tomorrow 9:00", day(now.AddDate(0, 0, 1))},
		{"next week (Monday 9:00)", monday},
	}
	out := make([]ochazuke.ChooserRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ochazuke.ChooserRow{ID: r.label, Label: r.label, Data: r.at.Format(time.RFC3339)})
	}
	return out
}

func (a *App) snoozeActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	until, _ := ctx.Args["until"].(string)
	span, _ := ctx.Args["for"].(string)
	if until != "" || span != "" || a.cli {
		return ctx.Each(a.snoozeOne)
	}
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	now := time.UnixMilli(a.inbox.items.Now()).In(a.location())
	title := "Snooze " + it.Label() + " until"
	_, byID := ctx.Args["id"]
	many := !byID && len(ctx.Subjects) > 1
	if many {
		title = fmt.Sprintf("Snooze %d items until", len(ctx.Subjects))
	}
	_, err = a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  "snooze-chooser",
		Title: title,
		Rows:  snoozeChoices(now),
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			q := ochazuke.Request{Noun: actionItemNoun, Verb: "snooze", Args: ochazuke.Arguments{"id": it.ID, "until": c.Data.(string)}}
			if many {
				// One chooser over a visual selection: its time for every item.
				q.Target, q.Subjects, q.Args = ctx.Instance.ID, ctx.SubjectIDs(), ochazuke.Arguments{"until": c.Data.(string)}
			}
			return resultOf(a.r.Execute(q))
		},
	})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// snoozeOne is action-item.snooze on one item, --for or --until given.
func (a *App) snoozeOne(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	now := time.UnixMilli(a.inbox.items.Now()).In(a.location())
	until, hasUntil := ctx.Args["until"].(string)
	span, hasFor := ctx.Args["for"].(string)
	switch {
	case hasFor && span != "":
		d, err := parseSpan(span)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return a.snoozeTo(it.ID, now.Add(d).UnixMilli())
	case hasUntil && until != "":
		at, err := parseUntil(until, now)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return a.snoozeTo(it.ID, at.UnixMilli())
	}
	return ochazuke.Failed(errors.New("snooze needs --for (90m, 4h, 2d, 1w) or --until (a time)"))
}

// snoozeTo is a snooze's write: the standard set of snoozed-until.
func (a *App) snoozeTo(id string, until int64) ochazuke.Result {
	return resultOf(a.r.Execute(ochazuke.Request{Noun: actionItemNoun, Verb: "set", Args: ochazuke.Arguments{"id": id, "prop": "snoozed-until", "value": until}}))
}

// parseSpan reads a snooze length: a Go duration, or a count of days (d)
// or weeks (w).
func parseSpan(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.ParseFloat(n, 64)
			if err != nil || v <= 0 {
				return 0, fmt.Errorf("--for %q: want 90m, 4h, 2d or 1w", s)
			}
			return time.Duration(v * float64(unit)), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--for %q: want 90m, 4h, 2d or 1w", s)
	}
	return d, nil
}

// parseUntil reads a snooze end: RFC 3339, "2006-01-02 15:04", a date (9:00
// that day) or "tomorrow" (9:00), in now's zone.
func parseUntil(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "tomorrow") {
		t := now.AddDate(0, 0, 1)
		return time.Date(t.Year(), t.Month(), t.Day(), 9, 0, 0, 0, now.Location()), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, now.Location()); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t.Add(9 * time.Hour), nil
	}
	return time.Time{}, fmt.Errorf("--until %q: want RFC 3339, 2006-01-02 15:04, 2006-01-02 or tomorrow", s)
}

func (a *App) promoteTodo(ctx ochazuke.Ctx) ochazuke.Result { return a.promoteActionItem(ctx, "todo") }
func (a *App) promoteTask(ctx ochazuke.Ctx) ochazuke.Result { return a.promoteActionItem(ctx, "task") }

// promoteActionItem adds a task for the item and archives the item, which
// then points at it. A todo is a top-level task (the outline's todos); a
// task is a subtask of --parent or the item's task when there is one.
// With no project anywhere the task is unassociated: never refused.
func (a *App) promoteActionItem(ctx ochazuke.Ctx, to string) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if id := it.Promoted(); id != "" {
		return ochazuke.Failed(fmt.Errorf("%s was already promoted (task %s)", it.Label(), id))
	}
	title := cmp.Or(ochazuke.StringArg(ctx.Args, "title", ""), it.Label())
	parent := ""
	if to == "task" {
		parent = cmp.Or(ochazuke.StringArg(ctx.Args, "parent", ""), it.TaskID)
	}
	project := ""
	if p := ochazuke.StringArg(ctx.Args, "project", ""); p != "" {
		project, _ = a.projectNamed(p)
	}
	var t work.Task
	if parent != "" {
		pt, err := a.work.Task(parent)
		if err != nil {
			return ochazuke.Failed(fmt.Errorf("parent task %s: %w", parent, err))
		}
		t, err = a.work.AddSubtask(pt.ProjectID, pt.ID, title)
		if err != nil {
			return ochazuke.Failed(err)
		}
	} else {
		project = cmp.Or(project, it.ProjectID, a.taskProject(it.TaskID), a.pin)
		if t, err = a.work.AddTask(project, title); err != nil {
			return ochazuke.Failed(err)
		}
	}
	it, err = a.inbox.items.Promoted(it.ID, to, t)
	if err == nil {
		err = errors.Join(a.refreshProjects(), a.refreshTasks(), a.refreshInbox())
	}
	if err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify(fmt.Sprintf("Promoted to a %s in %s: %s", to, a.ownerLabel(t.ProjectID), t.Title))
	return ochazuke.Done(struct {
		actionItemJSON
		Task work.Task `json:"task"`
	}{itemView(it, a.inbox.items.Now()), t})
}

// associateActionItem is action-item.associate. From a key with no owner
// named, the project chooser asks, as m (choose-move) does on every row;
// its choice runs the standard move. --project, --task, --dir, --pane and
// --scratch are the deprecated form of set.
func (a *App) associateActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	named := false
	for _, k := range []string{"project", "dir", "task", "pane", "scratch"} {
		if _, ok := ctx.Args[k]; ok {
			named = true
		}
	}
	if !a.cli && !named && ctx.Instance != nil && ctx.Instance.Noun == actionItemNoun {
		return a.moveTo(ctx, actionItemNoun)
	}
	associateNotice(ctx)
	return ctx.Each(a.associateActionItemOne)
}

// associateActionItemOne is associate's old form on one item, its owner
// given: the owner resolved as add resolves it (a name, a directory, a
// pane's owner), written by the standard set.
func (a *App) associateActionItemOne(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	var o actionitems.Owner
	if scratch, _ := ctx.Args["scratch"].(bool); !scratch {
		var notice string
		if o, notice, err = a.actionOwner(ctx.Args); err != nil {
			return ochazuke.Failed(err)
		}
		if notice != "" {
			ctx.Notify(notice)
		}
	}
	return a.actionItemSet(it.ID, actionOwnerChanges(o))
}

// relateActionItemTo is action-item's relation, the one handler behind
// task, pane and project associate --action-item ID: the standard set of
// the item's owner.
func (a *App) relateActionItemTo(id string, o relOwner) ochazuke.Result {
	return a.actionItemSet(id, actionOwnerChanges(actionitems.Owner{ProjectID: o.ProjectID, TaskID: o.TaskID, Window: o.Window, Session: o.Session}))
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

// actionItemJSON is an item as the CLI prints it: the stored row's
// matomeru-old names (service, source_url, seen_at, dismissed_at) read as
// what they are, with the state at the time of the call.
type actionItemJSON struct {
	ID             string         `json:"id"`
	Source         string         `json:"source"`
	URL            string         `json:"url,omitempty"`
	Key            string         `json:"key,omitempty"`
	Title          string         `json:"title,omitempty"`
	Why            string         `json:"why,omitempty"`
	Text           string         `json:"text,omitempty"`
	Kind           string         `json:"kind,omitempty"`
	State          string         `json:"state"`
	Read           bool           `json:"read"`
	ProjectID      string         `json:"project_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	Window         string         `json:"window,omitempty"`
	Session        string         `json:"session,omitempty"`
	Created        int64          `json:"created"`
	Updated        int64          `json:"updated"`
	ReadAt         int64          `json:"read_at,omitempty"`
	SnoozedUntil   int64          `json:"snoozed_until,omitempty"`
	ArchivedAt     int64          `json:"archived_at,omitempty"`
	PromotedTodoID string         `json:"promoted_todo_id,omitempty"`
	PromotedTaskID string         `json:"promoted_task_id,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

func itemView(it actionitems.Item, now int64) actionItemJSON {
	return actionItemJSON{ID: it.ID, Source: it.Source, URL: it.URL, Key: it.Key, Title: it.Title, Why: it.Why, Text: it.Text, Kind: it.Kind,
		State: it.State(now), Read: it.Read(), ProjectID: it.ProjectID, TaskID: it.TaskID, Window: it.Window, Session: it.Session,
		Created: it.Created, Updated: it.Updated, ReadAt: it.ReadAt, SnoozedUntil: it.SnoozedUntil, ArchivedAt: it.ArchivedAt,
		PromotedTodoID: it.PromotedTodoID, PromotedTaskID: it.PromotedTaskID, Extra: it.Extra}
}
