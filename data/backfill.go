package data

import (
	"errors"
	"slices"

	"ofam.dev/ochazuke/core"
)

var backfillKinds = []string{
	KindQuestion,
	KindCommand,
	KindContext,
	KindDocument,
	KindPage,
}

// BackfillPaneTask finds unlinked question, command, context, document, and
// ui.page records referencing paneID (via a "pane" field or a relation with the
// pane) whose "task" ref is empty, and patches their "task" ref to taskID.
// For ui.page and context records it also ensures the corresponding task
// relation (RelPage / RelContext). Returns the number of records updated.
func BackfillPaneTask(store *Store, paneID, taskID string, actor core.Actor) (int, error) {
	if store == nil {
		return 0, errors.New("data: BackfillPaneTask requires a non-nil store")
	}
	if paneID == "" || taskID == "" {
		return 0, nil
	}

	paneRef := core.Ref{Kind: KindPane, ID: paneID}
	taskRef := core.Ref{Kind: KindTask, ID: taskID}

	byRef := map[core.Ref]Record{}
	var order []core.Ref
	add := func(rec Record) {
		if rec.IsArchived() || !slices.Contains(backfillKinds, rec.Kind) {
			return
		}
		if _, hasTask := rec.RefField("task"); hasTask {
			return
		}
		ref := rec.Ref()
		if _, exists := byRef[ref]; exists {
			return
		}
		byRef[ref] = rec
		order = append(order, ref)
	}

	for _, kind := range backfillKinds {
		res, err := store.Query(Query{
			Kind:        kind,
			Parent:      &paneRef,
			ParentField: "pane",
		})
		if err != nil {
			return 0, err
		}
		for _, rec := range res.Items {
			add(rec)
		}
	}

	related, err := store.RelatedRecords(paneRef, "", Both)
	if err != nil {
		return 0, err
	}
	for _, rec := range related {
		add(rec)
	}

	updated := 0
	for _, ref := range order {
		rec := byRef[ref]
		if _, _, err := store.Patch(PatchInput{
			Ref:     ref,
			Fields:  map[string]any{"task": taskRef},
			Summary: "backfill task from pane " + paneID,
		}, actor); err != nil {
			return updated, err
		}
		switch rec.Kind {
		case KindPage:
			if _, _, err := store.Relate(RelateInput{From: taskRef, Kind: RelPage, To: ref}, actor); err != nil {
				return updated, err
			}
		case KindContext:
			if _, _, err := store.Relate(RelateInput{From: taskRef, Kind: RelContext, To: ref}, actor); err != nil {
				return updated, err
			}
		}
		updated++
	}
	return updated, nil
}
