package data

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/core"
)

const (
	FieldTaskType   = "taskType"
	TaskTypeHuman   = "human"
	TaskTypeRewrite = "rewrite"
	TaskTypeJoin    = "join"
	TaskTypeSplit   = "split"

	RelDerivedFrom = "derived_from"
	RelSplitFrom   = "split_from"
	RelJoinedFrom  = "joined_from"
)

// TaskSplitPart is the content of one task created by a split.
type TaskSplitPart struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// RewriteTask creates a sibling with new content and a link to the source.
func (s *Store) RewriteTask(sourceID, newTitle, newBody string, actor core.Actor) (*Record, error) {
	created, err := s.createTaskLineage([]string{sourceID}, []TaskSplitPart{{Title: newTitle, Body: newBody}}, TaskTypeRewrite, RelDerivedFrom, actor)
	if err != nil {
		return nil, err
	}
	return &created[0], nil
}

// SplitTask creates one sibling per part, each linked to the original task.
func (s *Store) SplitTask(sourceID string, parts []TaskSplitPart, actor core.Actor) ([]Record, error) {
	return s.createTaskLineage([]string{sourceID}, parts, TaskTypeSplit, RelSplitFrom, actor)
}

// JoinTasks creates one task linked to each distinct source. The new task
// inherits a parent or explicit project only when every source shares it.
func (s *Store) JoinTasks(sourceIDs []string, newTitle, newBody string, actor core.Actor) (*Record, error) {
	seen := make(map[string]bool, len(sourceIDs))
	unique := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		id = strings.TrimSpace(id)
		if !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	if len(unique) < 2 {
		return nil, fmt.Errorf("data: joining needs at least two distinct tasks")
	}
	created, err := s.createTaskLineage(unique, []TaskSplitPart{{Title: newTitle, Body: newBody}}, TaskTypeJoin, RelJoinedFrom, actor)
	if err != nil {
		return nil, err
	}
	return &created[0], nil
}

func (s *Store) createTaskLineage(sourceIDs []string, parts []TaskSplitPart, taskType, relation string, actor core.Actor) ([]Record, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("data: %s needs at least one new task", taskType)
	}
	for i, part := range parts {
		if strings.TrimSpace(part.Title) == "" {
			return nil, fmt.Errorf("data: %s task %d needs a title", taskType, i+1)
		}
	}
	created := make([]Record, 0, len(parts))
	_, err := s.write(actor, "task."+taskType, fmt.Sprintf("%s tasks", taskType), 0, false, func(t *txn) error {
		sources := make([]Record, 0, len(sourceIDs))
		for _, id := range sourceIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("data: %s needs a source task ID", taskType)
			}
			source, err := s.loadRecord(t.tx, core.Ref{Kind: KindTask, ID: strings.TrimSpace(id)})
			if err != nil {
				return err
			}
			sources = append(sources, *source)
		}
		placement := taskLineagePlacement(sources)
		for _, part := range parts {
			ref := core.Ref{Kind: KindTask, ID: newID()}
			fields := map[string]any{FieldTaskType: taskType, "title": strings.TrimSpace(part.Title), "description": part.Body}
			for field, value := range placement {
				fields[field] = value
			}
			if err := t.createRecord(ref, CreateInput{Kind: KindTask, Fields: fields}, actor); err != nil {
				return err
			}
			for _, source := range sources {
				link := Relation{From: ref, Kind: relation, To: source.Ref()}
				if err := t.apply(FieldChange{Ref: ref, Op: OpRelate, New: link}); err != nil {
					return err
				}
			}
			record, err := s.loadRecord(t.tx, ref)
			if err != nil {
				return err
			}
			created = append(created, *record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func taskLineagePlacement(sources []Record) map[string]any {
	placement := make(map[string]any)
	// Tasks belong to their parent's project. Preserve an explicit project
	// value too when it is present in the stored records.
	for _, name := range []string{"parent", "project"} {
		value, ok := sources[0].Value(name)
		if !ok {
			continue
		}
		shared := true
		for _, source := range sources[1:] {
			other, ok := source.Value(name)
			if !ok || !equalJSON(value, other) {
				shared = false
				break
			}
		}
		if shared {
			placement[name] = value
		}
	}
	return placement
}

func taskLineageSource(change FieldChange) (core.Ref, bool) {
	var raw any
	switch change.Op {
	case OpRelate:
		raw = change.New
	case OpUnrelate:
		raw = change.Old
	default:
		return core.Ref{}, false
	}
	relation, err := decodeAs[Relation](raw)
	if err != nil || relation.From.Kind != KindTask || relation.To.Kind != KindTask {
		return core.Ref{}, false
	}
	switch relation.Kind {
	case RelDerivedFrom, RelSplitFrom, RelJoinedFrom:
		return relation.To, true
	default:
		return core.Ref{}, false
	}
}
