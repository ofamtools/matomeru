package app

import (
	"fmt"
	"testing"
)

// walkScrollFollow moves focus down then up through the focusable rows of
// scroll container scrollID and checks the renderer's stateful focus follow:
// ui.scrolloff rows between the focused item and the edge it approaches
// (its bottom edge near the bottom, its top edge near the top), and an
// offset that stays put while the item is inside that padded window.
func walkScrollFollow(t *testing.T, a *App, d *Driver, scrollID string) {
	t.Helper()
	pad := a.scrollOff()
	type geo struct{ off, top, bottom, viewH, maxOff int }
	look := func() (geo, bool) {
		d.Frame()
		info, ok := a.State().Layout.Scrolls[scrollID]
		r, inside := info.ChildRects[a.Focus()]
		if !ok || !inside {
			return geo{}, false
		}
		return geo{info.Offset, r.Y, r.Y + r.H, info.ViewH, info.MaxOff}, true
	}
	check := func(step string, g geo) {
		t.Helper()
		p := min(pad, max(0, (g.viewH-(g.bottom-g.top))/2))
		if g.off < g.maxOff && g.off+g.viewH-g.bottom < p {
			t.Fatalf("%s: focused item bottom %d is %d rows from viewport bottom %d, want >= %d", step, g.bottom, g.off+g.viewH-g.bottom, g.off+g.viewH, p)
		}
		if g.off > 0 && g.top-g.off < p {
			t.Fatalf("%s: focused item top %d is %d rows from viewport top %d, want >= %d", step, g.top, g.top-g.off, g.off, p)
		}
	}

	var last geo
	scrolledDown := false
	for i := range 40 {
		d.Keys("j")
		g, ok := look()
		if !ok {
			continue
		}
		check(fmt.Sprintf("j #%d", i+1), g)
		last = g
		if g.off > 0 {
			scrolledDown = true
		}
		if scrolledDown && g.off < g.maxOff && g.bottom > 2*g.viewH {
			break
		}
	}
	if !scrolledDown {
		t.Fatalf("%s never scrolled; content too short for the test (last %+v)", scrollID, last)
	}
	// Moving back up: the offset must not change while the focused item is
	// still inside the padded window, and near the top the top edge is padded.
	for i := range 40 {
		prev := last
		d.Keys("k")
		g, ok := look()
		if !ok {
			break
		}
		check(fmt.Sprintf("k #%d", i+1), g)
		p := min(pad, max(0, (g.viewH-(g.bottom-g.top))/2))
		if g.top-prev.off >= p && g.off != prev.off {
			t.Fatalf("k #%d: offset moved %d -> %d although item top %d was inside the padded window", i+1, prev.off, g.off, g.top)
		}
		last = g
	}
}

// TestFeaturesNavScrollFollowPadding: a plain list in a KindScroll outside
// the Show page (the Features nav) gets the same padded, stateful follow from
// the renderer, with no app-side offset.
func TestFeaturesNavScrollFollowPadding(t *testing.T) {
	a, _ := newFeaturesTestApp(t, Options{FS: matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")})
	d := NewDriver(a, 150, 24)
	d.Keys("4")
	if a.Space() != SpaceFeatures {
		t.Fatalf("slot 4 did not open SpaceFeatures: space=%q", a.Space())
	}
	d.Frame()
	walkScrollFollow(t, a, d, "features.nav.scroll")
}

// TestScrollFollowTraitSetting: nav.scrollFollowTrait and ui.scrolloff
// configure the shell renderer, at startup and on a settings change.
func TestScrollFollowTraitSetting(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprint(on), func(t *testing.T) {
			a, _ := demo(t)
			a.cfg.userSettings[SetScrollFollowTrait] = on
			a.cfg.userSettings[SetScrollOff] = 3
			a.reloadSettings()
			r := a.Shell.Renderer
			if r.StatelessScrollFollow == on || r.ScrollOff != 3 {
				t.Fatalf("nav.scrollFollowTrait=%v: StatelessScrollFollow=%v ScrollOff=%d, want %v and 3", on, r.StatelessScrollFollow, r.ScrollOff, !on)
			}
		})
	}
}
