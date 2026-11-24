package views

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// ScreenPageID is the ID of the Screen entry a pane's site always has, even
// before any ui.page record with pageKind "screen" exists.
const ScreenPageID = "screen"

// PageScrollID is the scrollable viewport node ID wrapping non-screen ui.page content.
const PageScrollID = "site.page.scroll"

// TOCGroupPrefix prefixes collapsible group headers inside a compact pane TOC.
const TOCGroupPrefix = "site.toc.group."

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
// pages, then response and custom pages in the order they were made.
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

// PaneSiteView renders a pane's interactive site: a table of contents on the
// left and the active page on the right.
func PaneSiteView(pane data.Record, pages []data.Record, activePageID string, tocFilter string) ui.ViewNode {
	return PaneSiteViewWith(pane, pages, activePageID, tocFilter, SiteOptions{})
}

// SiteOptions are the optional parts of a pane's site.
type SiteOptions struct {
	// Store is read for the lane progress bar / TOC entry; nil shows none.
	Store *data.Store
	// ShowLaneBar puts the ambient lane progress bar (LaneBar) as one line
	// above the site when the pane works in an active lane tree.
	ShowLaneBar bool
	// LaneInTOC renders the pane's active lane as a compact 1-line row inside
	// the pane's Pages table of contents.
	LaneInTOC bool
	// CompactTOC renders TOC entries as crisp 1-line scannable rows instead of
	// multi-line bordered cards so 15+ agent pages fit cleanly.
	CompactTOC bool
	// TOCGrouped groups compact TOC entries into Pinned / Active / collapsible
	// Pages sections when no TOC filter is active.
	TOCGrouped bool
	// TOCCollapsed tracks collapsed TOC group keys (e.g. "pages").
	TOCCollapsed map[string]bool
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

// PaneSiteViewWith is PaneSiteView with options: lane bar / lane-in-TOC,
// compact grouped TOC, and scrollable ui.page wrapping.
func PaneSiteViewWith(pane data.Record, pages []data.Record, activePageID string, tocFilter string, opts SiteOptions) ui.ViewNode {
	paneRef := pane.Ref()
	paneTitle := Title(data.Schema{SummaryField: "title"}, pane)

	sorted := slices.Clone(pages)
	slices.SortStableFunc(sorted, func(a, b data.Record) int { return tocRank(a) - tocRank(b) })
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

	var laneRow ui.ViewNode
	laneID := ""
	hasLaneTOC := false
	if opts.Store != nil && opts.LaneInTOC {
		laneRow, laneID, hasLaneTOC = LaneTOCEntry(opts.Store, pane)
	}

	filter := strings.ToLower(strings.TrimSpace(tocFilter))
	unread := 0
	var matched []data.Record
	for _, page := range sorted {
		isUnread := !isScreen(page) && page.IsUnread()
		if isUnread {
			unread++
		}
		title := page.Text("title")
		if filter != "" && !strings.Contains(strings.ToLower(title+" "+page.Text("slug")), filter) {
			continue
		}
		matched = append(matched, page)
	}

	var entries []ui.ViewNode
	if opts.CompactTOC {
		if opts.TOCGrouped && filter == "" {
			var pinnedNodes, activeNodes, olderNodes []ui.ViewNode
			for _, page := range matched {
				isActive := page.ID == active.ID
				isUnread := !isScreen(page) && page.IsUnread()
				node := compactTOCRow(page, isActive, isUnread)
				switch {
				case isScreen(page):
					pinnedNodes = append(pinnedNodes, node)
					if hasLaneTOC {
						pinnedNodes = append(pinnedNodes, laneRow)
						hasLaneTOC = false
					}
				case page.Text("pageKind") == "questions" || page.Bool("pinned"):
					pinnedNodes = append(pinnedNodes, node)
				case isUnread || isActive:
					activeNodes = append(activeNodes, node)
				default:
					olderNodes = append(olderNodes, node)
				}
			}
			if hasLaneTOC {
				pinnedNodes = append(pinnedNodes, laneRow)
			}
			entries = append(entries, pinnedNodes...)
			if len(activeNodes) > 0 {
				entries = append(entries, activeNodes...)
			}
			if len(olderNodes) > 0 {
				collapsed := opts.TOCCollapsed["pages"]
				entries = append(entries, tocGroupHeader("pages", "Pages", len(olderNodes), collapsed))
				if !collapsed {
					entries = append(entries, olderNodes...)
				}
			}
		} else {
			for _, page := range matched {
				isActive := page.ID == active.ID
				isUnread := !isScreen(page) && page.IsUnread()
				entries = append(entries, compactTOCRow(page, isActive, isUnread))
				if isScreen(page) && hasLaneTOC && filter == "" {
					entries = append(entries, laneRow)
					hasLaneTOC = false
				}
			}
			if hasLaneTOC && filter == "" {
				entries = append(entries, laneRow)
			}
		}
	} else {
		for _, page := range matched {
			isActive := page.ID == active.ID
			isUnread := !isScreen(page) && page.IsUnread()
			entries = append(entries, cardTOCRow(page, isActive, isUnread))
			if isScreen(page) && hasLaneTOC && filter == "" {
				entries = append(entries, laneRow)
				hasLaneTOC = false
			}
		}
		if hasLaneTOC && filter == "" {
			entries = append(entries, laneRow)
		}
	}

	tocBadges := []ui.Badge{}
	if unread > 0 {
		tocBadges = append(tocBadges, ui.Badge{Text: itoa(unread) + " unread", Tone: "warning"})
	}
	if filter != "" {
		tocBadges = append(tocBadges, ui.Badge{Text: "/" + tocFilter, Tone: "accent"})
		if len(entries) == 0 {
			entries = []ui.ViewNode{text("", "no page matches", "muted")}
		}
	}
	tocBody := ui.ViewNode{Kind: ui.KindVStack, Children: entries}
	if opts.CompactTOC {
		tocBody = ui.ViewNode{
			ID:       "site.toc.scroll",
			Kind:     ui.KindScroll,
			Children: []ui.ViewNode{tocBody},
		}
	}
	toc := ui.ViewNode{ID: "site.toc", Kind: ui.KindPanel, Scope: ScopeTOC, Title: "Pages", Border: true, Badges: tocBadges,
		Facts:    map[string]any{"filter": tocFilter, "unread": unread},
		Children: []ui.ViewNode{tocBody}}

	activeRef := active.Ref()
	content := ui.ViewNode{ID: "site.content", Kind: ui.KindPanel, Scope: data.KindPage, Ref: &activeRef, Border: true,
		Title: active.Text("title"), Facts: map[string]any{"pageKind": active.Text("pageKind"), "screen": isScreen(active)}}
	if isScreen(active) {
		content.Children = []ui.ViewNode{{ID: "site.screen", Kind: ui.KindTerminal, Scope: "terminal", Focusable: true,
			Props: map[string]any{"streamId": pane.ID}}}
		content.Badges = []ui.Badge{{Text: "live", Tone: "success"}}
	} else if root, err := pageRoot(active); err != nil {
		content.Children = []ui.ViewNode{text("", "This page's tree cannot be read: "+err.Error(), "error")}
	} else if opts.WrapPageScroll {
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
		} else {
			content.Children = []ui.ViewNode{{
				ID:        PageScrollID,
				Kind:      ui.KindScroll,
				Scope:     data.KindPage,
				Focusable: true,
				Props:     map[string]any{"offset": opts.PageScrollOffset},
				Children:  []ui.ViewNode{root},
			}}
		}
	} else {
		content.Children = []ui.ViewNode{root}
	}

	tocRatio := 0.28
	if opts.CompactTOC {
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
		if bar, ok := LaneBar(opts.Store, pane, opts.LaneHint, opts.Width); ok {
			site.Facts["lane"] = bar.Facts["lane"]
			site.Children = append([]ui.ViewNode{bar}, site.Children...)
		}
	}
	return site
}
