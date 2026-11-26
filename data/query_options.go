package data

import "ofam.dev/ochazuke/core"

// QueryOption is one candidate value of a query field. Value is the exact
// text a query matches (for a reference, its stored form); Presentation is
// what a chip shows. The two never stand in for each other.
type QueryOption struct {
	Value        string
	Ref          *core.Ref
	Presentation ResolvedPresentation
}

// FieldOptionsResolved is FieldOptions with each value carried beside its
// presentation: a reference value presents as its target's chip (its title,
// never kind:id), any other value as itself.
func FieldOptionsResolved(s Schema, field string, records []Record, refs ResolvedRefs, limit int) []QueryOption {
	byValue := map[string]core.Ref{}
	for _, rec := range records {
		if ref, ok := AsRef(rec.Fields[field]); ok {
			byValue[RecordFieldText(rec, field)] = ref
		}
	}
	values := FieldOptions(s, field, records, limit)
	out := make([]QueryOption, 0, len(values))
	for _, v := range values {
		opt := QueryOption{Value: v, Presentation: ResolvedPresentation{Form: FormChip, Label: v}}
		if ref, ok := byValue[v]; ok {
			opt.Ref = &ref
			opt.Presentation = refs.Presentation(ref, FormChip)
		}
		out = append(out, opt)
	}
	return out
}

// FieldRefs lists the distinct references records hold in field, for one
// ResolveRefs batch ahead of FieldOptionsResolved.
func FieldRefs(records []Record, field string) []core.Ref {
	seen := map[core.Ref]bool{}
	var out []core.Ref
	for _, rec := range records {
		if ref, ok := AsRef(rec.Fields[field]); ok && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}
