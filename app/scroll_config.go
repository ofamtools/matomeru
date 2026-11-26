package app

import (
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
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

// applyRendererSettings configures the shell renderer's scroll follow from
// the settings: ui.scrolloff padding for every KindScroll container that
// follows focus, and nav.scrollFollowTrait=false restoring the stateless
// flush-to-bottom follow. Called at startup and whenever settings change.
func (a *App) applyRendererSettings() {
	if a.Shell == nil {
		return
	}
	if a.Shell.Renderer == nil {
		a.Shell.Renderer = ui.NewRenderer(ui.DefaultCacheEntries)
	}
	a.Shell.Renderer.ScrollOff = a.scrollOff()
	a.Shell.Renderer.StatelessScrollFollow = a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetScrollFollowTrait, true)
}
