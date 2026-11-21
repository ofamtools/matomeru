package views

import (
	"encoding/json"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

// ScreenPageID is the ID of the Screen entry a pane's site always has, even
// before any ui.page record with pageKind "screen" exists.
const ScreenPageID = "screen"

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
//
// The table of contents lists the pinned Screen page first (always present),
// then Questions with an unread badge, then the other pages, narrowed by
// tocFilter (matched against title and slug, ignoring case). Each entry is
// focusable with Scope "ui.page" and its page's Ref. The content panel
// renders the active page's stored ViewNode tree, or the live terminal
// placeholder when the Screen page is active (activePageID empty, "screen",
// or a screen page's ID). Focus anywhere in the site has the pane in its
// context chain: page, then pane, then site, then app.
func PaneSiteView(pane data.Record, pages []data.Record, activePageID string, tocFilter string) ui.ViewNode {
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

	filter := strings.ToLower(strings.TrimSpace(tocFilter))
	var entries []ui.ViewNode
	unread := 0
	for _, page := range sorted {
		isUnread := !isScreen(page) && page.IsUnread()
		if isUnread {
			unread++
		}
		title := page.Text("title")
		if filter != "" && !strings.Contains(strings.ToLower(title+" "+page.Text("slug")), filter) {
			continue
		}
		ref := page.Ref()
		entry := ui.ViewNode{
			ID: "site.toc." + page.ID, Kind: ui.KindCard, Scope: data.KindPage, Ref: &ref, Focusable: true, Border: true,
			Title: title, Unread: isUnread,
			Facts: map[string]any{"pageKind": page.Text("pageKind"), "unread": isUnread, "active": page.ID == active.ID,
				"pinned": page.Bool("pinned"), data.ColArchiveStatus: page.ArchiveStatus, data.ColStarStatus: page.StarStatus},
		}
		if page.ID == active.ID {
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
		entries = append(entries, entry)
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
	toc := ui.ViewNode{ID: "site.toc", Kind: ui.KindPanel, Scope: ScopeTOC, Title: "Pages", Border: true, Badges: tocBadges,
		Facts:    map[string]any{"filter": tocFilter, "unread": unread},
		Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: entries}}}

	activeRef := active.Ref()
	content := ui.ViewNode{ID: "site.content", Kind: ui.KindPanel, Scope: data.KindPage, Ref: &activeRef, Border: true,
		Title: active.Text("title"), Facts: map[string]any{"pageKind": active.Text("pageKind"), "screen": isScreen(active)}}
	if isScreen(active) {
		content.Children = []ui.ViewNode{{ID: "site.screen", Kind: ui.KindTerminal, Scope: "terminal", Focusable: true,
			Props: map[string]any{"streamId": pane.ID}}}
		content.Badges = []ui.Badge{{Text: "live", Tone: "success"}}
	} else if root, err := pageRoot(active); err != nil {
		content.Children = []ui.ViewNode{text("", "This page's tree cannot be read: "+err.Error(), "error")}
	} else {
		content.Children = []ui.ViewNode{root}
	}

	return ui.ViewNode{ID: "site", Kind: ui.KindVStack, Scope: ScopeSite,
		Facts: map[string]any{"activePage": active.ID, "unread": unread, "pane": paneTitle},
		Children: []ui.ViewNode{{ID: "site.pane", Kind: ui.KindSplit, Scope: data.KindPane, Ref: &paneRef, Ratio: 0.28, Gap: 1,
			Children: []ui.ViewNode{toc, content}}}}
}
