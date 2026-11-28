package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestSchemasRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if empty, err := LoadSchemas(dir); err != nil || len(empty.Nouns)+len(empty.Extend) != 0 {
		t.Fatalf("missing file = %+v, %v", empty, err)
	}
	want := SchemaOverlayFile{
		Nouns: []CustomNounSpec{{
			Kind: "reading", Title: "Reading", Plural: "Readings", Icon: "◆", SummaryField: "label",
			Fields: []CustomFieldSpec{{Name: "label", Type: "string", Required: true}},
			StatusColumns: []CustomStatusColSpec{{
				Name: "state", Default: "new", Values: []data.StatusValue{{Name: "new"}},
			}},
		}},
		Extend: map[string][]CustomFieldSpec{
			"task": {{Name: "severity", Type: "enum", Enum: []string{"high", "low"}, Default: "low"}},
		},
	}
	if err := SaveSchemas(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSchemas(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, %v, want %+v", got, err, want)
	}
	reg := data.DefaultSchemas()
	if err := NewSchemaSource(dir, reg).Refresh(); err != nil {
		t.Fatal(err)
	}
	task, _ := reg.Get("task")
	if field, ok := task.Field("severity"); !ok || field.Type != core.ParamString || !reflect.DeepEqual(field.Enum, []string{"high", "low"}) {
		t.Fatalf("compiled enum = %+v", field)
	}
	reading, _ := reg.Get("reading")
	if state, ok := reading.Field("state"); !ok || state.Default != "new" || reading.Presentations[data.FormDetail].Icon != "◆" {
		t.Fatalf("compiled reading schema = %+v", reading)
	}
}

func TestSchemasInvalidOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, schemasFileName)
	for _, tc := range []struct{ name, raw, message string }{
		{"empty kind", `{"nouns":[{"kind":" "}]}`, "kind is empty"},
		{"empty field", `{"extend":{"task":[{"name":"","type":"string"}]}}`, "field name is empty"},
		{"unknown type", `{"extend":{"task":[{"name":"severity","type":"bogus"}]}}`, "unknown type"},
		{"duplicate property", `{"extend":{"task":[{"name":"severity","type":"string"},{"name":"severity","type":"string"}]}}`, "duplicate field"},
		{"duplicate cross section", `{"nouns":[{"kind":"reading","fields":[{"name":"label","type":"string"}]}],"extend":{"reading":[{"name":"label","type":"string"}]}}`, "duplicate field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			bad, err := LoadSchemas(dir)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("load error = %v", err)
			}
			if err := SaveSchemas(dir, bad); err == nil {
				t.Fatal("invalid overlay was saved")
			}
			raw, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, []byte(tc.raw)) {
				t.Fatalf("failed save changed the file: %s, %v", raw, err)
			}
		})
	}
}

func TestSchemasConcurrentEditors(t *testing.T) {
	dir := t.TempDir()
	first, second := data.DefaultSchemas(), data.DefaultSchemas()
	sources := []*SchemaSource{NewSchemaSource(dir, first), NewSchemaSource(dir, second)}
	names := []string{"estimate", "effort"}
	errs := make(chan error, len(sources))
	var edits sync.WaitGroup
	for i, source := range sources {
		edits.Go(func() {
			errs <- source.Update(func(cfg *SchemaOverlayFile) {
				if cfg.Extend == nil {
					cfg.Extend = make(map[string][]CustomFieldSpec)
				}
				cfg.Extend["task"] = append(cfg.Extend["task"], CustomFieldSpec{Name: names[i], Type: "int"})
			})
		})
	}
	edits.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := sources[0].Refresh(); err != nil {
		t.Fatal(err)
	}
	task, _ := first.Get("task")
	for _, name := range names {
		if _, ok := task.Field(name); !ok {
			t.Fatalf("concurrent edit %q was lost", name)
		}
	}
}
