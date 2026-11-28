package app

import (
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/palette"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/tree"
)

// Modes.
const (
	ModeGlobal         = "global" // universal app overlays inherited by non-typing modes
	ModeNormal         = "normal"
	ModeTerminal       = "terminal"        // passthrough: keys go to the pane
	ModeTerminalLeader = "terminal-leader" // reached only through the leader layer
	ModeFilter         = "filter"          // typing narrows the focused list
	ModePicker         = "picker"          // a modal picker in normal mode: j/k move, / filters
	ModePickerFilter   = modal.ModePickerFilter
	ModeForm           = "form"         // a modal form: moving between fields
	ModeFormEdit       = "form-edit"    // typing into a form field
	ModeOutline        = "outline"      // the outline overlay; falls back to normal
	ModeOutlineEdit    = "outline-edit" // in-place inline editing inside Outline / Tasks tree

	ModeBindings        = "bindings"         // the binding editor: moving between rows
	ModeBindingsFilter  = "bindings-filter"  // typing the editor's filter
	ModeBindingsCapture = "bindings-capture" // pressing the new keys of a binding

	ModeReader      = "reader"      // the Markdown reader
	ModeInspect     = "inspect"     // the actions and modes inspector
	ModeCommandLine = "commandline" // shell I/O command bar (!, |, r!)
	ModeHints       = "hints"       // live terminal link/path hint labels
	ModeComposer    = "composer"    // multi-line prompt composer overlay
	ModeConfirm     = "confirm"     // shared confirmation modal (y/enter confirm, n/esc cancel)
	ModeAbout       = "about"       // scrollable About diagnostics overlay
)

// Commands the app adds to core's built-ins and the widgets' own.
const (
	CmdHistoryBack           = "history.back"
	CmdHistoryForward        = "history.forward"
	CmdUndo                  = "store.undo"
	CmdRedo                  = "store.redo"
	CmdSiteFocus             = "site.focus"
	CmdPaneEnterLive         = "pane.enter.live"
	CmdSideSplitSwitch       = "pane.side.switch"
	CmdSideSplitClose        = "pane.side.close"
	CmdProjectPageOpen       = "project.page.open"
	CmdSitePageOpen          = "site.page.open"
	CmdSiteTOCToggle         = "site.toc.toggle"
	CmdAnswer                = "question.answer"
	CmdTermAttach            = "terminal.attach"
	CmdTermDetach            = "terminal.detach"
	CmdTermInput             = "terminal.input"
	CmdRunRerun              = "run.rerun"
	CmdRunCancel             = "run.cancel"
	CmdRunNew                = "run.new"
	CmdExtRefresh            = "extension.refresh"
	CmdExtRun                = "extension.run"
	CmdExtInstall            = "extension.install"
	CmdExtInstallAll         = "extension.install.all"
	CmdTermPrefix            = "terminal.prefix"
	CmdWake                  = "app.wake"
	CmdOutlineOpen           = "outline.open"
	CmdOutlineJump           = "outline.jump"
	CmdOutlineMoveDown       = "outline.move.down"
	CmdOutlineMoveUp         = "outline.move.up"
	CmdOutlineIndent         = "outline.indent"
	CmdOutlineOutdent        = "outline.outdent"
	CmdOutlineStatusCycle    = "outline.status.cycle"
	CmdOutlineRename         = "outline.rename"
	CmdOutlineEditStart      = "outline.edit.start"
	CmdOutlineEditEnd        = "outline.edit.end"
	CmdOutlineAddBelow       = "outline.add.below"
	CmdOutlineAddAbove       = "outline.add.above"
	CmdOutlineEditInsert     = "outline.edit.insert"
	CmdOutlineEditBackspace  = "outline.edit.backspace"
	CmdOutlineEditIndent     = "outline.edit.indent"
	CmdOutlineEditOutdent    = "outline.edit.outdent"
	CmdOutlineEditCommit     = "outline.edit.commit"
	CmdOutlineEditCancel     = "outline.edit.cancel"
	CmdOutlineEditNewline    = "outline.edit.newline"
	CmdOutlineEditLinebreak  = "outline.edit.linebreak"
	CmdOutlineCleanSlate     = "outline.clean_slate"
	CmdOutlineCleanSlateExit = "outline.clean_slate.exit"
	CmdOutlineTriage         = "outline.triage"
	CmdRecentsPick           = "recents.pick"
	CmdPagesPick             = "pages.pick"
	CmdPaletteOpen           = "palette.open"
	CmdHelpOpen              = "help.open"
	CmdPathOpen              = "path.open"
	CmdModalCancel           = "modal.cancel"
	CmdFilterStart           = "filter.start"
	CmdFilterInsert          = "filter.insert"
	CmdFilterBack            = "filter.backspace"
	CmdFilterAccept          = "filter.accept"
	CmdFilterCancel          = "filter.cancel"
	CmdFilterClear           = "filter.clear"
	CmdFilterMode            = "filter.mode.cycle"
	CmdFilterField           = "filter.field.cycle"
	CmdSortCycle             = "table.sort.cycle"
	CmdSortField             = "table.sort.field.cycle"
	CmdGroupField            = "table.group.field.cycle"
	CmdGroupToggle           = "table.group.toggle"
	CmdMarkToggle            = "table.mark.toggle"
	CmdArchivedToggle        = "archived.toggle"
	CmdPickerInsert          = "picker.insert"
	CmdPickerBack            = "picker.backspace"
	CmdFormActivate          = "form.activate"

	CmdNewHere         = "record.new.here"
	CmdAssign          = "record.assign"
	CmdStatusCycle     = "record.status.cycle"
	CmdNounsPick       = "nouns.pick"
	CmdFilterFieldPick = "table.filter.field.pick"
	CmdSortFieldPick   = "table.sort.field.pick"
	CmdGroupFieldPick  = "table.group.field.pick"
	CmdSettingsOpen    = "settings.open"
	CmdBindingsOpen    = "bindings.open"
	CmdPaneZoom        = "pane.zoom"
	CmdPaneAttention   = "pane.attention.next"
	CmdPaneApprove     = "pane.approve"
	CmdPaneDeny        = "pane.deny"
	CmdDaemonReset     = "daemon.reset"
	CmdDaemonUpgrade   = "daemon.upgrade"
	CmdAppReload       = "app.reload"
	CmdSchedulePause   = "schedule.pause"
	CmdScheduleResume  = "schedule.resume"
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
	CmdShellOpen         = "shell.open"

	CmdPanesGroupToggle        = "panes.group.toggle"
	CmdContextPick             = "context.pick"
	CmdContextYank             = "context.yank"
	CmdContextAttach           = "context.attach"
	CmdProjectContextOpen      = "project.context.open"
	CmdProjectContextYank      = "project.context.yank"
	CmdProjectContextStar      = "project.context.star"
	CmdProjectContextAttach    = "project.context.attach"
	CmdPaneHints               = "pane.hints"
	CmdHintsKey                = "hints.key"
	CmdHintsCancel             = "hints.cancel"
	CmdHintsActionCycle        = "hints.action.cycle"
	CmdHintsActionSet          = "hints.action.set"
	CmdPaneCompose             = "pane.compose"
	CmdComposerInsert          = "composer.insert"
	CmdComposerBackspace       = "composer.backspace"
	CmdComposerEnter           = "composer.enter"
	CmdComposerSubmit          = "composer.submit"
	CmdComposerCancel          = "composer.cancel"
	CmdComposerModeCycle       = "composer.mode.cycle"
	CmdComposerComplete        = "composer.complete"
	CmdComposerUp              = "composer.up"
	CmdComposerDown            = "composer.down"
	CmdComposerDraftPrev       = "composer.draft.prev"
	CmdComposerDraftNext       = "composer.draft.next"
	CmdComposerAttachClipboard = "composer.attach.clipboard"
	CmdComposerAttachFile      = "composer.attach.file"
	CmdComposerAttachRemove    = "composer.attach.remove"
	CmdQuestionNext            = "question.next"
	CmdQuestionPrev            = "question.prev"
	CmdQuestionSubmitAll       = "question.submit_all"
	CmdAnswerBatchNext         = "question.batch.next"
	CmdAnswerBatchPrev         = "question.batch.prev"

	CmdConfirmAccept       = "confirm.accept"
	CmdConfirmCancel       = "confirm.cancel"
	CmdTablePresetPick     = "table.preset.pick"
	CmdTablePresetSave     = "table.preset.save"
	CmdTablePresetApply    = "table.preset.apply"
	CmdTablePresetDefault  = "table.preset.default"
	CmdTablePresetDelete   = "table.preset.delete"
	CmdPresetPickerSave    = "table.preset.picker.save"
	CmdPresetPickerDefault = "table.preset.picker.default"
	CmdPresetPickerDelete  = "table.preset.picker.delete"
	CmdFocusHints          = "focus.hints"

	CmdAppAbout          = "app.about"
	CmdPaneAssociate     = "pane.associate"
	CmdCommandRun        = "command.run"
	CmdCommandEditRun    = "command.edit_run"
	CmdExtSetup          = "extension.setup"
	CmdTaskAddSibling    = "task.add.sibling"
	CmdTaskAddChild      = "task.add.child"
	CmdTaskFoldToggle    = "task.fold.toggle"
	CmdTaskAgentToggle   = "task.agent.toggle"
	CmdTaskAgentStatus   = "task.agent.status"
	CmdTaskThreadToggle  = "task.thread.toggle"
	CmdTaskNoteReply     = "task.note.reply"
	CmdTaskQueueDispatch = "task.queue.dispatch"
	CmdTaskPriorityCycle = "task.priority.cycle"
	CmdWorkLayoutToggle  = "work.layout.toggle"
	CmdShowFoldToggle    = "show.fold.toggle"
	CmdShowYank          = "show.yank"
	CmdShowYankBody      = "show.yank.body"
	CmdShowRegionNext    = "show.region.next"
	CmdShowRegionPrev    = "show.region.prev"
)

var leader = core.Layer{Name: "leader", Modes: []string{ModeTerminalLeader, ModeNormal}, ExcludeTags: []string{"motion", "editing"}}

// Modes returns the app's modes.
func Modes() []core.Mode {
	return []core.Mode{
		{Name: ModeGlobal},
		{Name: ModeNormal, Base: ModeGlobal},
		{Name: ModeTerminal, Passthrough: true},
		{Name: ModeTerminalLeader},
		{Name: ModeFilter},
		{Name: ModeSearch},
		{Name: ModePicker, Base: ModeGlobal},
		{Name: ModePickerFilter},
		{Name: ModeForm, Base: ModeGlobal},
		{Name: ModeFormEdit},
		{Name: ModeOutline, Base: ModeNormal},
		{Name: ModeOutlineEdit},
		{Name: ModeFeaturesNote},
		{Name: ModeThreadsCompose},
		{Name: ModeBindings, Base: ModeGlobal},
		{Name: ModeBindingsFilter},
		{Name: ModeBindingsCapture},
		{Name: ModeReader, Base: ModeGlobal},
		{Name: ModeCodeView, Base: ModeGlobal},
		{Name: ModeCodeViewSearch},
		{Name: ModeInspect, Base: ModeGlobal},
		{Name: ModeCommandLine},
		{Name: ModeHints},
		{Name: ModeComposer},
		{Name: ModeConfirm, Base: ModeGlobal},
		{Name: ModeAbout, Base: ModeGlobal},
		{Name: ModePage, Base: ModeGlobal},
		{Name: ModeNoteDeck},
		{Name: ModeZeroField},
		{Name: ModeReminderAlerts, Base: ModeGlobal},
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
		with(bind(n, all, "I", core.CmdPageOpen, "panes"), map[string]any{"id": SpacePanes}),
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
		bind(n, all, "ctrl+w h", "motion.left", "previous UI region"),
		bind(n, all, "ctrl+w l", "motion.right", "next UI region"),
		hidden(bind(n, all, "ctrl+w left", "motion.left", "previous UI region")),
		hidden(bind(n, all, "ctrl+w right", "motion.right", "next UI region")),
		hidden(bind(n, all, "left", "motion.left", "column left")),
		hidden(bind(n, all, "right", "motion.right", "column right")),
		bind(n, all, "g g", "motion.top", "top"),
		bind(n, all, "G", "motion.bottom", "bottom"),
		bind(n, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(n, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(n, all, "ctrl+f", "motion.page.down", "page down"),
		bind(n, all, "ctrl+b", "motion.page.up", "page up"),
		bind(n, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(n, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
		bind(n, all, "}", "motion.block.next", "next group"),
		bind(n, all, "{", "motion.block.prev", "previous group"),
		bind(n, all, "tab", core.CmdFocusNext, "next"),
		bind(n, all, "shift+tab", core.CmdFocusPrev, "previous"),

		// Enter opens what is focused; inner scopes say what "open" means there.
		when(bind(n, ScopeRail, "enter", CmdPaneEnterLive, "enter live pane"), "facts.onPane && facts.railEnterLive"),
		when(bind(n, ScopeRail, "enter", CmdSiteFocus, "focus pages"), "!(facts.onPane && facts.railEnterLive)"),
		bind(n, ScopeRail, "P", CmdProjectPageOpen, "open project page"),
		bind(n, views.ScopeSite, "P", CmdProjectPageOpen, "open project page"),
		bind(n, ScopeProjectOverview, "P", CmdProjectPageOpen, "open project page"),
		bind(n, views.ScopeTOC, "enter", CmdSitePageOpen, "open page"),
		bind(n, "terminal", "enter", CmdTermAttach, "type in the pane"),
		hidden(bind(n, "terminal", "i", CmdTermAttach, "type in the pane")),
		bind(n, "question", "enter", CmdAnswer, "answer"),
		bind(n, "question", "a", CmdAnswer, "answer"),
		when(bind(n, "question", "n", CmdQuestionNext, "next question"), "facts.space == \"panes\" || facts.inChildPaneView"),
		when(bind(n, "question", "p", CmdQuestionPrev, "previous question"), "facts.space == \"panes\" || facts.inChildPaneView"),
		when(bind(n, "question", "ctrl+s", CmdQuestionSubmitAll, "submit all answers"), "facts.space == \"panes\" || facts.inChildPaneView"),
		bind(n, data.KindCommand, "enter", CmdCommandRun, "run command"),
		bind(n, data.KindCommand, "r", CmdCommandRun, "run command"),
		bind(n, data.KindCommand, "e", CmdCommandEditRun, "edit & run command"),
		hidden(when(bind(n, views.ScopeIndex, "enter", CmdOutlineEditEnd, "edit inline"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "h", tree.CmdFoldOrParent, "fold / parent"), "facts.isTree && facts.hlFolds")),
		hidden(when(bind(n, views.ScopeIndex, "l", tree.CmdFoldOrStepIn, "unfold / child"), "facts.isTree && facts.hlFolds")),
		hidden(when(bind(n, views.ScopeIndex, "i", CmdOutlineEditStart, "edit inline at start"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "a", CmdOutlineEditEnd, "edit inline at end"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "o", CmdOutlineAddBelow, "new item below"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "O", CmdOutlineAddAbove, "new item above"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "d", views.CmdArchive, "archive"), "facts.isTree && facts.archiveStatus != \"archived\"")),
		hidden(when(bind(n, views.ScopeIndex, "d", views.CmdRestore, "restore"), "facts.isTree && facts.archiveStatus == \"archived\"")),
		hidden(when(bind(n, views.ScopeIndex, "tab", CmdOutlineIndent, "indent"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "shift+tab", CmdOutlineOutdent, "outdent"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, ">", CmdOutlineIndent, "indent"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "<", CmdOutlineOutdent, "outdent"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "J", CmdOutlineMoveDown, "move down"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "K", CmdOutlineMoveUp, "move up"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "z a", tree.CmdFoldToggle, "fold/unfold subtree"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "z o", tree.CmdFoldOpen, "unfold subtree"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "z c", tree.CmdFoldClose, "fold subtree"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "z R", tree.CmdFoldOpenAll, "unfold all"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "z M", tree.CmdFoldCloseAll, "fold all"), "facts.isTree")),
		hidden(when(bind(n, views.ScopeIndex, "=", tree.CmdHoistIn, "hoist subtree"), "facts.isTree && facts.outlineRescoping")),
		hidden(when(bind(n, views.ScopeIndex, "-", tree.CmdHoistOut, "unhoist"), "facts.isTree && facts.hoisted && facts.outlineRescoping")),
		hidden(when(bind(n, views.ScopeIndex, "esc", tree.CmdHoistOut, "unhoist"), "facts.isTree && !facts.visualActive && !(facts.markedCount > 0) && facts.hoisted")),
		hidden(when(bind(n, views.ScopeIndex, "esc", CmdOutlineCleanSlateExit, "exit clean slate"), "facts.isTree && !facts.visualActive && !(facts.markedCount > 0) && !facts.hoisted && facts.cleanSlate")),
		hidden(when(bind(n, views.ScopeIndex, "X", CmdOutlineTriage, "triage outline with agent"), "facts.isTree")),
		hidden(bind(n, data.KindTask, "w", CmdTaskAgentToggle, "toggle agent queue")),
		hidden(bind(n, data.KindTask, "g P", CmdTaskPriorityCycle, "cycle task priority")),
		hidden(bind(n, data.KindTask, "r", CmdTaskNoteReply, "reply with note")),
		hidden(bind(n, data.KindNote, "r", CmdTaskNoteReply, "reply with note")),
		hidden(when(bind(n, views.ScopeIndex, "t", CmdTaskThreadToggle, "toggle note thread"), "facts.isTree && !facts.featuresOutline")),
		hidden(when(bind(n, views.ScopeIndex, "D", CmdTaskQueueDispatch, "dispatch agent queue"), "facts.isTree")),
		hidden(bind(n, SpaceWork, "g t", CmdWorkLayoutToggle, "toggle tree/table")),
		bind(n, ScopeProjectContext, "enter", CmdProjectContextOpen, "open context item"),
		bind(n, ScopeProjectContext, "y", CmdProjectContextYank, "yank context item"),
		bind(n, ScopeProjectContext, "m", CmdProjectContextStar, "star context item"),
		hidden(bind(n, ScopeProjectContext, "*", CmdProjectContextStar, "star context item")),
		bind(n, ScopeProjectContext, "a", CmdProjectContextAttach, "attach context item"),
		bind(n, SpacePanes, "g p", CmdPanesGroupToggle, "toggle rail grouping"),
		bind(n, ScopeRail, "f", CmdPaneHints, "screen hints"),
		bind(n, "terminal", "f", CmdPaneHints, "screen hints"),
		bind(n, views.ScopeTOC, "f", CmdPaneHints, "screen hints"),
		bind(n, views.ScopeShow, "f", CmdPaneHints, "screen hints"),
		bind(ModeReader, all, "f", CmdPaneHints, "screen hints"),
		bind(ModePage, all, "f", CmdPaneHints, "screen hints"),
		bind(n, SpacePanes, "c", CmdPaneCompose, "compose prompt"),
		bind(n, SpacePanes, "L", CmdPaneAssociate, "associate pane with task"),
		bind(n, all, "g c", CmdContextPick, "context items"),
		when(bind(n, all, "g C", CmdFeaturesSuggestConcept, "create concept"), "facts.featuresPageActive"),
		when(bind(n, views.ScopeFeaturesContent, "p", CmdFeaturesSuggestProp, "suggest prop"), "facts.featuresPageActive && facts.featuresCanSuggestProp"),
		when(bind(n, views.ScopeFeaturesContent, "v", CmdFeaturesSuggestView, "suggest view"), "facts.featuresPageActive && facts.featuresCanSuggestView"),
		when(bind(n, data.KindExtension, "enter", CmdExtInstall, "install extension"), "facts.extAvailable"),
		when(bind(n, data.KindExtension, "enter", CmdExtRun, "run"), "!facts.extAvailable"),
		when(bind(n, data.KindExtension, "i", CmdExtInstall, "install / update"), "facts.extInstallable"),
		bind(n, "show.fold", "space", CmdShowFoldToggle, "toggle empty relations"),
		bind(n, "show.fold", "enter", CmdShowFoldToggle, "toggle empty relations"),
		bind(n, views.ScopeShow, "enter", views.CmdOpen, "open"),
		when(bind(n, views.ScopeShow, "y", CmdShowYank, "yank focused value"), "facts.copy != \"\""),
		bind(n, views.ScopeShow, "Y", CmdShowYankBody, "yank full body"),
		bind(n, views.ScopeShow, "]", CmdShowRegionNext, "next show region"),
		bind(n, views.ScopeShow, "[", CmdShowRegionPrev, "previous show region"),
		bind(n, all, "E", views.CmdOpen, "open show"),
		when(bind(n, all, "enter", CmdGroupToggle, "fold group"), "facts.isGroupHeader && !facts.enterOpensShow"),
		when(bind(n, all, "enter", views.CmdOpen, "open"), "facts.enterOpensShow"),
		with(bind(n, SpaceWork, "backspace", CmdHistoryBack, "back"), map[string]any{"scope": "work"}),
		with(bind(n, SpaceWork, "H", CmdHistoryBack, "back"), map[string]any{"scope": "work"}),
		with(bind(n, SpaceWork, "L", CmdHistoryForward, "forward"), map[string]any{"scope": "work"}),
		bind(n, all, "ctrl+o", CmdHistoryBack, "jump back"),
		bind(n, all, "ctrl+i", CmdHistoryForward, "jump forward"),

		// Record actions, on whatever record is focused.
		bind(n, all, "e", views.CmdEdit, "edit"),
		hidden(bind(n, data.KindTask, "g W", CmdTaskRewrite, "rewrite task")),
		when(bind(n, views.ScopeIndex, "n", "motion.down", "next match"), "facts.hasFilter"),
		when(bind(n, views.ScopeIndex, "N", "motion.up", "prev match"), "facts.hasFilter"),
		when(bind(n, all, "n", "motion.down", "next match"), "facts.hasFilter"),
		when(bind(n, all, "N", "motion.up", "prev match"), "facts.hasFilter"),
		when(with(bind(n, all, "n", views.CmdNew, "new task"), map[string]any{"kind": "task"}), "!facts.hasFilter && !facts.hasSearch"),
		with(bind(n, SpacePanes, "C", views.CmdNew, "new terminal"), map[string]any{
			"kind":     data.KindPane,
			"defaults": map[string]any{"harness": "zsh"},
			"submit":   true,
		}),
		when(with(bind(n, SpacePanes, "n", views.CmdNew, "new agent"), map[string]any{
			"kind":           data.KindPane,
			"title":          "New Agent",
			"excludeChoices": map[string]any{"harness": []string{"zsh"}},
		}), "!facts.hasFilter && !facts.hasSearch"),
		when(bind(n, SpacePanes, "t", CmdSiteTOCToggle, "toggle pages"), "layer == nil"),
		when(bind(n, SpaceWork, "n", CmdNewHere, "new"), "!facts.hasFilter && !facts.hasSearch"),
		bind(n, all, "g m", CmdAssign, "move to a task"),
		when(with(bind(n, all, "N", views.CmdNew, "new note"), map[string]any{"kind": "note"}), "!facts.hasFilter && !facts.hasSearch"),
		when(bind(n, all, "s", CmdStatusCycle, "cycle status"), "facts.hasStatus"),
		when(bind(n, all, "m", views.CmdStar, "star"), "facts.starStatus != \"starred\""),
		when(bind(n, all, "m", views.CmdUnstar, "unstar"), "facts.starStatus == \"starred\""),
		hidden(when(bind(n, all, "*", views.CmdStar, "star"), "facts.starStatus != \"starred\"")),
		hidden(when(bind(n, all, "*", views.CmdUnstar, "unstar"), "facts.starStatus == \"starred\"")),
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
		bind(n, SpaceExtensions, "D", CmdExtInstallAll, "install all missing core tools"),
		bind(n, SpaceExtensions, "U", CmdExtSetup, "extension setup"),
		bind(n, SpaceExtensions, "R", CmdExtRefresh, "look for extensions"),
		bind(n, data.KindSignal, "R", CmdTriageRule, "rule from this signal"),
		when(bind(n, SpacePanes, "Y", CmdPaneApprove, "approve"), "facts.hasPendingApproval"),
		when(bind(n, SpacePanes, "D", CmdPaneDeny, "deny"), "facts.hasPendingApproval"),
		when(bind(n, data.KindSchedule, "p", CmdSchedulePause, "pause"), "!facts.paused"),
		when(bind(n, data.KindSchedule, "p", CmdScheduleResume, "resume"), "facts.paused"),
		bind(n, all, "g R", CmdDaemonReset, "reset daemon"),
		bind(n, all, "g D", CmdDaemonUpgrade, "upgrade daemon in place"),
		bind(n, all, "g U", CmdAppReload, "reload client"),

		// Yank: y lists every value of the record, y y copies its main one.
		bind(n, all, "y", CmdYankPick, "yank"),
		// Open: what the record points at, with the handler its rule names.
		when(bind(n, all, "o", CmdOpenTarget, "open"), "facts.openable"),
		when(bind(n, all, "O", CmdOpenWith, "open with"), "facts.openable"),

		// Filterable views: scoped to trait.filter (emitted automatically by
		// ochazuke whenever a ViewNode declares FilterSpec or TraitFilter) so
		// non-filterable views (Show pages, Project Home, Reader, etc.) never
		// carry / or M in the context chain or footer hints.
		bind(n, traits.TraitScope(traits.TraitFilter), "/", CmdFilterStart, "filter"),
		bind(n, traits.TraitScope(traits.TraitFilter), "M", CmdFilterMode, "match mode"),
		bind(ModePage, traits.TraitScope(traits.TraitFilter), "/", CmdFilterStart, "filter"),
		bind(ModePage, traits.TraitScope(traits.TraitFilter), "M", CmdFilterMode, "match mode"),

		// Lists: guarded on facts.listTarget so a page with no table hides
		// these keys (they fall through) instead of refusing.
		when(bind(n, all, "f", CmdFilterFieldPick, "filter field"), "facts.listTarget"),
		when(bind(n, all, "o", CmdSortCycle, "sort"), "facts.listTarget"),
		when(bind(n, all, "O", CmdSortField, "sort column"), "facts.listTarget"),
		when(bind(n, all, "g s", CmdSortFieldPick, "sort by any field"), "facts.listTarget"),
		when(bind(n, all, "g b", CmdGroupFieldPick, "group by any field"), "facts.listTarget"),
		when(bind(n, all, "g v", CmdTablePresetPick, "table presets"), "facts.listTarget"),
		bind(ModeGlobal, all, "g f", CmdFocusHints, "focus jump hints"),
		bind(ModeGlobal, all, "g h", CmdPaneHints, "screen hints"),
		bind(n, all, "g n", CmdNounsPick, "browse any noun"),
		bind(n, all, "g r", CmdRecentsPick, "recents"),
		when(bind(n, all, "space", CmdGroupToggle, "fold group"), "facts.isGroupHeader"),
		// Selection: guarded on facts.selectTarget so a page with no list or
		// tree hides these keys (they fall through) instead of refusing.
		when(bind(n, all, "space", traits.SelectCmdMarkToggle, "mark"), "!facts.isGroupHeader && facts.tocGroup == null && facts.selectTarget"),
		when(bind(n, all, "v", traits.SelectCmdVisualToggle, "visual select"), "facts.selectTarget"),
		when(bind(n, all, "V", traits.SelectCmdVisualLine, "visual line"), "facts.selectTarget"),
		when(bind(n, all, "esc", traits.SelectCmdVisualClear, "clear visual"), "facts.visualActive"),
		when(bind(n, all, "esc", traits.SelectCmdMarkClear, "clear marks"), "!facts.visualActive && facts.markedCount > 0"),
		bind(n, all, "A", CmdArchivedToggle, "show archived"),

		// Overlays, from anywhere (ModeGlobal is inherited by normal and modal normal modes).
		bind(n, all, "T", CmdOutlineOpen, "outline"),
		bind(n, all, "b", CmdOutlineCleanSlate, "blank outline"),
		bind(n, all, "'", CmdPagesPick, "pages"),
		bind(n, all, ";", CmdRecordActions, "record actions"),
		bind(ModeGlobal, all, ":", CmdPaletteOpen, "commands"),
		with(bind(n, all, "!", CmdShellOpen, "shell command"), map[string]any{"prefix": string(commandline.PrefixShell)}),
		with(bind(n, all, "|", CmdShellOpen, "pipe record to shell"), map[string]any{"prefix": string(commandline.PrefixPipe)}),
		with(bind(n, all, "g !", CmdShellOpen, "read shell into note"), map[string]any{"prefix": string(commandline.PrefixReadShell)}),
		bind(ModeGlobal, all, "?", CmdHelpOpen, "bindings"),
		bind(n, all, "g o", CmdPathOpen, "path"),
		bind(n, all, "S", CmdSettingsOpen, "settings"),
		bind(ModeGlobal, all, "B", CmdBindingsOpen, "edit bindings"),
		bind(n, all, "g A", CmdAppAbout, "about"),
		bind(n, all, "W", CmdPaneAttention, "next pane that wants you"),
		{Mode: n, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(n, all, "Q", core.CmdAppQuit, "quit"),
		hidden(bind(ModeGlobal, all, "ctrl+c", core.CmdAppQuit, "quit")),

		// Terminal: everything but the leader goes to the pane.
		{Mode: ModeTerminal, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},
		bind(ModeTerminalLeader, all, "d", CmdTermDetach, "detach"),
		bind(ModeTerminalLeader, all, "z", CmdPaneZoom, "zoom the pane"),
		bind(ModeTerminalLeader, all, "f", CmdPaneHints, "terminal hints"),
		bind(ModeTerminalLeader, all, "c", CmdPaneCompose, "compose prompt"),
		bind(ModeTerminalLeader, all, "L", CmdLaneShow, "the pane's lane"),
		bind(ModeTerminalLeader, all, "esc", CmdTermEscape, "send esc"),
		with(bind(ModeTerminalLeader, all, "[", CmdTermPrefix, "copy mode"), map[string]any{"key": "["}),
		with(bind(ModeTerminalLeader, all, "t", CmdTermPrefix, "side terminal"), map[string]any{"key": "t"}),
		with(bind(ModeTerminalLeader, all, "v", CmdTermPrefix, "side vim"), map[string]any{"key": "v"}),
		bind(ModeTerminalLeader, all, "o", CmdSideSplitSwitch, "switch side split"),
		bind(ModeTerminalLeader, all, "x", CmdSideSplitClose, "close side split"),
		hidden(with(bind(ModeTerminalLeader, all, "ctrl+a", CmdTermPrefix, "send ctrl+a"), map[string]any{"key": "ctrl+a"})),
		with(bind(ModeTerminalLeader, all, "S", CmdScratchToggle, "scratch terminal"), map[string]any{"role": data.RoleScratchTerm}),
		with(bind(ModeTerminalLeader, all, "V", CmdScratchToggle, "scratch notes"), map[string]any{"role": data.RoleScratchVim}),

		// Filter: typing narrows the list.
		{Mode: ModeFilter, Class: core.ClassPrintable, Command: CmdFilterInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeFilter, Class: core.ClassPaste, Command: CmdFilterInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeFilter, all, "backspace", CmdFilterBack, "erase"),
		hidden(bind(ModeFilter, all, "shift+backspace", CmdFilterBack, "erase")),
		bind(ModeFilter, all, "enter", CmdFilterAccept, "keep"),
		bind(ModeFilter, all, "esc", CmdFilterCancel, "cancel"),
		bind(ModeFilter, all, "ctrl+x", CmdFilterClear, "clear"),
		bind(ModeFilter, all, "tab", CmdFilterMode, "match mode"),
		bind(ModeFilter, all, "ctrl+t", CmdFilterField, "filter field"),
		bind(ModeFilter, all, "down", "motion.down", "down"),
		bind(ModeFilter, all, "up", "motion.up", "up"),
		hidden(bind(ModeFilter, all, "ctrl+n", "motion.down", "down")),
		hidden(bind(ModeFilter, all, "ctrl+p", "motion.up", "up")),
		hidden(bind(ModeFilter, all, "ctrl+j", "motion.down", "down")),
		hidden(bind(ModeFilter, all, "ctrl+k", "motion.up", "up")),
		when(bind(ModeFilter, all, "ctrl+d", "motion.half.down", "half page down"), "facts.filterScrollMotions"),
		when(bind(ModeFilter, all, "ctrl+u", "motion.half.up", "half page up"), "facts.filterScrollMotions"),
		when(bind(ModeFilter, all, "ctrl+f", "motion.page.down", "page down"), "facts.filterScrollMotions"),
		when(bind(ModeFilter, all, "ctrl+b", "motion.page.up", "page up"), "facts.filterScrollMotions"),

		// Pickers: Normal Mode (j/k navigate, / or i enters filter mode).
		bind(ModePicker, all, "j", "motion.down", "down"),
		bind(ModePicker, all, "k", "motion.up", "up"),
		hidden(bind(ModePicker, all, "down", "motion.down", "down")),
		hidden(bind(ModePicker, all, "up", "motion.up", "up")),
		hidden(bind(ModePicker, all, "ctrl+n", "motion.down", "down")),
		hidden(bind(ModePicker, all, "ctrl+p", "motion.up", "up")),
		hidden(bind(ModePicker, all, "ctrl+j", "motion.down", "down")),
		hidden(bind(ModePicker, all, "ctrl+k", "motion.up", "up")),
		bind(ModePicker, all, "g g", "motion.top", "top"),
		bind(ModePicker, all, "G", "motion.bottom", "bottom"),
		bind(ModePicker, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModePicker, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModePicker, all, "ctrl+f", "motion.page.down", "page down"),
		bind(ModePicker, all, "ctrl+b", "motion.page.up", "page up"),
		bind(ModePicker, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(ModePicker, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
		bind(ModePicker, all, "/", picker.CmdFilterStart, "filter"),
		hidden(bind(ModePicker, all, "i", picker.CmdFilterStart, "filter")),
		bind(ModePicker, "yank", "y", CmdYankPrimary, "yank the main value"),
		bind(ModePicker, "context", "y", CmdContextYank, "yank context item"),
		bind(ModePicker, "context", "a", CmdContextAttach, "attach context item"),
		bind(ModePicker, "table.presets", "s", CmdPresetPickerSave, "save current view as preset"),
		bind(ModePicker, "table.presets", "d", CmdPresetPickerDefault, "set default preset"),
		bind(ModePicker, "table.presets", "x", CmdPresetPickerDelete, "delete preset"),
		bind(ModePicker, "path", "h", pathpicker.CmdParent, "parent directory"),
		hidden(bind(ModePicker, "path", "-", pathpicker.CmdParent, "parent directory")),
		hidden(bind(ModePicker, "path", "left", pathpicker.CmdParent, "parent directory")),
		bind(ModePicker, "path", "l", pathpicker.CmdOpen, "enter directory"),
		hidden(bind(ModePicker, "path", "right", pathpicker.CmdOpen, "enter directory")),
		bind(ModePicker, "path", "tab", pathpicker.CmdSelect, "choose this path"),
		bind(ModePicker, "path", "enter", pathpicker.CmdSelect, "choose this path"),
		bind(ModePicker, "path", "~", pathpicker.CmdHome, "home directory"),
		bind(ModePicker, "path", ".", pathpicker.CmdHiddenToggle, "hidden files"),
		hidden(bind(ModePicker, "path", "ctrl+t", pathpicker.CmdHiddenToggle, "hidden files")),
		bind(ModePicker, "path", "p", pathpicker.CmdPreviewToggle, "preview"),
		when(bind(ModePicker, "palette", "tab", palette.CmdFocusForm, "edit arguments"), "facts.hasArgs"),
		when(bind(ModePicker, "palette", "right", palette.CmdFocusForm, "edit arguments"), "facts.hasArgs"),
		hidden(when(bind(ModePicker, "palette", "l", palette.CmdFocusForm, "edit arguments"), "facts.hasArgs")),
		when(bind(ModePicker, all, "space", picker.CmdMarkToggle, "toggle mark"), "facts.multiSelect"),
		bind(ModePicker, all, "enter", picker.CmdConfirm, "choose"),
		bind(ModePicker, all, "tab", picker.CmdModeCycle, "match mode"),
		with(bind(ModePicker, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
		hidden(bind(ModePicker, all, "q", CmdModalCancel, "close")),

		// Pickers: Filter Insert Mode (typing narrows the picker, esc returns to normal mode).
		{Mode: ModePickerFilter, Class: core.ClassPrintable, Command: CmdPickerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModePickerFilter, Class: core.ClassPaste, Command: CmdPickerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		when(bind(ModePickerFilter, "yank", "y", CmdYankPrimary, "yank the main value"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "context", "y", CmdContextYank, "yank context item"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "context", "a", CmdContextAttach, "attach context item"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "table.presets", "s", CmdPresetPickerSave, "save current view as preset"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "table.presets", "d", CmdPresetPickerDefault, "set default preset"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "table.presets", "x", CmdPresetPickerDelete, "delete preset"), "facts.query == \"\""),
		bind(ModePickerFilter, all, "backspace", CmdPickerBack, "erase"),
		hidden(bind(ModePickerFilter, all, "shift+backspace", CmdPickerBack, "erase")),
		when(bind(ModePickerFilter, all, "ctrl+space", picker.CmdMarkToggle, "toggle mark"), "facts.multiSelect"),
		bind(ModePickerFilter, all, "enter", picker.CmdConfirm, "choose"),
		bind(ModePickerFilter, all, "esc", picker.CmdFilterStop, "normal mode"),
		bind(ModePickerFilter, all, "down", "motion.down", "down"),
		bind(ModePickerFilter, all, "up", "motion.up", "up"),
		hidden(bind(ModePickerFilter, all, "ctrl+n", "motion.down", "down")),
		hidden(bind(ModePickerFilter, all, "ctrl+p", "motion.up", "up")),
		hidden(bind(ModePickerFilter, all, "ctrl+j", "motion.down", "down")),
		hidden(bind(ModePickerFilter, all, "ctrl+k", "motion.up", "up")),
		when(bind(ModePickerFilter, all, "ctrl+d", "motion.half.down", "half page down"), "facts.filterScrollMotions"),
		when(bind(ModePickerFilter, all, "ctrl+u", "motion.half.up", "half page up"), "facts.filterScrollMotions"),
		when(bind(ModePickerFilter, all, "ctrl+f", "motion.page.down", "page down"), "facts.filterScrollMotions"),
		when(bind(ModePickerFilter, all, "ctrl+b", "motion.page.up", "page up"), "facts.filterScrollMotions"),
		bind(ModePickerFilter, "path", "tab", pathpicker.CmdSelect, "choose this path"),
		bind(ModePickerFilter, "path", "ctrl+t", pathpicker.CmdHiddenToggle, "hidden files"),
		when(bind(ModePickerFilter, "path", "left", pathpicker.CmdParent, "parent directory"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "path", "right", pathpicker.CmdOpen, "enter directory"), "facts.query == \"\""),
		when(bind(ModePickerFilter, "palette", "tab", palette.CmdFocusForm, "edit arguments"), "facts.hasArgs"),
		when(bind(ModePickerFilter, "palette", "right", palette.CmdFocusForm, "edit arguments"), "facts.hasArgs"),
		bind(ModePickerFilter, all, "tab", picker.CmdModeCycle, "match mode"),

		// Forms: explicit submit (ctrl+s or [ Submit ] row); settings apply live.
		bind(ModeForm, all, "j", "motion.down", "next field"),
		bind(ModeForm, all, "k", "motion.up", "previous field"),
		hidden(bind(ModeForm, all, "down", "motion.down", "next field")),
		hidden(bind(ModeForm, all, "up", "motion.up", "previous field")),
		hidden(bind(ModeForm, all, "g g", "motion.top", "first field")),
		hidden(bind(ModeForm, all, "G", "motion.bottom", "last field")),
		bind(ModeForm, "palette", "enter", form.CmdSubmit, "run"),
		bind(ModeForm, "palette", "shift+tab", palette.CmdFocusList, "back to commands"),
		bind(ModeForm, "palette", "esc", palette.CmdFocusList, "back to commands"),
		when(bind(ModeForm, "answer", "ctrl+n", CmdAnswerBatchNext, "next question"), "facts.isBatch"),
		when(bind(ModeForm, "answer", "ctrl+p", CmdAnswerBatchPrev, "previous question"), "facts.isBatch"),
		when(bind(ModeForm, "answer", "ctrl+e", "motion.scroll.down", "scroll question down"), "facts.answerHasQuestion"),
		when(bind(ModeForm, "answer", "ctrl+y", "motion.scroll.up", "scroll question up"), "facts.answerHasQuestion"),
		hidden(when(bind(ModeForm, "answer", "ctrl+d", "motion.half.down", "question half page down"), "facts.answerHasQuestion")),
		hidden(when(bind(ModeForm, "answer", "ctrl+u", "motion.half.up", "question half page up"), "facts.answerHasQuestion")),
		bind(ModeForm, all, "ctrl+s", form.CmdSubmit, "submit"),
		hidden(bind(ModeForm, all, "ctrl+enter", form.CmdSubmit, "submit")),
		bind(ModeForm, all, "enter", CmdFormActivate, "change field"),
		bind(ModeForm, all, "i", form.CmdEdit, "type"),
		bind(ModeForm, all, "space", form.CmdToggle, "toggle"),
		with(bind(ModeForm, all, "l", form.CmdCycle, "next choice"), map[string]any{"dir": 1}),
		with(bind(ModeForm, all, "h", form.CmdCycle, "previous choice"), map[string]any{"dir": -1}),
		bind(ModeForm, all, "p", form.CmdPick, "pick"),
		bind(ModeForm, all, "[", form.CmdDraftPrev, "prev draft"),
		bind(ModeForm, all, "]", form.CmdDraftNext, "next draft"),
		bind(ModeForm, all, "D", form.CmdDraftDiscard, "discard draft"),
		bind(ModeForm, all, "u", CmdUndo, "undo"),
		bind(ModeForm, all, "ctrl+r", CmdRedo, "redo"),
		with(bind(ModeForm, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
		hidden(bind(ModeForm, all, "q", CmdModalCancel, "close")),
		{Mode: ModeFormEdit, Class: core.ClassPrintable, Command: form.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeFormEdit, Class: core.ClassPaste, Command: form.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeFormEdit, all, "ctrl+s", form.CmdSubmit, "submit"),
		hidden(bind(ModeFormEdit, all, "ctrl+enter", form.CmdSubmit, "submit")),
		bind(ModeFormEdit, all, "backspace", form.CmdBackspace, "erase"),
		hidden(bind(ModeFormEdit, all, "shift+backspace", form.CmdBackspace, "erase")),
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
		bind(ModeBindings, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(ModeBindings, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
		bind(ModeBindings, all, "/", CmdBindingsFilter, "filter"),
		bind(ModeBindings, all, "tab", CmdBindingsMatchMode, "match mode"),
		bind(ModeBindings, all, "r", CmdBindingsRebind, "rebind"),
		hidden(bind(ModeBindings, all, "enter", CmdBindingsRebind, "rebind")),
		bind(ModeBindings, all, "x", "bindings.unbind", "unbind"),
		bind(ModeBindings, all, "R", "bindings.reset", "reset"),
		bind(ModeBindings, all, "u", "bindings.undo", "undo"),
		bind(ModeBindings, all, "ctrl+r", "bindings.redo", "redo"),
		with(bind(ModeBindings, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
		hidden(bind(ModeBindings, all, "q", CmdModalCancel, "close")),
		{Mode: ModeBindingsFilter, Class: core.ClassPrintable, Command: "bindings.filter.append", Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeBindingsFilter, Class: core.ClassPaste, Command: "bindings.filter.append", Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeBindingsFilter, all, "backspace", "bindings.filter.backspace", "erase"),
		hidden(bind(ModeBindingsFilter, all, "shift+backspace", "bindings.filter.backspace", "erase")),
		bind(ModeBindingsFilter, all, "enter", CmdBindingsFilterDone, "keep"),
		bind(ModeBindingsFilter, all, "esc", CmdBindingsFilterClear, "clear"),
		bind(ModeBindingsFilter, all, "tab", CmdBindingsMatchMode, "match mode"),
		bind(ModeBindingsFilter, all, "down", "motion.down", "down"),
		bind(ModeBindingsFilter, all, "up", "motion.up", "up"),
		hidden(bind(ModeBindingsFilter, all, "ctrl+n", "motion.down", "down")),
		hidden(bind(ModeBindingsFilter, all, "ctrl+p", "motion.up", "up")),
		hidden(bind(ModeBindingsFilter, all, "ctrl+j", "motion.down", "down")),
		hidden(bind(ModeBindingsFilter, all, "ctrl+k", "motion.up", "up")),
		when(bind(ModeBindingsFilter, all, "ctrl+d", "motion.half.down", "half page down"), "facts.filterScrollMotions"),
		when(bind(ModeBindingsFilter, all, "ctrl+u", "motion.half.up", "half page up"), "facts.filterScrollMotions"),
		when(bind(ModeBindingsFilter, all, "ctrl+f", "motion.page.down", "page down"), "facts.filterScrollMotions"),
		when(bind(ModeBindingsFilter, all, "ctrl+b", "motion.page.up", "page up"), "facts.filterScrollMotions"),
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
		bind(ModeReader, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(ModeReader, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
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
		with(bind(ModeReader, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
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
		bind(ModeInspect, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(ModeInspect, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
		bind(ModeInspect, all, "/", CmdInspectFilter, "filter"),
		when(bind(ModeInspect, all, "a", CmdInspectBind, "bind to a free key"), "facts.inspecting == \"actions\""),
		with(bind(ModeInspect, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
		bind(ModeInspect, all, "q", CmdModalCancel, "close"),

		// Outline: the normal keys work on the selected task.
		// Explicit digit rows take precedence over ModeNormal's 1..4 space keys so counts like 2J/2K work.
		with(hidden(bind(ModeOutline, all, "1", core.CmdCountPush, "count")), map[string]any{"digit": 1}),
		with(hidden(bind(ModeOutline, all, "2", core.CmdCountPush, "count")), map[string]any{"digit": 2}),
		with(hidden(bind(ModeOutline, all, "3", core.CmdCountPush, "count")), map[string]any{"digit": 3}),
		with(hidden(bind(ModeOutline, all, "4", core.CmdCountPush, "count")), map[string]any{"digit": 4}),
		with(hidden(bind(ModeOutline, all, "5", core.CmdCountPush, "count")), map[string]any{"digit": 5}),
		with(hidden(bind(ModeOutline, all, "6", core.CmdCountPush, "count")), map[string]any{"digit": 6}),
		with(hidden(bind(ModeOutline, all, "7", core.CmdCountPush, "count")), map[string]any{"digit": 7}),
		with(hidden(bind(ModeOutline, all, "8", core.CmdCountPush, "count")), map[string]any{"digit": 8}),
		with(hidden(bind(ModeOutline, all, "9", core.CmdCountPush, "count")), map[string]any{"digit": 9}),
		when(with(hidden(bind(ModeOutline, all, "0", core.CmdCountPush, "count")), map[string]any{"digit": 0}), "count != null"),
		bind(ModeOutline, all, "enter", CmdOutlineEditEnd, "edit inline"),
		bind(ModeOutline, all, "E", CmdOutlineJump, "open task"),
		bind(ModeOutline, all, "J", CmdOutlineMoveDown, "move down"),
		bind(ModeOutline, all, "K", CmdOutlineMoveUp, "move up"),
		bind(ModeOutline, all, ">", CmdOutlineIndent, "indent"),
		bind(ModeOutline, all, "<", CmdOutlineOutdent, "outdent"),
		bind(ModeOutline, all, "tab", CmdOutlineIndent, "indent"),
		bind(ModeOutline, all, "shift+tab", CmdOutlineOutdent, "outdent"),
		bind(ModeOutline, all, "space", traits.SelectCmdMarkToggle, "mark"),
		bind(ModeOutline, all, "v", traits.SelectCmdVisualToggle, "visual select"),
		bind(ModeOutline, all, "V", traits.SelectCmdVisualLine, "visual line"),
		bind(ModeOutline, all, "s", CmdStatusCycle, "cycle status"),
		when(bind(ModeOutline, all, "h", tree.CmdFoldOrParent, "fold / parent"), "facts.hlFolds"),
		when(bind(ModeOutline, all, "l", tree.CmdFoldOrStepIn, "unfold / child"), "facts.hlFolds"),
		bind(ModeOutline, all, "i", CmdOutlineEditStart, "edit inline at start"),
		bind(ModeOutline, all, "a", CmdOutlineEditEnd, "edit inline at end"),
		bind(ModeOutline, all, "o", CmdOutlineAddBelow, "new item below"),
		bind(ModeOutline, all, "O", CmdOutlineAddAbove, "new item above"),
		when(bind(ModeOutline, all, "d", views.CmdArchive, "archive"), "facts.archiveStatus != \"archived\""),
		when(bind(ModeOutline, all, "d", views.CmdRestore, "restore"), "facts.archiveStatus == \"archived\""),
		bind(ModeOutline, all, "z a", tree.CmdFoldToggle, "fold/unfold subtree"),
		bind(ModeOutline, all, "z o", tree.CmdFoldOpen, "unfold subtree"),
		bind(ModeOutline, all, "z c", tree.CmdFoldClose, "fold subtree"),
		bind(ModeOutline, all, "z R", tree.CmdFoldOpenAll, "unfold all"),
		bind(ModeOutline, all, "z M", tree.CmdFoldCloseAll, "fold all"),
		when(bind(ModeOutline, all, "=", tree.CmdHoistIn, "hoist subtree"), "facts.outlineRescoping"),
		when(bind(ModeOutline, all, "-", tree.CmdHoistOut, "unhoist"), "facts.hoisted && facts.outlineRescoping"),
		bind(ModeOutline, all, "b", CmdOutlineCleanSlate, "blank outline"),
		bind(ModeOutline, all, "X", CmdOutlineTriage, "triage outline with agent"),
		bind(ModeOutline, all, "w", CmdTaskAgentToggle, "toggle agent queue"),
		bind(ModeOutline, all, "t", CmdTaskThreadToggle, "toggle note thread"),
		bind(ModeOutline, all, "r", CmdTaskNoteReply, "reply with note"),
		bind(ModeOutline, all, "D", CmdTaskQueueDispatch, "dispatch agent queue"),
		when(bind(ModeOutline, all, "esc", traits.SelectCmdVisualClear, "clear visual"), "facts.visualActive"),
		when(bind(ModeOutline, all, "esc", traits.SelectCmdMarkClear, "clear marks"), "!facts.visualActive && facts.markedCount > 0"),
		when(bind(ModeOutline, all, "esc", tree.CmdHoistOut, "unhoist"), "!facts.visualActive && !(facts.markedCount > 0) && facts.hoisted"),
		when(bind(ModeOutline, all, "esc", CmdOutlineCleanSlateExit, "exit clean slate"), "!facts.visualActive && !(facts.markedCount > 0) && !facts.hoisted && facts.cleanSlate"),
		when(with(bind(ModeOutline, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}), "!facts.visualActive && !(facts.markedCount > 0) && !facts.hoisted && !facts.cleanSlate"),
		bind(ModeOutline, all, "q", CmdModalCancel, "close"),
		bind(ModeOutline, all, "T", CmdModalCancel, "close"),

		// Inline Vim Outline edit mode (i, a, o, O in Outline or 2 Work Tasks Tree).
		{Mode: ModeOutlineEdit, Class: core.ClassPrintable, Command: CmdOutlineEditInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeOutlineEdit, Class: core.ClassPaste, Command: CmdOutlineEditInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeOutlineEdit, all, "backspace", CmdOutlineEditBackspace, "erase"),
		hidden(bind(ModeOutlineEdit, all, "shift+backspace", CmdOutlineEditBackspace, "erase")),
		bind(ModeOutlineEdit, all, "tab", CmdOutlineEditIndent, "indent"),
		bind(ModeOutlineEdit, all, "shift+tab", CmdOutlineEditOutdent, "outdent"),
		bind(ModeOutlineEdit, all, "enter", CmdOutlineEditNewline, "commit & new item"),
		bind(ModeOutlineEdit, all, "shift+enter", CmdOutlineEditLinebreak, "insert line break"),
		bind(ModeOutlineEdit, all, "esc", CmdOutlineEditCommit, "save & normal mode"),
		bind(ModeOutlineEdit, all, "ctrl+c", CmdOutlineEditCancel, "discard edit"),

		// Command line: !, |, r! with Readline editing, history and ctrl+r register paste.
		{Mode: ModeCommandLine, Class: core.ClassPrintable, Command: commandline.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeCommandLine, Class: core.ClassPaste, Command: commandline.CmdInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeCommandLine, all, "enter", commandline.CmdSubmit, "run"),
		bind(ModeCommandLine, all, "esc", commandline.CmdCancel, "cancel"),
		bind(ModeCommandLine, all, "ctrl+r", commandline.CmdRegister, "paste register"),
		bind(ModeCommandLine, all, "up", commandline.CmdHistoryPrev, "older"),
		bind(ModeCommandLine, all, "down", commandline.CmdHistoryNext, "newer"),
		hidden(bind(ModeCommandLine, all, "ctrl+p", commandline.CmdHistoryPrev, "older")),
		hidden(bind(ModeCommandLine, all, "ctrl+n", commandline.CmdHistoryNext, "newer")),
		bind(ModeCommandLine, all, "tab", commandline.CmdCompleteNext, "complete"),
		hidden(bind(ModeCommandLine, all, "shift+tab", commandline.CmdCompletePrev, "previous completion")),

		// Live terminal hints overlay.
		{Mode: ModeHints, Class: core.ClassAny, Command: CmdHintsKey, Args: map[string]any{"key": "$key", "text": "$text"}, Hidden: true},
		bind(ModeHints, all, "tab", CmdHintsActionCycle, "cycle hint action"),
		bind(ModeHints, all, "esc", CmdHintsCancel, "cancel hints"),
		bind(ModeHints, all, "ctrl+c", CmdHintsCancel, "cancel hints"),

		// Multi-line prompt composer overlay.
		{Mode: ModeComposer, Class: core.ClassPrintable, Command: CmdComposerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: ModeComposer, Class: core.ClassPaste, Command: CmdComposerInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(ModeComposer, all, "enter", CmdComposerEnter, "newline / accept"),
		bind(ModeComposer, all, "esc", CmdComposerCancel, "close & save draft"),
		bind(ModeComposer, all, "ctrl+s", CmdComposerSubmit, "submit prompt"),
		hidden(bind(ModeComposer, all, "ctrl+enter", CmdComposerSubmit, "submit prompt")),
		bind(ModeComposer, all, "tab", CmdComposerComplete, "complete / delivery mode"),
		bind(ModeComposer, all, "shift+tab", CmdComposerModeCycle, "cycle delivery mode"),
		bind(ModeComposer, all, "up", CmdComposerUp, "up"),
		bind(ModeComposer, all, "down", CmdComposerDown, "down"),
		bind(ModeComposer, all, "ctrl+p", CmdComposerDraftPrev, "previous draft / completion"),
		bind(ModeComposer, all, "ctrl+n", CmdComposerDraftNext, "next draft / completion"),
		bind(ModeComposer, all, "ctrl+v", CmdComposerAttachClipboard, "paste or attach clipboard"),
		bind(ModeComposer, all, "alt+i", CmdComposerAttachFile, "attach file"),
		when(bind(ModeComposer, all, "ctrl+f", CmdComposerAttachFile, "attach file"), "!facts.readlinePriority"),
		bind(ModeComposer, all, "ctrl+x", CmdComposerAttachRemove, "remove last attachment"),
		bind(ModeComposer, all, "backspace", CmdComposerBackspace, "erase"),
		hidden(bind(ModeComposer, all, "shift+backspace", CmdComposerBackspace, "erase")),

		// Shared confirmation modal.
		bind(ModeConfirm, all, "y", CmdConfirmAccept, "confirm"),
		bind(ModeConfirm, all, "enter", CmdConfirmAccept, "confirm"),
		bind(ModeConfirm, all, "n", CmdConfirmCancel, "cancel"),
		bind(ModeConfirm, all, "esc", CmdConfirmCancel, "cancel"),
		hidden(bind(ModeConfirm, all, "q", CmdConfirmCancel, "cancel")),
		hidden(bind(ModeConfirm, all, "ctrl+c", CmdConfirmCancel, "cancel")),

		// Scrollable About diagnostics overlay.
		bind(ModeAbout, all, "j", "motion.down", "down"),
		bind(ModeAbout, all, "k", "motion.up", "up"),
		hidden(bind(ModeAbout, all, "down", "motion.down", "down")),
		hidden(bind(ModeAbout, all, "up", "motion.up", "up")),
		bind(ModeAbout, all, "g g", "motion.top", "top"),
		bind(ModeAbout, all, "G", "motion.bottom", "bottom"),
		bind(ModeAbout, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModeAbout, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModeAbout, all, "ctrl+f", "motion.page.down", "page down"),
		bind(ModeAbout, all, "ctrl+b", "motion.page.up", "page up"),
		bind(ModeAbout, all, "ctrl+e", "motion.scroll.down", "scroll viewport down"),
		bind(ModeAbout, all, "ctrl+y", "motion.scroll.up", "scroll viewport up"),
		hidden(bind(ModeAbout, all, "space", "motion.page.down", "page down")),
		with(bind(ModeAbout, all, "esc", CmdModalCancel, "close"), map[string]any{"via": "esc"}),
		bind(ModeAbout, all, "q", CmdModalCancel, "close"),
	}
	out = append(out, macroBindings()...)
	out = append(out, viewCycleBindings()...)
	out = append(out, uxNotesBindings()...)
	out = append(out, pageModeBindings()...)
	out = append(out, fdSearchBindings()...)
	out = append(out, filesBindings()...)
	out = append(out, queueBindings()...)
	out = append(out, ideasBindings()...)
	out = append(out, threadsBindings()...)
	out = append(out, sessionsBindings()...)
	out = append(out, focusBindings()...)
	out = append(out, paneSearchBindings()...)
	out = append(out, laneBarBindings()...)
	out = append(out, projectHomeBindings()...)
	out = append(out, projectPortalBindings()...)
	out = append(out, noteDeckBindings()...)
	out = append(out, projectTabsBindings()...)
	out = append(out, projectPanesSectionBindings()...)
	out = append(out, featuresPageBindings()...)
	out = append(out, jobsBindings()...)
	out = append(out, composerContextBindings()...)
	out = append(out, insertPickerBindings()...)
	out = append(out, clipboardAttachBindings()...)
	out = append(out, clipboardExchangeBindings()...)
	out = append(out, gitHubBindings()...)
	out = append(out, zeroFieldBindings()...)
	out = append(out, pickerPickerBindings()...)
	out = append(out, splitResizeBindings()...)
	out = append(out, projectCycleBindings()...)
	out = append(out, contentHintsBindings()...)
	out = append(out, screenSearchBindings()...)
	out = append(out, outlineSuggestBindings()...)
	out = append(out, codeViewerBindings()...)
	out = append(out, captureBindings()...)
	out = append(out, pinnedAgentsBindings()...)
	out = append(out, pasteBindings()...)
	out = append(out, remindersBindings()...)
	out = append(out, outlineScopeBindings()...)
	for _, mode := range []string{ModeComposer, ModeFormEdit, ModeFilter, ModeSearch, ModeCommandLine, ModeOutlineEdit, ModeFeaturesNote, ModeThreadsCompose, ModePickerFilter} {
		out = append(out, with(bind(mode, all, "alt+h", CmdPaneHints, "paste terminal hint"), map[string]any{"action": HintsActionInsert}))
	}
	for _, mode := range []string{ModeFormEdit, ModeFilter, ModeSearch, ModePickerFilter, ModeBindingsFilter, ModeCommandLine, ModeComposer, ModeOutlineEdit, ModeFeaturesNote, ModeThreadsCompose, ModeNoteDeck} {
		out = appendReadlineBindings(out, mode, all)
	}
	// Motions and space switches work everywhere; the footer leaves them out
	// (the help overlay lists them).
	for i, b := range out {
		if strings.HasPrefix(b.Command, "motion.") || strings.HasPrefix(b.Command, "focus.") || strings.HasPrefix(b.Command, "page.") {
			out[i].Hidden = true
		}
	}
	return append(out, chipBindings()...)
}

// appendReadlineBindings appends Ochazuke's default Readline bindings for
// mode/scope, skipping keys already unconditionally bound in that mode and
// applying complementary When guards when a key has an opt-out conditional row.
func appendReadlineBindings(out []core.Binding, mode, scope string) []core.Binding {
	added := map[string]bool{}
	for _, rb := range input.ReadlineBindings(mode, scope) {
		k := strings.Join(rb.Keys, " ")
		if added[k] {
			continue
		}
		skip, guard := readlineGuardForKey(out, mode, scope, k)
		if skip {
			continue
		}
		if guard != "" {
			rb.When = guard
		}
		added[k] = true
		out = append(out, rb)
	}
	return out
}

func readlineGuardForKey(existing []core.Binding, mode, scope, key string) (bool, string) {
	guard := ""
	for _, b := range existing {
		if b.Mode != mode || b.Scope != scope || strings.Join(b.Keys, " ") != key {
			continue
		}
		if b.When == "" {
			return true, ""
		}
		switch b.When {
		case "!facts.readlinePriority":
			guard = "facts.readlinePriority"
		case "facts.filterScrollMotions":
			guard = "!facts.filterScrollMotions"
		}
	}
	return false, guard
}
