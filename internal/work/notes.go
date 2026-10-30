package work

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// Note retains unknown legacy fields, including provenance, across edits.
// Title is optional: an empty one (every note written before it existed)
// shows the first line of Text in its place (the label's fallback); the
// body is never the title.
type Note struct {
	ID              string `json:"id"`
	Title           string `json:"title,omitempty"`
	Text            string `json:"text"`
	ProjectID       string `json:"project_id"`
	TaskID          string `json:"task_id"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	FormerTaskID    string `json:"former_task_id,omitempty"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
	extra           map[string]json.RawMessage
}

type noteFields Note

func (n *Note) UnmarshalJSON(data []byte) error {
	var fields noteFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*n = Note(fields)
	if err := json.Unmarshal(data, &n.extra); err != nil {
		return err
	}
	for _, key := range []string{"id", "title", "text", "project_id", "task_id", "former_project_id", "former_task_id", "created", "updated"} {
		delete(n.extra, key)
	}
	return nil
}

func (n Note) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(noteFields(n))
	if err != nil || len(n.extra) == 0 {
		return data, err
	}
	fields := make(map[string]json.RawMessage, len(n.extra)+8)
	for key, value := range n.extra {
		fields[key] = value
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(data, &known); err != nil {
		return nil, err
	}
	for key, value := range known {
		fields[key] = value
	}
	return json.Marshal(fields)
}

func (s *Store) Notes() ([]Note, error) {
	notes, err := all[Note](s.db, "notes")
	slices.SortFunc(notes, func(a, b Note) int { return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID)) })
	return notes, err
}

func (s *Store) Note(id string) (Note, error) {
	var note Note
	err := s.db.Get("notes", id, &note)
	return note, err
}

func (s *Store) PutNote(note Note) error {
	return s.db.Tx([]string{"notes"}, func(tx *store.Tx) error { return tx.Put("notes", note.ID, note) })
}

func (s *Store) AddNote(projectID, taskID, text string) (Note, error) {
	now := Now()
	note := Note{ID: NewID(), Text: text, ProjectID: projectID, TaskID: taskID, Created: now, Updated: now}
	return note, s.PutNote(note)
}

// NoteProject resolves task-only legacy rows without rewriting them on read.
func (s *Store) NoteProject(note Note) (string, error) {
	if note.TaskID == "" {
		return note.ProjectID, nil
	}
	task, err := s.Task(note.TaskID)
	if err == store.ErrNotFound {
		return note.ProjectID, nil
	}
	return task.ProjectID, err
}

// DeleteNote removes a note. Nothing references a note, so nothing is
// orphaned; a missing note is store.ErrNotFound.
func (s *Store) DeleteNote(id string) error {
	return s.db.Tx([]string{"notes"}, func(tx *store.Tx) error {
		var note Note
		if err := tx.Get("notes", id, &note); err != nil {
			return err
		}
		return tx.Delete("notes", id)
	})
}
