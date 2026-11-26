package data

import (
	"cmp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ProjectScope holds the tasks and panes that anchor a project and the
// relations incident to those anchors. Related records never extend the task
// hierarchy into other projects.
type ProjectScope struct {
	TaskIDs   map[string]bool
	PaneIDs   map[string]bool
	Tasks     []Record
	Panes     []Record
	Relations []Relation

	related map[string]map[string]bool
}

func (s *Store) projectTasks(projectID string, includeArchived bool) ([]Record, map[string]bool, error) {
	ids := map[string]bool{}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ids, nil
	}
	res, err := s.Query(Query{Kind: KindTask, IncludeArchived: true})
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]Record, len(res.Items))
	children := map[string][]string{}
	for _, rec := range res.Items {
		byID[rec.ID] = rec
		if parent, ok := rec.RefField("parent"); ok && parent.Kind == KindTask && parent.ID != "" {
			children[parent.ID] = append(children[parent.ID], rec.ID)
		}
	}
	ids[projectID] = true
	queue := []string{projectID}
	var tasks []Record
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if rec, ok := byID[id]; ok {
			if rec.IsArchived() && !includeArchived && id == projectID {
				continue
			}
			tasks = append(tasks, rec)
		}
		for _, child := range children[id] {
			if !ids[child] && (includeArchived || !byID[child].IsArchived()) {
				ids[child] = true
				queue = append(queue, child)
			}
		}
	}
	return tasks, ids, nil
}

// ProjectTaskIDs returns a set containing projectID and its descendant task IDs.
func (s *Store) ProjectTaskIDs(projectID string, includeArchived bool) (map[string]bool, error) {
	_, ids, err := s.projectTasks(projectID, includeArchived)
	return ids, err
}

// projectRelations reads both directions through the relation endpoint indexes.
func (s *Store) projectRelations(kind string, ids map[string]bool) ([]Relation, error) {
	ordered := sortedProjectIDs(ids)
	seen := map[string]bool{}
	var out []Relation
	for start := 0; start < len(ordered); start += sqlInBatchSize {
		batch := ordered[start:min(start+sqlInBatchSize, len(ordered))]
		args := []any{kind}
		for _, id := range batch {
			args = append(args, id)
		}
		in := placeholders(len(batch))
		for _, prefix := range []string{"from", "to"} {
			rels, err := queryRelations(s.db, prefix+"_kind = ? AND "+prefix+"_id IN ("+in+")", args...)
			if err != nil {
				return nil, err
			}
			for _, rel := range rels {
				if !seen[rel.ID] {
					seen[rel.ID] = true
					out = append(out, rel)
				}
			}
		}
	}
	slices.SortFunc(out, compareProjectRelations)
	return out, nil
}

func compareProjectRelations(a, b Relation) int {
	if c := cmp.Compare(a.Position, b.Position); c != 0 {
		return c
	}
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func sortedProjectIDs(ids map[string]bool) []string {
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	return ordered
}

// ProjectScope includes project panes linked to a scoped task, selected by the
// caller's pane grouping, or nested below another scoped pane.
func (s *Store) ProjectScope(projectID string, paneSeeds []Record, includeArchived bool) (ProjectScope, error) {
	tasks, taskIDs, err := s.projectTasks(projectID, includeArchived)
	if err != nil {
		return ProjectScope{}, err
	}
	taskRelations, err := s.projectRelations(KindTask, taskIDs)
	if err != nil {
		return ProjectScope{}, err
	}
	scope := ProjectScope{Tasks: tasks, TaskIDs: taskIDs, PaneIDs: map[string]bool{}}
	scope.indexRelations(taskRelations)
	for _, p := range paneSeeds {
		if !scope.PaneIDs[p.ID] {
			scope.PaneIDs[p.ID] = true
			scope.Panes = append(scope.Panes, p)
		}
	}
	allPanes, err := s.Query(Query{Kind: KindPane, IncludeArchived: includeArchived})
	if err != nil {
		return ProjectScope{}, err
	}
	children := map[string][]Record{}
	for _, p := range allPanes.Items {
		if parent, ok := p.RefField("parentPane"); ok && parent.Kind == KindPane {
			children[parent.ID] = append(children[parent.ID], p)
		}
		if scope.containsRecord(p) && !scope.PaneIDs[p.ID] {
			scope.PaneIDs[p.ID] = true
			scope.Panes = append(scope.Panes, p)
		}
	}
	for i := 0; i < len(scope.Panes); i++ {
		for _, child := range children[scope.Panes[i].ID] {
			if !scope.PaneIDs[child.ID] {
				scope.PaneIDs[child.ID] = true
				scope.Panes = append(scope.Panes, child)
			}
		}
	}
	paneRelations, err := s.projectRelations(KindPane, scope.PaneIDs)
	if err != nil {
		return ProjectScope{}, err
	}
	scope.Relations = append(taskRelations, paneRelations...)
	slices.SortFunc(scope.Relations, compareProjectRelations)
	scope.Relations = slices.CompactFunc(scope.Relations, func(a, b Relation) bool { return a.ID == b.ID })
	scope.indexRelations(scope.Relations)
	return scope, nil
}

func (scope ProjectScope) contains(ref core.Ref) bool {
	switch ref.Kind {
	case KindTask:
		return scope.TaskIDs[ref.ID]
	case KindPane:
		return scope.PaneIDs[ref.ID]
	}
	return false
}

func (scope *ProjectScope) indexRelations(rels []Relation) {
	scope.related = map[string]map[string]bool{}
	add := func(ref core.Ref) {
		if scope.related[ref.Kind] == nil {
			scope.related[ref.Kind] = map[string]bool{}
		}
		scope.related[ref.Kind][ref.ID] = true
	}
	for _, rel := range rels {
		if scope.contains(rel.From) {
			add(rel.To)
		}
		if scope.contains(rel.To) {
			add(rel.From)
		}
	}
}

func (scope ProjectScope) containsRecord(rec Record) bool {
	if scope.related[rec.Kind][rec.ID] {
		return true
	}
	for _, field := range []string{"task", "pane"} {
		if ref, ok := rec.RefField(field); ok && scope.contains(ref) {
			return true
		}
	}
	return false
}

// ProjectRecords returns records of kind attached to the project's tasks or panes
// through reference fields or relations. Each record appears once.
func (s *Store) ProjectRecords(scope ProjectScope, kind string, includeArchived bool) ([]Record, error) {
	if len(scope.TaskIDs) == 0 && len(scope.PaneIDs) == 0 {
		return nil, nil
	}

	// Use the task/pane expression indexes and fetch relation-only and locally
	// overridden records by ID. Match effective fields again after decoding.
	local, err := loadLocal(s.db, kind, "")
	if err != nil {
		return nil, err
	}
	linked := make(map[string]bool, len(scope.related[kind]))
	for id := range scope.related[kind] {
		linked[id] = true
	}
	for ref := range local {
		linked[ref.ID] = true
	}
	var out []Record
	seen := map[string]bool{}
	seq := map[string]int64{}
	fetch := func(expr string, ids map[string]bool) error {
		ordered := sortedProjectIDs(ids)
		for start := 0; start < len(ordered); start += sqlInBatchSize {
			batch := ordered[start:min(start+sqlInBatchSize, len(ordered))]
			args := []any{kind}
			where := "kind = ?"
			if !includeArchived {
				where += " AND +archive_status = ?"
				args = append(args, ArchiveActive)
			}
			for _, id := range batch {
				args = append(args, id)
			}
			q := "SELECT " + recordCols + ", seq FROM records WHERE " + where + " AND " + expr + " IN (" + placeholders(len(batch)) + ") ORDER BY created_at, seq"
			rows, err := s.db.Query(q, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var order int64
				rec, err := scanRecord(rows, &order)
				if err != nil {
					rows.Close()
					return err
				}
				if seen[rec.ID] {
					continue
				}
				seen[rec.ID] = true
				rec.Local = local[rec.Ref()]
				s.coerce(&rec)
				if scope.containsRecord(rec) {
					seq[rec.ID] = order
					out = append(out, rec)
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	}
	if err := fetch("json_extract(fields, '$.task.id')", scope.TaskIDs); err != nil {
		return nil, err
	}
	if err := fetch("json_extract(fields, '$.pane.id')", scope.PaneIDs); err != nil {
		return nil, err
	}
	if err := fetch("id", linked); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Record) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(seq[a.ID], seq[b.ID])
	})
	return out, nil
}

// RelationMatch is one record at the opposite end of a scoped relation.
type RelationMatch struct {
	Kind      string
	Direction RelationDir
	Ref       core.Ref
}

// RecordRelationMatches returns a record's relation endpoints, including the
// descendant tasks' endpoints when the record is a project.
func (s *Store) RecordRelationMatches(rec Record) ([]RelationMatch, error) {
	ids := map[string]bool{rec.ID: true}
	if _, hasParent := rec.RefField("parent"); rec.Kind == KindTask && !hasParent {
		var err error
		ids, err = s.ProjectTaskIDs(rec.ID, false)
		if err != nil {
			return nil, err
		}
	}
	rels, err := s.projectRelations(rec.Kind, ids)
	if err != nil {
		return nil, err
	}
	var matches []RelationMatch
	for _, rel := range rels {
		if rel.From.Kind == rec.Kind && ids[rel.From.ID] {
			matches = append(matches, RelationMatch{Kind: rel.Kind, Direction: Outgoing, Ref: rel.To})
		}
		if rel.To.Kind == rec.Kind && ids[rel.To.ID] {
			matches = append(matches, RelationMatch{Kind: rel.Kind, Direction: Incoming, Ref: rel.From})
		}
	}
	return matches, nil
}
