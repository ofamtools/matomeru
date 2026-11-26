package data

import (
	"maps"
	"reflect"
	"time"

	"ofam.dev/ochazuke/core"
)

// FormSubmission is one explicit submit of a record form, held as a value:
// the record as it stood (Before, nil for a new record), its schema, what the
// form submitted, the hidden defaults laid under it, and the fields the form
// showed. A visible field missing from Submitted is cleared; a field the form
// did not show is never touched. SnoozeUntil is the parsed snoozedUntil the
// form submitted, nil when it submitted none.
//
// Its methods only read it: PatchInput is the one write an edit becomes, and
// CreateInput plus CreateOverlay are the writes a new record becomes.
type FormSubmission struct {
	Before        *Record
	Schema        Schema
	Submitted     map[string]any
	Defaults      map[string]any
	VisibleFields []core.ParamSpec
	SnoozeUntil   *time.Time
}

// isStatusField reports whether name is a universal status column rather
// than a schema field.
func isStatusField(name string) bool {
	return name == ColStarStatus || name == ColArchiveStatus || name == ColSnoozedUntil
}

// values returns Submitted laid over Defaults, as a new map.
func (f FormSubmission) values() map[string]any {
	out := maps.Clone(f.Defaults)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, f.Submitted)
	return out
}

// visible reports whether the form showed the field named name.
func (f FormSubmission) visible(name string) bool {
	for _, sp := range f.VisibleFields {
		if sp.Name == name {
			return true
		}
	}
	return false
}

// FieldDiff returns the schema fields the submit changes: a changed value,
// or nil for a visible field the form cleared. A new record has no diff.
func (f FormSubmission) FieldDiff() map[string]any {
	diff := map[string]any{}
	if f.Before == nil {
		return diff
	}
	vals := f.values()
	for _, sp := range f.VisibleFields {
		if isStatusField(sp.Name) {
			continue
		}
		newVal, hasNew := vals[sp.Name]
		oldVal, hasOld := f.Before.Value(sp.Name)
		switch {
		case !hasNew:
			if hasOld && oldVal != nil && oldVal != "" {
				diff[sp.Name] = nil
			}
		case !hasOld || oldVal == nil:
			if b, ok := newVal.(bool); ok && sp.Type == core.ParamBool && !b && sp.Default == nil {
				continue
			}
			diff[sp.Name] = newVal
		case !FormValuesEqual(oldVal, newVal):
			diff[sp.Name] = newVal
		}
	}
	return diff
}

// StatusDiff returns the status columns the submit changes, keyed by column.
// An edit changes only the statuses the form showed; a new record carries
// every non-default status it was given.
func (f FormSubmission) StatusDiff() map[string]any {
	before := Record{StarStatus: StarUnstarred, ArchiveStatus: ArchiveActive}
	if f.Before != nil {
		before = *f.Before
	}
	shown := func(name string) bool { return f.Before == nil || f.visible(name) }
	vals := f.values()
	diff := map[string]any{}
	if v := Text(vals[ColStarStatus]); shown(ColStarStatus) && v != "" && v != before.StarStatus {
		diff[ColStarStatus] = v
	}
	if v := Text(vals[ColArchiveStatus]); shown(ColArchiveStatus) && v != "" && v != before.ArchiveStatus {
		diff[ColArchiveStatus] = v
	}
	if shown(ColSnoozedUntil) {
		switch {
		case f.SnoozeUntil == nil && before.SnoozedUntil != nil:
			diff[ColSnoozedUntil] = nil
		case f.SnoozeUntil != nil && (before.SnoozedUntil == nil || !before.SnoozedUntil.Equal(*f.SnoozeUntil)):
			diff[ColSnoozedUntil] = f.SnoozeUntil.UTC().Format(TimeFormat)
		}
	}
	return diff
}

// HasChanges reports whether the submit writes anything. A new record is
// always a change.
func (f FormSubmission) HasChanges() bool {
	return f.Before == nil || len(f.FieldDiff()) > 0 || len(f.StatusDiff()) > 0
}

// PatchInput returns the one patch an edit submit becomes: the field diff and
// the status changes together, guarded by the revision the form saw, so the
// whole submit is one change set and one undo.
func (f FormSubmission) PatchInput() PatchInput {
	if f.Before == nil {
		return PatchInput{}
	}
	fields := f.FieldDiff()
	maps.Copy(fields, f.StatusDiff())
	return PatchInput{Ref: f.Before.Ref(), Fields: fields, IfRevision: f.Before.ChangeSetNumber}
}

// CreateInput returns the create a new-record submit becomes: every submitted
// and default value except the status columns, which CreateOverlay carries.
func (f FormSubmission) CreateInput() CreateInput {
	fields := f.values()
	maps.DeleteFunc(fields, func(k string, _ any) bool { return isStatusField(k) })
	return CreateInput{Kind: f.Schema.Kind, Fields: fields}
}

// CreateOverlay returns the status patch for the record created as ref, or a
// patch with no fields when the new record keeps the default statuses.
func (f FormSubmission) CreateOverlay(ref core.Ref) PatchInput {
	return PatchInput{Ref: ref, Fields: f.StatusDiff()}
}

// FormValuesEqual reports whether a record's stored field value equals a
// form's typed value: references, string lists, numbers of any width and
// durations compare by what they mean, not their Go type.
func FormValuesEqual(oldVal, newVal any) bool {
	if oldVal == nil || newVal == nil {
		return oldVal == nil && newVal == nil
	}
	switch nv := newVal.(type) {
	case core.Ref:
		or, ok := AsRef(oldVal)
		return ok && or == nv
	case []string:
		switch ov := oldVal.(type) {
		case []string:
			return reflect.DeepEqual(ov, nv)
		case []any:
			if len(ov) != len(nv) {
				return false
			}
			for i := range nv {
				if Text(ov[i]) != nv[i] {
					return false
				}
			}
			return true
		}
		return false
	case int:
		if of, ok := asFloat(oldVal); ok {
			return of == float64(nv)
		}
	case float64:
		if of, ok := asFloat(oldVal); ok {
			return of == nv
		}
	case time.Duration:
		if ov, ok := oldVal.(string); ok {
			return ov == nv.String()
		}
	}
	return reflect.DeepEqual(oldVal, newVal)
}

// asFloat widens a stored integer or float to float64.
func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
