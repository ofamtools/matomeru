package main

import (
	"errors"

	"ofam.dev/ochazuke"
)

// mtm's pickers in ochazuke's picker registry (pickers plan PK4): each is
// a PickerDef, so picker.open NAME opens it, picker.list and the pickers
// picker (picker.open pickers) list it with the keys bound to its verb,
// and the palette and the CLI reach it by name. Open builds the chooser
// (rows, cells, previews and picks are Go: a source verb's choices carry
// none of them); Verb is the verb that opens it, kept with its keys
// ("Keep actions"), so the pickers picker shows those keys. A verb with
// no thing of its own to act on (pane.pick, project.pick, panes.sides,
// agent.choose) opens its picker through the registry; a verb that takes
// its pane or project from its target (pane.pick-task, context-item.pick,
// project.directory) opens the same chooser for that target, and the
// registry's Open takes the focus's.

const (
	panePickerName      = "pane"
	pinPickerName       = "pin"
	paneTaskPickerName  = "pane-task"
	contextPickerName   = "context"
	directoryPickerName = "directory"
	sidePickerName      = "side"
	harnessPickerName   = "harness"
)

// pickerDefs are mtm's declared pickers. Noun is set only where the
// picker stands for its noun's things (it then replaces the noun's derived
// picker): the pane picker's rows are the panes, the context picker's the
// context items. The pin picker lists projects but its pick pins one, so
// the project noun keeps its derived picker.
func (a *App) pickerDefs() []ochazuke.PickerDef {
	return []ochazuke.PickerDef{
		{Name: panePickerName, Title: "Panes", Noun: "pane", Verb: "pane.pick", Open: a.openPanePicker,
			About: "Every pane, attached before most recent first, the attached one last; pick one to attach it live"},
		{Name: pinPickerName, Title: "Pin project", Verb: "project.pick", Open: a.openPinPicker,
			About: "The projects, most recently pinned first, and no pin; pick one to pin it (a typed name creates one)"},
		{Name: paneTaskPickerName, Title: "Task for a pane", Verb: "pane.pick-task", Open: a.openFocusedTaskPicker,
			About: "The focused pane's task or project: its current one first, open tasks, projects, a new task; pick one to associate the pane"},
		{Name: contextPickerName, Title: "Context", Noun: contextNoun, Verb: contextNoun + ".pick", Open: a.openLiveContextPicker,
			About: "The live pane's context items and prompts, then its project's, then no project's; pick one to copy it"},
		{Name: directoryPickerName, Title: "Directory for a project", Verb: "project.directory", Open: a.openFocusedDirectoryPicker,
			About: "A directory for the focused project (else the pinned one): walk, known, browse, recent and typed paths; pick one to attach it as the default"},
		{Name: sidePickerName, Title: "Side panes", Verb: "panes.sides", Open: a.openSidePicker,
			About: "Every open side across every pane; pick one to attach its pane, show the side and focus it"},
		{Name: harnessPickerName, Title: "Launch an agent", Verb: "agent.choose", Open: a.openHarnessPicker,
			About: "The harnesses, the pinned project's own first; pick one to launch an agent in the pinned project"},
	}
}

// definePickers registers mtm's pickers; it runs after every verb a
// picker names is defined.
func (a *App) definePickers() {
	for _, def := range a.pickerDefs() {
		if err := a.r.AddPicker(def); err != nil {
			panic(err)
		}
	}
}

// openNamedPicker is a verb that opens its picker by name, as
// picker.open NAME does.
func (a *App) openNamedPicker(name string) ochazuke.Result {
	_, err := a.r.OpenNamedPicker(name, "")
	return resultError(err)
}

// errNoProjectForDirectory is the directory picker's answer with no
// project at the focus and none pinned.
var errNoProjectForDirectory = errors.New("focus a project or pin one to choose its directory")

// openFocusedDirectoryPicker is the directory picker's Open: the focused
// project row's directory pick (c on a project), else the pinned
// project's.
func (a *App) openFocusedDirectoryPicker(*ochazuke.Runtime, string) (*ochazuke.Chooser, error) {
	id := a.pin
	// The focus is a project when its id is a loaded project's.
	if p, ok := a.project(a.r.FocusID()); ok {
		id = p.ID
	}
	if id == "" {
		return nil, errNoProjectForDirectory
	}
	if err := a.openDirectoryChooser(id); err != nil {
		return nil, err
	}
	return a.r.Chooser(dirChooser), nil
}
