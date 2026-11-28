package app

import (
	"slices"
	"strings"
	"sync"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

// Props on the Features page are never empty: every noun, page, component,
// domain group, organized group and concept kind resolves to declared rows.
// The role of a row says where the prop comes from.
const (
	featuresPropRoleSetting   = "setting"
	featuresPropRoleFact      = "fact"
	featuresPropRoleSurface   = "viewnode"
	featuresPropRoleToken     = "theme token"
	featuresPropRoleUniversal = "universal"
	featuresPropRoleCurrent   = "in use"
	featuresPropRolePlanned   = "planned"
)

// featuresPropsEntry declares the props of a page, component or domain group:
// its settings.json keys (resolved against SettingSpecs), the Facts it
// exposes to binding guards, its ViewNode props and surfaces, and the theme
// tokens it draws with.
type featuresPropsEntry struct {
	Settings []string
	Facts    []views.FeaturesPropRow
	Surfaces []views.FeaturesPropRow
	Tokens   []views.FeaturesPropRow
}

func propRow(role, name, typ, desc string) views.FeaturesPropRow {
	return views.FeaturesPropRow{Name: name, Type: typ, Role: role, Description: desc}
}

func propFact(name, typ, desc string) views.FeaturesPropRow {
	return propRow(featuresPropRoleFact, name, typ, desc)
}

func propSurface(name, kind, desc string) views.FeaturesPropRow {
	return propRow(featuresPropRoleSurface, name, kind, desc)
}

func propToken(name, desc string) views.FeaturesPropRow {
	return propRow(featuresPropRoleToken, name, "tone", desc)
}

// nprop declares one property of an existing or planned noun; its role is set
// from the noun's status when the rows are resolved.
func nprop(name, typ, desc string) views.FeaturesPropRow {
	return views.FeaturesPropRow{Name: name, Type: typ, Description: desc}
}

func propFacts(groups ...[]views.FeaturesPropRow) []views.FeaturesPropRow {
	return slices.Concat(groups...)
}

var featuresSettingSpecIndex = sync.OnceValue(func() map[string]core.ParamSpec {
	index := map[string]core.ParamSpec{}
	for _, spec := range SettingSpecs() {
		index[spec.Name] = spec
	}
	return index
})

// featuresSettingPropRows resolves settings.json keys to rows carrying the
// type, default, choices and description SettingSpecs declares.
func featuresSettingPropRows(keys []string) []views.FeaturesPropRow {
	index := featuresSettingSpecIndex()
	rows := make([]views.FeaturesPropRow, 0, len(keys))
	for _, key := range keys {
		row := views.FeaturesPropRow{Name: key, Role: featuresPropRoleSetting}
		if spec, ok := index[key]; ok {
			row.Type = string(spec.Type)
			row.Default = data.Text(spec.Default)
			row.Enum = strings.Join(spec.Enum, ", ")
			row.Description = spec.Description
		}
		rows = append(rows, row)
	}
	return rows
}

func (e featuresPropsEntry) rows() []views.FeaturesPropRow {
	return slices.Concat(featuresSettingPropRows(e.Settings), e.Facts, e.Surfaces, e.Tokens)
}

// featuresPagePropRows lists a page's props; a page with no declared entry
// still shows the props every page has.
func featuresPagePropRows(pageKey string) []views.FeaturesPropRow {
	if rows := featuresPagePropsCatalog[pageKey].rows(); len(rows) > 0 {
		return rows
	}
	return featuresUniversalPageProps.rows()
}

func featuresComponentPropRows(key string) []views.FeaturesPropRow {
	if rows := featuresComponentPropsCatalog[key].rows(); len(rows) > 0 {
		return rows
	}
	return featuresUniversalPageProps.rows()
}

// featuresDomainPropRows lists a domain group's props; "everything" and any
// group with no declared entry show the shape of a domain occurrence.
func featuresDomainPropRows(key string) []views.FeaturesPropRow {
	if rows := featuresDomainPropsCatalog[key].rows(); len(rows) > 0 {
		return rows
	}
	return slices.Clone(featuresDomainOccurrenceProps)
}

func featuresSchemaPropRows(sc data.Schema) []views.FeaturesPropRow {
	rows := make([]views.FeaturesPropRow, 0, len(sc.Fields))
	for _, field := range sc.Fields {
		rows = append(rows, views.FeaturesPropRow{
			Name:        field.Name,
			Type:        string(field.Type),
			Role:        field.EffectiveRole(sc.SummaryField),
			Default:     data.Text(field.Default),
			Enum:        strings.Join(field.Enum, ", "),
			Description: field.Description,
		})
	}
	return rows
}

// featuresDeclaredNounPropRows lists the declared props of an existing or
// planned noun, with the role its catalog status gives them.
func featuresDeclaredNounPropRows(kind string) []views.FeaturesPropRow {
	cat, _ := lookupFeaturesNounCatalog(kind)
	role := featuresPropRoleCurrent
	if cat.Status == NounStatusPlanned {
		role = featuresPropRolePlanned
	}
	rows := slices.Clone(cat.Props)
	for i := range rows {
		rows[i].Role = role
	}
	return rows
}

func featuresSuggestedPropRows(kind string, concepts []data.Record) []views.FeaturesPropRow {
	var rows []views.FeaturesPropRow
	for _, rec := range concepts {
		if featuresPendingConcept(rec, data.ConceptKindProp, kind, featureNounKey(kind).String()) {
			rows = append(rows, views.FeaturesPropRow{Name: rec.Text("title"), Description: rec.Text("summary"), Suggested: true})
		}
	}
	return rows
}

// featuresNounPropRows lists a noun's props. A live noun shows its schema
// fields; a noun with no schema shows its declared props. The noun's own
// detail (universal true) adds the fields every record has, or stands on them
// when nothing else is declared, so the list is never empty.
func featuresNounPropRows(sc data.Schema, isLive bool, kind string, concepts []data.Record, universal bool) []views.FeaturesPropRow {
	var rows []views.FeaturesPropRow
	if isLive {
		rows = featuresSchemaPropRows(sc)
	} else {
		rows = featuresDeclaredNounPropRows(kind)
	}
	if universal && (isLive || len(rows) == 0) {
		rows = append(rows, featuresUniversalRecordProps...)
	}
	return append(rows, featuresSuggestedPropRows(kind, concepts)...)
}

// featuresRecordPropRows lists the schema fields a record has a value for,
// then the universal record fields.
func featuresRecordPropRows(sc data.Schema, rec data.Record) []views.FeaturesPropRow {
	var rows []views.FeaturesPropRow
	for _, row := range featuresSchemaPropRows(sc) {
		if strings.TrimSpace(rec.Text(row.Name)) != "" {
			rows = append(rows, row)
		}
	}
	return append(rows, featuresUniversalRecordProps...)
}
