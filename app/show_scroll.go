package app

import "strconv"

// The Show page's two scroll columns (show.related, show.props.scroll) are
// followed by the renderer: it remembers each column's drawn offset and
// reveals the focused card with ui.scrolloff padding. The app only holds an
// explicit offset while the user scrolls a column with the scroll keys
// (showScrollMotion); a focus move drops it, so the renderer continues from
// where the column was drawn.

// showScrollSlot returns the explicit offset slot of one of the Show page's
// scroll containers, or nil for any other container.
func (a *App) showScrollSlot(scrollID string) **int {
	switch scrollID {
	case "show.related":
		return &a.nav.showRelatedOff
	case "show.props.scroll":
		return &a.nav.showPropsOff
	}
	return nil
}

// clearShowScroll drops both explicit Show offsets, handing the columns back
// to the renderer's focus follow. It reports whether anything was dropped.
func (a *App) clearShowScroll() bool {
	if a.nav.showRelatedOff == nil && a.nav.showPropsOff == nil {
		return false
	}
	a.nav.showRelatedOff, a.nav.showPropsOff = nil, nil
	return true
}

// showScrollOffsetText renders an explicit offset for the view memo key.
func showScrollOffsetText(off *int) string {
	if off == nil {
		return "-"
	}
	return strconv.Itoa(*off)
}
