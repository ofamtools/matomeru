package main

import (
	"fmt"

	"ofam.dev/ochazuke"
)

// Creation variants are data, not separate directory/base UI flows. Each
// variant supplies the same noun's initial props to the same path picker.
type creationVariant struct {
	ID, Label string
	Props     ochazuke.Arguments
	EditField string
}

var directoryCreationVariants = []creationVariant{
	{ID: "directory", Label: "A directory", Props: ochazuke.Arguments{"depth": 0}},
	{ID: "base", Label: "A base directory to scan", Props: ochazuke.Arguments{"depth": 1}, EditField: "depth"},
}

func (a *App) chooseCreation(noun string, variants []creationVariant) ochazuke.Result {
	choices := make([]ochazuke.Choice, 0, len(variants))
	byID := map[string]creationVariant{}
	for _, variant := range variants {
		choices = append(choices, plainChoice(variant.ID, variant.Label))
		byID[variant.ID] = variant
	}
	_, err := a.choose(pickSpec{Kind: "add-kind", Title: "What to add", Choices: choices, NoSort: true, Pick: func(id string) ochazuke.Result {
		variant := byID[id]
		return resultError(a.chooseDirectoryFor("Add "+variant.Label, "", func(path string) ochazuke.Result {
			props := ochazuke.Arguments{"path": path}
			for key, value := range variant.Props {
				props[key] = value
			}
			value, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: "create", Args: props})
			if err != nil {
				return ochazuke.Failed(err)
			}
			if variant.EditField != "" {
				return resultError(a.editDirectoryFields(a.expandPath(path), variant.EditField))
			}
			return ochazuke.Done(value)
		}))
	}})
	return resultError(err)
}

// The generated new key still calls create. Only argument-free interactive
// creation changes; CLI/API creates and the standard form remain available.
func (a *App) defineDirectoryCreation() {
	op, ok := a.r.Operations.Get("directory.create")
	if !ok {
		panic("directory.create is not registered")
	}
	// At this verb boundary path is optional: its absence asks what to add.
	// The store's key prop remains required, including for headless creation.
	pathParam := op.Parameters["path"]
	pathParam.Required = false
	op.Parameters["path"] = pathParam
	op.Positional = []string{"path", "pairs"}
	run := op.AppHandler
	op.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
		if !a.cli && !a.headless && len(c.Args) == 0 && a.r.Settings().Bool(settingDirectoryAddChoice) {
			return a.chooseCreation("directory", directoryCreationVariants)
		}
		return run(c)
	}
	a.r.Operations.Set(*op)
	edit, ok := a.r.Operations.Get("directory.edit")
	if !ok {
		panic("directory.edit is not registered")
	}
	standardEdit := edit.AppHandler
	edit.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
		path := a.directoryPathAt(c)
		if !a.cli && !a.headless && len(c.Args) == 0 && path != "" {
			return resultError(a.editDirectoryFields(path, ""))
		}
		return standardEdit(c)
	}
	a.r.Operations.Set(*edit)
}

// A mutable record key is owned here: after a location write the same form's
// next write follows the new key. All fields still use the scaffold's schema,
// validation, path picker, debounce, undo and commit-on-change editor.
func (a *App) editDirectoryFields(path, field string) error {
	spec, err := a.r.EditSpec("directory", path)
	if err != nil {
		return err
	}
	current := path
	spec.Title = "Directory · written as you type"
	spec.Debounce = a.editDebounce()
	spec.Start = field
	spec.Watch = "" // this editor follows its mutable key itself
	if field != "" {
		for _, f := range spec.Fields {
			if f.Name == field {
				spec.Fields = []ochazuke.RecordField{f}
				break
			}
		}
	}
	types := map[string]string{}
	for _, f := range spec.Fields {
		types[f.Name] = f.Type
	}
	spec.Save = func(w ochazuke.RecordWrite) ochazuke.Result {
		changes := ochazuke.Thing{}
		for _, key := range w.Changed {
			value, err := ochazuke.ParseFlagValue(types[key], w.Values[key])
			if err != nil {
				return ochazuke.Failed(err)
			}
			changes[key] = value
		}
		thing, err := a.directoryStore().Patch(ochazuke.Ctx{}, current, changes)
		if err != nil {
			return ochazuke.Failed(err)
		}
		current = fmt.Sprint(thing["path"])
		a.r.Changed("directory")
		return ochazuke.Done(thing)
	}
	spec.OnLeave = func() {
		a.directoryRecordChanged(current)
		if a.r.Settings().Bool(settingDirectorySelect) && a.r.Page == a.r.Instances[directoryListID].Parent {
			_ = a.directoryList.Focus()
			a.directories.Want = current
		}
		_ = a.loadDirectories()
	}
	_, err = a.r.OpenRecordEditor("directory-edit", spec)
	return err
}
