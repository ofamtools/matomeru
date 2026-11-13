package main

import (
	"encoding/json"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/store"
)

const (
	inboxGroupProp      = "group"
	inboxGroupClassProp = "group-class"
	inboxGroupField     = "_group"
)

// inboxGroupProjection reads only metadata already held by the app. Task
// references are resolved inside SQLite, not by listing or decoding the items.
// The same group table supplies the row labels and these SQL expressions.
// Pane and project names are two lookup tables built once per query from
// the app's maps (inbox_panes, inbox_projects), read by key for each row.
func (a *App) inboxGroupProjection() []store.QueryProjection {
	panes, projects := map[string]string{}, map[string]string{}
	for _, p := range a.panes {
		panes[p.ID] = p.label()
	}
	for _, p := range a.projects {
		projects[p.ID] = projectName(p)
	}
	paneNames, _ := json.Marshal(panes)
	projectNames, _ := json.Marshal(projects)
	meta, _ := json.Marshal(map[string]any{"known": len(a.panes) > 0 && a.paneErr == "", "now": a.inbox.items.Now()})
	g := a.inboxGrouping()
	expr := `json((SELECT json_object('label', ` + g.labelSQL + `, 'class', ` + g.classSQL + `) FROM (
SELECT *,
 CASE WHEN pane != '' THEN pane WHEN known THEN 'closed pane ' || COALESCE(NULLIF(pane_label, ''), window) ELSE 'pane ' || COALESCE(NULLIF(pane_label, ''), window) END AS pane_name,
 ` + inboxSQLProjectLabel("effective_project") + ` AS project_name
FROM (
 SELECT *, COALESCE(NULLIF(project_id, ''), json_extract(task_row, '$.project_id'), '') AS effective_project,
 ` + inboxSQLProjectLabel("project_id") + ` AS project,
 CASE WHEN task_row IS NULL THEN 'task ' || task_id ELSE ` + inboxSQLProjectLabel("COALESCE(json_extract(task_row, '$.project_id'), '')") + ` || ' › ' || json_extract(task_row, '$.title') END AS task
 FROM (
  SELECT item,
   COALESCE(json_extract(item, '$.window'), '') AS window,
   COALESCE(json_extract(item, '$.pane_label'), '') AS pane_label,
   COALESCE(json_extract(item, '$.task_id'), '') AS task_id,
   COALESCE(json_extract(item, '$.project_id'), '') AS project_id,
   (SELECT t.row FROM tasks AS t WHERE t.id = json_extract(item, '$.task_id')) AS task_row,
   COALESCE((SELECT label FROM ` + inboxPaneNames + ` WHERE id = json_extract(item, '$.window')), '') AS pane,
   json_extract(metadata, '$.known') AS known,
   CASE WHEN COALESCE(json_extract(item, '$.dismissed_at'), 0) > 0 THEN 'archived' WHEN COALESCE(json_extract(item, '$.snoozed_until'), 0) > json_extract(metadata, '$.now') THEN 'snoozed' ELSE 'open' END AS state
  FROM (SELECT source.row AS item, ? AS metadata)
 )
)
)))`
	names := func(name string, m []byte) store.QueryTable {
		return store.QueryTable{Name: name, Columns: []string{"id", "label"}, SQL: `SELECT key, value FROM json_each(?)`, Args: []any{string(m)}}
	}
	return []store.QueryProjection{{Field: inboxGroupField, SQL: expr, Args: []any{string(meta)},
		Tables: []store.QueryTable{names(inboxPaneNames, paneNames), names(inboxProjectNames, projectNames)}}}
}

// The lookup tables of the group projection: a pane's label and a
// project's name by id.
const (
	inboxPaneNames    = "inbox_panes"
	inboxProjectNames = "inbox_projects"
)

func inboxSQLProjectLabel(id string) string {
	return `CASE WHEN ` + id + ` = '' THEN 'unassociated' ELSE COALESCE((SELECT label FROM ` + inboxProjectNames + ` WHERE id = ` + id + `), ` + id + `) END`
}

func (a *App) inboxItemGroup(it actionitems.Item) itemGroup {
	g := a.inboxGrouping()
	if g.of == nil {
		return itemGroup{}
	}
	return g.of(a, it, a.inbox.items.Now())
}
