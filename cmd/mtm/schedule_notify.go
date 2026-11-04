package main

import (
	"encoding/json"
	"fmt"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/schedules"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
)

// Reconcile persisted schedule state, not lossy runner completion messages.
// The runner projects each completed run; inbox ticks also reconcile saved state.
// Reading both tables and updating items in one transaction prevents a stale
// success from archiving an item for a newer failure in another process.
func syncFailingSchedules(db *store.Store, limit int, now int64) error {
	return db.Tx([]string{schedules.Table.Name, actionitems.Table.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(actionitems.Table.Name)
		if err != nil {
			return err
		}
		items := map[string]actionitems.Item{}
		for _, raw := range rows {
			var it actionitems.Item
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			if it.Source == "mtm" {
				items[it.Key] = it
			}
		}
		states, err := tx.All(schedules.Table.Name)
		if err != nil {
			return err
		}
		for _, raw := range states {
			var state schedules.State
			if err := json.Unmarshal(raw, &state); err != nil {
				return err
			}
			key := "schedule-failing:" + state.ID
			it, exists := items[key]
			active, _ := it.Extra["failing"].(bool)
			if state.LastOutcome == schedules.OutcomeOK {
				if !exists || !active {
					continue
				}
				it.ArchivedAt, it.ReadAt = now, now
				it.Extra["failing"] = false
			} else {
				if limit <= 0 || state.Failures < limit {
					continue
				}
				if !exists {
					it = actionitems.Item{ID: work.NewID(), Source: "mtm", Key: key, Kind: "schedule-failing", ScheduleID: state.ID,
						Title: "Schedule failing: " + state.ID, Created: now, Extra: map[string]any{}}
				}
				if !active {
					// A new failure cycle reuses its row. Manual archive during an ongoing
					// failure stays archived; only a real recovery rearms it.
					it.ArchivedAt, it.ReadAt, it.SnoozedUntil, it.NotifiedAt = 0, 0, 0, 0
				}
				why := fmt.Sprintf("%d consecutive failures: %s", state.Failures, state.LastError)
				if exists && active && it.Why == why {
					continue
				}
				it.Notify, it.Why, it.Text = true, why, state.LastError
				if it.Extra == nil {
					it.Extra = map[string]any{}
				}
				it.Extra["failing"] = true
			}
			it.Updated = now
			if err := tx.Put(actionitems.Table.Name, it.ID, it); err != nil {
				return err
			}
		}
		return nil
	})
}
