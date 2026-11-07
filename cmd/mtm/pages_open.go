package main

import "ofam.dev/ochazuke"

// onOpen sets the page's open step: it runs each time the page is opened
// from another page (ochazuke Page.OnOpen), so the page refreshes once on
// opening. An error shows on the status line. The page is defined where its
// own setup is; this is the one place that says how a step is attached.
func (a *App) onOpen(id string, step func() error) {
	a.r.PageOf(id).OnOpen = func(*ochazuke.Runtime, string) {
		if err := step(); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// watchPageOpens makes every page that is opened ask for an extension
// registry sync (a refresh or register from the CLI shows when a page is
// opened). It wraps each page's open step once, so pages mounted later
// (an extension's) are covered by the next update. The first call also asks
// for the first sync.
func (a *App) watchPageOpens() {
	if a.pagesWatched == nil {
		a.pagesWatched = map[*ochazuke.Page]bool{}
		a.extSyncDue = true
	}
	for _, p := range a.r.PageList() {
		if a.pagesWatched[p] {
			continue
		}
		a.pagesWatched[p] = true
		next := p.OnOpen
		p.OnOpen = func(r *ochazuke.Runtime, from string) {
			a.extSyncDue = true
			if next != nil {
				next(r, from)
			}
		}
	}
}
