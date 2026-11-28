package data

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// SchemaOverlay adds nouns and extends the fields of registered nouns.
type SchemaOverlay struct {
	Nouns  []Schema
	Extend map[string][]FieldSpec
}

// ApplySchemaOverlay applies an additive overlay in one registry update.
func ApplySchemaOverlay(reg *Registry, overlay SchemaOverlay) error {
	return NewSchemaOverlayLayer(reg).Replace(overlay, nil)
}

// SchemaOverlayLayer tracks which definitions an overlay owns so it can be
// replaced or removed while keeping other registrations in the same registry.
type SchemaOverlayLayer struct {
	reg     *Registry
	current SchemaOverlay
}

func NewSchemaOverlayLayer(reg *Registry) *SchemaOverlayLayer {
	return &SchemaOverlayLayer{reg: reg}
}

// Replace validates the complete update before calling persist. Readers then
// see the new registry in one step. If persist fails, they keep the old one.
func (l *SchemaOverlayLayer) Replace(next SchemaOverlay, persist func() error) error {
	r := l.reg
	r.mu.Lock()
	defer r.mu.Unlock()

	staged := &Registry{byKey: maps.Clone(r.byKey), order: slices.Clone(r.order)}
	staged.removeSchemaOverlay(l.current)
	if err := staged.addSchemaOverlay(next); err != nil {
		return err
	}
	if persist != nil {
		if err := persist(); err != nil {
			return err
		}
	}
	r.byKey, r.order = staged.byKey, staged.order
	l.current = next
	return nil
}

func (r *Registry) removeSchemaOverlay(overlay SchemaOverlay) {
	for kind, fields := range overlay.Extend {
		if schema, ok := r.byKey[kind]; ok {
			schema.Fields = slices.DeleteFunc(slices.Clone(schema.Fields), func(f FieldSpec) bool {
				return slices.ContainsFunc(fields, func(added FieldSpec) bool { return added.Name == f.Name })
			})
			r.byKey[kind] = schema
		}
	}
	for _, schema := range overlay.Nouns {
		delete(r.byKey, schema.Kind)
		r.order = slices.DeleteFunc(r.order, func(kind string) bool { return kind == schema.Kind })
	}
}

func (r *Registry) addSchemaOverlay(overlay SchemaOverlay) error {
	for _, schema := range overlay.Nouns {
		if strings.TrimSpace(schema.Kind) == "" {
			return fmt.Errorf("custom noun kind is empty")
		}
		if _, exists := r.byKey[schema.Kind]; exists {
			return fmt.Errorf("noun %q is already registered", schema.Kind)
		}
		compiled, err := appendSchemaFields(Schema{Kind: schema.Kind}, schema.Fields)
		if err != nil {
			return err
		}
		schema.Fields = compiled.Fields
		r.byKey[schema.Kind] = schema
		r.order = append(r.order, schema.Kind)
	}
	for _, kind := range slices.Sorted(maps.Keys(overlay.Extend)) {
		schema, exists := r.byKey[kind]
		if !exists {
			return fmt.Errorf("cannot extend unregistered noun %q", kind)
		}
		extended, err := appendSchemaFields(schema, overlay.Extend[kind])
		if err != nil {
			return err
		}
		r.byKey[kind] = extended
	}
	return nil
}

func appendSchemaFields(schema Schema, fields []FieldSpec) (Schema, error) {
	names := make(map[string]bool, len(schema.Fields)+len(fields))
	for _, f := range schema.Fields {
		names[f.Name] = true
	}
	for _, col := range schema.AllStatusColumns() {
		names[col.Name] = true
	}
	for _, rel := range schema.FormRelations() {
		names[rel.FormField] = true
	}
	for _, f := range fields {
		if strings.TrimSpace(f.Name) == "" {
			return Schema{}, fmt.Errorf("noun %q has an empty field name", schema.Kind)
		}
		if names[f.Name] {
			return Schema{}, fmt.Errorf("noun %q already has field %q", schema.Kind, f.Name)
		}
		names[f.Name] = true
	}
	schema.Fields = append(slices.Clone(schema.Fields), fields...)
	return schema, nil
}
