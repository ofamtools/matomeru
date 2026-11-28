package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"ofam.dev/matomeru/internal/config"
	"ofam.dev/ochazuke/core"
)

const (
	CmdSchemaPropAdd = "schema.prop.add"
	CmdSchemaNounAdd = "schema.noun.add"
)

// SchemaCommandSpecs describes the schema editing verbs for the palette and CLI.
func SchemaCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID: CmdSchemaPropAdd, Description: "Add a property to a noun in schemas.json",
			Tags: []string{"editing"},
			Params: []core.ParamSpec{
				{Name: "noun", Type: core.ParamString, Required: true, Description: "Noun kind"},
				{Name: "name", Type: core.ParamString, Required: true, Description: "Property name"},
				{Name: "type", Type: core.ParamString, Required: true, Enum: config.SchemaFieldTypes(), Description: "Property type"},
				{Name: "description", Type: core.ParamString, Description: "Property help text"},
				{Name: "enum", Type: core.ParamList, Description: "Allowed values"},
				{Name: "default", Type: core.ParamObject, Description: "Default value as JSON"},
			},
		},
		{
			ID: CmdSchemaNounAdd, Description: "Add a noun to schemas.json",
			Tags: []string{"editing"},
			Params: []core.ParamSpec{
				{Name: "kind", Type: core.ParamString, Required: true, Description: "Noun kind"},
				{Name: "title", Type: core.ParamString, Description: "Singular display title"},
				{Name: "plural", Type: core.ParamString, Description: "Plural display title"},
				{Name: "icon", Type: core.ParamString, Description: "Presentation icon"},
				{Name: "summaryField", Type: core.ParamString, Description: "Property holding the record title"},
			},
		},
	}
}

func (a *App) loadSchemas(dir string) {
	a.schemaSource = config.NewSchemaSource(dir, a.Store.Registry())
	if err := a.schemaSource.Refresh(); err != nil {
		a.warn("schemas.json: %v", err)
	}
}

func (a *App) registerSchemaCommands() {
	for _, spec := range SchemaCommandSpecs() {
		a.handle(spec.ID, a.editSchema)
	}
}

func (a *App) editSchema(s core.State, c core.Command) core.StepResult {
	msg, err := RunSchemaCommand(a.schemaSource, c)
	if err != nil {
		a.fail(c.ID, err)
	} else {
		a.say("%s", msg)
	}
	return core.Next(s)
}

// RunSchemaCommand applies the same atomic edit for each command surface.
func RunSchemaCommand(source *config.SchemaSource, c core.Command) (string, error) {
	if source == nil {
		return "", fmt.Errorf("schemas.json needs a configuration directory")
	}
	switch c.ID {
	case CmdSchemaPropAdd:
		return addSchemaProp(source, c)
	case CmdSchemaNounAdd:
		return addSchemaNoun(source, c)
	}
	return "", fmt.Errorf("unknown schema command %q", c.ID)
}

func addSchemaProp(source *config.SchemaSource, c core.Command) (string, error) {
	noun := strings.TrimSpace(c.Arg("noun"))
	field := config.CustomFieldSpec{
		Name: strings.TrimSpace(c.Arg("name")), Type: core.ParamType(c.Arg("type")),
		Description: c.Arg("description"), Default: c.Args["default"],
	}
	if choices := c.Args["enum"]; choices != nil {
		raw, err := json.Marshal(choices)
		if err != nil {
			return "", fmt.Errorf("enum: %w", err)
		}
		if err := json.Unmarshal(raw, &field.Enum); err != nil {
			return "", fmt.Errorf("enum must be a list of strings: %w", err)
		}
	}
	err := source.Update(func(cfg *config.SchemaOverlayFile) {
		if cfg.Extend == nil {
			cfg.Extend = make(map[string][]config.CustomFieldSpec)
		}
		cfg.Extend[noun] = append(cfg.Extend[noun], field)
	})
	return fmt.Sprintf("Added property %q to %q", field.Name, noun), err
}

func addSchemaNoun(source *config.SchemaSource, c core.Command) (string, error) {
	noun := config.CustomNounSpec{
		Kind: strings.TrimSpace(c.Arg("kind")), Title: c.Arg("title"),
		Plural: c.Arg("plural"), Icon: c.Arg("icon"), SummaryField: strings.TrimSpace(c.Arg("summaryField")),
	}
	if noun.SummaryField == "" {
		noun.SummaryField = "title"
	}
	noun.Fields = []config.CustomFieldSpec{{Name: noun.SummaryField, Type: core.ParamString, Required: true}}
	err := source.Update(func(cfg *config.SchemaOverlayFile) {
		cfg.Nouns = append(cfg.Nouns, noun)
	})
	return fmt.Sprintf("Added noun %q", noun.Kind), err
}
