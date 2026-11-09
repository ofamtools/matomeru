package main

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/harness"
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

// rows are the spec's choices as chooser rows (ochazuke.ChoiceRows; a
// choice carries its props and payload), the Lead one first.
func (s pickSpec) rows() []ochazuke.PickerRow {
	if s.Lead != "" {
		s.Choices = slices.Clone(s.Choices)
		slices.SortStableFunc(s.Choices, func(x, y ochazuke.Choice) int { return -cmp.Compare(btoi(x.ID == s.Lead), btoi(y.ID == s.Lead)) })
	}
	return ochazuke.ChoiceRows(s.Choices)
}

// dataByID is the payload each choice carries (Choice.Data), by id: what a
// pick reads back for the id it was given.
func dataByID(choices []ochazuke.Choice) map[string]any {
	out := make(map[string]any, len(choices))
	for _, c := range choices {
		out[c.ID] = c.Data
	}
	return out
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

// choiceSet is one pick list a parameter declares as its Source: a named
// set of choices that the one verb choice.list answers (Of names it), and
// the parameter of a verb that takes its value from it. The table below is
// the data; no set has a verb of its own, and no noun is added per set.
type choiceSet struct {
	// Of names the set; Fn lists it. A set reads the row it is about from
	// its id, name or setting argument (SourceArgs maps the verb's own
	// parameters to them), else from the focus.
	Of string
	Fn func(ochazuke.Ctx) ([]ochazuke.Choice, error)
	// Key.Param is the parameter that takes its values from the set.
	Key, Param string
	// Type and Description declare the parameter when the verb does not.
	Type, Description string
	// Args maps the source's arguments to the verb's sibling parameters;
	// Fixed gives it constants; Free accepts a value outside the set.
	Args  map[string]string
	Fixed map[string]any
	Free  bool
}

// choiceSets are the pick lists of mtm's parameters (the general picker
// asks them through askParameter; the palette, the form and the CLI offer
// the same).
func (a *App) choiceSets() []choiceSet {
	return []choiceSet{
		{Of: "task-status", Fn: a.taskStatuses, Key: "task.choose-status", Param: "status", Type: "string", Description: "The status to set; omitted, a chooser asks", Args: map[string]string{"id": "id", "count": "count"}},
		{Of: "review-status", Fn: a.reviewStatusChoices, Key: "review.choose-status", Param: "status", Type: "string", Description: "The status to set; omitted, a chooser asks", Args: map[string]string{"id": "id"}},
		{Of: "schedule-interval", Fn: a.intervalChoices, Key: scheduleNoun + ".set-interval", Param: "interval", Args: map[string]string{"name": "name"}, Free: true},
		{Of: "snooze-time", Fn: a.snoozeTimes, Key: actionItemNoun + ".snooze", Param: "until", Free: true},
		{Of: "prompt-attachment", Fn: a.promptAttachments, Key: promptNoun + ".detach", Param: "n"},
		{Of: "outline-attachment", Fn: a.taskAttachments, Key: outlineModal + ".detach", Param: "n"},
		{Of: "pane-setting-value", Fn: a.settingValues, Key: "pane.set-model", Param: settingArgs[harness.SettingModel], Fixed: map[string]any{"setting": harness.SettingModel}, Free: true},
		{Of: "pane-setting-value", Fn: a.settingValues, Key: "pane.set-effort", Param: settingArgs[harness.SettingEffort], Fixed: map[string]any{"setting": harness.SettingEffort}, Free: true},
		{Of: "pane-setting-value", Fn: a.settingValues, Key: "pane.set-fast", Param: settingArgs[harness.SettingFast], Fixed: map[string]any{"setting": harness.SettingFast}, Free: true},
		{Of: "pane-setting-value", Fn: a.settingValues, Key: "pane.set-permission-mode", Param: settingArgs[harness.SettingPermission], Fixed: map[string]any{"setting": harness.SettingPermission}, Free: true},
	}
}

// choiceListKey is the verb every set answers.
const choiceListKey = "choice.list"

// defineChoiceSources registers choice.list and points each set's parameter
// at it.
func (a *App) defineChoiceSources() {
	sets := a.choiceSets()
	by := map[string]choiceSet{}
	for _, set := range sets {
		by[set.Of] = set
	}
	a.r.Define("choice").Verb("list", func(c ochazuke.Ctx) ochazuke.Result {
		set, ok := by[ochazuke.StringArg(c.Args, "of", "")]
		if !ok {
			return ochazuke.Failed(errors.New("unknown choice set"))
		}
		if c.Instance == nil {
			// The sets are about the row at the focus when none is named.
			c.Instance = a.r.Instances[a.r.FocusID()]
		}
		choices, err := set.Fn(c)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(choices)
	}, "List a set of choices a parameter takes its value from (of: task-status, review-status, schedule-interval, snooze-time, prompt-attachment, outline-attachment, pane-setting-value)").Fetch()
	a.parameters(choiceListKey, map[string]ochazuke.Parameter{
		"of":      {Type: "string", Required: true, Description: "The set"},
		"id":      {Type: "string", Description: "The row asked about; omitted, the selected one"},
		"count":   {Type: "integer", Description: "How many rows the choice applies to; the current value is marked only for one"},
		"name":    {Type: "string", Description: "The schedule asked about; omitted, the selected one"},
		"setting": {Type: "string", Description: "The pane setting: model, effort, fast or permission-mode"},
	})
	for _, set := range sets {
		op, ok := a.r.Operation(set.Key)
		if !ok {
			continue // a verb this build does not define (the prompt box's)
		}
		p := op.Parameters[set.Param]
		if set.Type != "" {
			p.Type, p.Description = set.Type, set.Description
		}
		fixed := map[string]any{"of": set.Of}
		maps.Copy(fixed, set.Fixed)
		p.Source, p.SourceArgs, p.SourceFixed, p.Free = choiceListKey, set.Args, fixed, set.Free
		a.parameters(set.Key, map[string]ochazuke.Parameter{set.Param: p})
	}
}

// plainChoice is a choice shown by its label alone: the id is only a
// handle (a project's), so Bare keeps the dim id off the row.
func plainChoice(id, label string) ochazuke.Choice {
	return ochazuke.Choice{ID: id, Label: label, RowFlags: ochazuke.RowFlags{Bare: true}}
}
