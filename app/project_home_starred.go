package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
)

// homeKindRecords maps a Project nav section key to the workspace records it
// lists. It is the only per-section data the starred-grid Home needs: the
// order, label and icon of each card come from projectNavSectionDefs (so
// projects.navSections reorders Home too), and titles come from each
// record's schema.
var homeKindRecords = map[string]func(ws projectWorkspaceData) []data.Record{
	"panes":     func(ws projectWorkspaceData) []data.Record { return ws.panes },
	"tasks":     func(ws projectWorkspaceData) []data.Record { return ws.tasks },
	"lanes":     func(ws projectWorkspaceData) []data.Record { return ws.allLanes },
	"pages":     func(ws projectWorkspaceData) []data.Record { return ws.pages },
	"context":   homeContextRecords,
	"documents": func(ws projectWorkspaceData) []data.Record { return ws.documents },
	"notes":     func(ws projectWorkspaceData) []data.Record { return ws.notes },
	"questions": func(ws projectWorkspaceData) []data.Record { return ws.questions },
	"commands":  func(ws projectWorkspaceData) []data.Record { return ws.commands },
}

func homeContextRecords(ws projectWorkspaceData) []data.Record {
	if len(ws.contextRecords) == 0 {
		return nil
	}
	out := make([]data.Record, 0, len(ws.contextRecords))
	seen := make(map[string]bool, len(ws.contextRecords))
	for _, it := range ws.contextItems {
		if it.RecordID == "" || seen[it.RecordID] {
			continue
		}
		if rec, ok := ws.contextRecords[it.RecordID]; ok {
			seen[it.RecordID] = true
			out = append(out, rec)
		}
	}
	return out
}

// homeStarredLayout projects Portal Home's starred grid (starred records
// grouped by kind) and one list card per non-empty kind
// (projects.homeStarredGrid).
func (a *App) homeStarredLayout(ws projectWorkspaceData, interactive bool) *views.ProjectHomeLayout {
	out := &views.ProjectHomeLayout{CardMaxH: a.projectsHomeCardMaxHeight(), Columns: views.ProjectHomeStarredColumns}
	for _, def := range a.projectNavSectionDefs() {
		recordsOf, ok := homeKindRecords[def.Key]
		if !ok {
			continue
		}
		recs := recordsOf(ws)
		if len(recs) == 0 {
			continue
		}
		list := views.ProjectHomeKindGroup{Key: def.Key, Kind: recs[0].Kind, Title: def.Label, Icon: def.Icon, Count: len(recs)}
		star := list
		star.Tone = "accent"
		for _, rec := range recs {
			list.Items = append(list.Items, a.homeKindItem(ws, def, rec, "", interactive))
			if rec.IsStarred() {
				star.Items = append(star.Items, a.homeKindItem(ws, def, rec, "starred.", interactive))
			}
		}
		star.Count = len(star.Items)
		out.Lists = append(out.Lists, list)
		if len(star.Items) > 0 {
			out.Starred = append(out.Starred, star)
		}
	}
	return out
}

// homeKindItem is one record row of a Home kind group; prefix keeps the
// starred copy's focus ID apart from the same record's list-card row.
func (a *App) homeKindItem(ws projectWorkspaceData, def projectNavDef, rec data.Record, prefix string, interactive bool) views.ProjectHomeItem {
	ref, title := rec.Ref(), views.Title(a.schema(rec.Kind), rec)
	unread := a.isNavRecordUnread(rec.Kind, rec, &ws)
	focusID := views.ProjectHomeItemPrefix + prefix + def.Key + "." + rec.ID
	icon := def.Icon
	if rec.IsStarred() {
		icon = "★"
	}
	if interactive {
		a.projectCtrl().registerHomeTarget(ws.projectKey, projectHomeTarget{
			FocusID: focusID, Group: def.Key, Label: title, Ref: &ref, Unread: unread,
		})
	}
	return views.ProjectHomeItem{
		ID: focusID, Icon: icon, Title: clip(title, 64), Unread: unread, Ref: &ref,
		Facts: map[string]any{"recordKind": rec.Kind, "starred": rec.IsStarred()},
	}
}
