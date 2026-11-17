package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The changes table (run plan 5.4a; spine S6; decisions 2, 3, 44 and 54)
// holds every change of the owner's that a source cannot hold itself, one
// row per thing:
//
//   - the thing: its noun and key, and for a mounted tool's thing the
//     extension and the noun as its catalog names it (Extension, Noun),
//     never the name mtm mounts it under, which the setting
//     extensions.noun-names changes (NounNames maps the two);
//   - the overlay's part (Props, Changes, Created, Archived): the props the
//     owner changed, each with the original as last seen, who and when
//     (ochazuke.PropChange). It is ochazuke's overlay backend in mtm
//     (overlay.store mtm-db, ChangesBackend);
//   - mtm's part: the project a tool's row was recorded in (Owner) and the
//     one it was in until that project was deleted (FormerOwner), each a
//     reference {noun, key}. They are not props of the thing: the overlay
//     never sees them (a row with only them is not one of its rows), and
//     mtm's scope code reads and writes them (ExtOwners, RecordExtOwner,
//     RekeyExtOwner) wherever overlay.store keeps the overlay's part.
//
// mtm's own nouns keep their own tables (decision 44). A row's id is the
// extension, the noun and the key joined by NULs (names hold none; the
// table's key field id, as every table has one); readers take the thing
// from the row's fields, never from its id.
const (
	// ChangesTable is the table's name.
	ChangesTable = "changes"
	// ChangesSchema names the rows' format (spine S13): version 1.
	ChangesSchema = "https://ofam.dev/schemas/changes.v1.json"
	// ChangesBackendName is the overlay.store value of the changes table.
	ChangesBackendName = "mtm-db"
	// ownerNoun is the noun a changes row's owner references name.
	ownerNoun = "project"
)

// RowRef is a stored reference to one thing: its noun and key (the
// family's one form, spine S5).
type RowRef struct {
	Noun string `json:"noun"`
	Key  string `json:"key"`
}

// ChangeRow is one row of the changes table.
type ChangeRow struct {
	Schema    string                          `json:"$schema,omitempty"`
	ID        string                          `json:"id"`
	Extension string                          `json:"extension,omitempty"`
	Noun      string                          `json:"noun"`
	Key       string                          `json:"key"`
	Props     map[string]any                  `json:"props,omitempty"`
	Changes   map[string]*ochazuke.PropChange `json:"changes,omitempty"`
	Created   bool                            `json:"created,omitempty"`
	Archived  bool                            `json:"archived,omitempty"`
	// Owner is the project mtm recorded a tool's row in (OwnerAt, Unix
	// milliseconds), FormerOwner the one it was in until that project was
	// deleted.
	Owner       *RowRef `json:"owner,omitempty"`
	OwnerAt     int64   `json:"owner_at,omitempty"`
	FormerOwner *RowRef `json:"former_owner,omitempty"`
}

// changeID is the id of a thing's changes row.
func changeID(ext, noun, key string) string { return ext + "\x00" + noun + "\x00" + key }

func (c ChangeRow) id() string { return changeID(c.Extension, c.Noun, c.Key) }

// overlayPart is the row as the overlay holds it, nil when the row holds
// nothing of the overlay's.
func (c ChangeRow) overlayPart() *ochazuke.OverlayRow {
	if len(c.Props) == 0 && !c.Created && !c.Archived {
		return nil
	}
	return &ochazuke.OverlayRow{Props: c.Props, Changes: c.Changes, Created: c.Created, Archived: c.Archived}
}

// setOverlayPart writes the overlay's part of the row (nil clears it).
func (c *ChangeRow) setOverlayPart(row *ochazuke.OverlayRow) {
	c.Props, c.Changes, c.Created, c.Archived = nil, nil, false, false
	if row != nil {
		c.Props, c.Changes, c.Created, c.Archived = row.Props, row.Changes, row.Created, row.Archived
	}
}

// empty reports a row with nothing left to keep.
func (c ChangeRow) empty() bool {
	return c.overlayPart() == nil && c.Owner == nil && c.FormerOwner == nil
}

// NounNames maps mtm's nouns and the identity the changes table keys a
// thing by: Mounted is mtm's noun of an extension's catalog noun, Identity
// the extension and catalog noun of one of mtm's nouns (ext "" and the
// noun itself for a noun of mtm's own or the framework's).
type NounNames interface {
	Mounted(ext, noun string) string
	Identity(noun string) (ext, catalogNoun string)
}

// OwnNames names every noun as mtm's own (no extension).
type OwnNames struct{}

func (OwnNames) Mounted(ext, noun string) string {
	if ext == "" {
		return noun
	}
	return ext + "." + noun
}

func (OwnNames) Identity(noun string) (string, string) { return "", noun }

// ChangesBackend is the changes table as ochazuke's overlay backend: each
// change is one transaction on the table, the row read inside it (its
// write lock is the backend's lock), and Version is the table's write
// count (store.WriteCount), which the overlay reads once per read pass.
func (s *Store) ChangesBackend(names NounNames) ochazuke.OverlayBackend {
	if names == nil {
		names = OwnNames{}
	}
	return changesBackend{s.db, names}
}

type changesBackend struct {
	db    *store.Store
	names NounNames
}

// Rows are the overlay's part of every row, by mtm's noun and key.
func (b changesBackend) Rows() (map[string]map[string]*ochazuke.OverlayRow, error) {
	rows, err := all[ChangeRow](b.db, ChangesTable)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]*ochazuke.OverlayRow{}
	for _, c := range rows {
		part := c.overlayPart()
		if part == nil {
			continue
		}
		noun := c.Noun
		if c.Extension != "" {
			noun = b.names.Mounted(c.Extension, c.Noun)
		}
		if out[noun] == nil {
			out[noun] = map[string]*ochazuke.OverlayRow{}
		}
		out[noun][c.Key] = part
	}
	return out, nil
}

// Change changes the overlay's part of one thing's row in one
// transaction; mtm's part of the row stays.
func (b changesBackend) Change(noun, key string, change func(*ochazuke.OverlayRow) bool) (*ochazuke.OverlayRow, map[string]map[string]*ochazuke.OverlayRow, error) {
	row, _, _, err := b.ChangeVersioned(noun, key, change)
	return row, nil, err
}

// ChangeVersioned is Change answering the table's write count read inside
// the transaction, before the write and after it.
func (b changesBackend) ChangeVersioned(noun, key string, change func(*ochazuke.OverlayRow) bool) (*ochazuke.OverlayRow, string, string, error) {
	ext, catalogNoun := b.names.Identity(noun)
	var after *ochazuke.OverlayRow
	var n, m int64
	err := b.db.Tx([]string{ChangesTable}, func(tx *store.Tx) error {
		after = nil
		var err error
		if n, err = tx.WriteCount(ChangesTable); err != nil {
			return err
		}
		row := ChangeRow{Extension: ext, Noun: catalogNoun, Key: key}
		if err := tx.Get(ChangesTable, row.id(), &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		part := row.overlayPart()
		if part == nil {
			part = &ochazuke.OverlayRow{}
		}
		if change(part) {
			after = part
			row.setOverlayPart(part)
		} else {
			row.setOverlayPart(nil)
		}
		if err := putChange(tx, row); err != nil {
			return err
		}
		m, err = tx.WriteCount(ChangesTable) // the write is counted as it happens
		return err
	})
	if err != nil {
		return nil, "", "", err
	}
	return after, strconv.FormatInt(n, 10), strconv.FormatInt(m, 10), nil
}

// Version is the table's write count (store.WriteCount): it moves only
// when a row of the changes table is written, by any writer of the file.
func (b changesBackend) Version() (string, error) {
	n, err := b.db.WriteCount(ChangesTable)
	return strconv.FormatInt(n, 10), err
}

// putChange writes a changes row, or removes it when it keeps nothing.
func putChange(tx *store.Tx, row ChangeRow) error {
	row.Schema, row.ID = ChangesSchema, row.id()
	if row.empty() {
		return tx.Delete(ChangesTable, row.ID)
	}
	return tx.Put(ChangesTable, row.ID, row)
}

// ExtRow names a tool's row by its extension, its catalog noun and its id.
type ExtRow struct{ Extension, Noun, Row string }

// ExtOwner is mtm's part of a tool's row: the project it was recorded in,
// when, and the project it was in until that one was deleted.
type ExtOwner struct {
	Project, Former string
	At              int64
}

// ChangesVersion is the changes table's write count: a reader of ExtOwners
// reads them again when it moved (a transaction in any process wrote the
// table, and no other table's write moves it).
func (s *Store) ChangesVersion() (int64, error) { return s.db.WriteCount(ChangesTable) }

// ExtOwners are mtm's part of every tool row that has one.
func (s *Store) ExtOwners() (map[ExtRow]ExtOwner, error) {
	rows, err := all[ChangeRow](s.db, ChangesTable)
	if err != nil {
		return nil, err
	}
	out := map[ExtRow]ExtOwner{}
	for _, c := range rows {
		if c.Owner == nil && c.FormerOwner == nil {
			continue
		}
		out[ExtRow{c.Extension, c.Noun, c.Key}] = ExtOwner{Project: refKey(c.Owner), Former: refKey(c.FormerOwner), At: c.OwnerAt}
	}
	return out, nil
}

// RecordExtOwner records project as the owner of a tool's row unless it
// has one already, deciding inside one transaction (another process's
// record stays); recorded says whether it wrote.
func (s *Store) RecordExtOwner(r ExtRow, project string, at int64) (recorded bool, err error) {
	err = s.db.Tx([]string{ChangesTable}, func(tx *store.Tx) error {
		recorded = false
		row := ChangeRow{Extension: r.Extension, Noun: r.Noun, Key: r.Row}
		if err := tx.Get(ChangesTable, row.id(), &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if refKey(row.Owner) != "" {
			return nil
		}
		row.Owner, row.OwnerAt, recorded = &RowRef{Noun: ownerNoun, Key: project}, at, true
		return putChange(tx, row)
	})
	return recorded, err
}

// RekeyExtOwner moves a legacy selector's owner to the row's durable key
// in one transaction. A different owner already under key is a conflict:
// both are kept and it is reported.
func (s *Store) RekeyExtOwner(ext, noun, old, key string) error {
	if old == key {
		return nil
	}
	return s.db.Tx([]string{ChangesTable}, func(tx *store.Tx) error {
		before := ChangeRow{Extension: ext, Noun: noun, Key: old}
		if err := tx.Get(ChangesTable, before.id(), &before); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		}
		if before.Owner == nil && before.FormerOwner == nil {
			return nil
		}
		after := ChangeRow{Extension: ext, Noun: noun, Key: key}
		if err := tx.Get(ChangesTable, after.id(), &after); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if after.Owner != nil || after.FormerOwner != nil {
			if refKey(after.Owner) != refKey(before.Owner) || refKey(after.FormerOwner) != refKey(before.FormerOwner) {
				return fmt.Errorf("conflicting extension row owners for %s and %s", old, key)
			}
		} else {
			after.Owner, after.OwnerAt, after.FormerOwner = before.Owner, before.OwnerAt, before.FormerOwner
			if err := putChange(tx, after); err != nil {
				return err
			}
		}
		before.Owner, before.OwnerAt, before.FormerOwner = nil, 0, nil
		return putChange(tx, before)
	})
}

func refKey(r *RowRef) string {
	if r == nil {
		return ""
	}
	return r.Key
}

// ownerPaths say where a table's rows hold an owner reference when it is
// not a field at the top of the row: a changes row holds a tool row's
// project as the reference fields owner and former_owner ({noun, key}),
// and names no task. A table not listed holds project_id,
// former_project_id, task_id and former_task_id at the top.
var ownerPaths = map[string]map[string]string{
	ChangesTable: {"project_id": "owner", "former_project_id": "former_owner"},
}

// ownerAt is the field of a table's row that holds an owner reference,
// and whether it holds it as a {noun, key} reference; ok is false when
// the table's rows hold no such field.
func ownerAt(table, field string) (name string, ref bool, ok bool) {
	paths, listed := ownerPaths[table]
	if !listed {
		return field, false, true
	}
	name, ok = paths[field]
	return name, true, ok
}

// ownerHeld is the owner a row (decoded) names in field, and whether the
// row holds the field at all.
func ownerHeld(table string, row map[string]any, field string) (string, bool) {
	name, ref, ok := ownerAt(table, field)
	if !ok {
		return "", false
	}
	v, held := row[name]
	if ref {
		m, _ := v.(map[string]any)
		s, _ := m["key"].(string)
		return s, held && m != nil
	}
	s, _ := v.(string)
	return s, held
}

// ownerOf is the owner a row (decoded) names in field, "" for none.
func ownerOf(table string, row map[string]any, field string) string {
	s, _ := ownerHeld(table, row, field)
	return s
}

// setOwner writes an owner reference of a row (decoded). For a reference
// field "" takes it out; a table whose rows hold no such field is left as
// it is.
func setOwner(table string, row map[string]any, field, value string) {
	name, ref, ok := ownerAt(table, field)
	switch {
	case !ok:
	case !ref:
		row[name] = value
	case value == "":
		delete(row, name)
	default:
		row[name] = map[string]any{"noun": ownerNoun, "key": value}
	}
}

// clearOwner takes an owner reference field out of a row (decoded).
func clearOwner(table string, row map[string]any, field string) {
	if name, _, ok := ownerAt(table, field); ok {
		delete(row, name)
	}
}

// ownerOfRaw and setOwnerRaw are ownerOf and setOwner on a row whose
// fields are kept encoded (the conversions' rowMap).
func ownerOfRaw(table string, row rowMap, field string) string {
	name, ref, ok := ownerAt(table, field)
	if !ok {
		return ""
	}
	if !ref {
		return str(row, name)
	}
	var r RowRef
	_ = json.Unmarshal(row[name], &r)
	return r.Key
}

func setOwnerRaw(table string, row rowMap, field, value string) {
	name, ref, ok := ownerAt(table, field)
	switch {
	case !ok:
	case !ref:
		setStr(row, name, value)
	case value == "":
		delete(row, name)
	default:
		raw, _ := json.Marshal(RowRef{Noun: ownerNoun, Key: value})
		row[name] = raw
	}
}

// The one-time move into the changes table (run plan 5.4a, OV2 and OV2b;
// spine S13: a version step, a backup first, a dry run): the ext_rows
// table (the project mtm recorded a tool's row in: mtm's part of the row,
// keyed by extension and catalog noun as ext_rows was) and the
// overlay.json beside settings.json (the overlay's part, under the
// identity NounNames gives each noun). ext_rows moves whenever it is
// declared; the file whenever it is present and given. A thing the table
// already holds a prop of keeps the newer of the two (PropChange.At; the
// table's on a tie), and one it already holds an owner of keeps it (a
// record is never overwritten); each value not taken is in the move's
// notes. The old table is renamed (ext_rows_folded_<time>) and the
// file too (overlay.json.folded-<time>), never dropped; a file that does
// not read is moved aside (overlay.json.bad-<time>) and the table moves
// anyway.

// FoldedChange is one change the move makes.
type FoldedChange struct {
	From  string         `json:"from"` // ext_rows, or the file's path
	Thing string         `json:"thing"`
	Props map[string]any `json:"props,omitempty"`
}

// ChangesFold is what the move does: the changes it moves, the backup it
// made, what it renamed and what it noted (a value not taken, a file that
// did not read).
type ChangesFold struct {
	Moved   []FoldedChange `json:"moved"`
	Backup  string         `json:"backup,omitempty"`
	Renamed []string       `json:"renamed,omitempty"`
	Notes   []string       `json:"notes,omitempty"`
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
// renames, the notes, then each change.
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
	for _, n := range f.Notes {
		fmt.Fprintf(&b, "note %s\n", n)
	}
	for _, c := range f.Moved {
		props, _ := json.Marshal(c.Props)
		fmt.Fprintf(&b, "move %s %s %s\n", c.From, c.Thing, props)
	}
	return b.String()
}

// extRowsTable is the table the project of a tool's row was kept in before
// the changes table.
const extRowsTable = "ext_rows"

// foldSource is the move's input: the rows it writes, in order, each with
// where it came from, and what it found of the two sources.
type foldSource struct {
	rows                     []ChangeRow
	from                     []string
	hasExt, hasFile, badFile bool
	notes                    []string
}

// foldSources reads ext_rows and the file at overlayPath (names gives each
// of the file's nouns its identity); a file that does not read is a note.
func foldSources(db *store.Store, names NounNames, overlayPath string) (foldSource, error) {
	var src foldSource
	if src.hasExt = db.Declared(extRowsTable); src.hasExt {
		owners, err := all[ExtRowOwner](db, extRowsTable)
		if err != nil {
			return src, err
		}
		for _, o := range owners {
			row := ChangeRow{Extension: o.Extension, Noun: o.Noun, Key: o.Row, OwnerAt: o.AssignedAt}
			if o.ProjectID != "" {
				row.Owner = &RowRef{Noun: ownerNoun, Key: o.ProjectID}
			}
			if o.FormerProjectID != "" {
				row.FormerOwner = &RowRef{Noun: ownerNoun, Key: o.FormerProjectID}
			}
			src.rows, src.from = append(src.rows, row), append(src.from, extRowsTable)
		}
	}
	if overlayPath == "" {
		return src, nil
	}
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		return src, nil
	}
	src.hasFile = true
	file := map[string]map[string]*ochazuke.OverlayRow{}
	if err := json.Unmarshal(data, &file); err != nil {
		src.badFile = true
		src.notes = append(src.notes, fmt.Sprintf("%s does not read (%v): moved aside as %s.bad-<time>, its changes not moved", overlayPath, err, overlayPath))
		return src, nil
	}
	for _, noun := range slices.Sorted(maps.Keys(file)) {
		for _, key := range slices.Sorted(maps.Keys(file[noun])) {
			part := file[noun][key]
			if part == nil {
				continue // a null row holds nothing
			}
			ext, catalogNoun := names.Identity(noun)
			row := ChangeRow{Extension: ext, Noun: catalogNoun, Key: key}
			row.setOverlayPart(part)
			src.rows, src.from = append(src.rows, row), append(src.from, overlayPath)
		}
	}
	return src, nil
}

// thingName is a changes row's thing as the move's lines name it.
func thingName(c ChangeRow) string {
	if c.Extension != "" {
		return fmt.Sprintf("%s/%s %q", c.Extension, c.Noun, c.Key)
	}
	return fmt.Sprintf("%s %q", c.Noun, c.Key)
}

// changedAt is when a prop's value was written, zero when unknown.
func changedAt(changes map[string]*ochazuke.PropChange, name string) time.Time {
	if ch := changes[name]; ch != nil {
		return ch.At
	}
	return time.Time{}
}

// foldInto merges a source row into the row the table holds for the thing
// (held: nil for none): of a prop both hold, the newer one stays
// (PropChange.At; the table's on a tie); of an owner, the table's record
// stays (a record is never overwritten); each value not taken is a note.
func foldInto(held *ChangeRow, in ChangeRow) (ChangeRow, []string) {
	if held == nil {
		return in, nil
	}
	out := *held
	out.Props, out.Changes = maps.Clone(held.Props), maps.Clone(held.Changes)
	var notes []string
	for _, name := range slices.Sorted(maps.Keys(in.Props)) {
		have, ok := out.Props[name]
		differ := ok && fmt.Sprint(have) != fmt.Sprint(in.Props[name])
		if ok && !changedAt(in.Changes, name).After(changedAt(out.Changes, name)) {
			if differ {
				notes = append(notes, fmt.Sprintf("%s %s: kept %v, not %v", thingName(in), name, have, in.Props[name]))
			}
			continue
		}
		if differ {
			notes = append(notes, fmt.Sprintf("%s %s: took %v (newer), not %v", thingName(in), name, in.Props[name], have))
		}
		if out.Props == nil {
			out.Props = map[string]any{}
		}
		out.Props[name] = in.Props[name]
		if ch := in.Changes[name]; ch != nil {
			if out.Changes == nil {
				out.Changes = map[string]*ochazuke.PropChange{}
			}
			out.Changes[name] = ch
		}
	}
	if in.Owner != nil || in.FormerOwner != nil {
		// An owner is never overwritten (recorded rows never move): the
		// table's record stays, whatever its time; the other is a note.
		switch {
		case out.Owner == nil && out.FormerOwner == nil:
			out.Owner, out.OwnerAt, out.FormerOwner = in.Owner, in.OwnerAt, in.FormerOwner
		case refKey(out.Owner) != refKey(in.Owner) || refKey(out.FormerOwner) != refKey(in.FormerOwner):
			notes = append(notes, fmt.Sprintf("%s project: kept the recorded %q, not %q", thingName(in), refKey(out.Owner), refKey(in.Owner)))
		}
	}
	out.Created = out.Created || in.Created
	out.Archived = out.Archived || in.Archived
	return out, notes
}

// RekeyOwnNouns moves the changes rows kept under one of mtm's own nouns
// (extension "") whose noun names now identify an extension's thing (an
// overlay.json row folded before its extension was registered) to that
// identity, in one transaction: of a prop both rows hold the newer stays,
// an owner the target holds stays; the values not taken are notes. One
// read of the table when there is nothing to move.
func (s *Store) RekeyOwnNouns(names NounNames) (moved int, notes []string, err error) {
	rows, err := all[ChangeRow](s.db, ChangesTable)
	if err != nil {
		return 0, nil, err
	}
	var move []ChangeRow
	for _, c := range rows {
		if c.Extension != "" {
			continue
		}
		if ext, _ := names.Identity(c.Noun); ext != "" {
			move = append(move, c)
		}
	}
	if len(move) == 0 {
		return 0, nil, nil
	}
	err = s.db.Tx([]string{ChangesTable}, func(tx *store.Tx) error {
		moved, notes = 0, nil
		for _, old := range move {
			ext, catalogNoun := names.Identity(old.Noun)
			in := old
			in.Extension, in.Noun = ext, catalogNoun
			var held *ChangeRow
			var row ChangeRow
			switch err := tx.Get(ChangesTable, in.id(), &row); {
			case err == nil:
				held = &row
			case !errors.Is(err, store.ErrNotFound):
				return err
			}
			out, more := foldInto(held, in)
			notes = append(notes, more...)
			if err := tx.Delete(ChangesTable, old.id()); err != nil {
				return err
			}
			if err := putChange(tx, out); err != nil {
				return err
			}
			moved++
		}
		return nil
	})
	return moved, notes, err
}

// FoldPending reports whether the move has anything to do: ext_rows is
// declared, or the file at overlayPath is there (one metadata query and
// one stat; the caller reads the registry only then).
func (s *Store) FoldPending(overlayPath string) bool {
	if s.db.Declared(extRowsTable) {
		return true
	}
	if overlayPath == "" {
		return false
	}
	_, err := os.Stat(overlayPath)
	return err == nil
}

// PlanChangesFold is the move's dry run over the file db (opened, no table
// declared or migrated): what it would move, rename and note. It writes
// nothing.
func PlanChangesFold(db *store.Store, names NounNames, overlayPath string) (ChangesFold, error) {
	return foldChanges(db, names, overlayPath, false)
}

// FoldChanges makes the move once: a backup of the file first
// (mtm.db.bak-<time>-changes), then, in one transaction, the changes rows
// (each merged with the row the table holds, read inside it) and the
// rename of ext_rows; then the rename of the file. A file with neither
// source does nothing.
func (s *Store) FoldChanges(names NounNames, overlayPath string) (ChangesFold, error) {
	return foldChanges(s.db, names, overlayPath, true)
}

func foldChanges(db *store.Store, names NounNames, overlayPath string, apply bool) (ChangesFold, error) {
	if names == nil {
		names = OwnNames{}
	}
	src, err := foldSources(db, names, overlayPath)
	if err != nil || (!src.hasExt && !src.hasFile) {
		return ChangesFold{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	fold, tableTo, fileTo := planFold(src, overlayPath, stamp)
	for i := 2; src.hasExt && db.TableExists(tableTo); i++ { // a second move in the same second
		tableTo = fmt.Sprintf("%s_folded_%s_%d", extRowsTable, strings.ToLower(strings.NewReplacer("T", "_", "Z", "").Replace(stamp)), i)
		fold.Renamed[0] = extRowsTable + " -> " + tableTo
	}
	if !apply {
		read := func(id string, row *ChangeRow) error {
			if !db.Declared(ChangesTable) {
				return store.ErrNotFound
			}
			return db.Get(ChangesTable, id, row)
		}
		notes, err := foldMerge(src.rows, read, func(ChangeRow) error { return nil })
		fold.Notes = append(fold.Notes, notes...)
		return fold, err
	}
	if path := db.Path(); path != "" && path != ":memory:" {
		fold.Backup = path + ".bak-" + stamp + "-changes"
		for i := 2; fileExists(fold.Backup); i++ { // a second move in the same second
			fold.Backup = fmt.Sprintf("%s.bak-%s-changes-%d", path, stamp, i)
		}
		if err := db.Backup(fold.Backup); err != nil {
			return fold, err
		}
	}
	tables := []string{ChangesTable}
	if src.hasExt {
		tables = append(tables, extRowsTable)
	}
	err = db.Tx(tables, func(tx *store.Tx) error {
		read := func(id string, row *ChangeRow) error { return tx.Get(ChangesTable, id, row) }
		notes, err := foldMerge(src.rows, read, func(row ChangeRow) error { return putChange(tx, row) })
		fold.Notes = append(fold.Notes, notes...)
		if err != nil || !src.hasExt {
			return err
		}
		return tx.Rename(extRowsTable, tableTo)
	})
	if err != nil || !src.hasFile {
		return fold, err
	}
	return fold, os.Rename(overlayPath, fileTo)
}

// planFold is what the move of src will say: what it renames (the table to
// tableTo, the file to fileTo: .folded-<stamp>, or .bad-<stamp> for a file
// that did not read), the rows it moves and the notes so far.
func planFold(src foldSource, overlayPath, stamp string) (fold ChangesFold, tableTo, fileTo string) {
	fold.Notes = src.notes
	tableTo = extRowsTable + "_folded_" + strings.ToLower(strings.NewReplacer("T", "_", "Z", "").Replace(stamp))
	fileTo = overlayPath + ".folded-" + stamp
	if src.badFile {
		fileTo = overlayPath + ".bad-" + stamp
	}
	if src.hasExt {
		fold.Renamed = append(fold.Renamed, extRowsTable+" -> "+tableTo)
	}
	if src.hasFile {
		fold.Renamed = append(fold.Renamed, overlayPath+" -> "+fileTo)
	}
	for i, r := range src.rows {
		props := maps.Clone(r.Props)
		if r.Owner != nil || r.FormerOwner != nil {
			if props == nil {
				props = map[string]any{}
			}
			props["(project)"], props["(former project)"] = refKey(r.Owner), refKey(r.FormerOwner)
		}
		fold.Moved = append(fold.Moved, FoldedChange{From: src.from[i], Thing: thingName(r), Props: props})
	}
	return fold, tableTo, fileTo
}

// foldMerge merges each source row into the row the table holds for its
// thing (read: the move's transaction, or the file in the dry run) and
// writes each merged row once through put; it answers the notes of the
// values not taken.
func foldMerge(rows []ChangeRow, read func(id string, row *ChangeRow) error, put func(ChangeRow) error) ([]string, error) {
	merged := map[string]ChangeRow{}
	var order, notes []string
	for _, in := range rows {
		id := in.id()
		var held *ChangeRow
		if base, seen := merged[id]; seen {
			held = &base
		} else {
			order = append(order, id)
			var row ChangeRow
			switch err := read(id, &row); {
			case err == nil:
				held = &row
			case !errors.Is(err, store.ErrNotFound):
				return notes, err
			}
		}
		out, more := foldInto(held, in)
		merged[id] = out
		notes = append(notes, more...)
	}
	for _, id := range order {
		if err := put(merged[id]); err != nil {
			return notes, err
		}
	}
	return notes, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
