package views

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

// ScreenPageID is the ID of the Screen entry a pane's site always has, even
// before any ui.page record with pageKind "screen" exists.
const ScreenPageID = "screen"

// PageScrollID is the scrollable viewport node ID wrapping non-screen ui.page content.
const PageScrollID = "site.page.scroll"

// TOCGroupPrefix prefixes collapsible group headers inside a compact pane TOC.
const TOCGroupPrefix = "site.toc.group."

// SiteTOCID is the ViewNode ID of the pane Pages table-of-contents panel.
const SiteTOCID = "site.toc"

// Pane TOC visual styles for SiteOptions.TOCStyle and panes.tocStyle.
const (
	TOCStyleAuto     = "auto"
	TOCStyleCompact  = "compact"
	TOCStyleTight    = "tight"
	TOCStyleSpacious = "spacious"
)

func isScreen(page data.Record) bool { return page.Text("pageKind") == "screen" }

// pageRoot decodes the ViewNode tree stored in a page's root field.
func pageRoot(page data.Record) (ui.ViewNode, error) {
	var root ui.ViewNode
	v, ok := page.Value("root")
	if !ok || v == nil {
		return ui.ViewNode{Kind: ui.KindVStack}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return root, err
	}
	err = json.Unmarshal(b, &root)
	return root, err
}

// tocRank orders the table of contents: Screen, Questions, other pinned
// pages, then response and custom pages (newest first within each rank).
func tocRank(page data.Record) int {
	switch {
	case isScreen(page):
		return 0
	case page.Text("pageKind") == "questions":
		return 1
	case page.Bool("pinned"):
		return 2
	}
	return 3
}

func compareTOCPages(a, b data.Record) int {
	if c := tocRank(a) - tocRank(b); c != 0 {
		return c
	}
	at, bt := a.UpdatedAt, b.UpdatedAt
	if at.IsZero() {
		at = a.CreatedAt
	}
	if bt.IsZero() {
		bt = b.CreatedAt
	}
	if c := bt.Compare(at); c != 0 {
		return c
	}
	return b.CreatedAt.Compare(a.CreatedAt)
}

// PaneSiteView renders a pane's interactive site: a table of contents on the
// left and the active page on the right.
func PaneSiteView(pane data.Record, pages []data.Record, activePageID string, tocFilter string) ui.ViewNode {
	return PaneSiteViewWith(pane, pages, activePageID, tocFilter, SiteOptions{})
}

// SiteOptions are the optional parts of a pane's site.
type SiteOptions struct {
	// LaneBars are the pane's lane trees (the store's PaneLaneBars), read by
	// the caller for the lane progress bars and the TOC entry; none shows none.
	LaneBars []data.LaneBarEntry
	// ShowLaneBar stacks the lane progress bars (LaneBarStack) above the site:
	// the pane's finished lane trees, then the tree it works in.
	ShowLaneBar bool
	// DismissedLaneBars holds the data.LaneBarEntry keys to leave out.
	DismissedLaneBars map[string]bool
	// CompactTopArea renders the lane progress bar as a prominent, concise
	// ui.KindProgress bar instead of the legacy verbose text strip.
	CompactTopArea bool
	// LaneSuffix appends an optional compact status badge (e.g. "● running")
	// to the compact lane progress bar suffix so the top area stays 1 line.
	LaneSuffix string
	// LaneSuffixTone sets the optional theme tone for the compact lane suffix.
	LaneSuffixTone string
	// LaneInTOC renders the pane's active lane as a compact 1-line row inside
	// the pane's Pages table of contents.
	LaneInTOC bool
	// CompactTOC renders TOC entries as crisp 1-line scannable rows instead of
	// multi-line bordered cards so 15+ agent pages fit cleanly.
	CompactTOC bool
	// TOCStyle overrides TOC presentation: "auto" (follows CompactTOC),
	// "compact" (1-line borderless rows), "tight" (bordered cards with explicit
	// [0,1,0,1] padding and inline badges in a scroll view), or "spacious"
	// (full bordered cards inheriting ui.density in a scroll view).
	TOCStyle string
	// TOCGrouped groups compact TOC entries into Pinned / Active / collapsible
	// Pages sections when no TOC filter is active.
	TOCGrouped bool
	// TOCCollapsed tracks collapsed TOC group keys (e.g. "pages").
	TOCCollapsed map[string]bool
	// MatchMode sets the traits.MatchMode used when filtering TOC pages.
	MatchMode traits.MatchMode
	// PageScrollOffset sets the vertical scroll offset for non-screen ui.page
	// content when WrapPageScroll is true.
	PageScrollOffset int
	// PageScrollLocked displays a reading-lock badge on the content panel while
	// ModePage is active.
	PageScrollLocked bool
	// WrapPageScroll wraps non-screen ui.page content inside a focusable
	// ui.KindScroll viewport (PageScrollID) honoring PageScrollOffset.
	WrapPageScroll bool
	// LaneHint names the key that opens the lane, shown at the end of the bar.
	LaneHint string
	// Width is the room the bar has, when known: 0 neither drops the hint
	// nor clips.
	Width int
	// HideTOC hides the left Pages table of contents rail and renders only the
	// active page content inside site.pane.
	HideTOC bool
}

func compactTOCRow(page data.Record, isActive, isUnread bool) ui.ViewNode {
	ref := page.Ref()
	marker := " "
	switch {
	case isActive:
		marker = "◆"
	case isUnread:
		marker = "●"
	}
	icon := "≡"
	switch {
	case isScreen(page):
		icon = "▣"
	case page.Text("pageKind") == "questions":
		icon = "?"
	case page.IsStarred():
		icon = "★"
	case page.Bool("pinned"):
		icon = "▪"
	}
	title := page.Text("title")
	lineText := fmt.Sprintf("%s %s %s", marker, icon, title)
	focusedText := fmt.Sprintf("▸ %s %s", icon, title)
	if page.IsStarred() && icon != "★" {
		lineText += " ★"
		focusedText += " ★"
	}
	tone := ""
	switch {
	case isActive:
		tone = "accent"
	case isUnread:
		tone = "warning"
	case !isScreen(page) && !page.Bool("pinned") && page.Text("pageKind") != "questions":
		tone = "muted"
	}
	return ui.ViewNode{
		ID:        "site.toc." + page.ID,
		Kind:      ui.KindText,
		Scope:     data.KindPage,
		Ref:       &ref,
		Focusable: true,
		Unread:    isUnread,
		Tone:      tone,
		Box:       layout.Constraints{FixedH: 1},
		Props:     map[string]any{"text": lineText, "focusedText": focusedText},
		Facts: map[string]any{
			"pageKind":            page.Text("pageKind"),
			"unread":              isUnread,
			"active":              isActive,
			"pinned":              page.Bool("pinned"),
			data.ColArchiveStatus: page.ArchiveStatus,
			data.ColStarStatus:    page.StarStatus,
			"snoozed":             page.IsSnoozed(time.Now()),
		},
	}
}

func cardTOCRow(page data.Record, isActive, isUnread bool) ui.ViewNode {
	ref := page.Ref()
	entry := ui.ViewNode{
		ID: "site.toc." + page.ID, Kind: ui.KindCard, Scope: data.KindPage, Ref: &ref, Focusable: true, Border: true,
		Title: page.Text("title"), Unread: isUnread,
		Facts: map[string]any{"pageKind": page.Text("pageKind"), "unread": isUnread, "active": isActive,
			"pinned": page.Bool("pinned"), data.ColArchiveStatus: page.ArchiveStatus, data.ColStarStatus: page.StarStatus,
			"snoozed": page.IsSnoozed(time.Now())},
	}
	if isActive {
		entry.Tone = "accent"
		entry.Badges = append(entry.Badges, ui.Badge{Text: "open", Tone: "accent"})
	}
	if isUnread {
		entry.Badges = append(entry.Badges, ui.Badge{Text: "Unread", Tone: "warning"})
	}
	if page.Bool("pinned") {
		entry.Badges = append(entry.Badges, ui.Badge{Text: "pinned"})
	}
	if page.IsStarred() {
		entry.Badges = append(entry.Badges, ui.Badge{Text: "★", Tone: "warning"})
	}
	return entry
}

func tightCardTOCRow(page data.Record, isActive, isUnread bool) ui.ViewNode {
	ref := page.Ref()
	title := page.Text("title")
	var tags []string
	if isActive {
		tags = append(tags, "open")
	}
	if isUnread {
		tags = append(tags, "unread")
	}
	if page.Bool("pinned") {
		tags = append(tags, "pin")
	}
	if page.IsStarred() {
		tags = append(tags, "★")
	}
	if len(tags) > 0 {
		title += " · " + strings.Join(tags, " ")
	}
	tone := ""
	if isActive {
		tone = "accent"
	}
	return ui.ViewNode{
		ID:        "site.toc." + page.ID,
		Kind:      ui.KindCard,
		Scope:     data.KindPage,
		Ref:       &ref,
		Focusable: true,
		Border:    true,
		Padding:   [4]int{0, 1, 0, 1},
		Title:     title,
		Unread:    isUnread,
		Tone:      tone,
		Facts: map[string]any{
			"pageKind":            page.Text("pageKind"),
			"unread":              isUnread,
			"active":              isActive,
			"pinned":              page.Bool("pinned"),
			data.ColArchiveStatus: page.ArchiveStatus,
			data.ColStarStatus:    page.StarStatus,
			"snoozed":             page.IsSnoozed(time.Now()),
		},
	}
}

func resolveTOCStyle(opts SiteOptions) (string, bool) {
	switch opts.TOCStyle {
	case TOCStyleCompact:
		return TOCStyleCompact, true
	case TOCStyleTight:
		return TOCStyleTight, true
	case TOCStyleSpacious:
		return TOCStyleSpacious, true
	default:
		if opts.CompactTOC {
			return TOCStyleCompact, true
		}
		return TOCStyleSpacious, false
	}
}

func sortSitePages(pane data.Record, pages []data.Record, activePageID string) ([]data.Record, data.Record) {
	sorted := slices.Clone(pages)
	slices.Reverse(sorted)
	slices.SortStableFunc(sorted, compareTOCPages)
	if !slices.ContainsFunc(sorted, isScreen) {
		screen := data.Record{Kind: data.KindPage, ID: ScreenPageID, CreatedAt: pane.CreatedAt, UpdatedAt: pane.CreatedAt,
			ArchiveStatus: data.ArchiveActive, StarStatus: data.StarUnstarred,
			Fields: map[string]any{"title": "Screen", "slug": "screen", "pageKind": "screen", "pinned": true,
				"viewedAt": pane.CreatedAt.Format(data.TimeFormat)}}
		sorted = append([]data.Record{screen}, sorted...)
	}
	active := sorted[0]
	for _, page := range sorted {
		if page.ID == activePageID {
			active = page
		}
	}
	return sorted, active
}

func activeLaneTOCEntry(opts SiteOptions) (ui.ViewNode, string, bool) {
	if !opts.LaneInTOC {
		return ui.ViewNode{}, "", false
	}
	activeLane := slices.IndexFunc(opts.LaneBars, func(e data.LaneBarEntry) bool { return !e.Completed })
	if activeLane < 0 {
		return ui.ViewNode{}, "", false
	}
	roll := opts.LaneBars[activeLane].Roll
	return LaneTOCEntry(roll), roll.Root.ID, true
}

func compactTOCSection(page data.Record, isActive, isUnread bool) string {
	switch {
	case isScreen(page):
		return "screen"
	case page.Text("pageKind") == "questions" || page.Bool("pinned"):
		return "pinned"
	case isUnread || isActive:
		return "active"
	default:
		return "older"
	}
}

func buildGroupedCompactTOCEntries(matched []data.Record, active data.Record, opts SiteOptions, hasLaneTOC bool, laneRow ui.ViewNode) []ui.ViewNode {
	var pinnedNodes, activeNodes, olderNodes []ui.ViewNode
	for _, page := range matched {
		isActive := page.ID == active.ID
		isUnread := !isScreen(page) && page.IsUnread()
		node := compactTOCRow(page, isActive, isUnread)
		switch compactTOCSection(page, isActive, isUnread) {
		case "screen":
			pinnedNodes = append(pinnedNodes, node)
			if hasLaneTOC {
				pinnedNodes = append(pinnedNodes, laneRow)
				hasLaneTOC = false
			}
		case "pinned":
			pinnedNodes = append(pinnedNodes, node)
		case "active":
			activeNodes = append(activeNodes, node)
		default:
			olderNodes = append(olderNodes, node)
		}
	}
	if hasLaneTOC {
		pinnedNodes = append(pinnedNodes, laneRow)
	}
	entries := append(pinnedNodes, activeNodes...)
	if len(olderNodes) > 0 {
		collapsed := opts.TOCCollapsed["pages"]
		entries = append(entries, tocGroupHeader("pages", "Pages", len(olderNodes), collapsed))
		if !collapsed {
			entries = append(entries, olderNodes...)
		}
	}
	return entries
}

func buildFlatTOCEntries(matched []data.Record, active data.Record, rowFn func(data.Record, bool, bool) ui.ViewNode, filter string, hasLaneTOC bool, laneRow ui.ViewNode) []ui.ViewNode {
	var entries []ui.ViewNode
	for _, page := range matched {
		isActive := page.ID == active.ID
		isUnread := !isScreen(page) && page.IsUnread()
		entries = append(entries, rowFn(page, isActive, isUnread))
		if isScreen(page) && hasLaneTOC && filter == "" {
			entries = append(entries, laneRow)
			hasLaneTOC = false
		}
	}
	if hasLaneTOC && filter == "" {
		entries = append(entries, laneRow)
	}
	return entries
}

func buildTOCEntries(style string, matched []data.Record, active data.Record, opts SiteOptions, filter string, hasLaneTOC bool, laneRow ui.ViewNode) []ui.ViewNode {
	switch style {
	case TOCStyleCompact:
		if opts.TOCGrouped && filter == "" {
			return buildGroupedCompactTOCEntries(matched, active, opts, hasLaneTOC, laneRow)
		}
		return buildFlatTOCEntries(matched, active, compactTOCRow, filter, hasLaneTOC, laneRow)
	case TOCStyleTight:
		return buildFlatTOCEntries(matched, active, tightCardTOCRow, filter, hasLaneTOC, laneRow)
	default:
		return buildFlatTOCEntries(matched, active, cardTOCRow, filter, hasLaneTOC, laneRow)
	}
}

func buildTOCPanel(pane data.Record, style string, wrapScroll bool, entries []ui.ViewNode, tocFilter, filter string, unread int) ui.ViewNode {
	tocBadges := []ui.Badge{}
	if unread > 0 {
		tocBadges = append(tocBadges, ui.Badge{Text: itoa(unread) + " unread", Tone: "warning"})
	}
	if filter != "" {
		tocBadges = append(tocBadges, ui.Badge{Text: "/" + tocFilter, Tone: "accent"})
		if len(entries) == 0 {
			entries = []ui.ViewNode{{
				ID:        "site.toc.empty",
				Kind:      ui.KindText,
				Scope:     ScopeTOC,
				Focusable: true,
				Tone:      "muted",
				Props:     map[string]any{"text": "no page matches"},
			}}
		}
	}
	tocBody := ui.ViewNode{Kind: ui.KindVStack, Children: entries}
	if wrapScroll {
		tocBody = ui.ViewNode{
			ID:       "site.toc.scroll",
			Kind:     ui.KindScroll,
			Props:    map[string]any{"scrollKey": data.RefText(pane.Ref())},
			Children: []ui.ViewNode{tocBody},
		}
	}
	return ui.ViewNode{ID: "site.toc", Kind: ui.KindPanel, Scope: ScopeTOC,
		Filter: &ui.FilterSpec{ID: "toc", Prefix: "pages "},
		Title:  "Pages", Border: true, Badges: tocBadges,
		Facts:    map[string]any{"siteTOC": true, "tocStyle": style, "filter": tocFilter, "hasFilter": filter != "", "unread": unread},
		Children: []ui.ViewNode{tocBody}}
}

func buildSiteContent(pane, active data.Record, opts SiteOptions) ui.ViewNode {
	activeRef := active.Ref()
	content := ui.ViewNode{ID: "site.content", Kind: ui.KindPanel, Scope: data.KindPage, Ref: &activeRef, Border: true,
		Title: active.Text("title"), Facts: map[string]any{"siteContent": true, "pageKind": active.Text("pageKind"), "screen": isScreen(active)}}
	if isScreen(active) {
		content.Children = []ui.ViewNode{{ID: "site.screen", Kind: ui.KindTerminal, Scope: "terminal", Focusable: true,
			Props: map[string]any{"streamId": pane.ID}}}
		content.Badges = []ui.Badge{{Text: "live", Tone: "success"}}
		return content
	}
	root, err := pageRoot(active)
	if err != nil {
		content.Children = []ui.ViewNode{text("", "This page's tree cannot be read: "+err.Error(), "error")}
		return content
	}
	if !opts.WrapPageScroll {
		content.Children = []ui.ViewNode{root}
		return content
	}
	if opts.PageScrollLocked {
		content.Badges = append(content.Badges, ui.Badge{Text: "reading · esc leaves", Tone: "accent"})
	}
	if root.Kind == ui.KindScroll {
		root.ID = PageScrollID
		root.Focusable = true
		if root.Scope == "" {
			root.Scope = data.KindPage
		}
		if root.Props == nil {
			root.Props = map[string]any{}
		}
		root.Props["offset"] = opts.PageScrollOffset
		content.Children = []ui.ViewNode{root}
		return content
	}
	content.Children = []ui.ViewNode{{
		ID:        PageScrollID,
		Kind:      ui.KindScroll,
		Scope:     data.KindPage,
		Focusable: true,
		Props:     map[string]any{"offset": opts.PageScrollOffset},
		Children:  []ui.ViewNode{root},
	}}
	return content
}

func tocGroupHeader(groupKey, label string, count int, collapsed bool) ui.ViewNode {
	glyph := "▾"
	if collapsed {
		glyph = "▸"
	}
	return ui.ViewNode{
		ID:        TOCGroupPrefix + groupKey,
		Kind:      ui.KindText,
		Scope:     ScopeTOC,
		Focusable: true,
		Tone:      "muted",
		Box:       layout.Constraints{FixedH: 1},
		Props:     map[string]any{"text": fmt.Sprintf("%s %s (%d)", glyph, label, count)},
		Facts:     map[string]any{"tocGroup": groupKey, "collapsed": collapsed},
	}
}

func filterTOCPages(sorted []data.Record, tocFilter string, mode traits.MatchMode) ([]data.Record, int) {
	filter := strings.TrimSpace(tocFilter)
	var mm traits.MultiMatcher
	if filter != "" {
		mm, _ = traits.CompileMultiTerm(tocFilter, mode, traits.FilterConfig{})
	}
	unread := 0
	var matched []data.Record
	for _, page := range sorted {
		if !isScreen(page) && page.IsUnread() {
			unread++
		}
		if filter != "" && !mm.Match(traits.FormatFilterableText("title", page.Text("title"), "slug", page.Text("slug"))) {
			continue
		}
		matched = append(matched, page)
	}
	return matched, unread
}

// PaneSiteViewWith is PaneSiteView with options: lane bar / lane-in-TOC,
// compact/tight/spacious TOC, and scrollable ui.page wrapping.
func PaneSiteViewWith(pane data.Record, pages []data.Record, activePageID string, tocFilter string, opts SiteOptions) ui.ViewNode {
	paneRef := pane.Ref()
	paneTitle := Title(data.Schema{SummaryField: "title"}, pane)
	sorted, active := sortSitePages(pane, pages, activePageID)
	laneRow, laneID, hasLaneTOC := activeLaneTOCEntry(opts)

	filter := strings.TrimSpace(tocFilter)
	matched, unread := filterTOCPages(sorted, tocFilter, opts.MatchMode)
	style, wrapScroll := resolveTOCStyle(opts)
	entries := buildTOCEntries(style, matched, active, opts, filter, hasLaneTOC, laneRow)
	toc := buildTOCPanel(pane, style, wrapScroll, entries, tocFilter, filter, unread)
	content := buildSiteContent(pane, active, opts)

	tocRatio := 0.28
	if style == TOCStyleCompact {
		tocRatio = 0.24
	}
	paneBody := ui.ViewNode{ID: "site.pane", Kind: ui.KindSplit, Scope: data.KindPane, Ref: &paneRef, Ratio: tocRatio, Gap: 1,
		Children: []ui.ViewNode{toc, content}}
	if opts.HideTOC {
		paneBody = ui.ViewNode{ID: "site.pane", Kind: ui.KindVStack, Scope: data.KindPane, Ref: &paneRef,
			Children: []ui.ViewNode{content}}
	}
	site := ui.ViewNode{ID: "site", Kind: ui.KindVStack, Scope: ScopeSite,
		Facts:    map[string]any{"activePage": active.ID, "unread": unread, "pane": paneTitle},
		Children: []ui.ViewNode{paneBody}}
	if laneID != "" {
		site.Facts["lane"] = laneID
	}
	if opts.ShowLaneBar {
		stack, ok := LaneBarStack(opts.LaneBars, LaneBarStackOptions{
			Compact: opts.CompactTopArea, Hint: opts.LaneHint, Width: opts.Width,
			Suffix: opts.LaneSuffix, SuffixTone: opts.LaneSuffixTone,
			Dismissed: opts.DismissedLaneBars,
		})
		if ok {
			if active := stack.Find(LaneBarID); active != nil {
				site.Facts["lane"] = active.Facts["lane"]
			}
			site.Children = append([]ui.ViewNode{stack}, site.Children...)
		}
	}
	return site
}
