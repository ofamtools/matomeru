package data

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// The named presentation forms every noun can declare. A call site asks for
// a form; which fields fill it was decided once, as data.
const (
	FormChip   = "chip"   // compact pill: filter chips, relation tags, breadcrumbs, ref cells
	FormInline = "inline" // a run or link inside prose, headers and status lines
	FormRow    = "row"    // a list, table or picker row
	FormCard   = "card"   // a dashboard, project home or site card
	FormDetail = "detail" // a Show page header
)

// PresentationForms lists the forms in display order.
var PresentationForms = []string{FormChip, FormInline, FormRow, FormCard, FormDetail}

// universalTitleFields are tried, after the schema's SummaryField, when a
// noun declares no title fields.
var universalTitleFields = []string{"title", "name", "label", "summary"}

// PresentationSpec declares how one form of a noun is filled from a record.
// Every field is optional; a zero field takes the layer below it.
type PresentationSpec struct {
	Icon           string            `json:"icon,omitempty"`
	IconField      string            `json:"iconField,omitempty"`
	IconMap        map[string]string `json:"iconMap,omitempty"`
	TitleFields    []string          `json:"titleFields,omitempty"`
	SubtitleFields []string          `json:"subtitleFields,omitempty"`
	MetaFields     []string          `json:"metaFields,omitempty"`
	BadgeFields    []string          `json:"badgeFields,omitempty"`
	ToneField      string            `json:"toneField,omitempty"`
	ToneMap        map[string]string `json:"toneMap,omitempty"`
	DefaultTone    string            `json:"defaultTone,omitempty"`
	MaxLen         int               `json:"maxLen,omitempty"`
	Prefix         string            `json:"prefix,omitempty"`
}

// Over lays p over base: each field p sets wins, and maps merge key by key.
func (p PresentationSpec) Over(base PresentationSpec) PresentationSpec {
	out := base
	pick := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	pickList := func(dst *[]string, v []string) {
		if len(v) > 0 {
			*dst = slices.Clone(v)
		}
	}
	pick(&out.Icon, p.Icon)
	pick(&out.IconField, p.IconField)
	pick(&out.ToneField, p.ToneField)
	pick(&out.DefaultTone, p.DefaultTone)
	pick(&out.Prefix, p.Prefix)
	pickList(&out.TitleFields, p.TitleFields)
	pickList(&out.SubtitleFields, p.SubtitleFields)
	pickList(&out.MetaFields, p.MetaFields)
	pickList(&out.BadgeFields, p.BadgeFields)
	out.IconMap = mergeStrings(base.IconMap, p.IconMap)
	out.ToneMap = mergeStrings(base.ToneMap, p.ToneMap)
	if p.MaxLen != 0 {
		out.MaxLen = p.MaxLen
	}
	return out
}

func mergeStrings(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := maps.Clone(base)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, over)
	return out
}

// NounPresentations is the set of forms one noun declares.
type NounPresentations struct {
	Kind  string                      `json:"kind"`
	Forms map[string]PresentationSpec `json:"forms"`
}

// presentationsFile is the shape of the embedded defaults and of the user's
// presentations.json.
type presentationsFile struct {
	Nouns []NounPresentations `json:"nouns"`
}

//go:embed presentations/*.json
var presentationsFS embed.FS

// PresentationRegistry holds the declared presentation forms of every noun:
// the embedded defaults with the user's file laid over them. A noun or form
// nobody declared resolves to a universal default computed from its Schema,
// and an extension's Schema.Presentations sit between the two.
type PresentationRegistry struct {
	byKind map[string]map[string]PresentationSpec
}

// DefaultPresentations returns the embedded defaults alone.
func DefaultPresentations() *PresentationRegistry {
	r, err := LoadPresentations(nil)
	if err != nil {
		panic(fmt.Sprintf("data: embedded presentations: %v", err)) // a build bug, caught by the tests
	}
	return r
}

// LoadPresentations returns the embedded defaults with user (plain JSON, the
// shape {"nouns":[{"kind":..,"forms":{..}}]}) laid over them form by form.
// An empty user leaves the defaults. Nothing is written.
func LoadPresentations(user []byte) (*PresentationRegistry, error) {
	r := &PresentationRegistry{byKind: map[string]map[string]PresentationSpec{}}
	entries, err := presentationsFS.ReadDir("presentations")
	if err != nil {
		return nil, err
	}
	for _, ent := range entries {
		b, err := presentationsFS.ReadFile("presentations/" + ent.Name())
		if err != nil {
			return nil, err
		}
		if err := r.merge(b); err != nil {
			return nil, fmt.Errorf("%s: %w", ent.Name(), err)
		}
	}
	if len(strings.TrimSpace(string(user))) > 0 {
		if err := r.merge(user); err != nil {
			return nil, fmt.Errorf("presentations.json: %w", err)
		}
	}
	return r, nil
}

// merge lays a presentations file over r.
func (r *PresentationRegistry) merge(b []byte) error {
	var f presentationsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	for _, noun := range f.Nouns {
		if noun.Kind == "" {
			return fmt.Errorf("a noun presentation has no kind")
		}
		forms := r.byKind[noun.Kind]
		if forms == nil {
			forms = map[string]PresentationSpec{}
			r.byKind[noun.Kind] = forms
		}
		for form, spec := range noun.Forms {
			forms[form] = spec.Over(forms[form])
		}
	}
	return nil
}

// Kinds lists the nouns that declare forms, sorted.
func (r *PresentationRegistry) Kinds() []string {
	if r == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(r.byKind))
}

// Declared returns the spec the registry itself holds for kind and form.
func (r *PresentationRegistry) Declared(kind, form string) (PresentationSpec, bool) {
	if r == nil {
		return PresentationSpec{}, false
	}
	spec, ok := r.byKind[kind][form]
	return spec, ok
}

// Spec returns the effective spec of one form of schema's noun: the universal
// default, then the schema's own Presentations, then the registry's.
func (r *PresentationRegistry) Spec(schema Schema, form string) PresentationSpec {
	spec := universalSpec(schema, form)
	if own, ok := schema.Presentations[form]; ok {
		spec = own.Over(spec)
	}
	if declared, ok := r.Declared(schema.Kind, form); ok {
		spec = declared.Over(spec)
	}
	return spec
}

// Effective lists every form of every noun in schemas (and every noun the
// registry declares that schemas leave out) as it resolves.
func (r *PresentationRegistry) Effective(schemas []Schema) []NounPresentations {
	seen := map[string]bool{}
	var out []NounPresentations
	add := func(s Schema) {
		if seen[s.Kind] {
			return
		}
		seen[s.Kind] = true
		np := NounPresentations{Kind: s.Kind, Forms: map[string]PresentationSpec{}}
		for _, form := range PresentationForms {
			np.Forms[form] = r.Spec(s, form)
		}
		out = append(out, np)
	}
	for _, s := range schemas {
		add(s)
	}
	for _, kind := range r.Kinds() {
		add(Schema{Kind: kind, Title: kind, Plural: kind})
	}
	return out
}

// primaryStatus is the status column that styles a noun's rows.
func (s Schema) primaryStatus() (StatusColumn, bool) {
	for _, c := range s.StatusColumns {
		if c.PrimaryRowStyle {
			return c, true
		}
	}
	if len(s.StatusColumns) > 0 {
		return s.StatusColumns[0], true
	}
	return StatusColumn{}, false
}

// universalSpec is the default every form of a noun gets from its schema
// alone: its summary or a common title field, its primary status as icon
// and tone, and per form a sensible length and detail.
func universalSpec(s Schema, form string) PresentationSpec {
	spec := PresentationSpec{TitleFields: universalTitleFields}
	if s.SummaryField != "" && !slices.Contains(universalTitleFields, s.SummaryField) {
		spec.TitleFields = append([]string{s.SummaryField}, universalTitleFields...)
	}
	status, hasStatus := s.primaryStatus()
	if hasStatus {
		spec.IconField, spec.ToneField = status.Name, status.Name
	}
	switch form {
	case FormChip:
		spec.MaxLen, spec.DefaultTone = 32, "accent"
	case FormInline:
		spec.MaxLen, spec.DefaultTone = 48, "accent"
	case FormRow:
		spec.MaxLen = 64
	case FormCard, FormDetail:
		spec.SubtitleFields = []string{"summary", "description"}
		for _, c := range s.StatusColumns {
			spec.BadgeFields = append(spec.BadgeFields, c.Name)
		}
	}
	return spec
}

// PresentationBadge is one badge of a resolved presentation.
type PresentationBadge struct {
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

// ResolvedPresentation is one record filled into one form: pure display
// data plus the exact Ref it stands for. Ref is the identity a caller
// matches, opens and yanks; Label is only ever shown.
type ResolvedPresentation struct {
	Ref      core.Ref            `json:"ref"`
	Form     string              `json:"form"`
	Icon     string              `json:"icon,omitempty"`
	Label    string              `json:"label"`
	Subtitle string              `json:"subtitle,omitempty"`
	Tone     string              `json:"tone,omitempty"`
	Meta     []string            `json:"meta,omitempty"`
	Badges   []PresentationBadge `json:"badges,omitempty"`
	Missing  bool                `json:"missing,omitempty"`
}

// Display is the icon and label as one string.
func (p ResolvedPresentation) Display() string {
	if p.Icon == "" {
		return p.Label
	}
	return p.Icon + " " + p.Label
}

// MissingPresentation is how a dangling reference presents: its kind, never
// its ID.
func MissingPresentation(ref core.Ref, form string) ResolvedPresentation {
	return ResolvedPresentation{Ref: ref, Form: form, Missing: true, Label: "(missing " + ref.Kind + ")", Tone: "muted"}
}

// Resolve fills one form of rec's noun.
func (r *PresentationRegistry) Resolve(rec Record, schema Schema, form string) ResolvedPresentation {
	spec := r.Spec(schema, form)
	status := map[string]StatusColumn{}
	for _, c := range schema.StatusColumns {
		status[c.Name] = c
	}
	p := ResolvedPresentation{Ref: rec.Ref(), Form: form, Icon: spec.Icon, Tone: spec.DefaultTone}
	title := clip(firstFieldText(rec, spec.TitleFields), spec.MaxLen)
	if title == "" {
		title = "Untitled " + strings.ToLower(cmpOr(schema.Title, rec.Kind))
	}
	p.Label = spec.Prefix + title
	p.Subtitle = firstFieldText(rec, spec.SubtitleFields)
	for _, field := range spec.MetaFields {
		if v := presentationText(rec, field); v != "" {
			p.Meta = append(p.Meta, v)
		}
	}
	for _, field := range spec.BadgeFields {
		if v := presentationText(rec, field); v != "" {
			p.Badges = append(p.Badges, statusBadge(status[field], v, spec.ToneMap))
		}
	}
	if v := presentationText(rec, spec.ToneField); v != "" {
		if tone := lookupTone(status[spec.ToneField], v, spec.ToneMap); tone != "" {
			p.Tone = tone
		}
	}
	if v := presentationText(rec, spec.IconField); v != "" {
		if icon := lookupIcon(status[spec.IconField], v, spec.IconMap); icon != "" {
			p.Icon = icon
		}
	}
	return p
}

// ResolveRef fills one form for a reference whose record may not be stored.
// A noun that titles itself by its "id" (a directory by its path, a tag by
// its name) presents from the reference alone; any other reference with no
// record is missing.
func (r *PresentationRegistry) ResolveRef(ref core.Ref, rec *Record, schema Schema, form string) ResolvedPresentation {
	if rec != nil {
		return r.Resolve(*rec, schema, form)
	}
	if slices.Contains(r.Spec(schema, form).TitleFields, "id") && ref.ID != "" {
		return r.Resolve(Record{Kind: ref.Kind, ID: ref.ID}, schema, form)
	}
	return MissingPresentation(ref, form)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func statusBadge(col StatusColumn, v string, toneMap map[string]string) PresentationBadge {
	b := PresentationBadge{Text: v, Tone: lookupTone(col, v, toneMap)}
	if sv, ok := col.Value(v); ok && sv.Label != "" {
		b.Text = sv.Label
	}
	return b
}

func lookupTone(col StatusColumn, v string, toneMap map[string]string) string {
	if tone := toneMap[v]; tone != "" {
		return tone
	}
	sv, _ := col.Value(v)
	return sv.Tone
}

func lookupIcon(col StatusColumn, v string, iconMap map[string]string) string {
	if icon := iconMap[v]; icon != "" {
		return icon
	}
	sv, _ := col.Value(v)
	return sv.Icon
}

// firstFieldText is the first field of fields with display text.
func firstFieldText(rec Record, fields []string) string {
	for _, field := range fields {
		if v := presentationText(rec, field); v != "" {
			return v
		}
	}
	return ""
}

// presentationText is a field's first line of display text. A reference
// field gives nothing: its target is presented on its own, never as kind:id.
func presentationText(rec Record, field string) string {
	if field == "" {
		return ""
	}
	var s string
	if v, ok := rec.Value(field); ok && v != nil {
		if _, isRef := AsRef(v); isRef {
			return ""
		}
		s = strings.TrimSpace(Text(v))
	} else {
		s = strings.TrimSpace(RecordFieldText(rec, field))
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// clip shortens s to at most n runes (n <= 0 keeps it whole).
func clip(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
