package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

// Injectable hooks for deterministic unit testing without a real PTY.
var (
	isInteractiveTTY   = defaultIsTTY
	runCLIRecordPicker = defaultRecordPicker
)

func defaultIsTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func recordSummary(schema data.Schema, rec data.Record) string {
	if schema.SummaryField != "" {
		if v := strings.TrimSpace(rec.Text(schema.SummaryField)); v != "" {
			return v
		}
	}
	for _, field := range []string{"title", "name"} {
		if v := strings.TrimSpace(rec.Text(field)); v != "" {
			return v
		}
	}
	return rec.ID
}

func recordsToCandidates(schema data.Schema, recs []data.Record) []oden.Candidate {
	items := make([]oden.Candidate, len(recs))
	for i, rec := range recs {
		items[i] = oden.Candidate{
			ID:    rec.ID,
			Title: recordSummary(schema, rec),
			Data:  rec,
		}
	}
	return items
}

func defaultRecordPicker(store *data.Store, kind string, candidates []data.Record, title string) (string, error) {
	schema := schemaOf(store, kind)
	items := recordsToCandidates(schema, candidates)
	picked, ok, err := ocli.DefaultPicker()(context.Background(), title, "", items)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", app.ErrCLISelectionCancelled
	}
	return picked.ID, nil
}

func recordPickerFunc(store *data.Store, kind string, schema data.Schema) oden.PickerFunc {
	return func(_ context.Context, _ string, pickQuery string, list []oden.Candidate) (oden.Candidate, bool, error) {
		recs := make([]data.Record, 0, len(list))
		for _, c := range list {
			if rec, ok := c.Data.(data.Record); ok {
				recs = append(recs, rec)
			}
		}
		label := schema.Title
		if label == "" {
			label = kind
		}
		if pickQuery != "" {
			label = fmt.Sprintf("Multiple %s match %q", kind, pickQuery)
		} else {
			label = "Select " + label
		}
		id, err := runCLIRecordPicker(store, kind, recs, label)
		if errors.Is(err, app.ErrCLISelectionCancelled) {
			return oden.Candidate{}, false, nil
		}
		if err != nil {
			return oden.Candidate{}, false, err
		}
		return oden.Candidate{ID: id}, true, nil
	}
}

// resolveRecordID resolves a user-supplied record identifier (exact ID, unique
// case-insensitive ID prefix, case-insensitive exact title/name, unique
// case-insensitive substring match on title/name, or omitted ID) to a canonical
// record ID using oden.ResolveCandidate. When ambiguous or omitted on an
// interactive TTY, it opens the Ochazuke CLI picker; on a non-TTY it returns a
// clear error.
func resolveRecordID(ctx context.Context, store *data.Store, kind, query string, stdin io.Reader, usageMsg string) (string, error) {
	q := strings.TrimSpace(query)
	schema := schemaOf(store, kind)
	interactive := isInteractiveTTY(stdin)
	pick := recordPickerFunc(store, kind, schema)

	// Case 6: omitted <id>.
	if q == "" {
		if !interactive {
			return "", errors.New(usageMsg)
		}
		res, err := store.Query(data.Query{Kind: kind})
		if err != nil {
			return "", err
		}
		if len(res.Items) == 0 {
			return "", fmt.Errorf("no active %s records found", kind)
		}
		cand, err := oden.ResolveCandidate(ctx, "", recordsToCandidates(schema, res.Items), oden.ResolveOptions{
			Noun:        kind,
			Interactive: true,
			Pick:        pick,
		})
		if err != nil {
			return "", err
		}
		return cand.ID, nil
	}

	// Case 1 fast path: exact record ID.
	if rec, err := store.Get(core.Ref{Kind: kind, ID: q}); err == nil {
		return rec.ID, nil
	} else if !errors.Is(err, data.ErrNotFound) {
		return "", err
	}

	res, err := store.Query(data.Query{Kind: kind, IncludeArchived: true})
	if err != nil {
		return "", err
	}
	cand, err := oden.ResolveCandidate(ctx, q, recordsToCandidates(schema, res.Items), oden.ResolveOptions{
		Noun:        kind,
		Interactive: interactive,
		Pick:        pick,
	})
	if err != nil {
		var amb *oden.AmbiguousError
		if errors.As(err, &amb) || errors.Is(err, app.ErrCLISelectionCancelled) {
			return "", err
		}
		if strings.HasSuffix(err.Error(), " not found") {
			return "", fmt.Errorf("%s %q: %w", kind, q, data.ErrNotFound)
		}
		return "", err
	}
	return cand.ID, nil
}

// resolveRefArg parses a target "<kind:id>" and resolves the ID using
// human-friendly matching when the target kind is registered in the store.
func resolveRefArg(ctx context.Context, store *data.Store, raw string, stdin io.Reader) (core.Ref, error) {
	ref, err := parseRefArg(raw)
	if err != nil {
		return core.Ref{}, err
	}
	if _, known := store.Registry().Get(ref.Kind); !known {
		return ref, nil
	}
	resolved, err := resolveRecordID(ctx, store, ref.Kind, ref.ID, stdin, fmt.Sprintf("target %q must be written as kind:id", raw))
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			return ref, nil
		}
		return core.Ref{}, err
	}
	ref.ID = resolved
	return ref, nil
}
