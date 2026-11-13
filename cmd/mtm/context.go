package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/history"
	"ofam.dev/matomeru/internal/work"
)

// The Context page: things extracted from panes (paths, URLs, PRs,
// branches, commits, commands), one table of kind · text · source ·
// project · last seen · ★, starred first, then the newest sighting. It
// follows the pin like every list (S shows all; no pin is every project).
// f cycles the kind shown, / searches, and a row answers y (copy), o
// (open), R (reveal), * (star), m (move) and d d (forget the record).
//
// The noun is context-item, not context: ochazuke already registers a
// context noun (context.select), and an app noun of the same name would
// share it. The CLI is mtm context-item VERB.

const (
	contextPage    = "context"
	contextListID  = "context/list"
	contextQueryID = "context/query"
	contextNoun    = "context-item"
)

// contextColumns are the context-item noun's table; narrow, the source
// drops first, then the project, then the age.
var contextColumns = []ochazuke.Column{
	{Title: "type", Min: 6},
	{Title: "text", Min: 20},
	{Title: "source", Priority: 3},
	{Title: "project", Priority: 2},
	{Title: "seen", Align: ochazuke.AlignRight, Priority: 1},
	{Title: "★"},
}

// contextState is the Context page's own state.
type contextState struct {
	extractFrom atomic.Value                         // immutable []string snapshot, also read by the journal goroutine
	itemsTok    string                               // the store token cx.items was read at; "" when not read
	stats       atomic.Pointer[contextitems.Statter] // path checks of extraction (context_stat.go)
	items       []contextitems.Item                  // every stored item, newest sighting first
	kind        string                               // the kind shown, "" for every kind
	query       *ochazuke.Input                      // the page's search field
	seen        string                               // the query the list was last filtered by
	list        *ochazuke.Collection
	load        pageLoad // the items load on first need (lazy.go)
	sys         systemActions
	// cmds are commands a completion asks the loop to run (an OSC 52
	// copy); contextUpdate hands them to Bubble Tea.
	cmds    []tea.Cmd
	follow  *contextFollower   // continuous extraction (context_follow.go)
	picking pane               // the pane the live picker was opened in (context_picker.go)
	turns   []history.PaneTurn // the picked pane's turns, oldest first (its prompt rows)
}

// systemActions are the clipboard and the system opener; tests replace
// them.
type systemActions interface {
	// Copy puts text on the clipboard. osc reports that no clipboard
	// program serves this session (ssh, Linux without one), so the caller
	// copies through the terminal with OSC 52.
	Copy(ctx context.Context, text string) (osc bool, err error)
	// Open opens a path or URL with the default application.
	Open(ctx context.Context, target string) error
	// Reveal shows a path in the file manager.
	Reveal(ctx context.Context, path string) error
}

// osSystem runs pbcopy; opening and revealing go through the runtime's
// opener (ochazuke.Opener), which tests and stories replace with a fake.
type osSystem struct{ rt *ochazuke.Runtime }

func (osSystem) Copy(ctx context.Context, text string) (bool, error) {
	pbcopy, err := exec.LookPath("pbcopy")
	if err != nil || os.Getenv("SSH_CONNECTION") != "" {
		return true, nil
	}
	cmd := exec.CommandContext(ctx, pbcopy)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("pbcopy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return false, nil
}

func (s osSystem) Open(_ context.Context, target string) error { return s.rt.Opener().Open(target) }

func (s osSystem) Reveal(_ context.Context, path string) error { return s.rt.Opener().Reveal(path) }

// openTarget resolves on the runtime's loop. Only the system calls run
// off it; a page or verb action must keep the runtime's single owner.
func (a *App) openTarget(target string, sys systemActions) ochazuke.Result {
	_, action, _ := a.r.ResolveOpen(target)
	open, reveal := sys.Open, sys.Reveal
	if _, native := sys.(osSystem); native {
		// Capture the native runner before handing work to a goroutine.
		opener := a.r.Opener()
		open = func(_ context.Context, text string) error { return opener.Open(text) }
		reveal = func(_ context.Context, text string) error { return opener.Reveal(text) }
	}
	switch action.Kind {
	case ochazuke.OpenActionOS:
		return ochazuke.Pending(func(c context.Context) (any, error) {
			return target, open(c, action.Arg)
		})
	case ochazuke.OpenActionReveal:
		return ochazuke.Pending(func(c context.Context) (any, error) {
			return target, reveal(c, action.Arg)
		})
	default:
		if _, err := a.r.Open(target); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(target)
	}
}

// osc52 is the terminal's clipboard sequence for text.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func (a *App) defineContext() {
	a.cx = &contextState{sys: osSystem{a.r}, follow: newContextFollower()}
	// Props and the store first: the standard verbs (get, list, create,
	// set, delete, watch, move, duplicate) come from them
	// (context_store.go); forget is delete's lasting alias, star and
	// associate run set and move; the rest are mtm's own.
	a.r.Define(contextNoun).Filters(contextFilters()...).Props(a.contextProps()...).Store(a.docStore(contextNoun, a.contextStore())).Columns(contextColumns...).
		Verb("copy", a.copyContext, "Copy the item's text to the clipboard (OSC 52 when no clipboard program serves the session)").Positional("id").
		Verb("open", a.openContext, "Open the item's target through open.openers; unmatched text follows open.unmatched").Positional("id").
		Verb("reveal", a.revealContext, "Reveal a file or dir item in Finder").Positional("id").
		Verb("associate", a.associateContext, "Choose the project to move the item to (omit the owner); --task, --project and --scratch are the deprecated forms of set task and move").Positional("id").Many().
		Verb("choose-move", a.chooseMove, "Choose the project to move the item to (most recent first, unassociated last); the choice is the standard set of @owner").Many().
		Verb("extract", a.extractContext, "Extract items from a pane's whole scrollback and command history now (panes are followed continuously anyway)").
		Verb("turns", a.extractTurns, "Extract items from every journaled turn's prompt and answer now (new turns are extracted as they are journaled)").
		Verb("follow", a.followPanes, "Read the panes that printed since the last read and keep what is new (runs by itself, see context.follow-interval)").Quiet().
		Verb("kind", a.cycleContextKind, "Show one kind at a time: all, then each kind in turn").
		Verb("search", a.searchContext, "Filter the context items by words").
		Verb("dismiss", a.triageContext(true), "Dismiss the item (the selected rows; or every item with --before, --filter or --all, in one write): it leaves the pane's rail, a star keeps it there; still listed by --where dismissed=true").Positional("id").Many().
		Verb("undismiss", a.triageContext(false), "Undismiss the item (the selected rows; or every item with --before, --filter or --all, in one write): back on the pane's rail, New unless starred").Positional("id").Many().
		Mode("normal").
		Bind("y", ochazuke.Do(contextNoun+".copy")).
		Bind("o", ochazuke.Do(contextNoun+".open")).
		Bind("R", ochazuke.Do(contextNoun+".reveal")).
		Bind("*", ochazuke.Do(contextNoun+".star")).
		Bind("-", ochazuke.Do(contextNoun+".dismiss").Hinted("dismiss")).
		Bind("+", ochazuke.Do(contextNoun+".undismiss").Hinted("undismiss")).
		Bind("m", ochazuke.Do(contextNoun+".edit", ochazuke.Arguments{"field": "@owner"}).Hinted("move"))
	// forget is the noun's own word for delete (the record only), kept as
	// a lasting alias: context-item forget ID works as before. star is the
	// standard set of starred, toggled (starAlias).
	for _, alias := range []ochazuke.ActionAlias{
		{Alias: contextNoun + ".forget", Action: contextNoun + ".delete", Note: "the record only; the file, page or branch it names is untouched"},
		starAlias(contextNoun),
	} {
		if err := a.r.AddActionAlias(alias); err != nil {
			panic(err)
		}
	}
	id := map[string]ochazuke.Parameter{"id": {Type: "string", Source: contextNoun + ".list", Description: "The item id; omitted, the selected row"}}
	for _, verb := range []string{"copy", "open", "reveal"} {
		a.parameters(contextNoun+"."+verb, id)
	}
	a.parameters(contextNoun+".associate", map[string]ochazuke.Parameter{"id": id["id"],
		"task":    {Type: "string", Noun: "task", Description: "Deprecated: context-item set ID task=TASK (its project follows)"},
		"project": {Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "Deprecated: context-item set ID @owner=PROJECT; one not found is left out with a notice"},
		"scratch": {Type: "boolean", Description: "Deprecated: context-item set ID @owner= (unassociate it)"}})
	for _, verb := range []string{"dismiss", "undismiss"} {
		a.parameters(contextNoun+"."+verb, contextTriageParams())
	}
	a.parameters(contextNoun+".extract", map[string]ochazuke.Parameter{"pane": {Type: "string", Description: "The window id or name; or --target a pane row"}})
	a.r.Define("context-search").Kind(ochazuke.NounKindPart).DefaultKeys(false).Pin("insert").
		Verb("done", a.leaveContextSearch, "Keep the filter and return to the items").
		Mode("insert").Bind("enter", ochazuke.Do("context-search.done")).Bind("esc", ochazuke.Do("context-search.done")).Bind("down", ochazuke.Do("context-search.done"))
	a.ownedBy(contextNoun, contextListID)
	a.defineContextPicker()
}

// addContextPage builds the page: the search field (reached with /), then
// the table.
func (a *App) addContextPage() error {
	a.r.Instance(contextPage, ochazuke.Kind("page"))
	a.cx.query = ochazuke.NewInput(contextQueryID, "", false)
	if err := a.r.AddInput(a.cx.query, ochazuke.Parent(contextPage), ochazuke.Kind("context-search")); err != nil {
		return err
	}
	a.r.Instances[contextQueryID].Unfocusable = true
	// The table and its notes: at once for the TUI, on the first load for
	// a headless call (lazy.go).
	focus, err := a.deferBuild(&a.cx.load, contextPage, contextListID, a.buildContextList)
	if err != nil {
		return err
	}
	a.cx.load.load = a.loadContext
	page, err := a.r.AddPage(contextPage, "Context", a.renderContext, focus...)
	if err != nil {
		return err
	}
	page.OnOpen = func(*ochazuke.Runtime, string) {
		if err := a.cx.load.ensure(); err != nil {
			a.r.UI().Error("context: " + err.Error())
		}
	}
	for _, row := range []struct {
		keys    string
		binding ochazuke.Binding
	}{
		{"/", ochazuke.Do(contextNoun + ".search")},
		{"f", ochazuke.Do(contextNoun + ".kind")},
	} {
		req := row.binding
		req.Instance, req.Mode, req.Keys = contextPage, "normal", row.keys
		if err := a.r.Bind(req); err != nil {
			return err
		}
	}
	return nil
}

// buildContextList adds the table, fed by the context-item store, and the
// focused item's notes, in a region under the table that exists only while
// it has some (contextNotes).
func (a *App) buildContextList() error {
	var err error
	if a.cx.list, err = a.r.AddList(contextListID, nil, ochazuke.Parent(contextPage), ochazuke.From(contextNoun), ochazuke.Columns(contextColumns...)); err != nil {
		return err
	}
	if a.detail.context, err = a.addDetailAbout(contextDetailID, contextPage, func() string {
		item, _ := a.cx.list.Selected()
		return savedRowKey(a.r.Instances[item.ID])
	}, nil, a.r.SelectionTarget(func() *ochazuke.Collection { return a.cx.list })); err != nil {
		return err
	}
	if a.cx.kind != "" || len(a.cx.query.Text) > 0 { // controls set before the build
		return a.syncContextView()
	}
	return nil
}

// reloadContext refreshes the rows when the page has loaded them.
func (a *App) reloadContext() error {
	if a.cx == nil || a.cx.list == nil {
		return nil
	}
	if picker := a.r.Chooser(contextPicker); picker != nil {
		if err := a.reloadContextItems(); err != nil {
			return err
		}
		picker.SetRows(a.contextPickSpec(a.cx.picking).rows())
	}
	return a.cx.load.refresh()
}

// loadContext refills only the visible window, never the whole table.
func (a *App) loadContext() error {
	return a.cx.list.Refill()
}

// contextProjects maps every task to its project, so an item owned by a
// task (matomeru-old stored those with no project) reads as its project's.
func (a *App) contextProjects() map[string]string {
	tasks, err := a.work.Tasks("")
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(tasks))
	for _, t := range tasks {
		out[t.ID] = t.ProjectID
	}
	return out
}

// itemProject is the project an item belongs to: its own, else its task's.
func itemProject(it contextitems.Item, tasks map[string]string) string {
	if it.ProjectID != "" {
		return it.ProjectID
	}
	return tasks[it.TaskID]
}

// cmpBool orders false before true.
func cmpBool(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return 1
	}
	return -1
}

// itemCell is an item's text for a cell: a path as pathCell shortens it
// (its head and last two folders), a command or prompt on one line.
func (a *App) itemCell(it contextitems.Item) string {
	switch it.Kind {
	case "file", "dir":
		home, _ := a.home()
		return pathCell(it.Text, home)
	case "command", "prompt":
		return oneLine(it.Text)
	}
	return it.Text
}

// syncContextView applies the curated kind and search controls to the index.
func (a *App) syncContextView() error {
	if a.cx.list == nil {
		return nil // not built yet: its build applies the controls
	}
	a.cx.seen = string(a.cx.query.Text)
	a.cx.list.SetFilter(a.cx.seen)
	condition := ochazuke.PropFilter{Prop: "type"}
	if a.cx.kind != "" {
		condition.Value = a.cx.kind
	}
	return setCollectionCondition(a.cx.list, condition)
}

// setCollectionCondition changes one declared condition without losing the
// user's other property filters. A nil value removes that condition.
func setCollectionCondition(list *ochazuke.Collection, condition ochazuke.PropFilter) error {
	filters := slices.DeleteFunc(list.PropFilters(), func(f ochazuke.PropFilter) bool { return f.Prop == condition.Prop })
	if condition.Value != nil {
		filters = append(filters, condition)
	}
	return list.SetPropFilters(filters)
}

// filterContext refilters when the search text changed (after every update).
func (a *App) filterContext() {
	if a.cx == nil || a.cx.query == nil || string(a.cx.query.Text) == a.cx.seen {
		return
	}
	if err := a.syncContextView(); err != nil {
		a.r.UI().Error(err.Error())
	}
}

// contextUpdate runs after every message: the search filter follows the
// typing, and commands completions queued (an OSC 52 copy) are handed to
// Bubble Tea, and continuous extraction follows the panes
// (context_follow.go).
func (a *App) contextUpdate(msg tea.Msg) tea.Cmd {
	if a.cx == nil {
		return nil
	}
	a.filterContext()
	follow := a.followUpdate(msg)
	cmds := append(a.cx.cmds, follow)
	a.cx.cmds = nil
	return tea.Batch(cmds...)
}

func (a *App) renderContext(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	if err := a.cx.load.ensure(); err != nil {
		a.r.UI().Error(err.Error())
	}
	s := a.pageStyles()
	kind := cmp.Or(a.cx.kind, "all")
	head := fmt.Sprintf("context (%d) · kind %s (f)", a.cx.list.Len(), kind)
	var lines []string
	var cursor ochazuke.Block
	focusLine := -1
	switch {
	case a.shownFocused(contextQueryID):
		prefix := head + "  / "
		field := ochazuke.InputBlock(a.cx.query, max(8, width-ansi.StringWidth(prefix)), 1)
		cursor, focusLine = field.Offset(ansi.StringWidth(prefix), 0), 0
		lines = append(lines, s.Muted.Render(prefix)+field.Text)
	case len(a.cx.query.Text) > 0:
		lines = append(lines, s.Muted.Render(ansi.Truncate(head+"  / "+string(a.cx.query.Text), width, "…")))
	default:
		lines = append(lines, s.Muted.Render(ansi.Truncate(head+"  / searches", width, "…")))
	}
	body := max(1, height-1)
	notes := ""
	if a.cx.list.Len() == 0 {
		a.detail.context.hide()
		lines = append(lines, a.fill(a.contextEmpty(), body, width))
	} else {
		var n int
		notes, n = a.contextNotes(width, body)
		lines = append(lines, a.view(a.cx.list, width, max(1, body-n)))
	}
	if notes != "" {
		lines = append(lines, notes)
	}
	text := strings.Join(lines, "\n")
	if focusLine >= 0 {
		return cursor.Offset(0, focusLine).WithText(text)
	}
	return ochazuke.Text(text)
}

// settingContextNotes shows the focused item's notes under the Context table.
const settingContextNotes = "context.notes"

var contextNotesSetting = ochazuke.Setting{Name: settingContextNotes, Type: ochazuke.SettingBoolean, Default: true,
	Description: "Show the notes about the focused context item in a region under the table, only while it has some; off leaves the table the whole page (detail.related also turns the sections off)"}

// contextNotes is the notes region under the table: the related stack of the
// focused item drawn at most half the body, preceded by a blank line, and the
// lines it takes from the table. "" and 0 while the item has no notes, the
// setting is off or the page is too short.
func (a *App) contextNotes(width, body int) (string, int) {
	d := a.detail.context
	stack := ""
	if a.r.Settings().Bool(settingContextNotes) && body >= 6 {
		stack = d.relatedText(width)
	}
	if stack == "" {
		d.hide()
		return "", 0
	}
	n := min(strings.Count(stack, "\n")+1, body/2)
	return "\n" + d.lines(stack, 0, width, n), n + 1
}

// contextEmpty says why the table is empty and what fills it.
func (a *App) contextEmpty() string {
	page, err := a.contextTableQuery().Query(ochazuke.Ctx{}, ochazuke.ListQuery{Every: true, Args: ochazuke.Arguments{"all": true, "kind": a.cx.kind}, Text: string(a.cx.query.Text), PerPage: 1})
	if err != nil {
		return err.Error()
	}
	hidden := page.Total
	var why []string
	switch {
	case hidden == 0:
		why = append(why, "nothing yet: paths, URLs, PRs, branches and commits appear here as panes print them")
	case hidden > 0:
		why = append(why, fmt.Sprintf("none in %s; %d in other projects (S shows all)", a.ownerLabel(a.pin), hidden))
	default:
		why = append(why, "no items match")
	}
	if a.cx.kind != "" {
		why = append(why, "f shows the next kind")
	}
	return strings.Join(why, "\n")
}

// contextAt is the item a verb acts on: the id argument, else the row.
func (a *App) contextAt(ctx ochazuke.Ctx) (contextitems.Item, error) {
	id := ochazuke.StringArg(ctx.Args, "id", "")
	if id == "" && ctx.Instance != nil && ctx.Instance.Noun == contextNoun {
		id = savedRowKey(ctx.Instance)
	}
	if id == "" {
		return contextitems.Item{}, errors.New("specify a context item id or select a row")
	}
	return a.contexts.Get(id)
}

// reloadContextItems reads the items without pushing rows (the CLI).
// It reads nothing while the store's token is the one the items were read
// at (every write of the table moves it).
func (a *App) reloadContextItems() error {
	tok, err := a.contexts.Version()
	if err == nil && tok != "" && tok == a.cx.itemsTok {
		return nil
	}
	items, err := a.contexts.All()
	a.cx.items = items
	a.cx.itemsTok = ""
	if err == nil {
		a.cx.itemsTok = tok
	}
	return err
}

// copyContext is context-item.copy (y): the clipboard off the loop; with
// no clipboard program the text goes out as OSC 52 (the terminal's own
// clipboard, which TUIOS and ssh pass on).
func (a *App) copyContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return a.copyText(it.Text)
}

// copyText puts text on the clipboard off the loop, through OSC 52 when
// no clipboard program serves the session.
func (a *App) copyText(text string) ochazuke.Result {
	sys := a.cx.sys
	return ochazuke.Pending(func(c context.Context) (any, error) {
		osc, err := sys.Copy(c, text)
		return osc, err
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if osc, _ := v.(bool); osc {
			if err := a.copyOSC52(text); err != nil {
				ctx.Error(err.Error())
				return
			}
		}
		ctx.Notify("copied " + oneLine(text))
	})
}

// copyOSC52 sends the text to the terminal's clipboard: through Bubble Tea
// in the TUI, straight to the controlling terminal from the CLI.
func (a *App) copyOSC52(text string) error {
	if !a.cli {
		a.cx.cmds = append(a.cx.cmds, tea.SetClipboard(text))
		return nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("no clipboard: pbcopy is not available and there is no terminal for OSC 52: %w", err)
	}
	defer tty.Close()
	_, err = tty.WriteString(osc52(text))
	return err
}

// openContext opens the item's text through the same table as the Open line.
func (a *App) openContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	target := contextitems.Target(it)
	return a.openTarget(target, a.cx.sys).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			ctx.Notify("opened " + oneLine(target))
		}
	})
}

// revealContext is context-item.reveal (R): Finder, for paths only.
func (a *App) revealContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if !it.Path() {
		return ochazuke.Failed(fmt.Errorf("reveal shows files and directories; this is a %s (o opens it, y copies it)", it.Kind))
	}
	sys := a.cx.sys
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return it.Text, sys.Reveal(c, it.Text)
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			ctx.Notify("revealed " + oneLine(it.Text))
		}
	})
}

// contextRelateParam is the context item task.associate and
// project.associate relate (relations.go).
var contextRelateParam = ochazuke.Parameter{Type: "string", Noun: contextNoun, Source: contextNoun + ".list", Free: true, Description: "A context item to associate with this one"}

// cycleContextKind is context-item.kind (f): all, then each kind.
func (a *App) cycleContextKind(ochazuke.Ctx) ochazuke.Result {
	kinds := append([]string{""}, contextitems.Kinds...)
	i := slices.Index(kinds, a.cx.kind)
	a.cx.kind = kinds[(i+1)%len(kinds)]
	if err := a.syncContextView(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(cmp.Or(a.cx.kind, "all"))
}

// searchContext is context-item.search (/): the search field takes the typing.
func (a *App) searchContext(ochazuke.Ctx) ochazuke.Result {
	if err := a.r.OpenPage(contextPage); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.r.SetFocus(contextQueryID))
}

// leaveContextSearch is context-search.done: the filter stays, the focus
// goes to the first match.
func (a *App) leaveContextSearch(ochazuke.Ctx) ochazuke.Result {
	if err := a.cx.load.ensure(); err != nil {
		return ochazuke.Failed(err)
	}
	a.filterContext()
	if items := a.cx.list.Items(); len(items) > 0 {
		if err := a.cx.list.Select(items[0].ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return resultError(a.cx.list.Focus())
}

// contextOwner is the owner extraction records for a pane: its saved
// owner (pane, else session) at extraction time, else unassociated.
func (a *App) contextOwner(p pane) contextitems.Owner {
	o := a.paneOwner(p)
	return contextitems.Owner{ProjectID: o.ProjectID, TaskID: o.TaskID}
}

// contextSource is where a pane's items were seen.
func contextSource(p pane, kind string) contextitems.Source {
	name := strings.TrimPrefix(p.label(), shortSession(p.Session)+"/")
	return contextitems.Source{Kind: kind, Session: p.Session, Window: p.ID, Pane: name, Cwd: p.Cwd, At: work.Now()}
}
