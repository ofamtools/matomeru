package data

import (
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// ShowLink is one record a Show page links to, with the schema of its kind.
type ShowLink struct {
	Ref    core.Ref
	Schema Schema
	// Record is nil when the target is not stored here (a tag, a directory,
	// a dead reference).
	Record *Record
	// Presentation is the link's declared form, resolved: a chip for a
	// reference field, a row for a section. A dead reference presents as
	// missing, never as kind:id.
	Presentation ResolvedPresentation
}

// ShowSection is one group of records connected to a Show page's record.
type ShowSection struct {
	Key string
	// Title is the declared title; a relation section nobody titled leaves it
	// empty for the presenter to name from RelKind and Direction.
	Title      string
	TargetKind string // schema kind of the section's records, when known
	Field      string // reference field linking those records to the page's record
	RelKind    string // relation kind, for a relation-backed section
	// Direction is the side of the relation the page's record is on.
	Direction RelationDir
	// Always marks a declared section, which keeps its place when empty.
	Always bool
	// Creatable reports that TargetKind is a registered schema.
	Creatable bool
	Links     []ShowLink
}

// ShowSnapshot is everything a Show page presents about one record, read once.
type ShowSnapshot struct {
	Record Record
	Schema Schema
	// Presentation is the record's own detail form.
	Presentation ResolvedPresentation
	// FieldLinks holds the target of each reference field that points
	// somewhere, by field name.
	FieldLinks map[string]ShowLink
	Sections   []ShowSection
	// Timeline is loaded when the schema declares ShowTimeline.
	Timeline []TimelineEntry
	Pinned   bool
	Marked   bool
}

// schemaOrBare returns the registered schema for kind, or a bare one, so a
// kind nobody declared still has a schema.
func (r *Registry) schemaOrBare(kind string) Schema {
	if s, ok := r.Get(kind); ok {
		return s
	}
	return Schema{Kind: kind, Title: kind, Plural: kind}
}

// link presents target in form from a batch of resolved references.
func (r ResolvedRefs) link(target core.Ref, form string) ShowLink {
	link := ShowLink{Ref: target, Schema: r.Schemas.schemaOrBare(target.Kind), Presentation: r.Presentation(target, form)}
	if rec, ok := r.Records[target]; ok {
		link.Record = &rec
	}
	return link
}

// showSections collects a record's sections and leaves out any record an
// earlier section already holds.
type showSections struct {
	store    *Store
	related  ResolvedRefs // the records of the relations, read in one batch
	seen     map[core.Ref]bool
	sections []ShowSection
}

// addReferenceSections adds one section per reference field, in any schema,
// that can point at ref.
func (b *showSections) addReferenceSections(ref core.Ref) error {
	for _, src := range b.store.reg.List() {
		for _, f := range src.Fields {
			if f.Type != core.ParamRef || !slices.Contains(f.RefKinds, ref.Kind) {
				continue
			}
			res, err := b.store.Query(Query{Kind: src.Kind, Parent: &ref, ParentField: f.Name, IncludeArchived: f.ShowIncludeArchived})
			if err != nil {
				return err
			}
			sec := ShowSection{Key: src.Kind + "." + f.Name, Title: src.Plural, TargetKind: src.Kind, Field: f.Name,
				Always: true, Creatable: true}
			if src.Kind == ref.Kind {
				sec.Title = "Sub" + strings.ToLower(src.Plural)
			}
			for _, item := range res.Items {
				if b.seen[item.Ref()] {
					continue
				}
				b.seen[item.Ref()] = true
				sec.Links = append(sec.Links, ShowLink{Ref: item.Ref(), Schema: src, Record: &item,
					Presentation: b.store.Presentations().Resolve(item, src, FormRow)})
			}
			b.sections = append(b.sections, sec)
		}
	}
	return nil
}

// addRelationSection adds the section of one relation kind and direction,
// unless it is undeclared and holds nothing.
func (b *showSections) addRelationSection(sec ShowSection, rels []RelationMatch) {
	for _, rel := range rels {
		if rel.Kind != sec.RelKind || (sec.Direction != Both && rel.Direction != sec.Direction) {
			continue
		}
		if b.seen[rel.Ref] || rel.Ref == WorkspaceRef {
			continue // pins and marks are the snapshot's Pinned and Marked
		}
		b.seen[rel.Ref] = true
		sec.Links = append(sec.Links, b.related.link(rel.Ref, FormRow))
	}
	if sec.Always || len(sec.Links) > 0 {
		_, sec.Creatable = b.store.reg.Get(sec.TargetKind)
		b.sections = append(b.sections, sec)
	}
}

// relationSuffix names a relation direction in a section key.
func relationSuffix(dir RelationDir) string {
	switch dir {
	case Incoming:
		return "in"
	case Both:
		return "both"
	}
	return "out"
}

// declaredDirection is the direction a relation spec declares.
func (spec RelationSpec) declaredDirection() RelationDir {
	switch spec.Direction {
	case DirectionIncoming:
		return Incoming
	case DirectionBoth:
		return Both
	}
	return Outgoing
}

// addRelationSections adds one section per relation kind the schema declares,
// then one per other relation kind the record is on either end of.
func (b *showSections) addRelationSections(schema Schema, rels []RelationMatch) {
	type relKey struct {
		kind string
		dir  RelationDir
	}
	declared := map[relKey]bool{}
	for _, spec := range schema.Relations {
		dir := spec.declaredDirection()
		declared[relKey{spec.Kind, dir}] = true
		if dir == Both {
			declared[relKey{spec.Kind, Outgoing}], declared[relKey{spec.Kind, Incoming}] = true, true
		}
		sec := ShowSection{Key: "rel." + spec.Kind + "." + relationSuffix(dir), Title: spec.Title, RelKind: spec.Kind,
			Direction: dir, Always: true}
		if len(spec.TargetKinds) == 1 {
			sec.TargetKind = spec.TargetKinds[0]
		}
		b.addRelationSection(sec, rels)
	}
	for _, rel := range rels {
		key := relKey{rel.Kind, Outgoing}
		if rel.Direction == Incoming {
			key.dir = Incoming
		}
		if declared[key] {
			continue
		}
		declared[key] = true
		b.addRelationSection(ShowSection{Key: "rel." + rel.Kind + "." + relationSuffix(key.dir), RelKind: rel.Kind, Direction: key.dir}, rels)
	}
}

// showFieldLinks resolves every reference field of rec that points
// somewhere, reading their records in one batch.
func (s *Store) showFieldLinks(rec Record) map[string]ShowLink {
	targets := map[string]core.Ref{}
	refs := make([]core.Ref, 0, len(rec.Fields))
	for name, v := range rec.MergedFields() {
		if target, ok := AsRef(v); ok {
			targets[name] = target
			refs = append(refs, target)
		}
	}
	resolved := s.ResolveRefsFor(refs)
	links := make(map[string]ShowLink, len(targets))
	for name, target := range targets {
		links[name] = resolved.link(target, FormChip)
	}
	return links
}

// LoadShowSnapshot reads what the Show page of ref presents, from data alone
// and with no per-noun code:
//
//  1. one section per reference field, in any schema, that can point at the
//     record (a task's subtasks, panes, pages, documents and runs);
//  2. one section per relation kind the record's schema declares, kept even
//     when empty, so an association always has its place;
//  3. one section per other relation kind the record is on either end of.
//
// A record already held by an earlier section is not repeated.
func (s *Store) LoadShowSnapshot(ref core.Ref) (ShowSnapshot, error) {
	rec, err := s.Get(ref)
	if err != nil {
		return ShowSnapshot{}, err
	}
	snap := ShowSnapshot{Record: *rec, Schema: s.reg.schemaOrBare(ref.Kind), FieldLinks: s.showFieldLinks(*rec)}
	snap.Presentation = s.Presentations().Resolve(*rec, snap.Schema, FormDetail)

	sections := showSections{store: s, seen: map[core.Ref]bool{ref: true}}
	if err := sections.addReferenceSections(ref); err != nil {
		return ShowSnapshot{}, err
	}
	rels, err := s.RecordRelationMatches(*rec)
	if err != nil {
		return ShowSnapshot{}, err
	}
	relRefs := make([]core.Ref, len(rels))
	for i, rel := range rels {
		relRefs[i] = rel.Ref
	}
	sections.related = s.ResolveRefsFor(relRefs)
	sections.addRelationSections(snap.Schema, rels)
	snap.Sections = sections.sections

	if snap.Schema.ShowTimeline {
		if snap.Timeline, err = s.Timeline(ref, 0); err != nil {
			return ShowSnapshot{}, err
		}
	}
	if snap.Pinned, err = s.IsPinned(ref); err != nil {
		return ShowSnapshot{}, err
	}
	if snap.Marked, err = s.IsMarked(ref); err != nil {
		return ShowSnapshot{}, err
	}
	return snap, nil
}
