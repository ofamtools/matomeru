package main

import (
	"maps"
	"slices"

	"ofam.dev/ochazuke"
)

// pageLoad defers a page's store reads until something needs its rows: the
// page opens (Page.OnOpen), a reader of the loaded data asks (ensure), or a
// verb that reads the rows through the page. A headless CLI/MCP call draws no
// page, so New builds the page empty and never decodes a table it does not
// use. Until the first load, a refresh is a no-op: nothing shows the rows
// yet, and the first load reads the newest state anyway.
//
// A page's store-fed lists (ochazuke.From) fill themselves when they are
// added, so a headless New does not add them: build adds them, and the
// page's other parts that need them, on the first load (deferBuild). The
// TUI builds every page in New, as before, so its first frame is the same.
type pageLoad struct {
	load   func() error // reads the store and pushes the rows
	loaded bool
	count  int // how many times load ran (the test seam)
	// build adds the page's lists; nil once it has run (or with none).
	build func() error
}

// ensure loads once; later calls are free.
func (p *pageLoad) ensure() error {
	if p.loaded {
		return nil
	}
	return p.reload()
}

// reload reads the store again (the first load included).
func (p *pageLoad) reload() error {
	if err := p.ensureBuilt(); err != nil {
		return err
	}
	p.loaded = true
	p.count++
	return p.load()
}

// ensureBuilt adds the page's deferred lists, once.
func (p *pageLoad) ensureBuilt() error {
	build := p.build
	if build == nil {
		return nil
	}
	p.build = nil
	return build()
}

// deferBuild runs build now for the TUI; a headless call draws no page, so
// build waits for the page's first load. build adds the last of the page's
// children (the lists and what follows them), so a later build keeps the
// page's drawing and focus order. focus is where the page starts, inside
// what build adds: the answer is the page's options, and a page registered
// before its build starts on itself until the build gives it its focus.
func (a *App) deferBuild(p *pageLoad, page, focus string, build func() error) ([]ochazuke.InstanceOption, error) {
	if !a.headless {
		return []ochazuke.InstanceOption{ochazuke.Focus(focus)}, build()
	}
	p.build = func() error {
		if err := build(); err != nil {
			return err
		}
		if pg := a.r.PageOf(page); pg != nil && pg.Focus == page {
			pg.Focus = focus
			if pg.Remembered == page {
				pg.Remembered = focus
			}
		}
		return nil
	}
	return nil, nil
}

// buildPages adds every page's deferred lists: a TUI started from a
// headless New (an explicit tui word) draws them all.
func (a *App) buildPages() error {
	for _, l := range a.lazyPages() {
		if err := l.ensureBuilt(); err != nil {
			return err
		}
	}
	return nil
}

// lazyPages are the loads of the pages whose rows wait for first need.
func (a *App) lazyPages() []*pageLoad {
	return []*pageLoad{&a.notesLoad, &a.reviewsLoad, &a.cx.load, &a.inboxLoad}
}

// refresh reloads only a page that has loaded; before that it does nothing.
func (p *pageLoad) refresh() error {
	if !p.loaded {
		return nil
	}
	return p.reload()
}

// loadPagesFor loads the lazy pages, one at a time, until a row holds id.
func (a *App) loadPagesFor(id string) error {
	for _, l := range a.lazyPages() {
		if a.r.Instances[id] != nil {
			return nil
		}
		if err := l.ensure(); err != nil {
			return err
		}
	}
	return nil
}

// hookLazyPages sets the one lookup-miss hook for every caller (keys,
// palette, CLI, MCP): a target no instance names loads the lazy pages until
// one holds it.
func (a *App) hookLazyPages() {
	a.r.ResolveTarget = func(target string) (string, error) {
		if err := a.loadPagesFor(target); err != nil {
			return "", err
		}
		if len(a.r.Ancestry(target)) > 0 {
			return target, nil
		}
		// From scopes a row's ID as LIST/KEY; explicit targets keep
		// naming the record key, independent of which list shows it.
		for _, id := range slices.Sorted(maps.Keys(a.r.Instances)) {
			list := a.r.List(id)
			if list == nil || list.From() == "" {
				continue
			}
			row := id + "/" + target
			if a.r.Instances[row] != nil {
				return row, nil
			}
		}
		return "", nil
	}
}
