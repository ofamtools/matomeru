package main

import (
	"encoding/json"
	"fmt"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/internal/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

func openConfiguredStore(g globalOpts) (*data.Store, *config.SchemaSource, error) {
	store, err := openSeededStore(g)
	if err != nil {
		return nil, nil, err
	}
	source := config.NewSchemaSource(configDir(), store.Registry())
	_ = source.Refresh()
	return store, source, nil
}

func openStoreWithSeed(g globalOpts) (*data.Store, error) {
	store, _, err := openConfiguredStore(g)
	return store, err
}

func mtmSchemaCommands(g globalOpts) []oden.Command {
	var commands []oden.Command
	for _, spec := range app.SchemaCommandSpecs() {
		var params []oden.Parameter
		for _, p := range spec.Params {
			params = append(params, oden.Parameter{
				Name: p.Name, Type: string(p.Type), Required: p.Required,
				Description: p.Description, Enum: p.Enum,
			})
		}
		commands = append(commands, oden.Command{
			Name: spec.ID, Summary: spec.Description, Category: "Configuration",
			Parameters: params,
			Handle: func(c *oden.Ctx) error {
				return runCLISchemaCommand(c, g, spec.ID, params)
			},
		})
	}
	return commands
}

func runCLISchemaCommand(c *oden.Ctx, g globalOpts, id string, params []oden.Parameter) error {
	p, err := c.Parse(params)
	if err != nil {
		return err
	}
	if len(p.Positional()) > 0 {
		return fmt.Errorf("%s takes flags only", id)
	}
	args, err := cliSchemaArgs(p, params)
	if err != nil {
		return err
	}
	store, source, err := openConfiguredStore(g)
	if err != nil {
		return err
	}
	defer store.Close()
	status, err := app.RunSchemaCommand(source, core.Command{ID: id, Args: args})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.Stdout, status)
	return err
}

func cliSchemaArgs(p oden.Parsed, params []oden.Parameter) (map[string]any, error) {
	args := make(map[string]any, len(params))
	for _, spec := range params {
		if !p.Has(spec.Name) {
			continue
		}
		switch core.ParamType(spec.Type) {
		case core.ParamList:
			args[spec.Name] = p.Strings(spec.Name)
		case core.ParamObject:
			var value any
			if err := json.Unmarshal([]byte(p.String(spec.Name)), &value); err != nil {
				return nil, fmt.Errorf("--%s: expected JSON: %w", spec.Name, err)
			}
			args[spec.Name] = value
		default:
			args[spec.Name] = p.String(spec.Name)
		}
	}
	return args, nil
}
