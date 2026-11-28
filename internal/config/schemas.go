package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"ofam.dev/matomeru/data"
	oconfig "ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
)

const schemasFileName = "schemas.json"

type CustomFieldSpec = data.FieldSpec
type CustomStatusColSpec = data.StatusColumn

type CustomNounSpec struct {
	Kind          string                `json:"kind"`
	Title         string                `json:"title,omitempty"`
	Plural        string                `json:"plural,omitempty"`
	Icon          string                `json:"icon,omitempty"`
	SummaryField  string                `json:"summaryField,omitempty"`
	Fields        []CustomFieldSpec     `json:"fields,omitempty"`
	StatusColumns []CustomStatusColSpec `json:"statusColumns,omitempty"`
}

type SchemaOverlayFile struct {
	Nouns  []CustomNounSpec             `json:"nouns,omitempty"`
	Extend map[string][]CustomFieldSpec `json:"extend,omitempty"`
}

// LoadSchemas reads schemas.json. An absent file yields an empty overlay.
func LoadSchemas(dir string) (SchemaOverlayFile, error) {
	raw, err := readSchemas(dir)
	if err != nil {
		return SchemaOverlayFile{}, err
	}
	return decodeSchemas(raw)
}

func readSchemas(dir string) ([]byte, error) {
	if dir == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, schemasFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

func decodeSchemas(raw []byte) (SchemaOverlayFile, error) {
	var cfg SchemaOverlayFile
	if raw == nil {
		return cfg, nil
	}
	dec := json.NewDecoder(bytes.NewReader(oconfig.StripJSONC(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("expected one schema overlay object")
	}
	return cfg, validateSchemas(cfg)
}

// ValidateSchemas reports structural errors. The registry checks collisions
// with registered nouns and fields when an overlay is applied.
func ValidateSchemas(cfg SchemaOverlayFile) []string {
	var problems []string
	fields := make(map[string]map[string]bool)
	for _, noun := range cfg.Nouns {
		if strings.TrimSpace(noun.Kind) == "" {
			problems = append(problems, "custom noun kind is empty")
			continue
		}
		if fields[noun.Kind] != nil {
			problems = append(problems, fmt.Sprintf("duplicate custom noun %q", noun.Kind))
			continue
		}
		names := make(map[string]bool)
		fields[noun.Kind] = names
		problems = append(problems, validateFields(noun.Kind, names, noun.Fields)...)
		problems = append(problems, validateStatusColumns(noun.Kind, names, noun.StatusColumns)...)
	}
	for _, kind := range slices.Sorted(maps.Keys(cfg.Extend)) {
		if strings.TrimSpace(kind) == "" {
			problems = append(problems, "extension noun kind is empty")
			continue
		}
		names := fields[kind]
		if names == nil {
			names = make(map[string]bool)
		}
		problems = append(problems, validateFields(kind, names, cfg.Extend[kind])...)
	}
	return problems
}

var schemaFieldTypes = []string{
	string(core.ParamString), string(core.ParamInt), string(core.ParamFloat),
	string(core.ParamBool), string(core.ParamDuration), string(core.ParamPath),
	string(core.ParamRef), string(core.ParamList), string(core.ParamObject), "enum",
}

func SchemaFieldTypes() []string { return slices.Clone(schemaFieldTypes) }

func validateFields(kind string, names map[string]bool, fields []CustomFieldSpec) []string {
	var problems []string
	for _, f := range fields {
		var problem string
		switch {
		case strings.TrimSpace(f.Name) == "":
			problem = "field name is empty"
		case names[f.Name]:
			problem = fmt.Sprintf("duplicate field %q", f.Name)
		case !slices.Contains(schemaFieldTypes, string(f.Type)):
			problem = fmt.Sprintf("field %q has unknown type %q", f.Name, f.Type)
		case f.Type == "enum" && len(f.Enum) == 0:
			problem = fmt.Sprintf("field %q has no enum choices", f.Name)
		case len(f.Enum) > 0 && invalidChoice(f.Enum, f.Default):
			problem = fmt.Sprintf("field %q default %v is outside its enum", f.Name, f.Default)
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("noun %q: %s", kind, problem))
		}
		names[f.Name] = true
	}
	return problems
}

func invalidChoice(choices []string, value any) bool {
	if value == nil {
		return false
	}
	text, ok := value.(string)
	return !ok || !slices.Contains(choices, text)
}

func validateStatusColumns(kind string, names map[string]bool, columns []CustomStatusColSpec) []string {
	var problems []string
	columnsSeen := make(map[string]bool)
	for _, col := range columns {
		if strings.TrimSpace(col.Name) == "" || columnsSeen[col.Name] {
			problems = append(problems, fmt.Sprintf("noun %q has an empty or duplicate status column %q", kind, col.Name))
		}
		if col.Default != "" && !slices.Contains(col.Names(), col.Default) {
			problems = append(problems, fmt.Sprintf("noun %q status column %q has an unknown default %q", kind, col.Name, col.Default))
		}
		columnsSeen[col.Name], names[col.Name] = true, true
	}
	return problems
}

func validateSchemas(cfg SchemaOverlayFile) error {
	if problems := ValidateSchemas(cfg); len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (cfg SchemaOverlayFile) overlay() data.SchemaOverlay {
	overlay := data.SchemaOverlay{Extend: make(map[string][]data.FieldSpec, len(cfg.Extend))}
	for kind, fields := range cfg.Extend {
		overlay.Extend[kind] = schemaFields(fields)
	}
	for _, noun := range cfg.Nouns {
		schema := data.Schema{
			Kind: noun.Kind, Title: noun.Title, Plural: noun.Plural, SummaryField: noun.SummaryField,
			Fields: schemaFields(noun.Fields), StatusColumns: noun.StatusColumns,
		}
		setSchemaDefaults(&schema, noun.Icon)
		overlay.Nouns = append(overlay.Nouns, schema)
	}
	return overlay
}

func schemaFields(fields []CustomFieldSpec) []data.FieldSpec {
	out := slices.Clone(fields)
	for i := range out {
		if out[i].Type == "enum" {
			out[i].Type = core.ParamString
		}
	}
	return out
}

func setSchemaDefaults(schema *data.Schema, icon string) {
	if schema.Title == "" {
		schema.Title = schema.Kind
	}
	if schema.Plural == "" {
		schema.Plural = schema.Title
	}
	if schema.SummaryField != "" {
		schema.DefaultColumns = []string{schema.SummaryField}
	}
	if icon != "" {
		schema.Presentations = make(map[string]data.PresentationSpec, len(data.PresentationForms))
		for _, form := range data.PresentationForms {
			schema.Presentations[form] = data.PresentationSpec{Icon: icon}
		}
	}
	for _, col := range schema.StatusColumns {
		mergeStatusField(schema, col)
	}
}

func mergeStatusField(schema *data.Schema, col CustomStatusColSpec) {
	i := slices.IndexFunc(schema.Fields, func(f data.FieldSpec) bool { return f.Name == col.Name })
	if i < 0 {
		schema.Fields = append(schema.Fields, data.FieldSpec{Name: col.Name, Type: core.ParamString})
		i = len(schema.Fields) - 1
	}
	schema.Fields[i].Enum = col.Names()
	if col.Default != "" {
		schema.Fields[i].Default = col.Default
	}
}

// SaveSchemas atomically replaces schemas.json with a validated JSON overlay.
func SaveSchemas(dir string, cfg SchemaOverlayFile) error {
	payload, err := encodeSchemas(cfg)
	if err != nil {
		return err
	}
	return withSchemasLock(dir, func() error { return writeSchemas(dir, payload) })
}

func encodeSchemas(cfg SchemaOverlayFile) ([]byte, error) {
	if err := validateSchemas(cfg); err != nil {
		return nil, err
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	return append(payload, '\n'), err
}

func writeSchemas(dir string, payload []byte) error {
	tmp, err := os.CreateTemp(dir, ".schemas-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(payload)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, schemasFileName))
}

func withSchemasLock(dir string, fn func() error) error {
	if dir == "" {
		return errors.New("schemas.json needs a configuration directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".schemas.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return fn()
}

// SchemaSource keeps a registry synchronized with a schemas.json file.
type SchemaSource struct {
	dir     string
	layer   *data.SchemaOverlayLayer
	mu      sync.Mutex
	applied []byte
}

func NewSchemaSource(dir string, reg *data.Registry) *SchemaSource {
	return &SchemaSource{dir: dir, layer: data.NewSchemaOverlayLayer(reg)}
}

// Refresh applies changes, preserving the last valid registry on an error.
func (s *SchemaSource) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := readSchemas(s.dir)
	if err != nil || bytes.Equal(raw, s.applied) {
		return err
	}
	cfg, err := decodeSchemas(raw)
	if err != nil {
		return err
	}
	return s.replace(cfg, raw, nil)
}

// Update reads the latest overlay under the file lock and publishes it to disk
// and the registry only after validation succeeds.
func (s *SchemaSource) Update(edit func(*SchemaOverlayFile)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withSchemasLock(s.dir, func() error {
		cfg, err := LoadSchemas(s.dir)
		if err != nil {
			return err
		}
		edit(&cfg)
		payload, err := encodeSchemas(cfg)
		if err != nil {
			return err
		}
		return s.replace(cfg, payload, func() error { return writeSchemas(s.dir, payload) })
	})
}

func (s *SchemaSource) replace(cfg SchemaOverlayFile, raw []byte, persist func() error) error {
	if err := s.layer.Replace(cfg.overlay(), persist); err != nil {
		return err
	}
	s.applied = raw
	return nil
}
