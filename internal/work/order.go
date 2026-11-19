package work

import (
	"cmp"
	"encoding/json"
	"slices"

	"ofam.dev/matomeru/store"
)

// positioned is a row ordered by a stored position (byPosition), whose
// position reorderTable writes.
type positioned interface {
	orderKey() (position, created int64, id string)
}

// byPosition orders rows by their stored position: a row with one (above
// 0) before a row without, lower first; then by created time and id, so
// rows never moved keep creation order after the moved ones.
func byPosition[T positioned](a, b T) int {
	posA, createdA, idA := a.orderKey()
	posB, createdB, idB := b.orderKey()
	if unA, unB := posA <= 0, posB <= 0; unA != unB {
		if unA {
			return 1
		}
		return -1
	}
	return cmp.Or(cmp.Compare(posA, posB), cmp.Compare(createdA, createdB), cmp.Compare(idA, idB))
}

// reorderTable puts the rows of table named by ids in that order and
// numbers every row's position from 1, in one write. The named rows take
// the places the same rows held in the current order (byPosition), so a
// row not named (archived, or out of the view) keeps its place between
// the others; an unknown or repeated id is skipped. It reports whether any
// position changed; a row whose position is already right is not written.
func reorderTable[T positioned, P interface {
	*T
	setPosition(int64)
}](db *store.Store, table string, ids []string) (bool, error) {
	changed := false
	err := db.Tx([]string{table}, func(tx *store.Tx) error {
		raws, err := tx.All(table)
		if err != nil {
			return err
		}
		rows := make([]T, len(raws))
		for i, raw := range raws {
			if err := json.Unmarshal(raw, &rows[i]); err != nil {
				return err
			}
		}
		slices.SortFunc(rows, byPosition[T])
		at := map[string]int{}
		for i, row := range rows {
			_, _, id := row.orderKey()
			at[id] = i
		}
		var slots []int
		var named []T
		for _, id := range ids {
			if i, ok := at[id]; ok {
				delete(at, id)
				slots = append(slots, i)
				named = append(named, rows[i])
			}
		}
		slices.Sort(slots)
		for k, i := range slots {
			rows[i] = named[k]
		}
		for i := range rows {
			if pos, _, id := rows[i].orderKey(); pos != int64(i+1) {
				P(&rows[i]).setPosition(int64(i + 1))
				changed = true
				if err := tx.Put(table, id, rows[i]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return changed, err
}
