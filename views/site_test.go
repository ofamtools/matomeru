package views

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

func TestPaneSiteTOCStylesAndTightDensity(t *testing.T) {
	w := build(t)
	plan, _, err := w.store.Create(data.CreateInput{
		Kind: data.KindPage,
		ID:   "page3",
		Fields: map[string]any{
			"title": "Plan", "slug": "plan", "pageKind": "plan", "pinned": true, "pane": w.pane.Ref(), "viewedAt": data.WriteTime{},
			"root": ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "# Plan"}},
		},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Star(plan.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	starredPlan, err := w.store.Get(plan.Ref())
	if err != nil {
		t.Fatal(err)
	}
	pages := []data.Record{w.page, *starredPlan, w.asks}

	spaciousTheme := ui.SpaciousDarkTheme()

	compactSite := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", SiteOptions{TOCStyle: TOCStyleCompact})
	tightSite := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", SiteOptions{TOCStyle: TOCStyleTight})
	spaciousSite := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", SiteOptions{TOCStyle: TOCStyleSpacious})

	// 1. Tight style emits KindCard with explicit [0,1,0,1] padding, inline badges, and KindScroll wrapper.
	tightTOC := tightSite.Find(SiteTOCID)
	if tightTOC == nil || tightTOC.Facts["tocStyle"] != TOCStyleTight {
		t.Fatalf("tight site.toc = %+v, want tocStyle=%q", tightTOC, TOCStyleTight)
	}
	if scroll := tightSite.Find("site.toc.scroll"); scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("tight TOC missing KindScroll wrapper: %+v", scroll)
	}
	wantPadding := [4]int{0, 1, 0, 1}
	for _, id := range []string{"site.toc.screen", "site.toc.page2", "site.toc.page3", "site.toc.page1"} {
		card := tightSite.Find(id)
		if card == nil || card.Kind != ui.KindCard {
			t.Fatalf("tight %s = %+v, want KindCard", id, card)
		}
		if card.Padding != wantPadding {
			t.Fatalf("tight %s padding = %v, want %v", id, card.Padding, wantPadding)
		}
		if len(card.Badges) != 0 {
			t.Fatalf("tight %s should inline badges in Title rather than separate Badges row: %+v", id, card.Badges)
		}
	}
	if got := tightSite.Find("site.toc.screen").Title; !strings.Contains(got, "Screen") || !strings.Contains(got, "open") || !strings.Contains(got, "pin") {
		t.Fatalf("tight Screen title = %q, want inline open+pin tags", got)
	}
	if got := tightSite.Find("site.toc.page2").Title; !strings.Contains(got, "Questions") || !strings.Contains(got, "unread") {
		t.Fatalf("tight Questions title = %q, want inline unread tag", got)
	}
	if got := tightSite.Find("site.toc.page3").Title; !strings.Contains(got, "Plan") || !strings.Contains(got, "★") {
		t.Fatalf("tight Plan title = %q, want inline star badge", got)
	}

	// 2. Spacious explicit style wraps in KindScroll and leaves Padding zero so ui.density controls it.
	if scroll := spaciousSite.Find("site.toc.scroll"); scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("spacious TOC missing KindScroll wrapper: %+v", scroll)
	}
	if card := spaciousSite.Find("site.toc.screen"); card == nil || card.Padding != ([4]int{}) {
		t.Fatalf("spacious Screen card padding = %v, want zero (inherit ui.density)", card)
	}

	// 3. Rendered height comparison under SpaciousDarkTheme: compact < tight < spacious.
	_, compactSolved := ui.Render(compactSite, spaciousTheme, "site.toc.screen", 130, 40)
	tightFrame, tightSolved := ui.Render(tightSite, spaciousTheme, "site.toc.screen", 130, 40)
	_, spaciousSolved := ui.Render(spaciousSite, spaciousTheme, "site.toc.screen", 130, 40)

	for _, id := range []string{"site.toc.screen", "site.toc.page2", "site.toc.page3", "site.toc.page1"} {
		hCompact := compactSolved.Rects[id].H
		hTight := tightSolved.Rects[id].H
		hSpacious := spaciousSolved.Rects[id].H
		t.Logf("%s rendered height under SpaciousDarkTheme: compact=%d tight=%d spacious=%d", id, hCompact, hTight, hSpacious)
		if hCompact != 1 {
			t.Errorf("%s compact height = %d, want 1", id, hCompact)
		}
		if hTight != 3 {
			t.Errorf("%s tight height = %d, want 3 (top border + single title line + bottom border)", id, hTight)
		}
		if hSpacious <= hTight {
			t.Errorf("%s spacious height (%d) must be greater than tight height (%d)", id, hSpacious, hTight)
		}
	}
	// Verify rendered tight frame contains all 4 TOC titles cleanly inside a compact height.
	plainTight := stripANSI(tightFrame.ANSI)
	for _, want := range []string{"Screen", "Questions", "Plan", "Compare architectures"} {
		if !strings.Contains(plainTight, want) {
			t.Errorf("tight rendered frame missing %q:\n%s", want, plainTight)
		}
	}

	// 4. Auto preserves card/row style and ALWAYS wraps in KindScroll so off-screen focus scrolls into view.
	autoCompact := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", SiteOptions{TOCStyle: TOCStyleAuto, CompactTOC: true})
	if n := autoCompact.Find("site.toc.screen"); n == nil || n.Kind != ui.KindText {
		t.Fatalf("auto+CompactTOC=true Screen = %+v, want KindText", n)
	}
	if scroll := autoCompact.Find("site.toc.scroll"); scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("auto+CompactTOC=true missing KindScroll wrapper: %+v", scroll)
	}
	autoCards := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", SiteOptions{TOCStyle: TOCStyleAuto, CompactTOC: false})
	if n := autoCards.Find("site.toc.screen"); n == nil || n.Kind != ui.KindCard || n.Padding != ([4]int{}) {
		t.Fatalf("auto+CompactTOC=false Screen = %+v, want default KindCard", n)
	}
	if scroll := autoCards.Find("site.toc.scroll"); scroll == nil || scroll.Kind != ui.KindScroll {
		t.Fatalf("auto+CompactTOC=false missing KindScroll wrapper: %+v", scroll)
	}
}

func TestPaneSiteTOCScrollFollowsFocusAcrossAllStyles(t *testing.T) {
	w := build(t)
	var pages []data.Record
	for i := 1; i <= 16; i++ {
		id := "p" + itoa(i)
		title := "TargetPage-" + itoa(i)
		rec, _, err := w.store.Create(data.CreateInput{
			Kind: data.KindPage,
			ID:   id,
			Fields: map[string]any{
				"title": title, "slug": id, "pageKind": "custom", "pane": w.pane.Ref(),
				"root": ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "# " + title}},
			},
		}, owner)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, *rec)
	}
	theme := ui.SpaciousDarkTheme()
	targetFocus := "site.toc.p1" // oldest -> last in newest-first TOC, well below an 18-row viewport
	cases := []struct {
		name string
		opts SiteOptions
	}{
		{"auto+spacious", SiteOptions{TOCStyle: TOCStyleAuto, CompactTOC: false}},
		{"auto+compact", SiteOptions{TOCStyle: TOCStyleAuto, CompactTOC: true}},
		{"tight", SiteOptions{TOCStyle: TOCStyleTight}},
		{"spacious", SiteOptions{TOCStyle: TOCStyleSpacious}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			site := PaneSiteViewWith(w.pane, pages, ScreenPageID, "", tc.opts)
			frame, solved := ui.Render(site, theme, targetFocus, 120, 16)
			if scroll := site.Find("site.toc.scroll"); scroll == nil || scroll.Kind != ui.KindScroll {
				t.Fatalf("missing site.toc.scroll KindScroll wrapper")
			}
			plain := stripANSI(frame.ANSI)
			if !strings.Contains(plain, "TargetPage-1") {
				t.Fatalf("focused off-screen TOC entry %q was not scrolled into view (scrolls=%+v):\n%s", targetFocus, solved.Scrolls, plain)
			}
			si, ok := solved.Scrolls["site.toc.scroll"]
			if !ok || si.Offset <= 0 {
				t.Fatalf("site.toc.scroll did not scroll down to reveal %s: ScrollInfo=%+v (ok=%v)", targetFocus, si, ok)
			}
		})
	}
}
