package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
)

// Modes.
const (
	ModeNormal         = "normal"
	ModeTerminal       = "terminal"        // passthrough: keys go to the pane
	ModeTerminalLeader = "terminal-leader" // reached only through the leader layer
	ModeFilter         = "filter"          // typing narrows the focused list
	ModePicker         = "picker"          // a modal picker: typing is the query
	ModeForm           = "form"            // a modal form: moving between fields
	ModeFormEdit       = "form-edit"       // typing into a form field
	ModeOutline        = "outline"         // the outline overlay; falls back to normal
)

// Commands the app adds to core's built-ins and the widgets' own.
const (
	CmdHistoryBack    = "history.back"
	CmdHistoryForward = "history.forward"
	CmdUndo           = "store.undo"
	CmdRedo           = "store.redo"
	CmdSiteFocus      = "site.focus"
	CmdSitePageOpen   = "site.page.open"
	CmdAnswer         = "question.answer"
	CmdTermAttach     = "terminal.attach"
	CmdTermDetach     = "terminal.detach"
	CmdTermInput      = "terminal.input"
	CmdRunRerun       = "run.rerun"
	CmdOutlineOpen    = "outline.open"
	CmdOutlineJump    = "outline.jump"
	CmdPagesPick      = "pages.pick"
	CmdPaletteOpen    = "palette.open"
	CmdHelpOpen       = "help.open"
	CmdPathOpen       = "path.open"
	CmdModalCancel    = "modal.cancel"
	CmdFilterStart    = "filter.start"
	CmdFilterInsert   = "filter.insert"
	CmdFilterBack     = "filter.backspace"
	CmdFilterAccept   = "filter.accept"
	CmdFilterCancel   = "filter.cancel"
	CmdFilterClear    = "filter.clear"
	CmdFilterMode     = "filter.mode.cycle"
	CmdFilterField    = "filter.field.cycle"
	CmdSortCycle      = "table.sort.cycle"
	CmdSortField      = "table.sort.field.cycle"
	CmdGroupField     = "table.group.field.cycle"
	CmdGroupToggle    = "table.group.toggle"
	CmdMarkToggle     = "table.mark.toggle"
	CmdArchivedToggle = "archived.toggle"
	CmdPickerInsert   = "picker.insert"
	CmdPickerBack     = "picker.backspace"
	CmdFormActivate   = "form.activate"
)

var leader = core.Layer{Name: "leader", Modes: []string{ModeTerminalLeader, ModeNormal}, ExcludeTags: []string{"motion", "editing"}}

// Modes returns the app's modes.
func Modes() []core.Mode {
	return []core.Mode{
		{Name: ModeNormal},
		{Name: ModeTerminal, Passthrough: true},
		{Name: ModeTerminalLeader},
		{Name: ModeFilter},
		{Name: ModePicker},
		{Name: ModeForm},
		{Name: ModeFormEdit},
		{Name: ModeOutline, Base: ModeNormal},
	}
}

func keys(seq string) []string { return strings.Fields(seq) }

// bind is one row of the default keymap: mode, scope ("" = any), a
// space-separated key sequence, the command and its hint.
func bind(mode, scope, seq, command, hint string) core.Binding {
	return core.Binding{Mode: mode, Scope: scope, Keys: keys(seq), Command: command, Hint: hint}
}

func with(b core.Binding, args map[string]any) core.Binding { b.Args = args; return b }
func when(b core.Binding, cond string) core.Binding         { b.When = cond; return b }
func hidden(b core.Binding) core.Binding                    { b.Hidden = true; return b }

// DefaultBindings returns the default keymap as data. A user's
// bindings.json is laid over it.
func DefaultBindings() []core.Binding {
	n, all := ModeNormal, ""
	out := []core.Binding{
		// Spaces.
		with(bind(n, all, "1", core.CmdPageOpen, "panes"), map[string]any{"id": SpacePanes}),
		with(bind(n, all, "2", core.CmdPageOpen, "work"), map[string]any{"id": SpaceWork}),
		with(bind(n, all, "3", core.CmdPageOpen, "runs"), map[string]any{"id": SpaceRuns}),
		with(bind(n, all, "4", core.CmdPageOpen, "extensions"), map[string]any{"id": SpaceExtensions}),
		bind(n, all, "]", core.CmdPageNext, "next space"),
		bind(n, all, "[", core.CmdPagePrev, "previous space"),

		// Motions: j and k spill from one list into the next at its edge.
		bind(n, all, "j", "motion.down", "down"),
		bind(n, all, "k", "motion.up", "up"),
		hidden(bind(n, all, "down", "motion.down", "down")),
		hidden(bind(n, all, "up", "motion.up", "up")),
		bind(n, all, "h", "motion.left", "column left"),
		bind(n, all, "l", "motion.right", "column right"),
		hidden(bind(n, all, "left", "motion.left", "column left")),
		hidden(bind(n, all, "right", "motion.right", "column right")),
		bind(n, all, "g g", "motion.top", "top"),
		bind(n, all, "G", "motion.bottom", "bottom"),
		bind(n, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(n, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(n, all, "ctrl+f", "motion.page.down", "page down"),
		bind(n, all, "ctrl+b", "motion.page.up", "page up"),
		bind(n, all, "}", "motion.block.next", "next group"),
		bind(n, all, "{", "motion.block.prev", "previous group"),
		bind(n, all, "tab", core.CmdFocusNext, "next"),
		bind(n, all, "shift+tab", core.CmdFocusPrev, "previous"),

		// Enter opens what is focused; inner scopes say what "open" means there.
		bind(n, ScopeRail, "enter", CmdSiteFocus, "pane's pages"),
		bind(n, views.ScopeTOC, "enter", CmdSitePageOpen, "open page"),
		bind(n, "terminal", "enter", CmdTermAttach, "type in the pane"),
		hidden(bind(n, "terminal", "i", CmdTermAttach, "type in the pane")),
		bind(n, "question", "enter", CmdAnswer, "answer"),
		bind(n, "question", "a", CmdAnswer, "answer"),
		bind(n, all, "enter", views.CmdOpen, "open"),
		bind(n, SpaceWork, "backspace", CmdHistoryBack, "back"),
		bind(n, SpaceWork, "H", CmdHistoryBack, "back"),
		bind(n, SpaceWork, "L", CmdHistoryForward, "forward"),

		// Record actions, on whatever record is focused.
		bind(n, all, "e", views.CmdEdit, "edit"),
		with(bind(n, all, "n", views.CmdNew, "new task"), map[string]any{"kind": "task"}),
		with(bind(n, all, "N", views.CmdNew, "new note"), map[string]any{"kind": "note"}),
		when(bind(n, all, "s", views.CmdStar, "star"), "facts.starStatus != \"starred\""),
		when(bind(n, all, "s", views.CmdUnstar, "unstar"), "facts.starStatus == \"starred\""),
		when(bind(n, all, "a", views.CmdArchive, "archive"), "facts.archiveStatus != \"archived\""),
		when(bind(n, all, "a", views.CmdRestore, "restore"), "facts.archiveStatus == \"archived\""),
		when(bind(n, all, "Z", views.CmdSnooze, "snooze"), "!facts.snoozed"),
		when(bind(n, all, "Z", views.CmdUnsnooze, "unsnooze"), "facts.snoozed"),
		bind(n, all, "u", CmdUndo, "undo"),
		bind(n, all, "ctrl+r", CmdRedo, "redo"),
		bind(n, "run", "r", CmdRunRerun, "rerun"),

		// Lists.
		bind(n, all, "/", CmdFilterStart, "filter"),
		bind(n, all, "m", CmdFilterMode, "match mode"),
		bind(n, all, "f", CmdFilterField, "filter field"),
		bind(n, all, "o", CmdSortCycle, "sort"),
		bind(n, all, "O", CmdSortField, "sort column"),
		bind(n, all, "B", CmdGroupField, "group by"),
		when(bind(n, all, "space", CmdGroupToggle, "fold group"), "facts.isGroupHeader"),
		bind(n, all, "x", CmdMarkToggle, "mark"),
		bind(n, all, "A", CmdArchivedToggle, "show archived"),

		// Overlays, from anywhere.
		bind(n, all, "T", CmdOutlineOpen, "outline"),
		bind(n, all, "'", CmdPagesPick, "pages"),
		bind(n, all, ":", CmdPaletteOpen, "commands"),
		bind(n, all, "?", CmdHelpOpen, "bindings"),
		bind(n, all, "ctrl+o", CmdPathOpen, "path"),
		{Mode: n, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(n, all, "q", core.CmdAppQuit, "quit"),
		hidden(bind(n, all, "ctrl+c", core.CmdAppQuit, "quit")),

		// Terminal: everything but the leader goes to the pane.
		{Mode: ModeTerminal, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(ModeTerminalLeader, all, "x", CmdTermDetach, "detach"),

		// Filter: typing narrows the list.
		{Mode: ModeFilter, Class: core.ClassPrintable, Command: CmdFilterInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeFilter, Class: core.ClassPaste, Command: CmdFilterInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeFilter, all, "backspace", CmdFilterBack, "erase"),
		bind(ModeFilter, all, "enter", CmdFilterAccept, "keep"),
		bind(ModeFilter, all, "esc", CmdFilterCancel, "clear"),
		bind(ModeFilter, all, "tab", CmdFilterMode, "match mode"),
		bind(ModeFilter, all, "ctrl+t", CmdFilterField, "filter field"),
		bind(ModeFilter, all, "ctrl+u", CmdFilterClear, "empty"),

		// Pickers.
		{Mode: ModePicker, Class: core.ClassPrintable, Command: CmdPickerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModePicker, Class: core.ClassPaste, Command: CmdPickerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModePicker, all, "backspace", CmdPickerBack, "erase"),
		bind(ModePicker, all, "enter", picker.CmdConfirm, "choose"),
		bind(ModePicker, all, "esc", CmdModalCancel, "close"),
		bind(ModePicker, all, "down", "motion.down", "down"),
		bind(ModePicker, all, "up", "motion.up", "up"),
		hidden(bind(ModePicker, all, "ctrl+n", "motion.down", "down")),
		hidden(bind(ModePicker, all, "ctrl+p", "motion.up", "up")),
		hidden(bind(ModePicker, all, "ctrl+j", "motion.down", "down")),
		hidden(bind(ModePicker, all, "ctrl+k", "motion.up", "up")),
		bind(ModePicker, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModePicker, all, "ctrl+u", "motion.half.up", "half page up"),
		when(bind(ModePicker, all, "tab", pathpicker.CmdSelect, "choose this path"), "modal.id == \"path\""),
		when(bind(ModePicker, all, "left", pathpicker.CmdParent, "parent directory"), "modal.id == \"path\""),
		when(bind(ModePicker, all, "right", pathpicker.CmdOpen, "enter directory"), "modal.id == \"path\""),
		when(bind(ModePicker, all, "ctrl+t", pathpicker.CmdHiddenToggle, "hidden files"), "modal.id == \"path\""),
		bind(ModePicker, all, "tab", picker.CmdModeCycle, "match mode"),

		// Forms: every change is written as it is made; there is no save key.
		bind(ModeForm, all, "j", "motion.down", "next field"),
		bind(ModeForm, all, "k", "motion.up", "previous field"),
		hidden(bind(ModeForm, all, "down", "motion.down", "next field")),
		hidden(bind(ModeForm, all, "up", "motion.up", "previous field")),
		hidden(bind(ModeForm, all, "g g", "motion.top", "first field")),
		hidden(bind(ModeForm, all, "G", "motion.bottom", "last field")),
		when(bind(ModeForm, all, "enter", form.CmdSubmit, "send answer"), "modal.id == \"answer\""),
		bind(ModeForm, all, "enter", CmdFormActivate, "change field"),
		bind(ModeForm, all, "i", form.CmdEdit, "type"),
		bind(ModeForm, all, "space", form.CmdToggle, "toggle"),
		with(bind(ModeForm, all, "l", form.CmdCycle, "next choice"), map[string]any{"dir": 1}),
		with(bind(ModeForm, all, "h", form.CmdCycle, "previous choice"), map[string]any{"dir": -1}),
		bind(ModeForm, all, "p", form.CmdPick, "pick"),
		bind(ModeForm, all, "u", CmdUndo, "undo"),
		bind(ModeForm, all, "ctrl+r", CmdRedo, "redo"),
		bind(ModeForm, all, "esc", CmdModalCancel, "close"),
		hidden(bind(ModeForm, all, "q", CmdModalCancel, "close")),
		{Mode: ModeFormEdit, Class: core.ClassPrintable, Command: form.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeFormEdit, Class: core.ClassPaste, Command: form.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeFormEdit, all, "backspace", form.CmdBackspace, "erase"),
		bind(ModeFormEdit, all, "enter", form.CmdStop, "done"),
		bind(ModeFormEdit, all, "esc", form.CmdStop, "done"),

		// Outline: the normal keys work on the selected task.
		bind(ModeOutline, all, "enter", CmdOutlineJump, "open task"),
		bind(ModeOutline, all, "esc", CmdModalCancel, "close"),
		bind(ModeOutline, all, "q", CmdModalCancel, "close"),
		bind(ModeOutline, all, "T", CmdModalCancel, "close"),
	}
	// Motions and space switches work everywhere; the footer leaves them out
	// (the help overlay lists them).
	for i, b := range out {
		if strings.HasPrefix(b.Command, "motion.") || strings.HasPrefix(b.Command, "focus.") || strings.HasPrefix(b.Command, "page.") {
			out[i].Hidden = true
		}
	}
	return out
}

func spec(id, desc string, tags ...string) core.CommandSpec {
	return core.CommandSpec{ID: id, Description: desc, Tags: tags}
}

// CommandSpecs returns the specs of every command the app handles beyond
// core's built-ins. Commands that change a record are tagged "editing", so
// the terminal's leader layer leaves them out.
func CommandSpecs() []core.CommandSpec {
	text := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return []core.CommandSpec{
		spec(views.CmdOpen, "Open the focused record's Show page"),
		spec(views.CmdEdit, "Edit the focused record", "editing"),
		{ID: views.CmdNew, Description: "Create a record", Tags: []string{"editing"}, Params: []core.ParamSpec{{Name: "kind", Type: core.ParamString, Default: "task"}}},
		spec(views.CmdStar, "Star the focused record", "editing"),
		spec(views.CmdUnstar, "Unstar the focused record", "editing"),
		spec(views.CmdArchive, "Archive the focused record", "editing"),
		spec(views.CmdRestore, "Restore the focused record", "editing"),
		spec(views.CmdSnooze, "Snooze the focused record", "editing"),
		spec(views.CmdUnsnooze, "Unsnooze the focused record", "editing"),
		spec(CmdUndo, "Undo my last change", "editing"),
		spec(CmdRedo, "Redo my last undone change", "editing"),
		spec(CmdHistoryBack, "Go back to the previous Show page"),
		spec(CmdHistoryForward, "Go forward again"),
		spec(CmdSiteFocus, "Go to the selected pane's pages"),
		spec(CmdSitePageOpen, "Open the focused page of the pane's site"),
		spec(CmdAnswer, "Answer the focused question", "editing"),
		spec(CmdTermAttach, "Type in the pane's terminal"),
		spec(CmdTermDetach, "Leave the pane's terminal"),
		{ID: CmdTermInput, Description: "Send a key to the pane", Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}, {Name: "text", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		spec(CmdRunRerun, "Queue the focused run again", "editing"),
		spec(CmdOutlineOpen, "Open the outline of every task"),
		spec(CmdOutlineJump, "Open the selected task"),
		spec(CmdPagesPick, "Pick a page of any pane"),
		spec(CmdPaletteOpen, "Pick a command to run"),
		spec(CmdHelpOpen, "List the bindings that work here"),
		spec(CmdPathOpen, "Pick a directory or file"),
		spec(CmdModalCancel, "Close the overlay"),
		spec(CmdFilterStart, "Filter the focused list"),
		{ID: CmdFilterInsert, Description: "Type into the filter", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdFilterBack, Description: "Erase the last character of the filter", Meta: map[string]any{"internal": true}},
		{ID: CmdFilterAccept, Description: "Keep the filter and stop typing", Meta: map[string]any{"internal": true}},
		{ID: CmdFilterCancel, Description: "Clear the filter and stop typing", Meta: map[string]any{"internal": true}},
		spec(CmdFilterClear, "Empty the filter"),
		spec(CmdFilterMode, "Change how the filter matches: substring, fuzzy, regex"),
		spec(CmdFilterField, "Change which field the filter matches"),
		spec(CmdSortCycle, "Sort the list: ascending, descending, off"),
		spec(CmdSortField, "Sort by the next column"),
		spec(CmdGroupField, "Group by the next column"),
		spec(CmdGroupToggle, "Fold or unfold the focused group"),
		spec(CmdMarkToggle, "Mark the focused row"),
		spec(CmdArchivedToggle, "Show or hide archived records"),
		{ID: CmdPickerInsert, Description: "Type into the picker", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdPickerBack, Description: "Erase the last character of the picker's query", Meta: map[string]any{"internal": true}},
		{ID: picker.CmdConfirm, Description: "Choose the highlighted item", Meta: map[string]any{"internal": true}},
		{ID: picker.CmdModeCycle, Description: "Change how the picker matches", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdSelect, Description: "Choose the highlighted path", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdParent, Description: "Go to the parent directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdOpen, Description: "Enter the highlighted directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdHiddenToggle, Description: "Show or hide hidden files", Meta: map[string]any{"internal": true}},
		{ID: CmdFormActivate, Description: "Change the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdSubmit, Description: "Send the answer", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdEdit, Description: "Type into the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdStop, Description: "Stop typing; the field is written", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdToggle, Description: "Toggle the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdCycle, Description: "Pick the next choice of the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdInsert, Description: "Type into the active field", Params: text, Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdBackspace, Description: "Erase the last character of the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdPick, Description: "Pick a value for the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
	}
}

// internal reports whether a command only makes sense from its own key (typing
// into a filter, a picker's confirm), so the palette leaves it out.
func internal(spec core.CommandSpec) bool {
	on, _ := spec.Meta["internal"].(bool)
	return on || strings.HasPrefix(spec.ID, "motion.") || strings.HasPrefix(spec.ID, "focus.") ||
		slices.Contains([]string{core.CmdModeSet, core.CmdModalOpen, core.CmdModalClose, core.CmdLayerPush, core.CmdLayerPop, core.CmdCountPush, core.CmdPageOpen}, spec.ID)
}
