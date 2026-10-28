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
	sig    string // what the rows were last built from; a reload that finds it unchanged sets nothing
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
	a.r.Define(n).Columns(inboxColumns...).
		Verb("add", a.addActionItem, "Record an action item; the same source and --key (else --url) refreshes its row instead of adding one. Prints the item; new is true when a row was added").
		Verb("list", a.listActionItems, "List action items: open ones by default; --state snoozed, archived or all").Rows(n).Fetch().
		Verb("show", a.showActionItem, "Show one action item").Positional("id").Fetch().
		Verb("count", a.countActionItems, "Count the open action items and the unread ones among them").Fetch().
		Verb("open", a.openActionItem, "Open the item's URL in the browser and mark it read").Positional("id").
		Verb("read", a.readActionItem, "Mark the item read").Positional("id").
		Verb("unread", a.unreadActionItem, "Mark the item unread").Positional("id").
		Verb("toggle-read", a.toggleReadActionItem, "Mark the item read, or a read item unread").Positional("id").
		Verb("snooze", a.snoozeActionItem, "Hide the item until --until (a time) or for --for (90m, 4h, 2d, 1w); omitted, a chooser asks").Positional("id").
		Verb("archive", a.archiveActionItem, "Archive the item: it leaves the open items and keeps its row").Positional("id").
		Verb("restore", a.restoreActionItem, "Make an archived or snoozed item open again").Positional("id").
		Verb("delete", a.deleteActionItem, "Delete the item's row (archive keeps it)").Positional("id").Confirm("Delete {{label}}? x archives it instead.").
		Verb("promote-todo", a.promoteTodo, "Promote the item to a todo: a top-level task of its project (else the pin, else unassociated); the item is archived and points at it").Positional("id").
		Verb("promote-task", a.promoteTask, "Promote the item to a task: a subtask of --parent, else of the item's task, else a top-level task like promote-todo").Positional("id").
		Verb("associate", a.associateActionItem, "Associate the item with a project, task or pane (omit them to choose a project); --scratch unassociates it").Positional("id").
		Mode("normal").
		Bind("enter", ochazuke.Do(n+".open")).
		Bind("u", ochazuke.Do(n+".toggle-read").Hinted("read/unread")).
		Bind("x", ochazuke.Do(n+".archive")).
		Bind("X", ochazuke.Do(n+".restore")).
		Bind("s", ochazuke.Do(n+".snooze")).
		Bind("t", ochazuke.Do(n+".promote-todo").Hinted("to todo")).
		Bind("c", ochazuke.Do(n+".promote-task").Hinted("to task")).
		Bind("m", ochazuke.Do(n+".associate")).
		Bind("d d", ochazuke.Do(n+".delete"))
	// I opens the inbox from anywhere: a row to the framework's page.open.
	a.r.Define("app").Mode("normal").Bind("I", ochazuke.Do("ochazuke.page.open", ochazuke.Arguments{"id": inboxPage}).Hinted("inbox"))

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
	a.parameters(n+".list", map[string]ochazuke.Parameter{
		"state":   {Type: "string", Enum: append([]string{"all"}, actionitems.States...), Description: "Which items (default open)"},
		"source":  {Type: "string", Description: "Only this source's items"},
		"project": {Type: "string", Noun: "project", Source: "projects.list", Free: true, Description: "Only this project's items (id or name)"},
	})
	for _, verb := range []string{"show", "open", "read", "unread", "toggle-read", "archive", "restore", "delete"} {
		a.parameters(n+"."+verb, map[string]ochazuke.Parameter{"id": inboxIDParam})
	}
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
	associate := map[string]ochazuke.Parameter{"id": inboxIDParam, "scratch": {Type: "boolean", Description: "Unassociate it"}}
	for k, v := range inboxOwnerParams {
		associate[k] = v
	}
	a.parameters(n+".associate", associate)
	a.ownedBy(n, inboxListID)

	// The reverse of action-item.associate on the nouns it relates to,
	// sharing its handler: task.associate, pane.associate and
	// project.associate take --action-item.
	a.r.Define("project").Verb("associate", a.associateProject, "Associate an action item (--action-item) with this project")
	a.parameters("project.associate", map[string]ochazuke.Parameter{"action-item": inboxRelateParam})
	for _, key := range []string{"task.associate", "pane.associate"} {
		op := a.r.Operations[a.r.CanonicalKey(key)]
		if op.Parameters == nil {
			op.Parameters = map[string]ochazuke.Parameter{}
		}
		op.Parameters["action-item"] = inboxRelateParam
		a.r.Operations[a.r.CanonicalKey(key)] = op
	}
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
	if _, err = a.r.AddPage(inboxPage, "Inbox", a.renderInbox, ochazuke.Focus(inboxListID)); err != nil {
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
	a.refreshTitle()
	return a.inbox.list.SetItems(rows)
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
		s := ochazuke.NewStyles(*a.m.Appearance)
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
		id = ctx.Instance.ID
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

// inboxEdit runs change on the targeted item.
func (a *App) inboxEdit(ctx ochazuke.Ctx, change func(id string) (actionitems.Item, error)) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.inboxChanged(change(it.ID))
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

func (a *App) listActionItems(ctx ochazuke.Ctx) ochazuke.Result {
	items, err := a.inbox.items.All()
	if err != nil {
		return ochazuke.Failed(err)
	}
	state := ochazuke.StringArg(ctx.Args, "state", actionitems.StateOpen)
	source := strings.ToLower(ochazuke.StringArg(ctx.Args, "source", ""))
	project := ochazuke.StringArg(ctx.Args, "project", "")
	if project != "" {
		if id, ok := a.projectNamed(project); ok {
			project = id
		}
	}
	now := a.inbox.items.Now()
	rows := []ocli.Row{}
	for _, it := range items {
		if state != "all" && it.State(now) != state || source != "" && it.Source != source {
			continue
		}
		if project != "" && cmp.Or(it.ProjectID, a.taskProject(it.TaskID)) != project {
			continue
		}
		rows = append(rows, a.inboxRow(it, now))
	}
	return ochazuke.Done(rows)
}

func (a *App) showActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(itemView(it, a.inbox.items.Now()))
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
	if it.URL == "" {
		return ochazuke.Failed(fmt.Errorf("%s has no URL to open; it is marked read", it.Label()))
	}
	sys, url := a.inbox.sys, it.URL
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return url, sys.Open(c, url)
	})
}

func (a *App) readActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	return a.inboxEdit(ctx, func(id string) (actionitems.Item, error) { return a.inbox.items.SetRead(id, true) })
}

func (a *App) unreadActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	return a.inboxEdit(ctx, func(id string) (actionitems.Item, error) { return a.inbox.items.SetRead(id, false) })
}

func (a *App) toggleReadActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.inboxChanged(a.inbox.items.SetRead(it.ID, !it.Read()))
}

func (a *App) archiveActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	return a.inboxEdit(ctx, a.inbox.items.Archive)
}

func (a *App) restoreActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	return a.inboxEdit(ctx, a.inbox.items.Restore)
}

func (a *App) deleteActionItem(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.actionItemAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.inbox.items.Delete(it.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshInbox(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(it.ID)
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
		return a.inboxChanged(a.inbox.items.Snooze(it.ID, now.Add(d).UnixMilli()))
	case hasUntil && until != "":
		at, err := parseUntil(until, now)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return a.inboxChanged(a.inbox.items.Snooze(it.ID, at.UnixMilli()))
	case a.cli:
		return ochazuke.Failed(errors.New("snooze needs --for (90m, 4h, 2d, 1w) or --until (a time)"))
	}
	_, err = a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  "snooze-chooser",
		Title: "Snooze " + it.Label() + " until",
		Rows:  snoozeChoices(now),
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			out, err := a.r.Execute(ochazuke.Request{Noun: actionItemNoun, Verb: "snooze", Args: ochazuke.Arguments{"id": it.ID, "until": c.Data.(string)}})
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(out)
		},
	})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
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
// named, the project chooser asks, as m does on every row.
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
	return a.relateActionItem(it.ID, o)
}

// relateActionItem is the one handler behind action-item.associate and its
// reverses on task, pane and project.
func (a *App) relateActionItem(id string, o actionitems.Owner) ochazuke.Result {
	return a.inboxChanged(a.inbox.items.Associate(id, o))
}

// relatedItem reads the --action-item argument of a reverse verb; ok is
// false when the call names none.
func relatedItem(ctx ochazuke.Ctx) (string, bool) {
	id := ochazuke.StringArg(ctx.Args, "action-item", "")
	return id, id != ""
}

// associateProject is project.associate --action-item ID.
func (a *App) associateProject(ctx ochazuke.Ctx) ochazuke.Result {
	id, ok := relatedItem(ctx)
	if !ok {
		return ochazuke.Failed(errors.New("name the action item to associate (--action-item)"))
	}
	if ctx.Instance == nil || ctx.Instance.Noun != "project" {
		return ochazuke.Failed(errors.New("target a project row"))
	}
	return a.relateActionItem(id, actionitems.Owner{ProjectID: ctx.Instance.ID})
}

// associateTaskItem is task.associate --action-item ID.
func (a *App) associateTaskItem(ctx ochazuke.Ctx, id string) ochazuke.Result {
	t, err := a.task(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.relateActionItem(id, actionitems.Owner{ProjectID: t.ProjectID, TaskID: t.ID})
}

// associatePaneItem is pane.associate --action-item ID.
func (a *App) associatePaneItem(ctx ochazuke.Ctx, id string) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	o := actionitems.Owner{Window: p.ID, Session: p.Session}
	if owner, err := a.work.PaneOwner(p.ID, p.SessionID); err == nil {
		o.ProjectID, o.TaskID = owner.ProjectID, owner.TaskID
	}
	return a.relateActionItem(id, o)
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
