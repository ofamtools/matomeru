package app

import (
	"strings"

	"ofam.dev/ochazuke/core"
)

// showScrollSlot returns the stored offset and its known flag for one of the
// Show page's scroll containers, or nils for any other container.
func (a *App) showScrollSlot(scrollID string) (*int, *bool) {
	switch scrollID {
	case "show.related":
		return &a.nav.showRelatedOff, &a.nav.showRelatedSet
	case "show.props.scroll":
		return &a.nav.showPropsOff, &a.nav.showPropsSet
	}
	return nil, nil
}

// clearShowScroll drops the stored offsets of the related and/or props
// scroller, handing focus-following back to the renderer.
func (a *App) clearShowScroll(related, props bool) bool {
	changed := false
	if related && (a.nav.showRelatedOff != 0 || a.nav.showRelatedSet) {
		a.nav.showRelatedOff, a.nav.showRelatedSet = 0, false
		changed = true
	}
	if props && (a.nav.showPropsOff != 0 || a.nav.showPropsSet) {
		a.nav.showPropsOff, a.nav.showPropsSet = 0, false
		changed = true
	}
	return changed
}

// showFollowFocus keeps a newly focused item visible inside the Show scroll
// container that encloses it: the stored offset moves only as far as
// traits.ScrollReveal needs to keep ui.scrolloff rows between the item and
// the edge it approaches. When the item has no laid-out rect yet, the stored
// offset is dropped and the renderer follows focus for that step.
func (a *App) showFollowFocus(s core.State, focusID string) {
	if scrollID, info, ok := enclosingScroll(s, focusID); ok {
		if off, set := a.showScrollSlot(scrollID); off != nil {
			base := *off
			if !*set {
				base = info.Offset
			}
			next := a.scrollFollowOffset(base, info, focusID)
			if next != *off || !*set {
				*off, *set = next, true
				a.viewMemo.gen++
			}
			return
		}
	}
	changed := false
	switch {
	case focusID == showHeader:
		changed = a.clearShowScroll(true, true)
	case strings.HasPrefix(focusID, "show.sec.") || focusID == "show.related":
		changed = a.clearShowScroll(true, false)
	case strings.HasPrefix(focusID, "show.field.") || focusID == "show.props":
		changed = a.clearShowScroll(false, true)
	}
	if changed {
		a.viewMemo.gen++
	}
}
