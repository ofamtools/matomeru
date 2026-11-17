package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The changes table (run plan 5.4a; spine S6; decisions 2, 3, 44 and 54)
// holds every change of the owner's that a source cannot hold itself:
// one row per thing, its noun and key (the thing's reference), the props
// the owner changed, and per prop the original as last seen, who changed
// it and when (ochazuke.PropChange). It is ochazuke's overlay backend in
// mtm (setting overlay.store, value mtm-db, ChangesBackend): what the
// overlay fills for a tool's rows, the framework's nouns and the system
// facts, and the project a tool's row was recorded in (ExtProjectProp,
// formerly the ext_rows table). mtm's own nouns keep their own tables
// (decision 44). A row's id is its noun and key joined by a NUL (a noun
// name holds none; the table's key field, id, as every table has one);
// readers take the noun and key from their fields, never from the id.
const (
	// ChangesTable is the table's name.
	ChangesTable = "changes"
	// ChangesSchema names the rows' format (spine S13): version 1.
	ChangesSchema = "https://ofam.dev/schemas/changes.v1.json"
	// ChangesBackendName is the overlay.store value of the changes table.
	ChangesBackendName = "mtm-db"
	// ExtProjectProp is the prop of a tool's row that names the project mtm
	// recorded it in (a project's key); ExtFormerProjectProp the project it
	// was in until that project was deleted.
	ExtProjectProp       = "project"
	ExtFormerProjectProp = "former_project"
)

// ChangeRow is one row of the changes table.
type ChangeRow struct {
	Schema   string                          `json:"$schema,omitempty"`
	ID       string                          `json:"id"`
	Noun     string                          `json:"noun"`
	Key      string                          `json:"key"`
	Props    map[string]any                  `json:"props,omitempty"`
	Changes  map[string]*ochazuke.PropChange `json:"changes,omitempty"`
	Created  bool                            `json:"created,omitempty"`
	Archived bool                            `json:"archived,omitempty"`
}

// ChangeID is the id of the changes row of a thing.
func ChangeID(noun, key string) string { return noun + "\x00" + key }

// overlay is the row as the overlay holds it.
func (c ChangeRow) overlay() *ochazuke.OverlayRow {
	return &ochazuke.OverlayRow{Props: c.Props, Changes: c.Changes, Created: c.Created, Archived: c.Archived}
}

// changeRowOf is the changes row of a thing's overlay row.
func changeRowOf(noun, key string, row *ochazuke.OverlayRow) ChangeRow {
	return ChangeRow{Schema: ChangesSchema, ID: ChangeID(noun, key), Noun: noun, Key: key, Props: row.Props, Changes: row.Changes, Created: row.Created, Archived: row.Archived}
}

// ChangesBackend is the changes table as ochazuke's overlay backend: every
// change is one transaction on the table (its write lock is the backend's
// lock), so the TUI, mcp serve and a one-shot CLI keep each other's rows.
func (s *Store) ChangesBackend() ochazuke.OverlayBackend { return changesBackend{s.db} }

type changesBackend struct{ db *store.Store }

// Rows are every row of the table, by noun and key.
func (b changesBackend) Rows() (map[string]map[string]*ochazuke.OverlayRow, error) {
	rows, err := all[ChangeRow](b.db, ChangesTable)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]*ochazuke.OverlayRow{}
	for _, c := range rows {
		if out[c.Noun] == nil {
			out[c.Noun] = map[string]*ochazuke.OverlayRow{}
		}
		out[c.Noun][c.Key] = c.overlay()
	}
	return out, nil
}

// Change changes one thing's row in one transaction: the row as the table
// holds it now, changed, written back (or removed). The other rows the
// process holds stay (fresh is nil).
func (b changesBackend) Change(noun, key string, change func(*ochazuke.OverlayRow) bool) (*ochazuke.OverlayRow, map[string]map[string]*ochazuke.OverlayRow, error) {
	var after *ochazuke.OverlayRow
	err := b.db.Tx([]string{ChangesTable}, func(tx *store.Tx) error {
		after = nil
		id := ChangeID(noun, key)
		row := &ochazuke.OverlayRow{}
		var held ChangeRow
		if err := tx.Get(ChangesTable, id, &held); err == nil {
			row = held.overlay()
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if !change(row) {
			return tx.Delete(ChangesTable, id)
		}
		after = row
		return tx.Put(ChangesTable, id, changeRowOf(noun, key, row))
	})
	if err != nil {
		return nil, nil, err
	}
	return after, nil, nil
}

// ownerPaths say where a table's rows hold an owner reference when it is
// not a field at the top of the row: a changes row holds a tool row's
// project under its props, and names no task. A table not listed holds
// project_id, former_project_id, task_id and former_task_id at the top.
var ownerPaths = map[string]map[string]string{
	ChangesTable: {"project_id": ExtProjectProp, "former_project_id": ExtFormerProjectProp},
}

// ownerAt is the owner reference field of a table's row: where it is held
// (under props or at the top) and its name there; ok is false when the
// table's rows hold no such field.
func ownerAt(table, field string) (underProps bool, name string, ok bool) {
	paths, listed := ownerPaths[table]
	if !listed {
		return false, field, true
	}
	name, ok = paths[field]
	return true, name, ok
}

// ownerOf is the owner a row (decoded) names in field, "" for none.
func ownerOf(table string, row map[string]any, field string) string {
	underProps, name, ok := ownerAt(table, field)
	if !ok {
		return ""
	}
	if underProps {
		props, _ := row["props"].(map[string]any)
		s, _ := props[name].(string)
		return s
	}
	s, _ := row[name].(string)
	return s
}

// ownerHeld is ownerOf with whether the row holds the field at all.
func ownerHeld(table string, row map[string]any, field string) (string, bool) {
	underProps, name, ok := ownerAt(table, field)
	if !ok {
		return "", false
	}
	if underProps {
		props, _ := row["props"].(map[string]any)
		v, held := props[name]
		s, _ := v.(string)
		return s, held
	}
	v, held := row[name]
	s, _ := v.(string)
	return s, held
}

// clearOwner takes an owner reference field out of a row (decoded).
func clearOwner(table string, row map[string]any, field string) {
	underProps, name, ok := ownerAt(table, field)
	switch {
	case !ok:
	case underProps:
		setOwner(table, row, field, "")
	default:
		delete(row, name)
	}
}

// setOwner writes an owner reference of a row (decoded); "" clears it (a
// prop under props is taken out, with what the row keeps about it). A
// table whose rows hold no such field is left as it is.
func setOwner(table string, row map[string]any, field, value string) {
	underProps, name, ok := ownerAt(table, field)
	if !ok {
		return
	}
	if !underProps {
		row[name] = value
		return
	}
	props, _ := row["props"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	changes, _ := row["changes"].(map[string]any)
	if value == "" {
		delete(props, name)
		delete(changes, name)
	} else {
		props[name] = value
	}
	row["props"] = props
	if changes != nil {
		row["changes"] = changes
	}
}

// ownerOfRaw and setOwnerRaw are ownerOf and setOwner on a row whose
// fields are kept encoded (the conversions' rowMap).
func ownerOfRaw(table string, row rowMap, field string) string {
	underProps, name, ok := ownerAt(table, field)
	if !ok {
		return ""
	}
	if !underProps {
		return str(row, name)
	}
	var props map[string]any
	_ = json.Unmarshal(row["props"], &props)
	s, _ := props[name].(string)
	return s
}

func setOwnerRaw(table string, row rowMap, field, value string) {
	underProps, name, ok := ownerAt(table, field)
	if !ok {
		return
	}
	if !underProps {
		setStr(row, name, value)
		return
	}
	decoded := map[string]any{}
	for _, f := range []string{"props", "changes"} {
		if raw, held := row[f]; held {
			var v any
			_ = json.Unmarshal(raw, &v)
			decoded[f] = v
		}
	}
	setOwner(table, decoded, field, value)
	for _, f := range []string{"props", "changes"} {
		if v, held := decoded[f]; held {
			raw, _ := json.Marshal(v)
			row[f] = raw
		}
	}
}

// The one-time move into the changes table (run plan 5.4a, OV2 and OV2b;
// spine S13: a version step, a backup first, a dry run): the ext_rows
// table (the project mtm recorded a tool's row in) and the overlay.json
// beside settings.json (the changes the overlay kept before the table)
// become changes rows. The step runs while either is there: ext_rows
// declared, the file present. A thing the table already holds a value of
// keeps the table's (the move reports it kept). The old table is renamed
// (ext_rows_folded_<time>) and the old file too (overlay.json.folded-<time>),
// never dropped.

// FoldedChange is one change the move makes (or keeps).
type FoldedChange struct {
	From  string         `json:"from"` // ext_rows, or the file's path
	Noun  string         `json:"noun"`
	Key   string         `json:"key"`
	Props map[string]any `json:"props,omitempty"`
	// Kept names the props the table held already: its values stay.
	Kept []string `json:"kept,omitempty"`
}

// ChangesFold is what the move does: the changes it moves, the backup it
// made and what it renamed.
type ChangesFold struct {
	Moved   []FoldedChange `json:"moved"`
	Backup  string         `json:"backup,omitempty"`
	Renamed []string       `json:"renamed,omitempty"`
}

// Counts are the moved rows per source.
func (f ChangesFold) Counts() map[string]int {
	out := map[string]int{}
	for _, c := range f.Moved {
		out[c.From]++
	}
	return out
}

// String is the plan as lines: one per source with its count, what it
// renames, then each change.
func (f ChangesFold) String() string {
	var b strings.Builder
	counts := f.Counts()
	for _, from := range slices.Sorted(maps.Keys(counts)) {
		fmt.Fprintf(&b, "changes: %d from %s\n", counts[from], from)
	}
	if f.Backup != "" {
		fmt.Fprintf(&b, "backup %s\n", f.Backup)
	}
	for _, r := range f.Renamed {
		fmt.Fprintf(&b, "rename %s\n", r)
	}
	for _, c := range f.Moved {
		props, _ := json.Marshal(c.Props)
		fmt.Fprintf(&b, "move %s %s %q %s", c.From, c.Noun, c.Key, props)
		if len(c.Kept) > 0 {
			fmt.Fprintf(&b, " (kept the table's %s)", strings.Join(c.Kept, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ExtNounOf names mtm's noun of an extension's catalog noun (the noun its
// rows are mounted under).
type ExtNounOf func(ext, noun string) string

// foldSources are the rows the move would write, by changes id, with the
// FoldedChange of each source row.
type foldSource struct {
	change FoldedChange
	row    *ochazuke.OverlayRow
}

func foldSources(db *store.Store, nounOf ExtNounOf, overlayPath string) ([]foldSource, bool, error) {
	var out []foldSource
	hasExt := db.Declared(extRowsTable)
	if hasExt {
		owners, err := all[ExtRowOwner](db, extRowsTable)
		if err != nil {
			return nil, false, err
		}
		for _, o := range owners {
			row := &ochazuke.OverlayRow{Props: map[string]any{}}
			if o.ProjectID != "" {
				row.Props[ExtProjectProp] = o.ProjectID
				ch := &ochazuke.PropChange{}
				if o.AssignedAt != 0 {
					ch.At = time.UnixMilli(o.AssignedAt).UTC()
				}
				row.Changes = map[string]*ochazuke.PropChange{ExtProjectProp: ch}
			}
			if o.FormerProjectID != "" {
				row.Props[ExtFormerProjectProp] = o.FormerProjectID
			}
			noun := nounOf(o.Extension, o.Noun)
			out = append(out, foldSource{FoldedChange{From: extRowsTable, Noun: noun, Key: o.Row, Props: maps.Clone(row.Props)}, row})
		}
	}
	if overlayPath != "" {
		if data, err := os.ReadFile(overlayPath); err == nil {
			rows := map[string]map[string]*ochazuke.OverlayRow{}
			if err := json.Unmarshal(data, &rows); err != nil {
				return nil, false, fmt.Errorf("%s: %w (not moved; fix or move it aside)", overlayPath, err)
			}
			for _, noun := range slices.Sorted(maps.Keys(rows)) {
				for _, key := range slices.Sorted(maps.Keys(rows[noun])) {
					row := rows[noun][key]
					out = append(out, foldSource{FoldedChange{From: overlayPath, Noun: noun, Key: key, Props: maps.Clone(row.Props)}, row})
				}
			}
		}
	}
	return out, hasExt, nil
}

// foldInto merges a source row into the row the table holds for the thing
// (nil for none): the table's values stay; it answers the merged row and
// the props kept.
func foldInto(held *ochazuke.OverlayRow, in *ochazuke.OverlayRow) (*ochazuke.OverlayRow, []string) {
	if held == nil {
		return in, nil
	}
	out := *held
	out.Props = maps.Clone(held.Props)
	if out.Props == nil {
		out.Props = map[string]any{}
	}
	out.Changes = maps.Clone(held.Changes)
	var kept []string
	for _, name := range slices.Sorted(maps.Keys(in.Props)) {
		if _, ok := out.Props[name]; ok {
			kept = append(kept, name)
			continue
		}
		out.Props[name] = in.Props[name]
		if ch := in.Changes[name]; ch != nil {
			if out.Changes == nil {
				out.Changes = map[string]*ochazuke.PropChange{}
			}
			out.Changes[name] = ch
		}
	}
	out.Created = out.Created || in.Created
	out.Archived = out.Archived || in.Archived
	return &out, kept
}

// PlanChangesFold is the move's dry run over the file db (opened, no table
// declared or migrated): what it would move from the ext_rows table and
// the file at overlayPath, and what it would rename. It writes nothing.
func PlanChangesFold(db *store.Store, nounOf ExtNounOf, overlayPath string) (ChangesFold, error) {
	return foldChanges(db, nounOf, overlayPath, false)
}

// FoldChanges makes the move once: a backup of the file first
// (mtm.db.bak-<time>-changes), then, in one transaction, the changes rows
// and the rename of ext_rows; then the rename of the file. A file with
// neither source does nothing.
func (s *Store) FoldChanges(nounOf ExtNounOf, overlayPath string) (ChangesFold, error) {
	return foldChanges(s.db, nounOf, overlayPath, true)
}

// extRowsTable is the table the project of a tool's row was kept in before
// the changes table.
const extRowsTable = "ext_rows"

func foldChanges(db *store.Store, nounOf ExtNounOf, overlayPath string, apply bool) (ChangesFold, error) {
	var fold ChangesFold
	sources, hasExt, err := foldSources(db, nounOf, overlayPath)
	if err != nil {
		return fold, err
	}
	hasFile := overlayPath != "" && fileExists(overlayPath)
	if !hasExt && !hasFile {
		return fold, nil
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	tableTo := extRowsTable + "_folded_" + strings.ToLower(strings.NewReplacer("T", "_", "Z", "").Replace(stamp))
	if hasExt {
		fold.Renamed = append(fold.Renamed, extRowsTable+" -> "+tableTo)
	}
	if hasFile {
		fold.Renamed = append(fold.Renamed, overlayPath+" -> "+overlayPath+".folded-"+stamp)
	}
	held := map[string]*ochazuke.OverlayRow{}
	if db.Declared(ChangesTable) {
		rows, err := (changesBackend{db}).Rows()
		if err != nil {
			return fold, err
		}
		for noun, byKey := range rows {
			for key, row := range byKey {
				held[ChangeID(noun, key)] = row
			}
		}
	}
	merged := map[string]*ochazuke.OverlayRow{}
	var order []string
	for _, src := range sources {
		id := ChangeID(src.change.Noun, src.change.Key)
		base := merged[id]
		if base == nil {
			base = held[id]
			order = append(order, id)
		}
		row, kept := foldInto(base, src.row)
		merged[id] = row
		src.change.Kept = kept
		fold.Moved = append(fold.Moved, src.change)
	}
	if !apply {
		return fold, nil
	}
	if path := db.Path(); path != "" && path != ":memory:" {
		fold.Backup = path + ".bak-" + stamp + "-changes"
		if err := db.Backup(fold.Backup); err != nil {
			return fold, err
		}
	}
	tables := []string{ChangesTable}
	if hasExt {
		tables = append(tables, extRowsTable)
	}
	nounKey := map[string][2]string{}
	for _, src := range sources {
		nounKey[ChangeID(src.change.Noun, src.change.Key)] = [2]string{src.change.Noun, src.change.Key}
	}
	err = db.Tx(tables, func(tx *store.Tx) error {
		for _, id := range order {
			nk := nounKey[id]
			if err := tx.Put(ChangesTable, id, changeRowOf(nk[0], nk[1], merged[id])); err != nil {
				return err
			}
		}
		if hasExt {
			return tx.Rename(extRowsTable, tableTo)
		}
		return nil
	})
	if err != nil {
		return fold, err
	}
	if hasFile {
		if err := os.Rename(overlayPath, overlayPath+".folded-"+stamp); err != nil {
			return fold, err
		}
	}
	return fold, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
