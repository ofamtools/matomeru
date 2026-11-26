package data

import (
	"fmt"

	"ofam.dev/ochazuke/core"
)

func (t *txn) createRecord(ref core.Ref, in CreateInput, actor core.Actor) error {
	fields := map[string]any{}
	for k, v := range in.Fields {
		nv, err := t.value(v)
		if err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
		if nv != nil {
			fields[k] = nv
		}
	}
	if schema, ok := t.s.reg.Get(in.Kind); ok {
		for _, f := range schema.Fields {
			if _, set := fields[f.Name]; !set && f.Default != nil {
				fields[f.Name] = f.Default
			}
		}
	}
	t.prepareCreateReadState(ref, actor, in.Fields, fields)
	rec := Record{Kind: ref.Kind, ID: ref.ID, Fields: fields, ReadOnly: in.ReadOnly, CreatedAt: t.now}
	return t.apply(FieldChange{Ref: ref, Op: OpCreate, New: rec})
}
