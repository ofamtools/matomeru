package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The Extensions page: a tab after mtm's own pages, before the extensions'
// tabs. Its table is discovery's rows (extension_discover.go), rows of the
// extension noun, read off the loop when the page opens and after every
// register, refresh or remove. The detail beside it is the selected row's
// catalog: its nouns and verbs, its pages, and for a stale row what
// changed. On a row: n registers an available tool, L refreshes a
// registered or stale one, d d removes a registered one (confirmed; the
// data file stays, `mtm extension remove NAME --purge` deletes it), enter
// opens a mounted one's first tab, s sets up one that needs setup
// (extension_setup.go: the setup column, the detail's items and the
// summary line). Each re-mounts live (extension_sync.go).
//
// A row's status column says what is fetching for the extension while it
// runs: "fetching catalog…" (discovery asking a changed bin, register,
// refresh), "loading rows…" (its pages' row and pick-list loads), and
// "error: …" after a failed one, until the next fetch starts or a load
// succeeds; else its discovery status.

const (
	extensionsPage = "extensions"
	extListID      = "extensions/list"
	extDetailID    = "extensions/detail"
)

var extColumns = []ochazuke.Column{
	{Title: "name", Min: 10}, {Title: "status", Min: 10}, {Title: "bin", Priority: 3},
	{Title: "tabs", Priority: 2}, {Title: "nouns", Priority: 2}, {Title: "verbs", Priority: 2},
	{Title: "registered", Priority: 1},
}

// extSetupColumn is the setup column, after status, while the
// extensions.setup-status setting is on.
var extSetupColumn = ochazuke.Column{Title: "setup", Min: 13, Priority: 1}

// extListColumns are the table's columns: extColumns, with the setup
// column after status when on.
func extListColumns(on bool) []ochazuke.Column {
	if !on {
		return extColumns
	}
	return slices.Insert(slices.Clone(extColumns), 2, extSetupColumn)
}

// extPageState is the page's discovery state.
type extPageState struct {
	discovered bool   // a discovery has landed
	err        string // why the last one failed
	due        bool   // the next update rediscovers while the page is open
	// rows are discovery's rows as they landed; the table's status cells
	// are theirs unless something is fetching for the extension (extActivity).
	rows []extCandidate
	// fetching counts the catalog execs running per extension name
	// (discovery's probes, register, refresh); failed is the last failure
	// of a fetch for it (a catalog or its rows), until the next one starts.
	fetching map[string]int
	failed   map[string]string
	// seenJob is the last finished row-load job read for failed.
	seenJob uint64
	// setup is each registered extension's last setup check
	// (extension_setup.go), by name.
	setup map[string]extSetupInfo
}

// Status cells while something is fetching for an extension.
const (
	cellFetchingCatalog = "fetching catalog…"
	cellLoadingRows     = "loading rows…"
)

// extFetchStart notes a catalog exec for name starting (on the loop).
func (a *App) extFetchStart(name string) {
	if a.extPage.fetching == nil {
		a.extPage.fetching = map[string]int{}
	}
	a.extPage.fetching[name]++
	delete(a.extPage.failed, name)
}

// extFetchDone notes it ended, failed when err is set.
func (a *App) extFetchDone(name string, err error) {
	if a.extPage.fetching[name]--; a.extPage.fetching[name] <= 0 {
		delete(a.extPage.fetching, name)
	}
	if err != nil {
		a.extFailed(name, extErrLine(name, err))
	}
}

func (a *App) extFailed(name, msg string) {
	if a.extPage.failed == nil {
		a.extPage.failed = map[string]string{}
	}
	a.extPage.failed[name] = msg
}

// extErrLine is name's own line of a joined error ("NAME: why", as
// refresh joins them), else the whole error.
func extErrLine(name string, err error) string {
	for _, line := range strings.Split(err.Error(), "\n") {
		if rest, ok := strings.CutPrefix(line, name+": "); ok {
			return rest
		}
	}
	return err.Error()
}

// extOwners maps the job owners of each mounted extension's row and
// pick-list loads (its pages, their lists) to its name.
func (a *App) extOwners() map[string]string {
	owners := map[string]string{}
	for _, x := range a.exts {
		for _, v := range x.views {
			owners[v.Page()] = x.name
			for _, l := range v.Spec().Lists {
				owners[l.ID] = x.name
			}
		}
	}
	return owners
}

// extActivity is the status cell of each extension something is fetching
// for, or whose last fetch failed: a catalog exec, else a row or pick-list
// load (a running cli.LoadVerb job of its pages), else "error: …". Row
// load failures are read from the jobs' outcomes as they finish.
func (a *App) extActivity() map[string]string {
	owners := a.extOwners()
	loading := map[string]bool{}
	for _, j := range a.r.Jobs() {
		name, ok := owners[j.Owner]
		if !ok || j.Verb != ocli.LoadVerb {
			continue
		}
		switch {
		case j.State == ochazuke.JobRunning:
			loading[name] = true
		case j.Job > a.extPage.seenJob:
			a.extPage.seenJob = j.Job
			if j.State == ochazuke.JobFailed {
				a.extFailed(name, j.Error)
			} else if j.State == ochazuke.JobDone {
				delete(a.extPage.failed, name)
			}
		}
	}
	out := map[string]string{}
	for name, msg := range a.extPage.failed {
		out[name] = "error: " + msg
	}
	for name := range loading {
		out[name] = cellLoadingRows
	}
	for name := range a.extPage.fetching {
		out[name] = cellFetchingCatalog
	}
	return out
}

// statusCell is a row's status column: what is fetching for it, else its
// discovery status.
func statusCell(c extCandidate, activity map[string]string) string {
	if s, ok := activity[c.Name]; ok {
		return s
	}
	return c.Status
}

// syncExtStatus redraws the status cells when what is fetching changed
// (after every message). Rows keep their instances.
func (a *App) syncExtStatus() {
	if a.cli || a.extList == nil {
		return
	}
	activity := a.extActivity()
	for _, item := range a.extList.Items() {
		c, ok := item.Data.(extCandidate)
		if ok && len(item.Cells) > 1 && item.Cells[1] != statusCell(c, activity) {
			if err := a.setExtRows(a.extPage.rows); err != nil {
				a.r.UI().Error(err.Error())
			}
			return
		}
	}
}

// defineExtensionRows adds the row keys and their guards.
func (a *App) defineExtensionRows() {
	a.r.Define("extension").Columns(extColumns...).
		Verb("open", a.openExtension, "Open a registered extension's first tab").Positional("name").
		Mode("normal").
		Bind("n", ochazuke.Do("extension.register").Guard("ext-available")).
		Bind("L", ochazuke.Do("extension.refresh").Guard("ext-registered")).
		Bind("d d", ochazuke.Do("extension.remove").Guard("ext-in-registry")).
		Bind("enter", ochazuke.Do("extension.open").Guard("ext-mounted"))
	a.parameters("extension.open", map[string]ochazuke.Parameter{"name": {Type: "string", Description: "The extension; on an Extensions row, the row's"}})
	status := func(want ...string) ochazuke.Guard {
		return func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
			c, ok := extRow(r.Instances[ev.Focus])
			for _, s := range want {
				if ok && c.Status == s {
					return true
				}
			}
			return false
		}
	}
	a.r.DefineGuard("ext-available", status(statusAvailable))
	a.r.DefineGuard("ext-registered", status(statusRegistered, statusStale))
	a.r.DefineGuard("ext-in-registry", status(statusRegistered, statusStale, statusMissing))
	a.r.DefineGuard("ext-mounted", func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		c, ok := extRow(r.Instances[ev.Focus])
		return ok && a.extTab(c.Name) != ""
	})
}

// extRow is the discovery row an instance is, if it is one.
func extRow(inst *ochazuke.Instance) (extCandidate, bool) {
	if inst == nil || inst.Noun != "extension" {
		return extCandidate{}, false
	}
	c, ok := inst.Data.(extCandidate)
	return c, ok
}

// extTab is the first tab of a mounted extension, "" when it has none.
func (a *App) extTab(name string) string {
	if x := a.extension(name); x != nil && len(x.views) > 0 {
		return x.views[0].Page()
	}
	return ""
}

// rowExtName is the name argument, else the focused row's name.
func rowExtName(ctx ochazuke.Ctx) string {
	if name := ochazuke.StringArg(ctx.Args, "name", ""); name != "" {
		return name
	}
	if c, ok := extRow(ctx.Instance); ok {
		return c.Name
	}
	return ""
}

// openExtension is extension.open: the extension's first tab.
func (a *App) openExtension(ctx ochazuke.Ctx) ochazuke.Result {
	name := rowExtName(ctx)
	page := a.extTab(name)
	if page == "" {
		return ochazuke.Failed(fmt.Errorf("extension %s has no tab: register it first", name))
	}
	if err := a.r.OpenPage(page); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(page)
}

func (a *App) addExtensionsPage() error {
	a.r.Instance(extensionsPage, ochazuke.Kind("page"))
	var err error
	if a.extList, err = a.r.AddList(extListID, nil, ochazuke.Parent(extensionsPage), ochazuke.Columns(extColumns...), ochazuke.NoMarks()); err != nil {
		return err
	}
	if a.detail.ext, err = a.addDetail(extDetailID, extensionsPage, a.extSubject, nil); err != nil {
		return err
	}
	a.extList.SetColumns(extListColumns(a.extSetupOn())...)
	_, err = a.r.AddPage(extensionsPage, "Extensions", a.renderExtensions, ochazuke.Focus(extListID))
	return err
}

// extSubject names what the detail shows: the selected row and its status.
func (a *App) extSubject() string {
	if item, ok := a.extList.Selected(); ok {
		if c, ok := item.Data.(extCandidate); ok {
			return c.Name + "\x00" + c.Status + "\x00" + setupCell(a.extPage.setup[c.Name], true)
		}
	}
	return ""
}

// rediscover reads discovery's rows again off the loop (a newer read
// replaces an older one); they land in the table. It is two jobs: the plan
// (PATH, the registry, the bins' stamps), then the probe of registered
// tools and changed unregistered bins, each row's status
// reading "fetching catalog…" meanwhile.
func (a *App) rediscover() {
	d := a.discovery()
	land := func(v any, err error) {
		a.extPage.discovered = true
		a.extPage.err = ""
		if err != nil {
			a.extPage.err = err.Error()
			return
		}
		a.extPage.rows = v.([]extCandidate)
		a.autoRefreshCatalogs(a.extPage.rows)
		a.checkExtSetup()
		if err := a.setExtRows(a.extPage.rows); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
	launch := func(res ochazuke.Result) {
		ctx := ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Instance: a.r.Instances[extListID]}
		if _, err := a.r.Launch(extListID, "extension.available", res, ctx); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
	launch(ochazuke.Pending(func(context.Context) (any, error) { return d.plan(), nil }).Replacing("discover").
		Then(func(_ ochazuke.Ctx, v any, err error) {
			if err != nil {
				land(nil, err)
				return
			}
			p := v.(*extProbePlan)
			if len(p.ask) == 0 {
				land(d.rows(p), nil)
				return
			}
			names := slices.Collect(maps.Values(p.ask))
			for _, name := range names {
				a.extFetchStart(name)
			}
			launch(ochazuke.Pending(func(c context.Context) (any, error) {
				d.probe(c, p)
				if err := c.Err(); err != nil {
					return nil, err
				}
				return d.rows(p), nil
			}).Replacing("discover").Then(func(_ ochazuke.Ctx, v any, err error) {
				for _, name := range names {
					var perr error
					for bin, n := range p.ask {
						if n == name && p.answers[bin].err != nil {
							perr = p.answers[bin].err
						}
					}
					a.extFetchDone(name, perr)
				}
				land(v, err)
			}))
		}))
}

// setExtRows pushes discovery's rows to the table.
func (a *App) setExtRows(rows []extCandidate) error {
	home, _ := a.home()
	activity := a.extActivity()
	on := a.extSetupOn()
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, c := range rows {
		registered := ""
		if !c.RegisteredAt.IsZero() {
			registered = c.RegisteredAt.Local().Format("2006-01-02 15:04")
		}
		cells := []string{c.Name, statusCell(c, activity), tildePath(c.Bin, home), fmt.Sprint(c.Tabs), fmt.Sprint(c.Nouns), fmt.Sprint(c.Verbs), registered}
		if on {
			info, ok := a.extPage.setup[c.Name]
			if !ok && (c.Status == statusRegistered || c.Status == statusStale) && !extHasSetup(c.catalog) {
				info, ok = extSetupInfo{State: setupNone}, true
			}
			cells = slices.Insert(cells, 2, setupCell(info, ok && c.Status != statusAvailable && c.Status != statusMissing))
		}
		items = append(items, ochazuke.ListItem{ID: extListID + "/" + c.Name, Noun: "extension", Label: c.Name, Data: c, Cells: cells})
	}
	a.extList.SetColumns(extListColumns(on)...)
	return a.extList.SetItems(items)
}

// extensionsUpdate runs after every message: a discovery when the page is
// open and one is due (the page was just opened, or a register, refresh or
// remove changed the registry). It reports whether one started.
func (a *App) extensionsUpdate() bool {
	if a.cli || a.extList == nil || !a.extPage.due || a.r.Page != extensionsPage {
		return false
	}
	a.extPage.due = false
	a.rediscover()
	return true
}

// renderExtensions draws the table with the selected row's detail beside
// it (below it on a narrow screen).
func (a *App) renderExtensions(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	left := func(w, h int) string {
		if a.extList.Len() == 0 {
			return fillLines(a.extEmpty(), h, w)
		}
		// The summary line, when some extension needs setup, is the
		// table's last line.
		if line := a.extSetupSummary(); line != "" && h > 2 {
			return a.view(a.extList, w, h-1) + "\n" + fillLines(line, 1, w)
		}
		return a.view(a.extList, w, h)
	}
	if width >= sideBySide {
		leftW := width * 3 / 5
		rightW := width - leftW - 2
		return ochazuke.Text(joinColumns(left(leftW, height), leftW+2, a.extPanel(rightW, height), height))
	}
	topH := max(3, height*3/5)
	if topH >= height {
		a.detail.ext.hide()
		return ochazuke.Text(left(width, height))
	}
	return ochazuke.Text(fillLines(left(width, topH), topH, width) + "\n" + a.extPanel(width, height-topH-1))
}

// extEmpty is the table's text while it has no rows.
func (a *App) extEmpty() string {
	switch {
	case a.extPage.err != "":
		return "Could not look for family tools: " + a.extPage.err
	case !a.extPage.discovered:
		return "Looking for family tools on PATH…"
	}
	return "No family tools found: an executable o-NAME on PATH that answers `o-NAME catalog` is listed here."
}

// extPanel is the detail in width by height.
func (a *App) extPanel(width, height int) string {
	item, ok := a.extList.Selected()
	c, isRow := item.Data.(extCandidate)
	if !ok || !isRow || width < 1 || height < 1 {
		a.detail.ext.hide()
		return fillLines("", height, width)
	}
	return a.detail.ext.lines(a.extFacts(c), width, width, height)
}

// extFacts is the detail's text for a row: the facts, the catalog's nouns
// and verbs and its pages, and for a stale row what changed.
func (a *App) extFacts(c extCandidate) string {
	home, _ := a.home()
	var registered string
	if !c.RegisteredAt.IsZero() {
		registered = c.RegisteredAt.Local().Format(time.DateTime)
	}
	var b strings.Builder
	b.WriteString(c.Name + "  " + c.Status + "\n\n")
	b.WriteString(facts(
		"bin", pathCell(c.Bin, home),
		"version", c.Version,
		"registered", registered,
		"tabs", fmt.Sprint(c.Tabs),
	))
	switch c.Status {
	case statusMissing:
		b.WriteString("\n\nThe bin is gone; the registered catalog is below.")
	case statusAvailable:
		b.WriteString("\n\nNot registered: mtm would mount the catalog below.")
	}
	if len(c.Changes) > 0 {
		b.WriteString("\n\nchanged since registered")
		if key := a.keyFor(extListID+"/"+c.Name, "extension.refresh"); key != "" {
			b.WriteString(" (" + key + " refreshes)")
		}
		for _, ch := range c.Changes {
			b.WriteString("\n  " + ch)
		}
	}
	if setup := a.extSetupFacts(c); setup != "" {
		b.WriteString("\n\n" + setup)
	}
	plan := extPlan(c.catalog)
	b.WriteString("\n\nnouns and verbs\n")
	if len(plan) == 0 {
		b.WriteString("  (none mtm mounts)\n")
	}
	w := 0
	for _, n := range plan {
		w = max(w, len(n.Name))
	}
	for _, n := range plan {
		verbs := make([]string, len(n.Verbs))
		for i, v := range n.Verbs {
			verbs[i] = v.Name
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", w, n.Name, strings.Join(verbs, ", "))
	}
	b.WriteString("\npages\n")
	for _, p := range extPageLines(c.Name, c.catalog, plan) {
		b.WriteString("  " + p + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// extPageLines names the tabs mtm would mount for a catalog: its data
// pages, else the one page made from its nouns.
func extPageLines(name string, doc ocli.CatalogDoc, plan []ocli.CatalogNoun) []string {
	mounted := map[string]string{}
	for _, n := range plan {
		mounted[n.Name] = n.Name // pagesOf reads only which nouns mount
	}
	pages := pagesOf(name, doc, plan, mounted)
	if len(pages) == 0 {
		return []string{"(none)"}
	}
	declared := len(doc.Pages) > 0 && pages[0].ID != extPageID(name, "")
	out := make([]string, 0, len(pages))
	for _, p := range pages {
		lists := make([]string, len(p.Lists))
		for i, l := range p.Lists {
			lists[i] = l.Noun
		}
		title := p.Title
		if title == "" {
			title = p.ID
		}
		how := "from its nouns"
		if declared {
			how = "declared"
		}
		out = append(out, fmt.Sprintf("%s (%s: %s)", title, how, strings.Join(lists, ", ")))
	}
	return out
}
