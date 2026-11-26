package data

import (
	"slices"
	"strings"
	"sync"

	"ofam.dev/ochazuke/core"
)

// resolveChunkSize bounds the IDs of one IN (...) query.
const resolveChunkSize = 500

// defaultPresentations parses the embedded defaults once.
var defaultPresentations = sync.OnceValue(DefaultPresentations)

// SetPresentations sets the presentation registry the store's snapshots
// resolve references with. nil restores the embedded defaults.
func (s *Store) SetPresentations(p *PresentationRegistry) { s.pres = p }

// Presentations returns the store's presentation registry.
func (s *Store) Presentations() *PresentationRegistry {
	if s.pres == nil {
		return defaultPresentations()
	}
	return s.pres
}

// ResolveRefs loads the records refs point at, one query per kind (and per
// 500 IDs), with their local overrides. A reference with no stored record
// is left out of the map; a read error leaves out the kind it hit.
func (s *Store) ResolveRefs(refs []core.Ref) map[core.Ref]Record {
	byKind := map[string][]string{}
	seen := map[core.Ref]bool{}
	for _, ref := range refs {
		if ref.Kind == "" || ref.ID == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		byKind[ref.Kind] = append(byKind[ref.Kind], ref.ID)
	}
	out := make(map[core.Ref]Record, len(seen))
	for kind, ids := range byKind {
		local, err := loadLocal(s.db, kind, "")
		if err != nil {
			continue
		}
		for chunk := range slices.Chunk(ids, resolveChunkSize) {
			s.resolveChunk(kind, chunk, local, out)
		}
	}
	return out
}

// resolveChunk reads the records of one kind with the given IDs into out.
func (s *Store) resolveChunk(kind string, ids []string, local map[core.Ref]map[string]any, out map[core.Ref]Record) {
	args := make([]any, 0, len(ids)+1)
	args = append(args, kind)
	for _, id := range ids {
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.Query("SELECT "+recordCols+" FROM records WHERE kind = ? AND id IN ("+marks+")", args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			continue
		}
		rec.Local = local[rec.Ref()]
		s.coerce(&rec)
		out[rec.Ref()] = rec
	}
}

// ResolvedRefs is a batch of referenced records with what presents them:
// the pure value a presenter or a query builder is handed in place of a
// store.
type ResolvedRefs struct {
	Records       map[core.Ref]Record
	Schemas       *Registry
	Presentations *PresentationRegistry
}

// ResolveRefsFor loads refs into a ResolvedRefs with the store's schemas
// and presentations.
func (s *Store) ResolveRefsFor(refs []core.Ref) ResolvedRefs {
	return ResolvedRefs{Records: s.ResolveRefs(refs), Schemas: s.reg, Presentations: s.Presentations()}
}

// Presentation fills form for ref: from its record when one was loaded,
// from the reference alone for a noun titled by its ID, or as missing.
func (r ResolvedRefs) Presentation(ref core.Ref, form string) ResolvedPresentation {
	var rec *Record
	if got, ok := r.Records[ref]; ok {
		rec = &got
	}
	return r.Presentations.ResolveRef(ref, rec, r.Schemas.schemaOrBare(ref.Kind), form)
}
