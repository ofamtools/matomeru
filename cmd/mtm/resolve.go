package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// Injectable hooks for deterministic unit testing without a real PTY.
var (
	isInteractiveTTY   = defaultIsTTY
	runCLIRecordPicker = app.PickRecordCLI
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

// resolveRecordID resolves a user-supplied record identifier (exact ID, unique
// case-insensitive ID prefix, case-insensitive exact title/name, unique
// case-insensitive substring match on title/name, or omitted ID) to a canonical
// record ID. When ambiguous or omitted on an interactive TTY, it opens a
// lightweight Ochazuke picker; on a non-TTY it returns a clear error.
func resolveRecordID(store *data.Store, kind, query string, stdin io.Reader, usageMsg string) (string, error) {
	q := strings.TrimSpace(query)
	schema := schemaOf(store, kind)

	// Case 6: omitted <id>.
	if q == "" {
		if !isInteractiveTTY(stdin) {
			return "", errors.New(usageMsg)
		}
		res, err := store.Query(data.Query{Kind: kind})
		if err != nil {
			return "", err
		}
		if len(res.Items) == 0 {
			return "", fmt.Errorf("no active %s records found", kind)
		}
		title := schema.Title
		if title == "" {
			title = kind
		}
		return runCLIRecordPicker(store, kind, res.Items, "Select "+title)
	}

	// Case 1: exact record ID.
	if rec, err := store.Get(core.Ref{Kind: kind, ID: q}); err == nil {
		return rec.ID, nil
	} else if !errors.Is(err, data.ErrNotFound) {
		return "", err
	}

	res, err := store.Query(data.Query{Kind: kind, IncludeArchived: true})
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(q)

	// Case 2: unique case-insensitive ID prefix.
	var prefixMatches []data.Record
	for _, rec := range res.Items {
		if strings.HasPrefix(strings.ToLower(rec.ID), lower) {
			prefixMatches = append(prefixMatches, rec)
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0].ID, nil
	}
	if len(prefixMatches) > 1 {
		return resolveAmbiguous(store, kind, q, prefixMatches, stdin)
	}

	// Case 3: case-insensitive exact match on summary/title field.
	var exactTitleMatches []data.Record
	for _, rec := range res.Items {
		if strings.EqualFold(recordSummary(schema, rec), q) {
			exactTitleMatches = append(exactTitleMatches, rec)
		}
	}
	if len(exactTitleMatches) == 1 {
		return exactTitleMatches[0].ID, nil
	}
	if len(exactTitleMatches) > 1 {
		return resolveAmbiguous(store, kind, q, exactTitleMatches, stdin)
	}

	// Case 4: unique case-insensitive substring match on summary/title field.
	var subMatches []data.Record
	for _, rec := range res.Items {
		if strings.Contains(strings.ToLower(recordSummary(schema, rec)), lower) {
			subMatches = append(subMatches, rec)
		}
	}
	if len(subMatches) == 1 {
		return subMatches[0].ID, nil
	}
	if len(subMatches) > 1 {
		return resolveAmbiguous(store, kind, q, subMatches, stdin)
	}

	return "", fmt.Errorf("%s %q: %w", kind, q, data.ErrNotFound)
}

func resolveAmbiguous(store *data.Store, kind, query string, matches []data.Record, stdin io.Reader) (string, error) {
	if isInteractiveTTY(stdin) {
		return runCLIRecordPicker(store, kind, matches, fmt.Sprintf("Multiple %s match %q", kind, query))
	}
	schema := schemaOf(store, kind)
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous %s %q (%d matches):", kind, query, len(matches))
	for _, rec := range matches {
		fmt.Fprintf(&b, "\n  %s  %s", rec.ID, recordSummary(schema, rec))
	}
	return "", errors.New(b.String())
}

// resolveRefArg parses a target "<kind:id>" and resolves the ID using
// human-friendly matching when the target kind is registered in the store.
func resolveRefArg(store *data.Store, raw string, stdin io.Reader) (core.Ref, error) {
	ref, err := parseRefArg(raw)
	if err != nil {
		return core.Ref{}, err
	}
	if _, known := store.Registry().Get(ref.Kind); !known {
		return ref, nil
	}
	resolved, err := resolveRecordID(store, ref.Kind, ref.ID, stdin, fmt.Sprintf("target %q must be written as kind:id", raw))
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			return ref, nil
		}
		return core.Ref{}, err
	}
	ref.ID = resolved
	return ref, nil
}
