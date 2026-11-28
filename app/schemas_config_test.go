package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/internal/config"
	"ofam.dev/ochazuke/core"
)

func TestSchemaCommandsPersistAndApplyLive(t *testing.T) {
	dir := t.TempDir()
	a := newSchemaTestApp(t, dir)
	d := NewDriver(a, 100, 30)
	run := func(id string, args map[string]any) {
		d.Send(core.CommandEvent{Command: core.Command{ID: id, Source: "palette", Actor: User, Args: args}})
	}
	run(CmdSchemaPropAdd, map[string]any{
		"noun": data.KindTask, "name": "severity", "type": "enum", "description": "Incident severity",
		"enum": []any{"low", "high"}, "default": "low",
	})
	if !strings.Contains(a.Status(), "Added property") {
		t.Fatalf("prop add status = %q", a.Status())
	}
	run(CmdSchemaNounAdd, map[string]any{
		"kind": "reading", "title": "Reading", "plural": "Readings", "summaryField": "label", "icon": "◆",
	})
	if !strings.Contains(a.Status(), "Added noun") {
		t.Fatalf("noun add status = %q", a.Status())
	}
	run(CmdSchemaPropAdd, map[string]any{"noun": data.KindTask, "name": "title", "type": "string"})
	if !strings.Contains(a.Status(), "already has field") {
		t.Fatalf("duplicate prop add status = %q", a.Status())
	}
	cfg, err := config.LoadSchemas(dir)
	if err != nil || len(cfg.Nouns) != 1 || len(cfg.Extend[data.KindTask]) != 1 {
		t.Fatalf("persisted schemas = %+v, %v", cfg, err)
	}
	for _, instance := range []*App{a, newSchemaTestApp(t, dir)} {
		task, _, err := instance.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "An incident"}}, User)
		if err != nil || task.Text("severity") != "low" {
			t.Fatalf("task from overlay = %+v, %v", task, err)
		}
		reading, ok := instance.Store.Registry().Get("reading")
		if !ok || reading.SummaryField != "label" || reading.Plural != "Readings" {
			t.Fatalf("reading schema = %+v", reading)
		}
		rec, _, err := instance.Store.Create(data.CreateInput{Kind: reading.Kind, Fields: map[string]any{"label": "A book"}}, User)
		if err != nil || rec.Text("label") != "A book" {
			t.Fatalf("reading from overlay = %+v, %v", rec, err)
		}
	}
}

func newSchemaTestApp(t *testing.T, dir string) *App {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(store, Options{ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}
