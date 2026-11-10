package main

import (
	"maps"
	"slices"
)

// pageLoad defers a page's store reads until something needs its rows: the
// page opens (Page.OnOpen), a reader of the loaded data asks (ensure), or a
// verb that reads the rows through the page. A headless CLI/MCP call draws no
// page, so New builds the page empty and never decodes a table it does not
// use. Until the first load, a refresh is a no-op: nothing shows the rows
// yet, and the first load reads the newest state anyway.
type pageLoad struct {
	load   func() error // reads the store and pushes the rows
	loaded bool
	count  int // how many times load ran (the test seam)
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
	p.loaded = true
	p.count++
	return p.load()
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
	for _, l := range []*pageLoad{&a.notesLoad, &a.reviewsLoad, &a.cx.load, &a.inboxLoad} {
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
