package main

import (
	"cmp"
	"errors"
	"slices"
	"strings"

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
	a.defineChoiceSources()
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

// The one general picker (pickers plan PU5). Every mtm chooser that is not
// a path chooser or the palette is a pickSpec: rows come from a data source
// ([]ochazuke.Choice: labels, cells and tones are data, Choice.Row the only
// row builder), and the picked id goes to Pick. A chooser asking for a
// verb's missing parameter takes its rows from the parameter's own
// declaration (askParameter: Noun, Source, SourceArgs, Enum); a chooser
// that says more than a parameter can (a preview, props for the prop
// filters, a create row, a fixed order) is a pickerDefs row, still opened
// here. OpenPicker is called nowhere else in cmd/mtm outside the path
// chooser (TestPickersDeclared).

// pickFilter is how typing narrows a pickSpec's rows.
type pickFilter int

const (
	// pickFilterDefault is the chooser's own match mode.
	pickFilterDefault pickFilter = iota
	// pickFilterPrefix matches the start of the label (status choosers:
	// t, a, b, d).
	pickFilterPrefix
	// pickFilterTrailing is fuzzy, but a trailing row ("unassociated")
	// matches by substring only.
	pickFilterTrailing
)

// pickSpec is one chooser as data.
type pickSpec struct {
	Kind, Title, Empty, Plural string
	// Noun is the rows' noun (prop filters); Match the match mode.
	Noun, Match string
	// Keep names the columns cut last; NoSort keeps the source order.
	Keep   []int
	NoSort bool
	Filter pickFilter
	// Choices are the rows: the source's answer.
	Choices []ochazuke.Choice
	// Lead is the id listed first (the current one, the project's own).
	Lead string
	// Props are a row's props for the prop filters (nil: none).
	Props func(id string) ochazuke.Thing
	// Data is a row's app payload, read back from a highlighted row by
	// the picker's own keys (nil: none).
	Data func(id string) any
	// Preview is the highlighted row's text (nil: none).
	Preview func(id string) string
	// Pick is what enter does with the chosen id.
	Pick func(id string) ochazuke.Result
	// Create, when set, adds a row that creates from the typed query;
	// CreateLabel says what it will do.
	Create      func(query string) ochazuke.Result
	CreateLabel func(query string) string
	// Notice, when the source has no rows, is said instead of opening.
	Notice string
}

// rows are the spec's choices as chooser rows (Choice.Row), with props.
func (s pickSpec) rows() []ochazuke.PickerRow {
	if s.Lead != "" {
		s.Choices = slices.Clone(s.Choices)
		slices.SortStableFunc(s.Choices, func(x, y ochazuke.Choice) int { return -cmp.Compare(btoi(x.ID == s.Lead), btoi(y.ID == s.Lead)) })
	}
	rows := make([]ochazuke.PickerRow, len(s.Choices))
	for i, c := range s.Choices {
		rows[i] = c.Row()
		if s.Props != nil {
			rows[i].Props = s.Props(c.ID)
		}
		if s.Data != nil {
			rows[i].Data = s.Data(c.ID)
		}
	}
	return rows
}

// choose opens the general picker over s.
func (a *App) choose(s pickSpec) (*ochazuke.Chooser, error) {
	if len(s.Choices) == 0 && s.Notice != "" {
		a.r.UI().Notify(s.Notice)
		return nil, nil
	}
	spec := ochazuke.PickerSpec{
		Kind: s.Kind, Noun: s.Noun, Title: s.Title, Match: s.Match, Rows: s.rows(),
		Empty: s.Empty, Plural: s.Plural, Keep: s.Keep, NoSort: s.NoSort,
		Choose: func(row ochazuke.PickerRow) ochazuke.Result { return s.Pick(row.ID) },
	}
	switch s.Filter {
	case pickFilterPrefix:
		spec.Filter = func(query string, row ochazuke.PickerRow) bool {
			return strings.HasPrefix(row.Label, strings.ToLower(strings.TrimSpace(query)))
		}
	case pickFilterTrailing:
		spec.Filter = func(query string, row ochazuke.PickerRow) bool {
			if row.Trailing {
				return strings.Contains(row.Label, strings.ToLower(query))
			}
			return fuzzy(query, row.Label)
		}
	}
	if s.Preview != nil {
		spec.Preview = func(row ochazuke.PickerRow) string { return s.Preview(row.ID) }
	}
	if s.Create != nil {
		spec.Create, spec.CreateLabel, spec.CreateRow = s.Create, s.CreateLabel, true
	}
	return a.r.OpenPicker(spec)
}

// askParameter is the chooser for a verb's missing parameter: its rows
// are the parameter's declared choices (Noun, Source with SourceArgs read
// from args, Enum), so the palette, the form and the CLI offer the same.
func (a *App) askParameter(key, name string, args ochazuke.Arguments, s pickSpec) (*ochazuke.Chooser, error) {
	op, ok := a.r.Operation(key)
	if !ok {
		return nil, errors.New("unknown operation " + key)
	}
	choices, err := a.r.ParameterChoices(a.r.Context(), op, name, "", args)
	if err != nil {
		return nil, err
	}
	s.Choices = choices
	return a.choose(s)
}

// choiceSource registers key (noun.verb) as a read verb whose answer is
// choices: the Source of a parameter that asks for a thing the verb's
// noun has no list of (a status, an interval, the attachments of the
// prompt). Like projects.all, it reads the app's state; one that depends
// on the row it asks about reads it from its name parameter or the focus.
func (a *App) choiceSource(key, about string, params map[string]ochazuke.Parameter, fn func(ochazuke.Ctx) ([]ochazuke.Choice, error)) {
	noun, verb := ochazuke.SplitKey(key)
	a.r.Define(noun).Verb(verb, func(c ochazuke.Ctx) ochazuke.Result {
		choices, err := fn(c)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(choices)
	}, about).Fetch()
	if len(params) > 0 {
		a.parameters(key, params)
	}
}

// defineChoiceSources registers the choice sources the general picker's
// parameters read, and declares those parameters on the verbs that ask.
func (a *App) defineChoiceSources() {
	id := map[string]ochazuke.Parameter{"id": {Type: "string", Description: "The row asked about; omitted, the selected one"}}
	a.choiceSource("task.statuses", "List the statuses as choices with their glyph, the task's own marked (the status parameter's pick list)",
		map[string]ochazuke.Parameter{"id": id["id"], "count": {Type: "integer", Description: "How many tasks the choice sets; the current status is marked only for one"}}, a.taskStatuses)
	a.parameters("task.choose-status", map[string]ochazuke.Parameter{"status": {Type: "string", Source: "task.statuses", SourceArgs: map[string]string{"id": "id", "count": "count"},
		Description: "The status to set; omitted, a chooser asks"}})
	a.choiceSource(actionItemNoun+".snooze-times", "List when an item can be snoozed until as choices (the until parameter's pick list)", nil, a.snoozeTimes)
	a.choiceSource("review.statuses", "List the review statuses as choices, the review's own marked (the status parameter's pick list)", id, a.reviewStatusChoices)
	a.parameters("review.choose-status", map[string]ochazuke.Parameter{"status": {Type: "string", Source: "review.statuses", SourceArgs: map[string]string{"id": "id"},
		Description: "The status to set; omitted, a chooser asks"}})
}

// plainChoice is a choice shown by its label alone: the id is a machine
// id (a project's), so a single cell keeps it from being drawn.
func plainChoice(id, label string) ochazuke.Choice {
	return ochazuke.Choice{ID: id, Label: label, Cells: []string{label}}
}

// choiceRows are choices as chooser rows (Choice.Row).
func choiceRows(choices []ochazuke.Choice) []ochazuke.PickerRow {
	return pickSpec{Choices: choices}.rows()
}
