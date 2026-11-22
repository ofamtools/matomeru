package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/markdown"
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

	ModeBindings        = "bindings"         // the binding editor: moving between rows
	ModeBindingsFilter  = "bindings-filter"  // typing the editor's filter
	ModeBindingsCapture = "bindings-capture" // pressing the new keys of a binding

	ModeReader  = "reader"  // the Markdown reader
	ModeInspect = "inspect" // the actions and modes inspector
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
	CmdRunCancel      = "run.cancel"
	CmdRunNew         = "run.new"
	CmdExtRefresh     = "extension.refresh"
	CmdExtRun         = "extension.run"
	CmdTermPrefix     = "terminal.prefix"
	CmdWake           = "app.wake"
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

	CmdNewHere         = "record.new.here"
	CmdAssign          = "record.assign"
	CmdNounsPick       = "nouns.pick"
	CmdFilterFieldPick = "table.filter.field.pick"
	CmdSortFieldPick   = "table.sort.field.pick"
	CmdGroupFieldPick  = "table.group.field.pick"
	CmdSettingsOpen    = "settings.open"
	CmdBindingsOpen    = "bindings.open"
	CmdPaneZoom        = "pane.zoom"
	CmdPaneAttention   = "pane.attention.next"
	CmdLaneShow        = "lane.show"
	CmdTermEscape      = "terminal.escape"

	CmdBindingsFilter        = "bindings.filter.start"
	CmdBindingsFilterDone    = "bindings.filter.done"
	CmdBindingsFilterClear   = "bindings.filter.clear"
	CmdBindingsRebind        = "bindings.rebind.start"
	CmdBindingsCaptureKey    = "bindings.rebind.key"
	CmdBindingsCaptureDone   = "bindings.rebind.done"
	CmdBindingsCaptureCancel = "bindings.rebind.cancel"
	CmdBindingsMatchMode     = "bindings.filter.mode.cycle"

	CmdYankPick          = "yank.pick"
	CmdYankPrimary       = "yank.primary"
	CmdMacroRecord       = "macro.start"
	CmdMacroStop         = "macro.stop"
	CmdMacroReplay       = "macro.replay"
	CmdOpenTarget        = "open.target"
	CmdOpenWith          = "open.with"
	CmdReaderBack        = "reader.back"
	CmdReaderHeadingNext = "reader.heading.next"
	CmdReaderHeadingPrev = "reader.heading.prev"
	CmdScratchToggle     = "scratch.toggle"
	CmdInspectOpen       = "inspect.open"
	CmdInspectFilter     = "inspect.filter.start"
	CmdInspectBind       = "inspect.bind"
	CmdTriageRule        = "triage.rule.from-signal"
	CmdRecordActions     = "record.actions"
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
		{Name: ModeBindings},
		{Name: ModeBindingsFilter},
		{Name: ModeBindingsCapture},
		{Name: ModeReader},
		{Name: ModeInspect},
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
		bind(n, data.KindExtension, "enter", CmdExtRun, "run"),
		bind(n, all, "enter", views.CmdOpen, "open"),
		bind(n, SpaceWork, "backspace", CmdHistoryBack, "back"),
		bind(n, SpaceWork, "H", CmdHistoryBack, "back"),
		bind(n, SpaceWork, "L", CmdHistoryForward, "forward"),

		// Record actions, on whatever record is focused.
		bind(n, all, "e", views.CmdEdit, "edit"),
		with(bind(n, all, "n", views.CmdNew, "new task"), map[string]any{"kind": "task"}),
		bind(n, SpaceWork, "n", CmdNewHere, "new"),
		bind(n, all, "m", CmdAssign, "move to a task"),
		with(bind(n, all, "N", views.CmdNew, "new note"), map[string]any{"kind": "note"}),
		when(bind(n, all, "s", views.CmdStar, "star"), "facts.starStatus != \"starred\""),
		when(bind(n, all, "s", views.CmdUnstar, "unstar"), "facts.starStatus == \"starred\""),
		when(bind(n, all, "a", views.CmdArchive, "archive"), "facts.archiveStatus != \"archived\""),
		when(bind(n, all, "a", views.CmdRestore, "restore"), "facts.archiveStatus == \"archived\""),
		when(bind(n, all, "Z", views.CmdSnooze, "snooze"), "!facts.snoozed"),
		when(bind(n, all, "Z", views.CmdUnsnooze, "unsnooze"), "facts.snoozed"),
		bind(n, all, "u", CmdUndo, "undo"),
		bind(n, all, "ctrl+r", CmdRedo, "redo"),
		bind(n, data.KindRun, "r", CmdRunRerun, "rerun"),
		bind(n, data.KindRun, "x", CmdRunCancel, "cancel"),
		bind(n, SpaceRuns, "n", CmdRunNew, "run a command"),
		bind(n, data.KindExtension, "r", CmdExtRun, "run"),
		bind(n, SpaceExtensions, "R", CmdExtRefresh, "look for extensions"),
		bind(n, data.KindSignal, "R", CmdTriageRule, "rule from this signal"),

		// Yank: y lists every value of the record, y y copies its main one.
		bind(n, all, "y", CmdYankPick, "yank"),
		// Open: what the record points at, with the handler its rule names.
		when(bind(n, all, "o", CmdOpenTarget, "open"), "facts.openable"),
		when(bind(n, all, "O", CmdOpenWith, "open with"), "facts.openable"),

		// Lists.
		bind(n, all, "/", CmdFilterStart, "filter"),
		bind(n, all, "M", CmdFilterMode, "match mode"),
		bind(n, all, "f", CmdFilterFieldPick, "filter field"),
		bind(n, all, "o", CmdSortCycle, "sort"),
		bind(n, all, "O", CmdSortField, "sort column"),
		bind(n, all, "g s", CmdSortFieldPick, "sort by any field"),
		bind(n, all, "g b", CmdGroupFieldPick, "group by any field"),
		bind(n, all, "g n", CmdNounsPick, "browse any noun"),
		when(bind(n, all, "space", CmdGroupToggle, "fold group"), "facts.isGroupHeader"),
		bind(n, all, "x", CmdMarkToggle, "mark"),
		bind(n, all, "A", CmdArchivedToggle, "show archived"),

		// Overlays, from anywhere.
		bind(n, all, "T", CmdOutlineOpen, "outline"),
		bind(n, all, "'", CmdPagesPick, "pages"),
		bind(n, all, ";", CmdRecordActions, "record actions"),
		bind(n, all, ":", CmdPaletteOpen, "commands"),
		bind(n, all, "?", CmdHelpOpen, "bindings"),
		bind(n, all, "ctrl+o", CmdPathOpen, "path"),
		bind(n, all, "S", CmdSettingsOpen, "settings"),
		bind(n, all, "B", CmdBindingsOpen, "edit bindings"),
		bind(n, all, "W", CmdPaneAttention, "next pane that wants you"),
		{Mode: n, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(n, all, "Q", core.CmdAppQuit, "quit"),
		hidden(bind(n, all, "ctrl+c", core.CmdAppQuit, "quit")),

		// Terminal: everything but the leader goes to the pane.
		{Mode: ModeTerminal, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(ModeTerminalLeader, all, "d", CmdTermDetach, "detach"),
		bind(ModeTerminalLeader, all, "z", CmdPaneZoom, "zoom the pane"),
		bind(ModeTerminalLeader, all, "L", CmdLaneShow, "the pane's lane"),
		bind(ModeTerminalLeader, all, "esc", CmdTermEscape, "send esc"),
		with(bind(ModeTerminalLeader, all, "[", CmdTermPrefix, "copy mode"), map[string]any{"key": "["}),
		with(bind(ModeTerminalLeader, all, "t", CmdTermPrefix, "side terminal"), map[string]any{"key": "t"}),
		with(bind(ModeTerminalLeader, all, "v", CmdTermPrefix, "side vim"), map[string]any{"key": "v"}),
		hidden(with(bind(ModeTerminalLeader, all, "ctrl+a", CmdTermPrefix, "send ctrl+a"), map[string]any{"key": "ctrl+a"})),
		with(bind(ModeTerminalLeader, all, "S", CmdScratchToggle, "scratch terminal"), map[string]any{"role": data.RoleScratchTerm}),
		with(bind(ModeTerminalLeader, all, "V", CmdScratchToggle, "scratch notes"), map[string]any{"role": data.RoleScratchVim}),

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
		// In the yank picker a second y, before anything is typed, copies the main value.
		when(bind(ModePicker, all, "y", CmdYankPrimary, "yank the main value"), "modal.id == \"yank\" && facts.query == \"\""),
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
		when(bind(ModeForm, all, "enter", form.CmdSubmit, "run"), "modal.id == \"launch\""),
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

		// The binding editor: every change is live and saved; there is no save key.
		bind(ModeBindings, all, "j", "motion.down", "down"),
		bind(ModeBindings, all, "k", "motion.up", "up"),
		hidden(bind(ModeBindings, all, "down", "motion.down", "down")),
		hidden(bind(ModeBindings, all, "up", "motion.up", "up")),
		bind(ModeBindings, all, "g g", "motion.top", "top"),
		bind(ModeBindings, all, "G", "motion.bottom", "bottom"),
		bind(ModeBindings, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModeBindings, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModeBindings, all, "/", CmdBindingsFilter, "filter"),
		bind(ModeBindings, all, "tab", CmdBindingsMatchMode, "match mode"),
		bind(ModeBindings, all, "r", CmdBindingsRebind, "rebind"),
		hidden(bind(ModeBindings, all, "enter", CmdBindingsRebind, "rebind")),
		bind(ModeBindings, all, "x", "bindings.unbind", "unbind"),
		bind(ModeBindings, all, "R", "bindings.reset", "reset"),
		bind(ModeBindings, all, "u", "bindings.undo", "undo"),
		bind(ModeBindings, all, "ctrl+r", "bindings.redo", "redo"),
		bind(ModeBindings, all, "esc", CmdModalCancel, "close"),
		hidden(bind(ModeBindings, all, "q", CmdModalCancel, "close")),
		{Mode: ModeBindingsFilter, Class: core.ClassPrintable, Command: "bindings.filter.append", Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeBindingsFilter, Class: core.ClassPaste, Command: "bindings.filter.append", Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeBindingsFilter, all, "backspace", "bindings.filter.backspace", "erase"),
		bind(ModeBindingsFilter, all, "enter", CmdBindingsFilterDone, "keep"),
		bind(ModeBindingsFilter, all, "esc", CmdBindingsFilterClear, "clear"),
		bind(ModeBindingsFilter, all, "tab", CmdBindingsMatchMode, "match mode"),
		{Mode: ModeBindingsCapture, Class: core.ClassAny, Command: CmdBindingsCaptureKey, Args: map[string]any{"key": "$key"}, Hidden: true},
		bind(ModeBindingsCapture, all, "enter", CmdBindingsCaptureDone, "set these keys"),
		bind(ModeBindingsCapture, all, "esc", CmdBindingsCaptureCancel, "cancel"),

		// The Markdown reader.
		bind(ModeReader, all, "j", "motion.down", "down"),
		bind(ModeReader, all, "k", "motion.up", "up"),
		hidden(bind(ModeReader, all, "down", "motion.down", "down")),
		hidden(bind(ModeReader, all, "up", "motion.up", "up")),
		bind(ModeReader, all, "g g", "motion.top", "top"),
		bind(ModeReader, all, "G", "motion.bottom", "bottom"),
		bind(ModeReader, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModeReader, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModeReader, all, "ctrl+f", "motion.page.down", "page down"),
		bind(ModeReader, all, "ctrl+b", "motion.page.up", "page up"),
		hidden(bind(ModeReader, all, "space", "motion.page.down", "page down")),
		bind(ModeReader, all, "}", "motion.block.next", "next heading"),
		bind(ModeReader, all, "{", "motion.block.prev", "previous heading"),
		bind(ModeReader, all, "t", markdown.CmdTOCToggle, "contents"),
		bind(ModeReader, all, "]", CmdReaderHeadingNext, "next in contents"),
		bind(ModeReader, all, "[", CmdReaderHeadingPrev, "previous in contents"),
		bind(ModeReader, all, "tab", markdown.CmdLinkNext, "next link"),
		bind(ModeReader, all, "shift+tab", markdown.CmdLinkPrev, "previous link"),
		bind(ModeReader, all, "enter", markdown.CmdLinkOpen, "open link"),
		hidden(bind(ModeReader, all, "o", markdown.CmdLinkOpen, "open link")),
		bind(ModeReader, all, "y", markdown.CmdCodeYank, "yank code block"),
		bind(ModeReader, all, "backspace", CmdReaderBack, "back"),
		hidden(bind(ModeReader, all, "H", CmdReaderBack, "back")),
		bind(ModeReader, all, "esc", CmdModalCancel, "close"),
		bind(ModeReader, all, "q", CmdModalCancel, "close"),

		// The inspector: actions and modes.
		bind(ModeInspect, all, "j", "motion.down", "down"),
		bind(ModeInspect, all, "k", "motion.up", "up"),
		hidden(bind(ModeInspect, all, "down", "motion.down", "down")),
		hidden(bind(ModeInspect, all, "up", "motion.up", "up")),
		bind(ModeInspect, all, "g g", "motion.top", "top"),
		bind(ModeInspect, all, "G", "motion.bottom", "bottom"),
		bind(ModeInspect, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModeInspect, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModeInspect, all, "/", CmdInspectFilter, "filter"),
		when(bind(ModeInspect, all, "a", CmdInspectBind, "bind to a free key"), "facts.inspecting == \"actions\""),
		bind(ModeInspect, all, "esc", CmdModalCancel, "close"),
		bind(ModeInspect, all, "q", CmdModalCancel, "close"),

		// Outline: the normal keys work on the selected task.
		bind(ModeOutline, all, "enter", CmdOutlineJump, "open task"),
		bind(ModeOutline, all, "esc", CmdModalCancel, "close"),
		bind(ModeOutline, all, "q", CmdModalCancel, "close"),
		bind(ModeOutline, all, "T", CmdModalCancel, "close"),
	}
	out = append(out, macroBindings()...)
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

func recordSpec(id, desc string, kinds []string, whenExpr string, tags ...string) core.CommandSpec {
	meta := map[string]any{"record": true}
	if len(kinds) > 0 {
		meta["kinds"] = kinds
	}
	if whenExpr != "" {
		meta["when"] = whenExpr
	}
	return core.CommandSpec{ID: id, Description: desc, Tags: tags, Meta: meta}
}

// CommandSpecs returns the specs of every command the app handles beyond
// core's built-ins. Commands that change a record are tagged "editing", so
// the terminal's leader layer leaves them out.
func CommandSpecs() []core.CommandSpec {
	text := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return append(readerSpecs(), []core.CommandSpec{
		recordSpec(views.CmdOpen, "Open the focused record's Show page", nil, ""),
		recordSpec(views.CmdEdit, "Edit the focused record", nil, "", "editing"),
		{ID: views.CmdNew, Description: "Create a record", Tags: []string{"editing"}, Params: []core.ParamSpec{{Name: "kind", Type: core.ParamString, Default: "task"}}},
		recordSpec(views.CmdStar, "Star the focused record", nil, "facts.starStatus != \"starred\"", "editing"),
		recordSpec(views.CmdUnstar, "Unstar the focused record", nil, "facts.starStatus == \"starred\"", "editing"),
		recordSpec(views.CmdArchive, "Archive the focused record", nil, "facts.archiveStatus != \"archived\"", "editing"),
		recordSpec(views.CmdRestore, "Restore the focused record", nil, "facts.archiveStatus == \"archived\"", "editing"),
		recordSpec(views.CmdSnooze, "Snooze the focused record", nil, "!facts.snoozed", "editing"),
		recordSpec(views.CmdUnsnooze, "Unsnooze the focused record", nil, "facts.snoozed", "editing"),
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
		recordSpec(CmdRunRerun, "Run the focused run again", []string{data.KindRun}, "", "editing"),
		recordSpec(CmdRunCancel, "Cancel the focused run", []string{data.KindRun}, "", "editing"),
		spec(CmdRunNew, "Run a shell command as a run", "editing"),
		spec(CmdExtRefresh, "Look for extensions and check their health", "editing"),
		recordSpec(CmdExtRun, "Run a command of the focused extension", []string{data.KindExtension}, "", "editing"),
		{ID: CmdTermPrefix, Description: "The pane's prefix key and the key after it: [ copy mode, t side terminal, v side vim",
			Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		{ID: CmdWake, Description: "Look at what changed off the UI goroutine", Meta: map[string]any{"internal": true}},
		spec(CmdOutlineOpen, "Open the outline of every task"),
		spec(CmdOutlineJump, "Open the selected task"),
		spec(CmdPagesPick, "Pick a page of any pane"),
		spec(CmdRecordActions, "Pick an action for the focused record"),
		spec(CmdPaletteOpen, "Pick a command to run"),
		spec(CmdHelpOpen, "List the bindings that work here"),
		recordSpec(CmdPathOpen, "Attach a directory or file to the focused record", nil, ""),
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
		spec(CmdNewHere, "Create a record of the noun the Work index lists", "editing"),
		recordSpec(CmdAssign, "Move the focused record to a task (a task: under another task, or to the root)", nil, "", "editing"),
		spec(CmdNounsPick, "Browse any noun: pick one and its index opens in Work"),
		spec(CmdFilterFieldPick, "Pick the field the filter matches, from every field of the rows"),
		spec(CmdSortFieldPick, "Pick the field to sort by, from every field of the rows"),
		spec(CmdGroupFieldPick, "Pick the field to group by, from every field of the rows"),
		spec(CmdSettingsOpen, "Change the settings; each change applies at once and is saved"),
		spec(CmdBindingsOpen, "Browse, filter, rebind, unbind and reset the key bindings"),
		spec(CmdPaneZoom, "Zoom the pane: hide the rail and the pages, or bring them back"),
		spec(CmdPaneAttention, "Go to the next pane with an unread question or a waiting program"),
		recordSpec(CmdLaneShow, "Open the Show page of the pane's active lane", []string{data.KindPane, data.KindLane}, ""),
		{ID: CmdTermEscape, Description: "Send a literal escape to the pane", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsFilter, Description: "Type the binding editor's filter", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsFilterDone, Description: "Keep the binding editor's filter", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsFilterClear, Description: "Clear the binding editor's filter", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsRebind, Description: "Press new keys for the focused binding", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsCaptureKey, Description: "One key of the new sequence", Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsCaptureDone, Description: "Set the keys pressed", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsCaptureCancel, Description: "Leave the binding as it was", Meta: map[string]any{"internal": true}},
		{ID: CmdBindingsMatchMode, Description: "Change how the binding editor's filter matches", Meta: map[string]any{"internal": true}},
		{ID: "bindings.unbind", Description: "Unbind the focused binding", Meta: map[string]any{"internal": true}},
		{ID: "bindings.reset", Description: "Reset the focused binding to what it was", Meta: map[string]any{"internal": true}},
		{ID: "bindings.undo", Description: "Undo the last binding change", Meta: map[string]any{"internal": true}},
		{ID: "bindings.redo", Description: "Redo the last undone binding change", Meta: map[string]any{"internal": true}},
		{ID: "bindings.filter.append", Description: "Type into the binding editor's filter", Params: text, Meta: map[string]any{"internal": true}},
		{ID: "bindings.filter.backspace", Description: "Erase the last character of the binding editor's filter", Meta: map[string]any{"internal": true}},
		recordSpec(CmdYankPick, "Yank from the focused record: pick any of its values, each shown as it will be copied", nil, ""),
		recordSpec(CmdYankPrimary, "Yank the focused record's main value (its title, URL or file) at once", nil, ""),
		{ID: CmdMacroRecord, Description: "Record the keys pressed into a register (q then a letter)", Params: []core.ParamSpec{{Name: "reg", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		{ID: CmdMacroStop, Description: "Stop recording the macro", Meta: map[string]any{"internal": true}},
		{ID: CmdMacroReplay, Description: "Replay a recorded macro (@ then its letter; @ @ the last one)", Params: []core.ParamSpec{{Name: "reg", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		recordSpec(CmdOpenTarget, "Open what the focused record points at (file, URL, directory or its own text) with the first matching handler", nil, "facts.openable"),
		recordSpec(CmdOpenWith, "Open with: pick among every handler that matches, or copy the path", nil, "facts.openable"),
		{ID: CmdReaderBack, Description: "Go back to the document a link was followed from", Meta: map[string]any{"internal": true}},
		{ID: CmdReaderHeadingNext, Description: "Go to the next heading of the contents", Meta: map[string]any{"internal": true}},
		{ID: CmdReaderHeadingPrev, Description: "Go to the previous heading of the contents", Meta: map[string]any{"internal": true}},
		{ID: CmdScratchToggle, Description: "Show or hide a global scratch over the current space: the terminal, or the notes editor",
			Params: []core.ParamSpec{{Name: "role", Type: core.ParamString, Enum: []string{data.RoleScratchTerm, data.RoleScratchVim}, Default: data.RoleScratchTerm}}},
		{ID: CmdInspectOpen, Description: "Inspect a registry the app runs on: actions or modes",
			Params: []core.ParamSpec{{Name: "what", Type: core.ParamString, Enum: []string{InspectActions, InspectModes}, Default: InspectActions}}, Meta: map[string]any{"internal": true}},
		{ID: CmdInspectFilter, Description: "Type the inspector's filter", Meta: map[string]any{"internal": true}},
		{ID: CmdInspectBind, Description: "Bind the focused action to a free key", Meta: map[string]any{"internal": true}},
		recordSpec(CmdTriageRule, "Make a triage rule from the focused signal and apply it to the signals still unreviewed", []string{data.KindSignal}, "", "editing"),
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
	}...)
}

// readerSpecs are the Markdown widget's own commands. They only make sense
// in the reader, so the palette leaves them out.
func readerSpecs() []core.CommandSpec {
	specs := markdown.Specs()
	for i := range specs {
		specs[i].Meta = map[string]any{"internal": true}
	}
	return specs
}

// internal reports whether a command only makes sense from its own key (typing
// into a filter, a picker's confirm), so the palette leaves it out.
func internal(spec core.CommandSpec) bool {
	on, _ := spec.Meta["internal"].(bool)
	return on || strings.HasPrefix(spec.ID, "motion.") || strings.HasPrefix(spec.ID, "focus.") ||
		slices.Contains([]string{core.CmdModeSet, core.CmdModalOpen, core.CmdModalClose, core.CmdLayerPush, core.CmdLayerPop, core.CmdCountPush, core.CmdPageOpen}, spec.ID)
}
