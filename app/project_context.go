package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

const projectContextReviewTable = "project.context.review.table"

var projectContextColumns = []table.Column{
	{Key: "tier", Title: "Tier", Width: 17},
	{Key: "kind", Title: "Kind", Width: 6},
	{Key: "value", Title: "Value"},
	{Key: "status", Title: "Status", Width: 18},
	{Key: "source", Title: "Source", Width: 10},
}

var projectContextReviewColumns = []table.Column{
	{Key: "value", Title: "Value"},
	{Key: "kind", Title: "Kind", Width: 6},
}

func (a *App) projectContextReviewEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectContextReview, true)
}

func (a *App) isProjectContextUnread(rec data.Record) bool {
	if rec.ID == "" || rec.IsArchived() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(rec.Text("source"))) {
	case "prompt", "screen":
		return (rec.Bool("official") || rec.Bool("pinned")) && a.isGenericNavRecordUnread(rec)
	default:
		return a.isGenericNavRecordUnread(rec)
	}
}

func (a *App) projectSectionContext(ws projectWorkspaceData) ui.ViewNode {
	badges := []ui.Badge{}
	if ws.officialCount > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("★ %d official", ws.officialCount), Tone: "accent"})
	}
	badges = append(badges, ui.Badge{Text: "p official · N add", Tone: "muted"})
	full := a.projectContextPanel("project.context", projectContextTable, "context", "Context · Official & Collected", ws, ws.contextItems, projectContextColumns, badges)
	full.Children[0].Facts["projectOverview"] = true
	if !a.projectContextReviewEnabled() || len(ws.reviewContext) == 0 {
		return full
	}
	review := a.projectContextPanel("project.context.review", projectContextReviewTable, "context", "New Items for Review", ws, ws.reviewContext, projectContextReviewColumns,
		[]ui.Badge{{Text: "r mark read", Tone: "muted"}})
	// Two table header lines plus the panel border leave up to three visible
	// review rows. The table can navigate to the rest.
	review.Box = layout.Constraints{MaxH: min(3, len(ws.reviewContext)) + 4}
	full.Box.Flex = 2
	return ui.ViewNode{
		ID: "project.context.sections", Kind: ui.KindVStack,
		Children: []ui.ViewNode{review, full},
	}
}

func (a *App) projectContextTableModel(ws projectWorkspaceData) *table.Model {
	return a.buildContextSectionTable(projectContextTable, "context", ws, ws.contextItems, projectContextColumns)
}

func (a *App) buildContextSectionTable(tableID, section string, ws projectWorkspaceData, items []CollectedContextItem, cols []table.Column) *table.Model {
	cacheKey := fmt.Sprintf("ctx|%s|%s|%d|%d|%v|%s|%d", tableID, ws.projectKey, ws.contextRev, ws.cacheCtxGen, a.nav.showArchived, ws.cacheRailKey, len(items))
	if tbl, ok := a.cachedProjectTable(tableID, cacheKey); ok && (len(items) == 0 || len(a.panes.projectCtxRows) > 0) {
		a.panes.projectCtxGroup, a.panes.projectCtxRef = ws.groupName, ws.projRef
		return tbl
	}
	rows := make([]table.Row, len(items))
	if a.panes.projectCtxRows == nil {
		a.panes.projectCtxRows = map[string]CollectedContextItem{}
	}
	for i, it := range items {
		tier, status, rowTone := "Collected", "", ""
		var badges []ui.Badge
		switch {
		case it.Official:
			tier, status, rowTone = "★ Official (Auto)", "[official · auto]", "accent"
			badges = append(badges, ui.Badge{Text: "official · auto", Tone: "accent"})
		case it.Pinned:
			status = "[pinned]"
			badges = append(badges, ui.Badge{Text: "pinned", Tone: "info"})
		case it.Starred:
			status = "★"
		}
		display := it.Value
		if it.Kind == contextitems.KindCode {
			display = singleLineContextText(it.Label)
			if display == "" {
				display = data.CodeContextTitle(it.Value, it.Language)
			}
		} else if lbl := strings.TrimSpace(it.Label); lbl != "" && lbl != it.Value {
			display = fmt.Sprintf("%s — %s", lbl, it.Value)
		}
		if notes := strings.TrimSpace(it.Notes); notes != "" {
			display += " (" + clip(notes, 32) + ")"
		}
		ref := core.Ref{Kind: "context." + it.Kind, ID: it.Value}
		if it.RecordID != "" {
			ref = core.Ref{Kind: data.KindContext, ID: it.RecordID}
		}
		rec, present := ws.contextRecords[it.RecordID]
		unread := present && a.isProjectContextUnread(rec)
		rows[i] = table.Row{
			ID: it.Key, Ref: &ref, Scope: ScopeProjectContext, Tone: rowTone, Badges: badges, Unread: unread,
			Cells: map[string]any{
				"tier": tier, "kind": it.Kind, "value": display, "status": status, "pane": it.PaneTitle,
				"source": it.Source, "label": it.Label, "notes": it.Notes, "project": it.Project,
				views.AnyField: strings.Join([]string{tier, it.Kind, it.Language, it.Value, it.Label, it.Notes, it.PaneTitle, it.Source, status}, " "),
			},
			Facts: map[string]any{
				"contextItem": true, "contextKey": it.Key, "contextKind": it.Kind,
				"starred": it.Starred, "official": it.Official, "pinned": it.Pinned,
				"recordId": it.RecordID, "section": section, "unread": unread,
			},
		}
		a.panes.projectCtxRows[it.Key] = it
	}
	a.panes.projectCtxGroup, a.panes.projectCtxRef = ws.groupName, ws.projRef
	tbl := a.keepProjectTable(tableID, table.New(cols, rows))
	a.storeProjectTableKey(tableID, cacheKey)
	return tbl
}

func (a *App) projectContextPanel(panelID, tableID, section, title string, ws projectWorkspaceData, items []CollectedContextItem, cols []table.Column, extra []ui.Badge) ui.ViewNode {
	tbl := a.buildContextSectionTable(tableID, section, ws, items, cols)
	node := tbl.View(tableID)
	node.Scope = ScopeProjectContext
	if node.Facts == nil {
		node.Facts = map[string]any{}
	}
	node.Facts["section"] = section
	badges := append([]ui.Badge{{Text: fmt.Sprintf("%d/%d", tableFilteredCount(node, tbl), len(tbl.Rows))}}, extra...)
	if tbl.Query != "" {
		badges = append(badges, ui.Badge{Text: "/" + tbl.Query, Tone: "accent"})
	}
	panel := ui.ViewNode{
		ID: panelID, Kind: ui.KindPanel, Scope: ScopeProjectContext, Filter: &ui.FilterSpec{ID: tableID}, Title: title, Border: true,
		Badges: badges, Facts: map[string]any{"section": section}, Children: []ui.ViewNode{node},
	}
	if a.livePointerChipsEnabled() && (a.Focus() == tableID || strings.HasPrefix(a.Focus(), panelID+".chips")) {
		if row, ok := tbl.FocusedRow(); ok && row.Ref != nil {
			panel = withPointerChips(panel, panelID+".chips", a.contextPointerChips(a.panes.projectCtxRows[row.ID], *row.Ref))
		}
	}
	return panel
}

func (a *App) projectUnifiedContextItems(ws projectWorkspaceData, scope data.ProjectScope, paneTitles map[string]string) (items, review []CollectedContextItem, records map[string]data.Record) {
	items = a.projectContextItems(ws.groupName, ws.projRef, ws.paneIDs)
	if a.Store == nil {
		return items, nil, nil
	}
	persisted, err := a.Store.ProjectContextRecords(scope, a.nav.showArchived)
	if err != nil {
		return items, nil, nil
	}
	records = make(map[string]data.Record, len(persisted))
	for _, rec := range persisted {
		records[rec.ID] = rec
	}
	items = slices.DeleteFunc(items, func(item CollectedContextItem) bool {
		_, inScope := records[item.RecordID]
		return item.RecordID != "" && !inScope
	})
	byVal := make(map[string]int, len(items)*2)
	for i, it := range items {
		byVal[strings.ToLower(strings.TrimSpace(it.Value))] = i
		if it.RecordID != "" {
			byVal["id:"+it.RecordID] = i
		}
	}
	chosen := map[string]bool{}
	for _, rec := range persisted {
		rawVal := rec.Text("value")
		val := strings.TrimSpace(rawVal)
		if val == "" {
			continue
		}
		kind := rec.Text("contextKind")
		if kind == "" {
			kind = data.ClassifyContextKind(val, "")
		}
		if kind == contextitems.KindCode {
			val = strings.Trim(strings.ReplaceAll(rawVal, "\r\n", "\n"), "\n")
		}
		canon := strings.TrimSpace(rec.Text("canonKey"))
		if canon == "" {
			canon = contextCanonKey(kind, val)
		}
		item := CollectedContextItem{
			RecordID: rec.ID, Key: "rec\x00" + rec.ID, CanonKey: canon, Kind: kind, Value: val,
			Label: rec.Text("title"), Language: rec.Text("language"), Source: rec.Text("source"), ProjectID: ws.projectID, Project: ws.groupName,
			Starred: rec.IsStarred() || a.isContextStarred(canon), Official: rec.Bool("official"), Pinned: rec.Bool("pinned"),
			Notes: rec.Text("notes"), SeenAt: rec.UpdatedAt,
		}
		if pRef, ok := rec.RefField("pane"); ok {
			title, found := paneTitles[pRef.ID]
			if !found {
				title = a.title(pRef)
				if paneTitles != nil {
					paneTitles[pRef.ID] = title
				}
			}
			item.PaneID, item.PaneTitle = pRef.ID, title
		}
		if a.isProjectContextUnread(rec) {
			review = append(review, item)
		}
		valueKey := strings.ToLower(val)
		if chosen[valueKey] {
			continue
		}
		chosen[valueKey] = true
		if idx, found := byVal["id:"+rec.ID]; found {
			items[idx] = mergeContextItem(items[idx], item)
			continue
		}
		if idx, found := byVal[valueKey]; found {
			items[idx] = mergeContextItem(items[idx], item)
			continue
		}
		byVal["id:"+rec.ID], byVal[valueKey] = len(items), len(items)
		items = append(items, item)
	}
	sortCollectedContext(items)
	sortReviewContextNewestFirst(review)
	return items, review, records
}

func sortReviewContextNewestFirst(review []CollectedContextItem) {
	slices.Reverse(review)
	slices.SortStableFunc(review, func(x, y CollectedContextItem) int {
		return y.SeenAt.Compare(x.SeenAt)
	})
}

func mergeContextItem(harvested, persisted CollectedContextItem) CollectedContextItem {
	out := persisted
	if harvested.Key != "" {
		out.Key = harvested.Key
	}
	if out.RecordID == "" {
		out.RecordID = harvested.RecordID
	}
	if out.Label == "" {
		out.Label = harvested.Label
	}
	if out.Language == "" {
		out.Language = harvested.Language
	}
	if out.PaneID == "" {
		out.PaneID, out.PaneTitle = harvested.PaneID, harvested.PaneTitle
	}
	if out.Notes == "" {
		out.Notes = harvested.Notes
	}
	out.Starred = harvested.Starred || persisted.Starred
	if harvested.SeenAt.After(out.SeenAt) {
		out.SeenAt = harvested.SeenAt
	}
	return out
}
