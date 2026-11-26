package app

import (
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
)

// scrollPageOverlap is the number of rows a page step (ctrl+f / ctrl+b)
// keeps from the previous screen.
const scrollPageOverlap = 2

// scrollFallbackViewH is the view height assumed before a scroll region has
// been laid out once.
const scrollFallbackViewH = 20

// scrollUnboundedContentH stands in for content height while a scroll region
// has no layout info yet, so stepping is clamped only at zero.
const scrollUnboundedContentH = 1 << 30

// scrollConfig is the app's shared traits.ScrollConfig: page overlap, the
// ui.scrolloff context rows used when revealing a target, and the
// nav.longJumpPolicy setting.
func (a *App) scrollConfig() traits.ScrollConfig {
	policy := traits.LongJumpHalfPage
	if a.cfg.Settings != nil {
		policy = a.cfg.Settings.String(SetLongJumpPolicy, traits.LongJumpHalfPage)
	}
	return traits.ScrollConfig{
		ScrollOff:      a.scrollOff(),
		PageOverlap:    scrollPageOverlap,
		LongJumpPolicy: policy,
	}
}

// scrollStateFromInfo builds a trait scroll state at offset off from a laid
// out scroll region. Without info it assumes the fallback view height and an
// unbounded content height.
func scrollStateFromInfo(off int, info layout.ScrollInfo, hasInfo bool) traits.ScrollState {
	viewH := scrollFallbackViewH
	contentH := scrollUnboundedContentH
	if hasInfo {
		if info.ViewH > 0 {
			viewH = info.ViewH
		}
		contentH = max(info.MaxOff, 0) + viewH
	}
	return traits.ScrollState{OffsetY: off, ViewH: viewH, ContentH: contentH}
}

// scrollSpanOf returns the content-space span of child id in info.
func scrollSpanOf(info layout.ScrollInfo, id string) (traits.ItemSpan, bool) {
	if r, ok := info.ChildRects[id]; ok {
		return traits.ItemSpan{ID: id, Y: r.Y, H: max(1, r.H)}, true
	}
	if y, ok := info.ChildY[id]; ok {
		return traits.ItemSpan{ID: id, Y: y, H: 1}, true
	}
	return traits.ItemSpan{}, false
}

// enclosingScroll returns the innermost laid-out scroll container whose
// content holds id (from the previous frame's layout).
func enclosingScroll(s core.State, id string) (string, layout.ScrollInfo, bool) {
	best := ""
	var bestInfo layout.ScrollInfo
	for sid, info := range s.Layout.Scrolls {
		if _, ok := info.ChildRects[id]; !ok {
			continue
		}
		if _, inner := bestInfo.ChildRects[sid]; best == "" || inner {
			best, bestInfo = sid, info
		}
	}
	return best, bestInfo, best != ""
}

// scrollFollowOffset returns the offset that keeps child id of info visible
// starting from off, under the shared scroll config (ui.scrolloff padding,
// measured from the item's near edge; tall items align to their top).
func (a *App) scrollFollowOffset(off int, info layout.ScrollInfo, id string) int {
	span, ok := scrollSpanOf(info, id)
	if !ok {
		return off
	}
	return traits.ScrollReveal(scrollStateFromInfo(off, info, true), a.scrollConfig(), span).OffsetY
}
