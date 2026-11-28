package data

import (
	"errors"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestApplySchemaOverlay(t *testing.T) {
	store := openTest(t)
	overlay := SchemaOverlay{
		Nouns: []Schema{{
			Kind: "reading", Title: "Reading", SummaryField: "label",
			Fields: []FieldSpec{{Name: "label", Type: core.ParamString, Default: "Untitled"}},
		}},
		Extend: map[string][]FieldSpec{
			KindTask:  {{Name: "estimate", Type: core.ParamInt, Default: 3}},
			"reading": {{Name: "finished", Type: core.ParamBool, Default: false}},
		},
	}
	if err := ApplySchemaOverlay(store.Registry(), overlay); err != nil {
		t.Fatal(err)
	}
	task := create(t, store, KindTask, map[string]any{"title": "Estimate me"})
	if got := task.Fields["estimate"]; got != int64(3) {
		t.Fatalf("estimate default = %#v", got)
	}
	task, _, err := store.Patch(PatchInput{Ref: task.Ref(), Fields: map[string]any{"estimate": 5}}, owner)
	if err != nil || task.Fields["estimate"] != int64(5) {
		t.Fatalf("patched task = %+v, %v", task, err)
	}
	reading := create(t, store, "reading", nil)
	if reading.Text("label") != "Untitled" || reading.Fields["finished"] != false {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestSchemaOverlayLayerPublishesOnlyCompleteEdits(t *testing.T) {
	reg := DefaultSchemas()
	layer := NewSchemaOverlayLayer(reg)
	overlay := SchemaOverlay{Nouns: []Schema{{Kind: "reading"}}, Extend: map[string][]FieldSpec{
		KindTask: {{Name: "estimate", Type: core.ParamInt}},
	}}
	failure := errors.New("persistence failed")
	if err := layer.Replace(overlay, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("persistence error = %v", err)
	}
	if _, exists := reg.Get("reading"); exists {
		t.Fatal("new noun leaked after persistence failed")
	}
	if err := layer.Replace(overlay, nil); err != nil {
		t.Fatal(err)
	}
	invalid := SchemaOverlay{Extend: map[string][]FieldSpec{KindTask: {{Name: "title"}}}}
	if err := layer.Replace(invalid, nil); err == nil || !strings.Contains(err.Error(), "already has field") {
		t.Fatalf("duplicate field error = %v", err)
	}
	collision := SchemaOverlay{Nouns: []Schema{{Kind: KindTask}}}
	if err := layer.Replace(collision, nil); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate noun error = %v", err)
	}
	if _, exists := reg.Get("reading"); !exists {
		t.Fatal("valid noun disappeared after a rejected replacement")
	}
	if err := layer.Replace(SchemaOverlay{}, nil); err != nil {
		t.Fatal(err)
	}
	task, _ := reg.Get(KindTask)
	if _, exists := task.Field("estimate"); exists {
		t.Fatal("removed overlay field is still registered")
	}
	if _, exists := task.Field("title"); !exists {
		t.Fatal("built-in title was removed")
	}
	if _, exists := reg.Get("reading"); exists {
		t.Fatal("removed custom noun is still registered")
	}
}
