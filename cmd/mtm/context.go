package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The Context page: things extracted from panes (paths, URLs, PRs,
// branches, commits, commands), one table of kind · text · source ·
// project · last seen · ★, starred first, then the newest sighting. It
// follows the pin like every list (S shows all; no pin is every project).
// f cycles the kind shown, / searches, and a row answers y (copy), o
// (open), R (reveal), * (star) and d d (forget the record).
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
	{Title: "kind", Min: 6},
	{Title: "text", Min: 20},
	{Title: "source", Priority: 3},
	{Title: "project", Priority: 2},
	{Title: "seen", Align: ochazuke.AlignRight, Priority: 1},
	{Title: "★"},
}

// contextState is the Context page's own state.
type contextState struct {
	items []contextitems.Item // every stored item, newest sighting first
	kind  string              // the kind shown, "" for every kind
	query *ochazuke.Input     // the page's search field
	seen  string              // the query the list was last filtered by
	list  *ochazuke.List
	sys   systemActions
	// cmds are commands a completion asks the loop to run (an OSC 52
	// copy); contextUpdate hands them to Bubble Tea.
	cmds []tea.Cmd
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

// osSystem runs pbcopy, open and open -R (xdg-open on Linux).
type osSystem struct{}

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

func (osSystem) Open(ctx context.Context, target string) error {
	if runtime.GOOS == "darwin" {
		return runOpener(ctx, "open", "--", target)
	}
	return runOpener(ctx, "xdg-open", target)
}

func (osSystem) Reveal(ctx context.Context, path string) error {
	if runtime.GOOS == "darwin" {
		return runOpener(ctx, "open", "-R", "--", path)
	}
	return runOpener(ctx, "xdg-open", filepath.Dir(path))
}

func runOpener(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// osc52 is the terminal's clipboard sequence for text.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func (a *App) defineContext() {
	a.cx = &contextState{sys: osSystem{}}
	a.r.Define(contextNoun).Columns(contextColumns...).
		Verb("list", a.listContext, "List context items in scope, starred first then newest; --kind narrows, --all widens past the pin").Rows(contextNoun).
		Verb("copy", a.copyContext, "Copy the item's text to the clipboard (OSC 52 when no clipboard program serves the session)").Positional("id").
		Verb("open", a.openContext, "Open the item with the default app: a URL or PR in the browser, a path in its app").Positional("id").
		Verb("reveal", a.revealContext, "Reveal a file or dir item in Finder").Positional("id").
		Verb("star", a.starContext, "Star or unstar the item (toggles without --starred); starred items sort first").Positional("id").
		Verb("forget", a.forgetContext, "Forget the record only; the file, page or branch it names is untouched").Positional("id").Confirm("Forget {{label}}?").
		Verb("extract", a.extractContext, "Extract items from a pane's whole scrollback and command history now (panes are followed continuously anyway)").
		Verb("kind", a.cycleContextKind, "Show one kind at a time: all, then each kind in turn").
		Verb("search", a.searchContext, "Filter the context items by words").
		Mode("normal").
		Bind("y", ochazuke.Do(contextNoun+".copy")).
		Bind("o", ochazuke.Do(contextNoun+".open")).
		Bind("R", ochazuke.Do(contextNoun+".reveal")).
		Bind("*", ochazuke.Do(contextNoun+".star")).
		Bind("d d", ochazuke.Do(contextNoun+".forget"))
	id := map[string]ochazuke.Parameter{"id": {Type: "string", Source: contextNoun + ".list", Description: "The item id; omitted, the selected row"}}
	for _, verb := range []string{"copy", "open", "reveal", "forget"} {
		a.parameters(contextNoun+"."+verb, id)
	}
	a.parameters(contextNoun+".star", map[string]ochazuke.Parameter{"id": id["id"], "starred": {Type: "boolean", Description: "Set instead of toggling"}})
	a.parameters(contextNoun+".extract", map[string]ochazuke.Parameter{"pane": {Type: "string", Description: "The window id or name; or --target a pane row"}})
	a.parameters(contextNoun+".list", map[string]ochazuke.Parameter{"kind": {Type: "string", Enum: contextitems.Kinds}, "all": {Type: "boolean", Description: "Every project, whatever the pin"}})
	a.r.Define("context-search").Pin("insert").
		Verb("done", a.leaveContextSearch, "Keep the filter and return to the items").
		Mode("insert").Bind("enter", ochazuke.Do("context-search.done")).Bind("esc", ochazuke.Do("context-search.done")).Bind("down", ochazuke.Do("context-search.done"))
	a.ownedBy(contextNoun, contextListID)
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
	var err error
	if a.cx.list, err = a.r.AddList(contextListID, nil, ochazuke.Parent(contextPage), ochazuke.Columns(contextColumns...)); err != nil {
		return err
	}
	if _, err := a.r.AddPage(contextPage, "Context", a.renderContext, ochazuke.Focus(contextListID)); err != nil {
		return err
	}
	for keys, req := range map[string]ochazuke.Binding{"/": ochazuke.Do(contextNoun + ".search"), "f": ochazuke.Do(contextNoun + ".kind")} {
		req.Instance, req.Mode, req.Keys = contextPage, "normal", keys
		if err := a.r.Bind(req); err != nil {
			return err
		}
	}
	return a.reloadContext()
}

// reloadContext reads the items from the store and pushes the rows.
func (a *App) reloadContext() error {
	if a.cx == nil || a.cx.list == nil {
		return nil
	}
	items, err := a.contexts.All()
	if err != nil {
		return err
	}
	a.cx.items = items
	return a.refreshContext()
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

// contextRows are the items in scope (the pin, unless all) of kind ("" for
// any) matching the query's words: starred first, then the newest.
func (a *App) contextRows(kind, query string, all bool) []ocli.Row {
	tasks := a.contextProjects()
	now := time.Now()
	var rows []ocli.Row
	for _, it := range a.cx.items {
		project := itemProject(it, tasks)
		if !all && !a.inScope(project) || kind != "" && it.Kind != kind {
			continue
		}
		cells := a.contextCells(it, project, now)
		if query != "" && !wordsMatch(query, strings.Join(cells, " ")) {
			continue
		}
		rows = append(rows, ocli.Row{Noun: contextNoun, ID: it.ID, Label: it.Text, Cells: cells, Data: it})
	}
	slices.SortStableFunc(rows, func(x, y ocli.Row) int {
		return cmpBool(y.Data.(contextitems.Item).Starred, x.Data.(contextitems.Item).Starred)
	})
	return rows
}

// contextCells is a row's cells in contextColumns order.
func (a *App) contextCells(it contextitems.Item, project string, now time.Time) []string {
	text := it.Text
	if it.Kind == "command" || it.Kind == "prompt" {
		text = oneLine(text)
	}
	if it.Exists != nil && !*it.Exists {
		text += " (missing)"
	}
	source := shortSession(it.Session)
	if it.Pane != "" {
		source += "/" + it.Pane
	}
	star := ""
	if it.Starred {
		star = "★"
	}
	return []string{it.Kind, text, source, a.ownerLabel(project), age(now.Sub(time.UnixMilli(it.Seen()))), star}
}

// refreshContext pushes the filtered rows; selection is kept by id.
func (a *App) refreshContext() error {
	if a.cx == nil || a.cx.list == nil {
		return nil
	}
	a.cx.seen = string(a.cx.query.Text)
	rows := a.contextRows(a.cx.kind, a.cx.seen, false)
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ochazuke.ListItem{ID: row.ID, Noun: contextNoun, Label: row.Label, Cells: row.Cells, Data: row.Data})
	}
	return a.cx.list.SetItems(items)
}

// filterContext refilters when the search text changed (after every update).
func (a *App) filterContext() {
	if a.cx == nil || a.cx.query == nil || string(a.cx.query.Text) == a.cx.seen {
		return
	}
	if err := a.refreshContext(); err != nil {
		a.m.Status = err.Error()
	}
}

// contextUpdate runs after every message: the search filter follows the
// typing, and commands completions queued (an OSC 52 copy) are handed to
// Bubble Tea.
func (a *App) contextUpdate(tea.Msg) tea.Cmd {
	if a.cx == nil {
		return nil
	}
	a.filterContext()
	cmds := a.cx.cmds
	a.cx.cmds = nil
	return tea.Batch(cmds...)
}

func (a *App) renderContext(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	kind := cmp.Or(a.cx.kind, "all")
	head := fmt.Sprintf("context (%d) · kind %s (f)", a.cx.list.Len(), kind)
	var lines []string
	var cursor ochazuke.Block
	focusLine := -1
	switch {
	case a.r.Focus == contextQueryID:
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
	if a.cx.list.Len() == 0 {
		lines = append(lines, a.fill(a.contextEmpty(), body, width))
	} else {
		lines = append(lines, a.view(a.cx.list, width, body))
	}
	text := strings.Join(lines, "\n")
	if focusLine >= 0 {
		return cursor.Offset(0, focusLine).WithText(text)
	}
	return ochazuke.Text(text)
}

// contextEmpty says why the table is empty and what fills it.
func (a *App) contextEmpty() string {
	hidden := len(a.contextRows(a.cx.kind, string(a.cx.query.Text), true))
	var why []string
	switch {
	case len(a.cx.items) == 0:
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
		id = ctx.Instance.ID
	}
	if id == "" {
		return contextitems.Item{}, errors.New("specify a context item id or select a row")
	}
	return a.contexts.Get(id)
}

func (a *App) listContext(ctx ochazuke.Ctx) ochazuke.Result {
	all, _ := ctx.Args["all"].(bool)
	kind := ochazuke.StringArg(ctx.Args, "kind", "")
	if err := a.reloadContextItems(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.contextRows(kind, "", all))
}

// reloadContextItems reads the items without pushing rows (the CLI).
func (a *App) reloadContextItems() error {
	items, err := a.contexts.All()
	a.cx.items = items
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
	sys := a.cx.sys
	return ochazuke.Pending(func(c context.Context) (any, error) {
		osc, err := sys.Copy(c, it.Text)
		return osc, err
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err != nil {
			return
		}
		if osc, _ := v.(bool); osc {
			if err := a.copyOSC52(it.Text); err != nil {
				a.m.Status = err.Error()
				return
			}
		}
		a.m.Notify("copied " + oneLine(it.Text))
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

// openContext is context-item.open (o): the default app, off the loop.
func (a *App) openContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	switch it.Kind {
	case "file", "dir", "link", "pr":
	default:
		return ochazuke.Failed(fmt.Errorf("a %s has nothing to open; y copies it", it.Kind))
	}
	sys, target := a.cx.sys, contextitems.Target(it)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return target, sys.Open(c, target)
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			a.m.Notify("opened " + oneLine(target))
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
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			a.m.Notify("revealed " + oneLine(it.Text))
		}
	})
}

// starContext is context-item.star (*): toggled, or set by --starred.
func (a *App) starContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	starred := !it.Starred
	if v, ok := ctx.Args["starred"].(bool); ok {
		starred = v
	}
	if it, err = a.contexts.SetStarred(it.ID, starred); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContext(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(it)
}

// forgetContext is context-item.forget (d d, confirmed): the record only.
func (a *App) forgetContext(ctx ochazuke.Ctx) ochazuke.Result {
	it, err := a.contextAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.contexts.Forget(it.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContext(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(map[string]any{"id": it.ID, "forgotten": true})
}

// cycleContextKind is context-item.kind (f): all, then each kind.
func (a *App) cycleContextKind(ochazuke.Ctx) ochazuke.Result {
	kinds := append([]string{""}, contextitems.Kinds...)
	i := slices.Index(kinds, a.cx.kind)
	a.cx.kind = kinds[(i+1)%len(kinds)]
	if err := a.refreshContext(); err != nil {
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
