package data

// maxDeltaChangeSets caps how many change sets ChangedKindsSince will inspect
// before requesting a full cache rebuild.
const maxDeltaChangeSets = 64

// ChangeDelta summarizes the record kinds and relation/undo activity recorded
// strictly after a prior store revision.
type ChangeDelta struct {
	Kinds       map[string]bool
	HasRelation bool
	FullRebuild bool
}

// LocalWriteSeq returns a monotonically increasing counter bumped after every
// committed in-process Store write (including undo/redo).
func (s *Store) LocalWriteSeq() uint64 {
	if s == nil {
		return 0
	}
	return s.localWriteSeq.Load()
}

// LastWrittenRev returns the change set number of the most recent in-process
// committed write, or 0 before any write through this Store handle.
func (s *Store) LastWrittenRev() int64 {
	if s == nil {
		return 0
	}
	return s.lastWrittenRev.Load()
}

// IsMemory reports whether the store is backed by an in-memory SQLite database.
func (s *Store) IsMemory() bool {
	return s != nil && s.memory
}

// ChangedKindsSince reports which record kinds and whether any relations or
// undo/redo writes occurred strictly after sinceRev. When more than
// maxDeltaChangeSets change sets elapsed, or an undo/redo occurred, FullRebuild
// is true so callers fall back cleanly to a full refresh.
func (s *Store) ChangedKindsSince(sinceRev int64) (ChangeDelta, error) {
	if s == nil || s.db == nil || sinceRev < 0 {
		return ChangeDelta{FullRebuild: true}, nil
	}
	count, err := s.countChangeSetsSince(sinceRev)
	if err != nil {
		return ChangeDelta{FullRebuild: true}, err
	}
	if count == 0 {
		return ChangeDelta{}, nil
	}
	if count > maxDeltaChangeSets {
		return ChangeDelta{FullRebuild: true}, nil
	}
	hasUndo, hasRel, err := s.changeSetFlagsSince(sinceRev)
	if err != nil || hasUndo {
		return ChangeDelta{FullRebuild: true}, err
	}
	kinds, err := s.queryChangedKinds(sinceRev)
	if err != nil {
		return ChangeDelta{FullRebuild: true}, err
	}
	return ChangeDelta{Kinds: kinds, HasRelation: hasRel}, nil
}

func (s *Store) countChangeSetsSince(sinceRev int64) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM change_sets WHERE number > ?", sinceRev).Scan(&count)
	return count, err
}

func (s *Store) changeSetFlagsSince(sinceRev int64) (hasUndo, hasRel bool, err error) {
	var undoFlag, relFlag int
	err = s.db.QueryRow(
		`SELECT
			EXISTS(SELECT 1 FROM change_sets WHERE number > ? AND +undo_of > 0),
			EXISTS(SELECT 1 FROM change_sets WHERE number > ? AND (instr(changes, '"op":"relate"') > 0 OR instr(changes, '"op":"unrelate"') > 0))`,
		sinceRev, sinceRev,
	).Scan(&undoFlag, &relFlag)
	return undoFlag != 0, relFlag != 0, err
}

// changedKindsSQL reads the targets of the recent change sets by a range seek
// on change_set. It has no DISTINCT on purpose: DISTINCT kind lets SQLite
// prefer change_targets_ref and scan every target ever written; the caller
// is bounded by maxDeltaChangeSets, so de-duplicating in Go is cheap.
const changedKindsSQL = "SELECT kind FROM change_targets WHERE change_set > ?"

func (s *Store) queryChangedKinds(sinceRev int64) (map[string]bool, error) {
	rows, err := s.db.Query(changedKindsSQL, sinceRev)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kinds := map[string]bool{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		if kind != "" {
			kinds[kind] = true
		}
	}
	return kinds, rows.Err()
}
