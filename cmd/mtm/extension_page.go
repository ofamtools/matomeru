package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The Extensions page: a tab after mtm's own pages, before the extensions'
// tabs. Its table is discovery's rows (extension_discover.go), rows of the
// extension noun, read off the loop when the page opens and after every
// register, refresh or remove. The detail beside it is the selected row's
// catalog: its nouns and verbs, its pages, and for a stale row what
// changed. On a row: n registers an available tool, L refreshes a
// registered or stale one, d d removes a registered one (confirmed; the
// data file stays, `mtm extension remove NAME --purge` deletes it), enter
// opens a mounted one's first tab. Each re-mounts live (extension_sync.go).

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

// extPageState is the page's discovery state.
type extPageState struct {
	discovered bool   // a discovery has landed
	err        string // why the last one failed
	due        bool   // the next update rediscovers while the page is open
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
	_, err = a.r.AddPage(extensionsPage, "Extensions", a.renderExtensions, ochazuke.Focus(extListID))
	return err
}

// extSubject names what the detail shows: the selected row and its status.
func (a *App) extSubject() string {
	if item, ok := a.extList.Selected(); ok {
		if c, ok := item.Data.(extCandidate); ok {
			return c.Name + "\x00" + c.Status
		}
	}
	return ""
}

// rediscover reads discovery's rows again off the loop (a newer read
// replaces an older one); they land in the table.
func (a *App) rediscover() {
	d := a.discovery()
	res := ochazuke.Pending(func(c context.Context) (any, error) { return d.run(c) }).Replacing("discover").
		Then(func(_ ochazuke.Ctx, v any, err error) {
			a.extPage.discovered = true
			a.extPage.err = ""
			if err != nil {
				a.extPage.err = err.Error()
				return
			}
			if err := a.setExtRows(v.([]extCandidate)); err != nil {
				a.r.UI().Status(err.Error())
			}
		})
	ctx := ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Instance: a.r.Instances[extListID]}
	if _, err := a.r.Launch(extListID, "extension.available", res, ctx); err != nil {
		a.r.UI().Status(err.Error())
	}
}

// setExtRows pushes discovery's rows to the table.
func (a *App) setExtRows(rows []extCandidate) error {
	home, _ := a.home()
	items := make([]ochazuke.ListItem, 0, len(rows))
	for _, c := range rows {
		registered := ""
		if !c.RegisteredAt.IsZero() {
			registered = c.RegisteredAt.Local().Format("2006-01-02 15:04")
		}
		items = append(items, ochazuke.ListItem{ID: extListID + "/" + c.Name, Noun: "extension", Label: c.Name, Data: c,
			Cells: []string{c.Name, c.Status, tildePath(c.Bin, home), fmt.Sprint(c.Tabs), fmt.Sprint(c.Nouns), fmt.Sprint(c.Verbs), registered}})
	}
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
		mounted[n.Name] = extNoun(name, n.Name)
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
