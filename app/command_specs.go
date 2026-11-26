package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/palette"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/tree"
)

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
	base := append(append(append(append(readerSpecs(), readlineSpecs()...), paletteSpecs()...), viewCycleSpecs()...), traits.SelectCommandSpecs()...)
	base = append(base, filesCommandSpecs()...)
	return append(append(base, []core.CommandSpec{
		recordSpec(views.CmdOpen, "Open the focused record's Show page", nil, ""),
		recordSpec(views.CmdEdit, "Edit the focused record", nil, "", "editing"),
		{ID: views.CmdNew, Description: "Create a record", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "kind", Type: core.ParamString, Default: "task", Description: "Noun kind to create"},
			{Name: "title", Type: core.ParamString, Description: "Optional form title override"},
			{Name: "defaults", Type: core.ParamObject, Description: "Optional map of default field values applied to the form or instant creation"},
			{Name: "fields", Type: core.ParamObject, Description: "Optional list of field names to include in the form"},
			{Name: "hideFields", Type: core.ParamObject, Description: "Optional list of field names to omit from the form while keeping their defaults on submit"},
			{Name: "choices", Type: core.ParamObject, Description: "Optional map of field name to allowed choice values"},
			{Name: "excludeChoices", Type: core.ParamObject, Description: "Optional map of field name to excluded choice values"},
			{Name: "submit", Type: core.ParamBool, Default: false, Description: "Skip the form modal and create the record immediately from defaults and inferred context"},
		}},
		recordSpec(views.CmdStar, "Star the focused record", nil, "facts.starStatus != \"starred\"", "editing"),
		recordSpec(views.CmdUnstar, "Unstar the focused record", nil, "facts.starStatus == \"starred\"", "editing"),
		recordSpec(CmdStatusCycle, "Cycle the focused record's primary status field", nil, "facts.hasStatus", "editing"),
		recordSpec(views.CmdArchive, "Archive the focused record", nil, "facts.archiveStatus != \"archived\"", "editing"),
		recordSpec(views.CmdRestore, "Restore the focused record", nil, "facts.archiveStatus == \"archived\"", "editing"),
		recordSpec(views.CmdSnooze, "Snooze the focused record", nil, "!facts.snoozed", "editing"),
		recordSpec(views.CmdUnsnooze, "Unsnooze the focused record", nil, "facts.snoozed", "editing"),
		spec(CmdUndo, "Undo my last change", "editing"),
		spec(CmdRedo, "Redo my last undone change", "editing"),
		{ID: CmdHistoryBack, Description: "Go back in jump history (or Work Show stack)", Params: []core.ParamSpec{
			{Name: "scope", Type: core.ParamString, Enum: []string{"global", "work"}, Default: "global", Description: "jump scope: global or work"},
		}},
		{ID: CmdHistoryForward, Description: "Go forward in jump history (or Work Show stack)", Params: []core.ParamSpec{
			{Name: "scope", Type: core.ParamString, Enum: []string{"global", "work"}, Default: "global", Description: "jump scope: global or work"},
		}},
		spec(CmdSiteFocus, "Go to the selected pane's pages"),
		spec(CmdPaneEnterLive, "Enter the selected pane's live terminal directly from the rail"),
		spec(CmdSideSplitSwitch, "Toggle focus between the main Screen and the side split terminal"),
		spec(CmdSideSplitClose, "Close the active side split panel"),
		spec(CmdProjectPageOpen, "Open the assigned top-level Project Show page"),
		spec(CmdSitePageOpen, "Open the focused page of the pane's site"),
		spec(CmdSiteTOCToggle, "Show or hide the pane's Pages table of contents rail"),
		recordSpec(CmdAnswer, "Answer the focused question", []string{data.KindQuestion}, "", "editing"),
		spec(CmdTermAttach, "Type in the pane's terminal"),
		spec(CmdTermDetach, "Leave the pane's terminal"),
		{ID: CmdTermInput, Description: "Send a key to the pane", Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}, {Name: "text", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		recordSpec(CmdRunRerun, "Run the focused run again", []string{data.KindRun}, "", "editing"),
		recordSpec(CmdRunCancel, "Cancel the focused run", []string{data.KindRun}, "", "editing"),
		spec(CmdRunNew, "Run a shell command as a run", "editing"),
		spec(CmdExtRefresh, "Look for extensions and check their health", "editing"),
		recordSpec(CmdExtRun, "Run a command of the focused extension", []string{data.KindExtension}, "", "editing"),
		recordSpec(CmdExtInstall, "Download and install (or update) the focused OFAM extension via go install", []string{data.KindExtension}, "facts.extInstallable", "editing"),
		spec(CmdExtInstallAll, "Install all missing core OFAM extensions via go install", "editing"),
		{ID: CmdTermPrefix, Description: "The pane's prefix key and the key after it: [ copy mode, t side terminal, v side vim",
			Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		{ID: CmdWake, Description: "Look at what changed off the UI goroutine", Meta: map[string]any{"internal": true}},
		spec(CmdOutlineOpen, "Open the outline of every task"),
		spec(CmdOutlineJump, "Open the selected task"),
		spec(CmdOutlineMoveDown, "Move the selected task down among its siblings", "editing"),
		spec(CmdOutlineMoveUp, "Move the selected task up among its siblings", "editing"),
		spec(CmdOutlineIndent, "Indent the selected task under its preceding sibling", "editing"),
		spec(CmdOutlineOutdent, "Outdent the selected task to become a sibling of its parent", "editing"),
		spec(CmdOutlineStatusCycle, "Cycle the selected task's workStatus", "editing"),
		spec(CmdOutlineRename, "Rename the selected task via form", "editing"),
		spec(CmdOutlineEditStart, "Edit the selected outline task title in place with cursor at start", "editing"),
		spec(CmdOutlineEditEnd, "Edit the selected outline task title in place with cursor at end", "editing"),
		spec(CmdOutlineAddBelow, "Insert a new outline task inline below at the same nesting level", "editing"),
		spec(CmdOutlineAddAbove, "Insert a new outline task inline above at the same nesting level", "editing"),
		{ID: CmdOutlineEditInsert, Description: "Insert text during inline outline edit", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditBackspace, Description: "Erase character during inline outline edit", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditIndent, Description: "Indent a new inline outline task under the item above", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditOutdent, Description: "Outdent a new inline outline task one level", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditCommit, Description: "Commit inline outline edit", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditCancel, Description: "Cancel inline outline edit", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditNewline, Description: "Commit inline outline edit and start a new sibling below", Meta: map[string]any{"internal": true}},
		{ID: CmdOutlineEditLinebreak, Description: "Insert a line break in inline outline edit", Meta: map[string]any{"internal": true}},
		spec(CmdOutlineCleanSlate, "Open a fresh blank outline canvas hiding prior clutter while allowing multiple top-level trees", "editing"),
		spec(CmdOutlineCleanSlateExit, "Exit clean-slate outline filter and show the full tree", "editing"),
		{
			ID:          CmdOutlineTriage,
			Description: "Send the focused outline subtree to an agent pane for read-only triage into a ui.page proposal",
			Params:      []core.ParamSpec{{Name: "pane", Type: core.ParamString, Description: "Optional target agent pane ID or title"}},
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindTask, data.KindLane}},
		},
		spec(CmdRecentsPick, "Pick from recently updated records across all nouns"),
		spec(CmdPagesPick, "Pick a page of any pane"),
		spec(CmdRecordActions, "Pick an action for the focused record"),
		{ID: CmdShellOpen, Description: "Open the shell I/O bar (! run, | pipe focused record, r! capture note)",
			Params: []core.ParamSpec{{Name: "prefix", Type: core.ParamString, Enum: []string{"!", "|", "r!"}, Default: "!"}}},
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
		spec(CmdNewHere, "Create a record for the current space or section", "editing"),
		recordSpec(CmdAssign, "Move the focused record to a task (a task: under another task, or to the root)", nil, "", "editing"),
		spec(CmdNounsPick, "Browse any noun: pick one and its index opens in Work"),
		spec(CmdFilterFieldPick, "Pick the field the filter matches, from every field of the rows"),
		spec(CmdSortFieldPick, "Pick the field to sort by, from every field of the rows"),
		spec(CmdGroupFieldPick, "Pick the field to group by, from every field of the rows"),
		spec(CmdSettingsOpen, "Change the settings; each change applies at once and is saved"),
		spec(CmdBindingsOpen, "Browse, filter, rebind, unbind and reset the key bindings"),
		spec(CmdPaneZoom, "Zoom the pane: hide the rail and the pages, or bring them back"),
		spec(CmdPaneAttention, "Go to the next pane with an unread question or a waiting program"),
		recordSpec(CmdPaneApprove, "Approve the selected pane's pending permission request", []string{data.KindPane}, "facts.hasPendingApproval"),
		recordSpec(CmdPaneDeny, "Deny the selected pane's pending permission request", []string{data.KindPane}, "facts.hasPendingApproval"),
		spec(CmdDaemonReset, "Reset the local daemon: close all live panes, clear telemetry and archive dropped panes"),
		spec(CmdDaemonUpgrade, "Upgrade the local daemon binary in place without killing live panes or subagents"),
		spec(CmdAppReload, "Reload the Matomeru TUI client binary in place without disturbing running daemon panes"),
		recordSpec(CmdSchedulePause, "Pause the focused schedule so the daemon stops firing it", []string{data.KindSchedule}, "!facts.paused", "editing"),
		recordSpec(CmdScheduleResume, "Resume the focused schedule so the daemon fires it again", []string{data.KindSchedule}, "facts.paused", "editing"),
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
		{ID: picker.CmdFilterStart, Description: "Enter filter mode in the picker", Meta: map[string]any{"internal": true}},
		{ID: picker.CmdFilterStop, Description: "Leave picker filter mode", Meta: map[string]any{"internal": true}},
		{ID: picker.CmdFilterClear, Description: "Clear the picker's filter query", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdSelect, Description: "Choose the highlighted path", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdParent, Description: "Go to the parent directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdOpen, Description: "Enter the highlighted directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdHiddenToggle, Description: "Show or hide hidden files", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdHome, Description: "Go to the home directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdPreviewToggle, Description: "Show or hide the path preview", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdSelectCurrent, Description: "Choose the current directory", Meta: map[string]any{"internal": true}},
		{ID: pathpicker.CmdModeCycle, Description: "Change how the path picker matches", Meta: map[string]any{"internal": true}},
		{ID: CmdFormActivate, Description: "Change the active field or submit on the Submit row", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdSubmit, Description: "Submit the form", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdEdit, Description: "Type into the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdStop, Description: "Stop typing into the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdToggle, Description: "Toggle the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdCycle, Description: "Pick the next choice of the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdInsert, Description: "Type into the active field", Params: text, Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdBackspace, Description: "Erase the last character of the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdPick, Description: "Pick a value for the active field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdDraftPrev, Description: "Load the previous saved draft", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdDraftNext, Description: "Step forward toward newer drafts or current edits", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdDraftDiscard, Description: "Discard the currently loaded draft", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdItemAdd, Description: "Add an item to the focused repeatable field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: form.CmdItemRemove, Description: "Remove the focused item from a repeatable field", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdInsert, Description: "Type into the shell command bar", Params: text, Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdSubmit, Description: "Run the shell command bar", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdCancel, Description: "Close the shell command bar", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdRegister, Description: "Paste a register into the shell command bar", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdHistoryPrev, Description: "Older shell command from history", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdHistoryNext, Description: "Newer shell command from history", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdCompleteNext, Description: "Next completion in the shell command bar", Meta: map[string]any{"internal": true}},
		{ID: commandline.CmdCompletePrev, Description: "Previous completion in the shell command bar", Meta: map[string]any{"internal": true}},
		spec(CmdPanesGroupToggle, "Toggle pane rail grouping between project-pane and flat"),
		spec(CmdContextPick, "Search and act on high-signal context items collected across panes"),
		{ID: CmdContextYank, Description: "Yank the focused context item", Meta: map[string]any{"internal": true}},
		{ID: CmdContextAttach, Description: "Attach the focused context item via context relation", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		spec(CmdProjectContextOpen, "Open the focused project context item with its matching handler"),
		spec(CmdProjectContextYank, "Yank the focused project context item"),
		spec(CmdProjectContextStar, "Star or unstar the focused project context item"),
		spec(CmdProjectContextAttach, "Attach the focused project context item via context relation", "editing"),
		spec(CmdShowFoldToggle, "Expand or collapse empty relation sections on a Show page"),
		spec(CmdShowYank, "Copy the focused Show page value or token to the clipboard"),
		spec(CmdShowYankBody, "Copy the full Markdown body of the current Show page"),
		spec(CmdShowRegionNext, "Jump focus to the next Show page region"),
		spec(CmdShowRegionPrev, "Jump focus to the previous Show page region"),
		{
			ID:          CmdPaneHints,
			Description: "Show link/path/PR hint labels over the terminal screen",
			Params: []core.ParamSpec{{
				Name:        "action",
				Type:        core.ParamString,
				Enum:        []string{HintsActionCopy, HintsActionInsert, HintsActionOpen, HintsActionReveal, HintsActionPick},
				Default:     HintsActionCopy,
				Description: "Action performed when activating a lowercase hint label (copy, insert, open, reveal, or pick)",
			}},
		},
		{ID: CmdHintsKey, Description: "Process a key in terminal hint mode", Params: []core.ParamSpec{{Name: "key", Type: core.ParamString}, {Name: "text", Type: core.ParamString}}, Meta: map[string]any{"internal": true}},
		{ID: CmdHintsCancel, Description: "Cancel terminal hint mode", Meta: map[string]any{"internal": true}},
		{ID: CmdHintsActionCycle, Description: "Cycle the active hint action (copy, insert, open, reveal)", Meta: map[string]any{"internal": true}},
		{
			ID:          CmdHintsActionSet,
			Description: "Set the active hint action for lowercase labels",
			Params: []core.ParamSpec{{
				Name:        "action",
				Type:        core.ParamString,
				Enum:        []string{HintsActionCopy, HintsActionInsert, HintsActionOpen, HintsActionReveal, HintsActionPick},
				Required:    true,
				Description: "Target hint action (copy, insert, open, reveal, or pick)",
			}},
			Meta: map[string]any{"internal": true},
		},
		spec(CmdPaneCompose, "Open the multi-line prompt composer for the selected pane"),
		{ID: CmdComposerInsert, Description: "Insert text into the prompt composer", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdComposerBackspace, Description: "Erase before cursor in the prompt composer", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerEnter, Description: "Insert a newline or accept a completion in the prompt composer", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerSubmit, Description: "Deliver the composed prompt using the active delivery mode", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerCancel, Description: "Close the prompt composer and save a draft", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerModeCycle, Description: "Cycle prompt delivery mode (send, queue, interrupt)", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerComplete, Description: "Accept completion or cycle delivery mode in the prompt composer", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerUp, Description: "Move up in the prompt composer or completion list", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerDown, Description: "Move down in the prompt composer or completion list", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerDraftPrev, Description: "Load older draft or move up in completions", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerDraftNext, Description: "Load newer draft or move down in completions", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerAttachClipboard, Description: "Attach clipboard image/file or paste clipboard text into the composer", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerAttachFile, Description: "Pick and attach a local file to the composer", Meta: map[string]any{"internal": true}},
		{ID: CmdComposerAttachRemove, Description: "Remove the last attachment chip from the composer", Meta: map[string]any{"internal": true}},
		spec(CmdQuestionNext, "Move to the next question on the Questions page"),
		spec(CmdQuestionPrev, "Move to the previous question on the Questions page"),
		spec(CmdQuestionSubmitAll, "Submit all staged answers for the active question batch", "editing"),
		{ID: CmdAnswerBatchNext, Description: "Stage current answer and move to the next question in the batch", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},
		{ID: CmdAnswerBatchPrev, Description: "Stage current answer and move to the previous question in the batch", Tags: []string{"editing"}, Meta: map[string]any{"internal": true}},

		{ID: CmdConfirmAccept, Description: "Confirm the active confirmation dialog", Meta: map[string]any{"internal": true}},
		{ID: CmdConfirmCancel, Description: "Cancel the active confirmation dialog", Meta: map[string]any{"internal": true}},
		spec(CmdTablePresetPick, "Pick, save, set default, or delete saved view presets for the focused table"),
		{ID: CmdTablePresetSave, Description: "Save the focused table's filter, sort, and group as a named preset",
			Params: []core.ParamSpec{
				{Name: "name", Type: core.ParamString, Required: true, Description: "preset name"},
				{Name: "default", Type: core.ParamBool, Default: false, Description: "make default for this table"},
				{Name: "table", Type: core.ParamString, Description: "target table ID (defaults to focused table)"},
			}},
		{ID: CmdTablePresetApply, Description: "Apply a saved preset to the focused table",
			Params: []core.ParamSpec{
				{Name: "name", Type: core.ParamString, Required: true, Description: "preset name"},
				{Name: "table", Type: core.ParamString, Description: "target table ID (defaults to focused table)"},
			}},
		{ID: CmdTablePresetDefault, Description: "Set (or clear) the default preset for the focused table",
			Params: []core.ParamSpec{
				{Name: "name", Type: core.ParamString, Description: "preset name (empty clears default)"},
				{Name: "table", Type: core.ParamString, Description: "target table ID (defaults to focused table)"},
			}},
		{ID: CmdTablePresetDelete, Description: "Delete a saved preset from the focused table",
			Params: []core.ParamSpec{
				{Name: "name", Type: core.ParamString, Required: true, Description: "preset name"},
				{Name: "table", Type: core.ParamString, Description: "target table ID (defaults to focused table)"},
			}},
		{ID: CmdPresetPickerSave, Description: "Save the current table view as a preset from the presets picker", Meta: map[string]any{"internal": true}},
		{ID: CmdPresetPickerDefault, Description: "Set the highlighted preset as default for this table", Meta: map[string]any{"internal": true}},
		{ID: CmdPresetPickerDelete, Description: "Delete the highlighted preset from this table", Meta: map[string]any{"internal": true}},
		spec(CmdFocusHints, "Show focus-jump hint labels over visible focusable regions"),
		spec(CmdAppAbout, "Show build versions, daemon status, live pane PIDs, storage paths, extensions, and harnesses"),
		recordSpec(CmdPaneAssociate, "Associate the selected pane with a task/project and backfill its records", []string{data.KindPane}, "", "editing"),
		recordSpec(CmdCommandRun, "Run the suggested command in 3 Runs and record status and lastRun", []string{data.KindCommand}, "", "editing"),
		recordSpec(CmdCommandEditRun, "Edit the suggested command, arguments, or working directory before running it", []string{data.KindCommand}, "", "editing"),
		recordSpec(CmdExtSetup, "Run setup for the focused extension if it advertises a setup command", []string{data.KindExtension}, "", "editing"),
		spec(CmdTaskAddSibling, "Add a sibling task beside the focused task", "editing"),
		spec(CmdTaskAddChild, "Add a child subtask under the focused task", "editing"),
		spec(CmdTaskFoldToggle, "Fold or unfold the focused task subtree"),
		recordSpec(CmdTaskAgentToggle, "Toggle whether the focused task is queued for agent pickup", []string{data.KindTask}, "", "editing"),
		{
			ID:          CmdTaskAgentStatus,
			Description: "Set or cycle the focused task's agent workflow status (none, queued, working, needs_you, done)",
			Tags:        []string{"editing"},
			Params: []core.ParamSpec{{
				Name:        "status",
				Type:        core.ParamString,
				Enum:        []string{data.AgentStatusNone, data.AgentStatusQueued, data.AgentStatusWorking, data.AgentStatusNeedsYou, data.AgentStatusDone},
				Description: "Target agentStatus (empty cycles to the next state)",
			}},
			Meta: map[string]any{"record": true, "kinds": []string{data.KindTask}},
		},
		recordSpec(CmdTaskThreadToggle, "Expand or collapse the focused task's inline note thread", []string{data.KindTask, data.KindNote}, ""),
		recordSpec(CmdTaskNoteReply, "Compose and append a rich note reply to the focused task's thread", []string{data.KindTask, data.KindNote}, "", "editing"),
		{
			ID:          CmdTaskQueueDispatch,
			Description: "Dispatch queued agent tasks for the focused project/subtree (or all queued tasks) to an agent pane",
			Params:      []core.ParamSpec{{Name: "pane", Type: core.ParamString, Description: "Optional target agent pane ID or title"}},
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindTask}},
		},
		spec(tree.CmdFoldToggle, "Fold or unfold the focused tree subtree"),
		spec(tree.CmdFoldOpen, "Unfold the focused tree subtree"),
		spec(tree.CmdFoldClose, "Fold the focused tree subtree"),
		spec(tree.CmdFoldOpenAll, "Unfold every node in the focused tree"),
		spec(tree.CmdFoldCloseAll, "Fold every parent node in the focused tree"),
		spec(tree.CmdFoldOrStepIn, "Unfold or step into the focused tree node (spills right at leaf)"),
		spec(tree.CmdFoldOrParent, "Fold or jump to parent in the focused tree (spills left at root)"),
		{
			ID:          tree.CmdHoistIn,
			Description: "Zoom into the focused tree node subtree",
			Params:      []core.ParamSpec{{Name: "id", Type: core.ParamString, Description: "Optional node ID to hoist (default: focused node)"}},
		},
		spec(tree.CmdHoistOut, "Zoom out one level from the hoisted subtree"),
		spec(tree.CmdHoistClear, "Clear all hoisting and return to the full tree"),
		spec(CmdWorkLayoutToggle, "Toggle 2 Work between tree and table layout for hierarchical nouns"),
	}...), append(append(append(append(append(append(append(append(append(append(sessionRecorderSpecs(), uxNotesSpecs()...), pageModeSpecs()...), fdSearchSpecs()...), paneSearchSpecs()...), projectHomeSpecs()...), projectTabsSpecs()...), featuresPageSpecs()...), composerContextSpecs()...), insertPickerSpecs()...), clipboardAttachSpecs()...)...)
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

// readlineSpecs exposes Ochazuke's Readline input actions in the registry and
// inspector while leaving them out of the normal-mode command palette.
func readlineSpecs() []core.CommandSpec {
	specs := input.ReadlineSpecs()
	for i := range specs {
		specs[i].Meta = map[string]any{"internal": true}
	}
	return specs
}

func paletteSpecs() []core.CommandSpec {
	specs := palette.CommandSpecs()
	for i := range specs {
		specs[i].Meta = map[string]any{"internal": true}
	}
	return specs
}

// internal reports whether a command only makes sense from its own key (typing
// into a filter, a picker's confirm), so the palette leaves it out.
func internal(spec core.CommandSpec) bool {
	on, _ := spec.Meta["internal"].(bool)
	return on || strings.HasPrefix(spec.ID, "motion.") || (strings.HasPrefix(spec.ID, "focus.") && spec.ID != CmdFocusHints) ||
		slices.Contains([]string{core.CmdModeSet, core.CmdModalOpen, core.CmdModalClose, core.CmdLayerPush, core.CmdLayerPop, core.CmdCountPush, core.CmdPageOpen}, spec.ID)
}
