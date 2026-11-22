package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// The catalog protocol. An extension that keeps its own store describes
// itself with one JSON document and answers queries with another:
//
//	TOOL catalog          a Manifest
//	TOOL query [TEXT]     {"items": [record...], "total": n}
//
// Matomeru never writes to the tool. What the owner does to one of its
// records (star, archive, snooze, a note, a changed field) is kept in
// Matomeru's store as a shadow record of the same kind and ID, and
// QueryExternal lays it over what the tool returns.

// Manifest is what `TOOL catalog` prints: the nouns the tool has, the
// commands it adds, the keys it would like them on, and views by name.
//
// "nouns" is read as "schemas" and "verbs" as "commands" (a verb's "summary"
// as its description), so a tool written against either wording mounts.
type Manifest struct {
	Name     string                 `json:"name"`
	Version  string                 `json:"version,omitempty"`
	Schemas  []data.Schema          `json:"schemas,omitempty"`
	Commands []core.CommandSpec     `json:"commands,omitempty"`
	Bindings []core.Binding         `json:"bindings,omitempty"`
	Views    map[string]ui.ViewNode `json:"views,omitempty"`
}

// UnmarshalJSON reads a manifest in either wording.
func (m *Manifest) UnmarshalJSON(b []byte) error {
	type plain Manifest
	var doc struct {
		plain
		Nouns []data.Schema `json:"nouns"`
		Verbs []struct {
			core.CommandSpec
			Summary string `json:"summary"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	*m = Manifest(doc.plain)
	m.Schemas = append(m.Schemas, doc.Nouns...)
	for _, v := range doc.Verbs {
		spec := v.CommandSpec
		if spec.Description == "" {
			spec.Description = v.Summary
		}
		m.Commands = append(m.Commands, spec)
	}
	return nil
}

// Validate reports what is wrong with a manifest: a missing name, a schema
// without a kind, a command without an ID, or two of either with the same.
func (m Manifest) Validate() error {
	var errs []error
	if strings.TrimSpace(m.Name) == "" {
		errs = append(errs, errors.New("the manifest has no name"))
	}
	kinds := map[string]bool{}
	for i, s := range m.Schemas {
		switch {
		case s.Kind == "":
			errs = append(errs, fmt.Errorf("schema %d has no kind", i+1))
		case kinds[s.Kind]:
			errs = append(errs, fmt.Errorf("schema %s is declared twice", s.Kind))
		}
		kinds[s.Kind] = true
	}
	ids := map[string]bool{}
	for i, c := range m.Commands {
		switch {
		case c.ID == "":
			errs = append(errs, fmt.Errorf("command %d has no id", i+1))
		case ids[c.ID]:
			errs = append(errs, fmt.Errorf("command %s is declared twice", c.ID))
		}
		ids[c.ID] = true
	}
	for i, b := range m.Bindings {
		if b.Command == "" && b.Layer == nil && !b.Unbind {
			errs = append(errs, fmt.Errorf("binding %d has no command", i+1))
		}
	}
	return errors.Join(errs...)
}

// output runs one read-only invocation of an extension and returns its
// standard output.
func output(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout limitedBuffer
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := firstLine([]byte(stderr.String())); msg != "" {
			return nil, fmt.Errorf("ext: %s %s: %s", binary, strings.Join(args, " "), msg)
		}
		return nil, fmt.Errorf("ext: %s %s: %w", binary, strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// ParseManifest reads and checks a manifest document.
func ParseManifest(doc []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(doc, &m); err != nil {
		return Manifest{}, fmt.Errorf("ext: the catalog is not a manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("ext: catalog of %q: %w", m.Name, err)
	}
	return m, nil
}

// LoadCatalog runs `binary catalog` and reads the manifest it prints.
func LoadCatalog(ctx context.Context, binary string) (Manifest, error) {
	out, err := output(ctx, binary, "catalog")
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(out)
}

// MountCatalog registers the manifest's schemas, so its nouns can be
// browsed, queried and shadowed like any other. A schema with no title
// takes its kind's.
func MountCatalog(reg *data.Registry, m Manifest) error {
	if reg == nil {
		return errors.New("ext: no registry to mount the catalog on")
	}
	if err := m.Validate(); err != nil {
		return fmt.Errorf("ext: catalog of %q: %w", m.Name, err)
	}
	for _, s := range m.Schemas {
		if s.Title == "" {
			s.Title = s.Kind
		}
		if s.Plural == "" {
			s.Plural = s.Title
		}
		reg.Register(s)
	}
	return nil
}

// Overlay lays what Matomeru keeps about an external record over it: its
// local overrides, star, archive and snooze. Nothing is laid over a record
// Matomeru has no shadow of.
func Overlay(store *data.Store, rec data.Record) data.Record {
	if rec.ArchiveStatus == "" {
		rec.ArchiveStatus = data.ArchiveActive
	}
	if rec.StarStatus == "" {
		rec.StarStatus = data.StarUnstarred
	}
	if store == nil {
		return rec
	}
	shadow, err := store.Get(rec.Ref())
	if err != nil {
		return rec
	}
	if len(shadow.Local) > 0 {
		local := make(map[string]any, len(rec.Local)+len(shadow.Local))
		for k, v := range rec.Local {
			local[k] = v
		}
		for k, v := range shadow.Local {
			local[k] = v
		}
		rec.Local = local
	}
	rec.StarStatus, rec.ArchiveStatus = shadow.StarStatus, shadow.ArchiveStatus
	rec.ArchivedAt, rec.SnoozedUntil = shadow.ArchivedAt, shadow.SnoozedUntil
	rec.ChangeSetNumber = shadow.ChangeSetNumber
	return rec
}

// Shadow returns Matomeru's own record of an external one, making it when
// there is none. Every field the tool gave is read-only on it, so a change
// made here is a local override and star, archive, snooze and notes work as
// on any record, while the tool's data is never written.
func Shadow(store *data.Store, rec data.Record, actor core.Actor) (*data.Record, error) {
	if rec.Kind == "" || rec.ID == "" {
		return nil, errors.New("ext: an external record needs a kind and an id to be shadowed")
	}
	if have, err := store.Get(rec.Ref()); err == nil {
		return have, nil
	} else if !errors.Is(err, data.ErrNotFound) {
		return nil, err
	}
	readOnly := make(map[string]bool, len(rec.Fields))
	for name := range rec.Fields {
		readOnly[name] = true
	}
	shadow, _, err := store.Create(data.CreateInput{Kind: rec.Kind, ID: rec.ID, Fields: rec.Fields, ReadOnly: readOnly}, actor)
	return shadow, err
}

// QueryExternal runs `binary query` and returns the records of kind it
// prints ("" keeps every kind), each with Matomeru's local overrides, star,
// archive and snooze laid over it. The tool is only read.
func QueryExternal(ctx context.Context, binary, kind string, store *data.Store) (data.QueryResult, error) {
	out, err := output(ctx, binary, "query")
	if err != nil {
		return data.QueryResult{}, err
	}
	var res data.QueryResult
	if err := json.Unmarshal(out, &res); err != nil {
		return data.QueryResult{}, fmt.Errorf("ext: %s query did not print records: %w", binary, err)
	}
	items := make([]data.Record, 0, len(res.Items))
	for _, rec := range res.Items {
		if kind != "" && rec.Kind != kind {
			continue
		}
		items = append(items, Overlay(store, rec))
	}
	return data.QueryResult{Items: items, Total: len(items), FilterErr: res.FilterErr}, nil
}
