package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"ofam.dev/ochazuke/core"
)

// ErrNotFound reports a record, relation or change set that does not exist.
var ErrNotFound = errors.New("data: not found")

// TimeFormat is how times are written into record fields (always UTC).
const TimeFormat = time.RFC3339Nano

// WriteTime is a field value the store replaces with the timestamp of the
// write, formatted with TimeFormat. A field written this way equals the
// record's UpdatedAt exactly (a page's viewedAt, for example).
type WriteTime struct{}

// Record is the universal record: every noun is stored in this shape.
type Record struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`

	// Fields holds the source values. Local holds local overrides, which win
	// when read through MergedFields and never change Fields. ReadOnly names
	// the source fields the owner of the record does not let us write; a
	// patch of such a field becomes a local override.
	Fields   map[string]any  `json:"fields"`
	Local    map[string]any  `json:"local,omitempty"`
	ReadOnly map[string]bool `json:"readOnly,omitempty"`

	// Common features on every record.
	ArchiveStatus   string     `json:"archiveStatus"`
	StarStatus      string     `json:"starStatus"`
	ChangeSetNumber int64      `json:"changeSetNumber"` // revision: the last change set that touched the record
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"` // moves when Fields change
	ArchivedAt      *time.Time `json:"archivedAt,omitempty"`
	// SnoozedUntil hides the record until then. Setting it never moves UpdatedAt.
	SnoozedUntil *time.Time `json:"snoozedUntil,omitempty"`
}

// Ref returns the record's identity.
func (r Record) Ref() core.Ref { return core.Ref{Kind: r.Kind, ID: r.ID} }

// MergedFields returns Fields with Local laid over it, as a new map.
func (r Record) MergedFields() map[string]any {
	out := make(map[string]any, len(r.Fields)+len(r.Local))
	maps.Copy(out, r.Fields)
	maps.Copy(out, r.Local)
	return out
}

// Value returns the merged value of field.
func (r Record) Value(field string) (any, bool) {
	if v, ok := r.Local[field]; ok {
		return v, true
	}
	v, ok := r.Fields[field]
	return v, ok
}

// Text returns the merged value of field as display text.
func (r Record) Text(field string) string {
	v, _ := r.Value(field)
	return Text(v)
}

// Bool returns the merged value of field as a bool.
func (r Record) Bool(field string) bool {
	v, _ := r.Value(field)
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	return false
}

// RefField returns the merged value of a single-valued reference field.
func (r Record) RefField(field string) (core.Ref, bool) {
	v, _ := r.Value(field)
	return AsRef(v)
}

// Time returns the merged value of a time field written with TimeFormat.
func (r Record) Time(field string) time.Time {
	t, err := time.Parse(TimeFormat, r.Text(field))
	if err != nil {
		return time.Time{}
	}
	return t
}

// IsArchived reports whether the record is archived.
func (r Record) IsArchived() bool { return r.ArchiveStatus == ArchiveArchived }

// IsStarred reports whether the record is starred.
func (r Record) IsStarred() bool { return r.StarStatus == StarStarred }

// IsSnoozed reports whether the record is snoozed at now.
func (r Record) IsSnoozed(now time.Time) bool {
	return r.SnoozedUntil != nil && now.Before(*r.SnoozedUntil)
}

// IsUnreadBy reports whether the record changed after the timestamp in field
// (or field is unset). When field is empty, the noun has no unread state and
// reports false.
func (r Record) IsUnreadBy(field string) bool {
	if field == "" {
		return false
	}
	viewed := r.Time(field)
	return viewed.IsZero() || r.UpdatedAt.After(viewed)
}

// IsUnread reports whether the record changed after it was last viewed: its
// viewedAt field is empty or earlier than UpdatedAt. It is meant for ui.page.
func (r Record) IsUnread() bool {
	return r.IsUnreadBy("viewedAt")
}

// AsRef reads a single-valued reference: a core.Ref or its stored
// {kind, id} form. An empty reference reports false.
func AsRef(v any) (core.Ref, bool) {
	var ref core.Ref
	switch x := v.(type) {
	case core.Ref:
		ref = x
	case *core.Ref:
		if x != nil {
			ref = *x
		}
	case map[string]any:
		ref.Kind, _ = x["kind"].(string)
		ref.ID, _ = x["id"].(string)
	}
	return ref, ref.Kind != "" && ref.ID != ""
}

// RefText is a reference's text form, "kind:id".
func RefText(ref core.Ref) string { return ref.Kind + ":" + ref.ID }

// Text renders a field value as display text. A reference renders as
// "kind:id", a list as comma-separated items and other objects as JSON.
func Text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case core.Ref:
		return RefText(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = Text(e)
		}
		return strings.Join(parts, ", ")
	case []string:
		return strings.Join(x, ", ")
	case map[string]any:
		if ref, ok := AsRef(x); ok && len(x) == 2 {
			return RefText(ref)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Relation is a many-valued reference between any two things. Either end may
// be any noun, so pins, marks, tags, notes and context work on every record.
type Relation struct {
	ID        string         `json:"id"`
	From      core.Ref       `json:"from"`
	Kind      string         `json:"kind"`
	To        core.Ref       `json:"to"`
	Position  int            `json:"position"`
	Meta      map[string]any `json:"meta,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Other returns the end of the relation that is not ref.
func (r Relation) Other(ref core.Ref) core.Ref {
	if r.From == ref {
		return r.To
	}
	return r.From
}

// RelationDir selects which end of a relation a reference is on.
type RelationDir int

// Relation directions.
const (
	Outgoing RelationDir = iota // ref is From
	Incoming                    // ref is To
	Both
)

// Change operations. Each has an exact opposite, so any change set can be
// undone without knowing which verb wrote it.
const (
	OpCreate   = "create"   // New holds the whole Record
	OpDelete   = "delete"   // Old holds the whole Record
	OpField    = "field"    // one source field
	OpLocal    = "local"    // one local override
	OpStatus   = "status"   // archiveStatus, starStatus or snoozedUntil
	OpRelate   = "relate"   // New holds the Relation
	OpUnrelate = "unrelate" // Old holds the Relation
)

// FieldChange is one change inside a change set.
type FieldChange struct {
	Ref    core.Ref `json:"ref"`
	Op     string   `json:"op"`
	Field  string   `json:"field,omitempty"`
	Old    any      `json:"old,omitempty"`
	New    any      `json:"new,omitempty"`
	HadOld bool     `json:"hadOld,omitempty"` // the value existed before the change
	HasNew bool     `json:"hasNew,omitempty"` // the value exists after the change
}

// Conflict kinds.
const (
	ConflictRevision = "revision" // a write named a revision that was no longer current
	ConflictUndo     = "undo"     // an undo or redo found a value changed since
	ConflictMissing  = "missing"  // an undo or redo found its target gone, or already there
	ConflictUnique   = "unique"   // a write repeated a value of one of the schema's UniqueFields
)

// Conflict records that a write went ahead over something its caller had not
// seen. Writes are never refused: the conflict is kept on the change set so
// callers and the UI can show it.
type Conflict struct {
	Kind       string   `json:"kind"`
	Ref        core.Ref `json:"ref"`
	Field      string   `json:"field,omitempty"`
	Fields     []string `json:"fields,omitempty"`
	IfRevision int64    `json:"ifRevision,omitempty"` // revision the writer had seen
	Revision   int64    `json:"revision,omitempty"`   // revision that was current
	Expected   any      `json:"expected,omitempty"`
	Actual     any      `json:"actual,omitempty"`
	Message    string   `json:"message"`
}

// ChangeSet is one immutable write: who did what, and every change it made.
// Undo and redo write new change sets; whether one was undone is derived from
// a later change set's UndoOf.
type ChangeSet struct {
	Number    int64         `json:"number"`
	Actor     core.Actor    `json:"actor"`
	Action    string        `json:"action"`
	Summary   string        `json:"summary,omitempty"`
	UndoOf    int64         `json:"undoOf,omitempty"`
	Changes   []FieldChange `json:"changes"`
	Conflicts []Conflict    `json:"conflicts,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
}

// Targets returns one index row per thing the change set touched. The index
// is rebuildable from the change sets.
func (c ChangeSet) Targets() []ChangeTarget {
	seen := map[core.Ref]bool{}
	var out []ChangeTarget
	add := func(ref core.Ref) {
		if ref.Kind == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ChangeTarget{ChangeSet: c.Number, Ref: ref})
	}
	for _, ch := range c.Changes {
		add(ch.Ref)
		if ch.Op == OpRelate || ch.Op == OpUnrelate {
			for _, v := range []any{ch.Old, ch.New} {
				if rel, err := decodeAs[Relation](v); err == nil && v != nil {
					add(rel.From)
					add(rel.To)
				}
			}
		}
	}
	return out
}

// ChangeTarget is an index row answering "what touched this record or noun".
type ChangeTarget struct {
	ChangeSet int64    `json:"changeSet"`
	Ref       core.Ref `json:"ref"`
}

// TreeNode is one record in a recursive hierarchy tree. A node at depth 0 is a
// root record (for example, a root task displays as a project).
type TreeNode struct {
	Record   Record     `json:"record"`
	Depth    int        `json:"depth"`
	Children []TreeNode `json:"children,omitempty"`
}

// IsRoot reports whether the node is at the top level of the tree.
func (n TreeNode) IsRoot() bool { return n.Depth == 0 }

// IsProject reports whether the node is a root task.
func (n TreeNode) IsProject() bool { return n.IsRoot() }

// TaskNode is an alias for TreeNode kept for existing callers.
type TaskNode = TreeNode

// decodeAs converts v to T by type assertion or a JSON round trip, so values
// built in Go and values read back from the store behave the same.
func decodeAs[T any](v any) (T, error) {
	if t, ok := v.(T); ok {
		return t, nil
	}
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
