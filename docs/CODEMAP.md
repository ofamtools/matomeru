# Code map

Generated from Go ASTs with the ochazuke cmd/codemap adapter retained in the N1 lane artifacts. Do not edit by hand.

Go source files are grouped by package. Each entry lists what the file declares and the sibling files whose identifiers it uses most ("leans on"), so an agent can open two or three files instead of grepping.

## Files

## Package cmd/mtm

Go package `cmd/mtm`: 136 source files, 199 test files.

### cmd/mtm/about.go

- lines: 114 · tests: about_test.go
- purpose: defineAbout adds mtm's sections to ochazuke's About page (ochazuke.about.open, about.show, about.copy): where mtm keeps its data and config, which daemon it ...
- methods: App(4)
- leans on: main.go(6), version.go(4), extensions.go(3), notices.go(2)

### cmd/mtm/action_item_store.go

- lines: 293 · tests: action_item_store_test.go
- purpose: actionItemProps are an item's props: the key; what it is (title, the label, shown by its summary when empty; why, text, url, kind), written by add, create an...
- methods: App(7)
- leans on: main.go(8), notices.go(6), task_store.go(4), inbox.go(1)

### cmd/mtm/actor.go

- lines: 282 · tests: actor_test.go
- purpose: agentSettings are the agents.* settings.
- methods: App(10)
- leans on: main.go(11), notices.go(9), panes.go(3), owner.go(2)

### cmd/mtm/agent_declarations.go

- lines: 109
- purpose: A transport's actor becomes Runtime.Actor only while the request executes.
- methods: App(3)
- leans on: main.go(6), notices.go(2), context.go(1), journal.go(1)

### cmd/mtm/agent_verbs.go

- lines: 197
- purpose: Evidence parameters enrich the standard task set; aliases are data presets.
- methods: App(6)
- leans on: main.go(15), notices.go(5), panes.go(2), context.go(1)

### cmd/mtm/capture.go

- lines: 219 · tests: capture_test.go
- purpose: defineCapture adds the capture verbs to the history noun.
- methods: App(4)
- leans on: main.go(5), notices.go(3)

### cmd/mtm/claude_session.go

- lines: 224 · tests: claude_session_test.go
- purpose: claudeLookup is the lookup of p's current transcript against the listing (the other panes) and the recorded sessions.
- methods: App(1), agentStarts(2)
- leans on: panes.go(8), notices.go(5), main.go(2), turns_model.go(1)

### cmd/mtm/cli.go

- lines: 115 · tests: cli_test.go
- purpose: shell is the CLI over this app's runtime.
- methods: App(3)
- leans on: main.go(5), launch.go(3), agent_declarations.go(2), notices.go(1)

### cmd/mtm/clipboard.go

- lines: 95
- purpose: clipboardContent is what the clipboard held, by precedence: files (Finder's copy puts their URLs and an icon bitmap; the files win, as omp's paste does), els...
- methods: macClipboard(1)
- leans on: main.go(2)

### cmd/mtm/colors.go

- lines: 91 · tests: colors_test.go
- purpose: pinPalette is what one pin state draws: a theme in place of the base ("" keeps the base) and an accent (nil keeps the theme's).
- methods: App(2)
- leans on: main.go(3)

### cmd/mtm/config_reload.go

- lines: 83 · tests: config_reload_test.go
- purpose: defineConfigReload hooks mtm's files into config.reload.
- methods: App(4)
- leans on: main.go(5), notices.go(2), sides_config.go(1)

### cmd/mtm/context.go

- lines: 549 · tests: context_test.go
- purpose: contextState is the Context page's own state.
- methods: App(23), osSystem(3)
- leans on: main.go(25), notices.go(13), panes.go(9), launch.go(8)

### cmd/mtm/context_extract.go

- lines: 140
- purpose: contextDaemon is the part of the daemon extraction reads: a pane's scrollback as plain text and its shell's command history.
- methods: App(2), tuios(2)
- leans on: main.go(7), panes.go(7), context.go(4), context_origins.go(2)

### cmd/mtm/context_follow.go

- lines: 268 · tests: context_follow_test.go
- purpose: contextOutput is a wake-up: some pane wrote output.
- methods: App(3), contextFollower(6)
- leans on: notices.go(12), main.go(4), context.go(3), panes.go(2)

### cmd/mtm/context_origins.go

- lines: 46 · tests: context_origins_test.go
- purpose: contextExtraction is an immutable setting snapshot.
- methods: App(2)
- leans on: main.go(3), notices.go(2)

### cmd/mtm/context_picker.go

- lines: 362 · tests: context_picker_test.go
- purpose: pickedPane is the pane a live terminal instance streams: the attached pane, or the side pane shown beside it.
- methods: App(14)
- leans on: main.go(20), panes.go(14), notices.go(10), context.go(9)

### cmd/mtm/context_store.go

- lines: 335 · tests: context_store_test.go
- purpose: contextProps are a context item's props: the key; its kind and text, written by create only (an item is what was seen; the text is its label); the project th...
- methods: App(10)
- leans on: notices.go(12), main.go(11), context.go(8), context_picker.go(8)

### cmd/mtm/context_turns.go

- lines: 249 · tests: context_turns_test.go
- purpose: turnContext extracts context items from the turns of journaled entries and returns how many were new.
- methods: App(3)
- leans on: notices.go(11), main.go(6), context_origins.go(3), context.go(2)

### cmd/mtm/convert.go

- lines: 244
- purpose: defineConversions adds the conversion verbs; the outline's keys are bound in defineOutline.
- methods: App(11)
- leans on: main.go(16), outline.go(16), notices.go(3), pin.go(1)

### cmd/mtm/copybuf.go

- lines: 608
- purpose: copyBuf is copy mode's read-only buffer: plain lines (the pane's scrollback, then its screen), a cursor, the view's top line and the visual anchor.
- methods: copyBuf(24)
- leans on: main.go(4)

### cmd/mtm/copymode.go

- lines: 698 · tests: copymode_test.go
- purpose: copyMode is the copy instance's component and the panes page's copy state: the buffer, the pane it reads and where its screen comes from.
- methods: App(17), copyMode(8)
- leans on: main.go(27), panes.go(27), notices.go(25), copybuf.go(11)

### cmd/mtm/delete.go

- lines: 143
- purpose: project.delete and task.delete are the standard delete over their stores (Delete is removeProject and removeTask, which orphan what the record held); the sto...
- methods: App(5)
- leans on: main.go(6), notices.go(6)

### cmd/mtm/detail_links.go

- lines: 413
- purpose: linkTarget is a link row's data: what it opens.
- methods: App(19), linkedDetail(1)
- leans on: main.go(26), notices.go(13), copybuf.go(8), directories.go(7)

### cmd/mtm/details.go

- lines: 144 · tests: details_test.go
- purpose: pageDetails are the pages' details.
- methods: App(1), pageDetail(5)
- leans on: main.go(2), notices.go(2), detail_links.go(1)

### cmd/mtm/directories.go

- lines: 1406 · tests: directories_test.go
- purpose: dirsConfigPath is dirs.json in the app's config directory.
- methods: App(46)
- leans on: notices.go(61), main.go(57), panes.go(13), editor.go(8)

### cmd/mtm/directories_detail.go

- lines: 175 · tests: directories_detail_test.go
- purpose: directorySubject is the detail's directory (a directoryKey), its text and its links.
- methods: App(4)
- leans on: notices.go(12), detail_links.go(10), main.go(9), directories.go(3)

### cmd/mtm/editor.go

- lines: 536
- purpose: editRecord is what is being edited: the record's noun, id and owner.
- methods: App(20), recordEditor(2)
- leans on: main.go(32), notices.go(7), notes.go(2), directories.go(1)

### cmd/mtm/extension_autorefresh.go

- lines: 70 · tests: extension_autorefresh_test.go
- purpose: refreshStaleExtensions checks the catalogs off the loop at startup and when an extension tab opens.
- methods: App(2)
- leans on: extension_discover.go(4), main.go(4), notices.go(2), extensions.go(1)

### cmd/mtm/extension_context.go

- lines: 134
- purpose: registerDetailKinds registers the detail kinds mtm draws.
- methods: App(4), extension(1)
- leans on: notices.go(6), main.go(5), extensions.go(3), launch.go(2)

### cmd/mtm/extension_discover.go

- lines: 513 · tests: extension_discover_test.go
- purpose: extCandidate is one row of discovery: a tool on PATH, a registered extension, or both.
- methods: App(2), extCandidate(1), extDiscovery(6), extProbe(1), extProbePlan(1), extRegistry(2)
- leans on: notices.go(27), saved_detail.go(12), extensions.go(10), main.go(6)

### cmd/mtm/extension_forms.go

- lines: 396 · tests: extension_forms_test.go
- purpose: extCRUD names a catalog noun's verbs for the key map ("" when it has none).
- methods: App(8), extForm(2), extension(1)
- leans on: main.go(19), notices.go(18), extensions.go(4), launch.go(4)

### cmd/mtm/extension_names.go

- lines: 220 · tests: extension_names_test.go
- purpose: extNounNaming is one way of naming an extension's nouns.
- methods: App(7), extension(1)
- leans on: notices.go(13), main.go(9), extensions.go(3), owner.go(1)

### cmd/mtm/extension_page.go

- lines: 530 · tests: extension_page_test.go
- purpose: extListColumns are the table's columns: extColumns, with the setup column after status when on.
- methods: App(18)
- leans on: extension_discover.go(24), notices.go(23), main.go(19), extension_setup.go(6)

### cmd/mtm/extension_scope.go

- lines: 634 · tests: extension_scope_test.go
- purpose: extScopeSetting names an extension's scope setting, in the extension's section of the Settings page's Extensions tab.
- methods: App(23), extension(3)
- leans on: notices.go(29), main.go(24), extensions.go(12), directories.go(4)

### cmd/mtm/extension_segment_reload.go

- lines: 71 · tests: extension_segment_reload_test.go
- purpose: extSegmentOf is the extension a segment id belongs to, or nil.
- methods: App(4)
- leans on: main.go(5), notices.go(4), extensions.go(3), owner.go(1)

### cmd/mtm/extension_setup.go

- lines: 626 · tests: extension_setup_test.go
- purpose: extSetupInfo is one extension's setup, as extension.setup-status answers it.
- methods: App(16)
- leans on: notices.go(28), main.go(17), extension_discover.go(7), extension_page.go(7)

### cmd/mtm/extension_sources.go

- lines: 182 · tests: extension_sources_test.go
- purpose: extSource is one source of an extension and the parameters it fills.
- methods: App(3)
- leans on: notices.go(15), extensions.go(4), main.go(4), extension_forms.go(2)

### cmd/mtm/extension_sync.go

- lines: 125 · tests: extension_sync_test.go
- purpose: entryJSON is a registry entry as compared between syncs.
- methods: App(5)
- leans on: notices.go(9), main.go(6), extensions.go(3)

### cmd/mtm/extensions.go

- lines: 1092 · tests: extensions_test.go
- purpose: extEntry is one registry file.
- methods: App(22), extRegistry(6), extension(1)
- leans on: notices.go(54), main.go(24), launch.go(12), saved_detail.go(9)

### cmd/mtm/hint_overlay.go

- lines: 108
- purpose: overlayText draws o over the first shown lines of text (each padded or cut to width cells): those lines dimmed with fade (faint when fade draws nothing), the...
- leans on: main.go(1), notices.go(1)

### cmd/mtm/inbox.go

- lines: 845 · tests: inbox_test.go
- purpose: inboxState is the page's state: the store, its list and detail, the opener and the counts the title row shows.
- methods: App(31)
- leans on: main.go(36), notices.go(20), panes.go(5), saved_detail.go(5)

### cmd/mtm/inbox_notify.go

- lines: 196 · tests: inbox_notify_test.go
- purpose: inboxNotifySince uses the existing app-state row, preserving its other fields.
- methods: App(6)
- leans on: notices.go(8), main.go(7), inbox.go(2), notify.go(1)

### cmd/mtm/journal.go

- lines: 336 · tests: journal_test.go
- purpose: journalSource is the part of the daemon the reader needs; tests replace it.
- methods: App(9), journalFeed(3)
- leans on: main.go(10), notices.go(9), panes.go(6), panes_events.go(3)

### cmd/mtm/keys.go

- lines: 83 · tests: keys_test.go
- purpose: defineKeyMap sets mtm's outline keys and palette quit behavior.
- methods: App(4)
- leans on: main.go(5), notices.go(2)

### cmd/mtm/launch.go

- lines: 681 · tests: launch_test.go
- purpose: sessionFor is the session a project's agents launch into: the project's name as a lowercase slug ("first project" → "first-project"), else its id.
- methods: App(24), tuios(3)
- leans on: main.go(31), notices.go(20), panes.go(6), version.go(4)

### cmd/mtm/launch_owner.go

- lines: 172
- purpose: recordingDriver persists a pane before the harness starts waiting for readiness.
- methods: App(2), creationRecorder(1), recordingDriver(2)
- leans on: launch.go(5), panes.go(4), main.go(3), owner.go(3)

### cmd/mtm/live_hints.go

- lines: 625 · tests: live_hints_test.go
- purpose: hintScreen is one labelled screen: a live terminal, or the preview of a pane row (term nil), and the pane a capital types into.
- methods: App(19), hintScreen(1), hintText(1)
- leans on: main.go(25), panes.go(22), notices.go(12), launch.go(4)

### cmd/mtm/livescreen.go

- lines: 111 · tests: livescreen_test.go
- purpose: liveScreen replays a pane's stream-pane frames into a terminal emulator, as gmtm's live session does: the snapshot paints a fresh screen, output frames conti...
- methods: liveScreen(4), liveStream(1)
- leans on: notices.go(7), main.go(1), panes.go(1)

### cmd/mtm/main.go

- lines: 911 · tests: main_test.go
- purpose: Options are the command line: where the database is and which daemon to talk to.
- types: Options, App
- methods: App(7), Options(7)
- funcs: DefaultData, New
- leans on: notices.go(17), panes.go(13), editor.go(5), journal.go(4)

### cmd/mtm/mcp.go

- lines: 204 · tests: mcp_test.go
- purpose: catalogMCP binds the pane at startup and keeps one headless runtime so history and exact undo survive tool calls.
- methods: App(4)
- leans on: main.go(7), notices.go(7), actor.go(1), journal.go(1)

### cmd/mtm/mcp_ops.go

- lines: 160
- purpose: paneArgs are --session and --window, else the pane this runs in.
- methods: App(4)
- leans on: main.go(10), context.go(3), owner.go(2), context_extract.go(1)

### cmd/mtm/note_store.go

- lines: 313 · tests: note_store_test.go
- purpose: noteProps are the note's props: the key; the optional title (the label; empty, the summary stands in, owner decision 23); the read-only summary (the body's f...
- methods: App(8)
- leans on: main.go(9), notices.go(9), notes.go(2), task_store.go(2)

### cmd/mtm/notes.go

- lines: 501 · tests: notes_test.go
- purpose: noteOwnerCells are a note's project and task as table cells.
- methods: App(18)
- leans on: main.go(26), notices.go(14), editor.go(5), note_store.go(4)

### cmd/mtm/notes_doc.go

- lines: 143 · tests: notes_doc_test.go
- purpose: noteSource is the Doc's source: the text of note id in the store.
- methods: App(7)
- leans on: main.go(9), notes.go(2), notices.go(1)

### cmd/mtm/notices.go

- lines: 124 · tests: notices_test.go
- purpose: noticeTimer is the expiry of the shown notices.
- methods: App(3), noticeSink(5)
- leans on: main.go(4)

### cmd/mtm/notify.go

- lines: 145
- purpose: notificationSender snapshots all runtime-owned settings and endpoints on the loop.
- methods: App(4)
- leans on: main.go(8), notices.go(3)

### cmd/mtm/oden.go

- lines: 82 · tests: oden_test.go
- purpose: commands is the oden App for one invocation; tui runs the TUI.
- methods: App(1), familyFS(2)
- leans on: main.go(4), notices.go(1)

### cmd/mtm/outline.go

- lines: 987 · tests: outline_test.go
- purpose: outlineScope is what an open outline shows: one project's tasks ("" is the unassociated tasks) or, all, every project as a branch.
- methods: App(33)
- leans on: main.go(42), notices.go(31), editor.go(5), actor.go(3)

### cmd/mtm/outline_attach.go

- lines: 197 · tests: outline_attach_test.go
- methods: App(4), outlineState(2)
- leans on: main.go(10), notices.go(10), prompt_attach.go(10), outline.go(8)

### cmd/mtm/owner.go

- lines: 111
- purpose: Owner is captured at dispatch, never recomputed when an editor or job finishes.
- types: Owner
- methods: App(2)
- leans on: main.go(4), notices.go(4), editor.go(1), panes.go(1)

### cmd/mtm/owners.go

- lines: 79
- purpose: associateTaskOne is task.associate's move form on one task, its owner given: the standard task.move to the owner's project (a task names its project; a task ...
- methods: App(4)
- leans on: main.go(5), notices.go(2), panes.go(2), task_store.go(2)

### cmd/mtm/pane_activity.go

- lines: 368 · tests: pane_activity_test.go
- purpose: paneActivities is what was read of one pane's activities.
- methods: App(9)
- leans on: notices.go(12), main.go(11), panes.go(6), turn_status.go(4)

### cmd/mtm/pane_border.go

- lines: 49 · tests: pane_border_test.go
- purpose: paneBorderTone is the tone panes.status-border gives p's panel border.
- methods: App(2)
- leans on: turn_status.go(6), main.go(3), panes.go(2), panes_tree.go(1)

### cmd/mtm/pane_locate.go

- lines: 46 · tests: pane_locate_test.go
- purpose: locatePanes corrects panes' sessions through l (nil: as listed).
- leans on: panes.go(2), main.go(1), notices.go(1)

### cmd/mtm/pane_picker.go

- lines: 200 · tests: pane_picker_test.go
- purpose: paneVisits stamps each pane row id with the order of its last attach.
- methods: App(6), paneVisits(1)
- leans on: panes.go(17), main.go(7), notices.go(4), panes_tree.go(3)

### cmd/mtm/pane_settings.go

- lines: 876 · tests: pane_settings_test.go
- purpose: settingField is one field of the line.
- methods: App(24), tuios(1)
- leans on: notices.go(32), main.go(29), panes.go(21), launch.go(5)

### cmd/mtm/pane_signals.go

- lines: 276
- purpose: paneSignalState is what the last reconcile was built from; a listing that matches it writes nothing.
- methods: App(7), paneSignalState(2)
- leans on: notices.go(18), main.go(8), panes.go(7), inbox.go(2)

### cmd/mtm/pane_stars.go

- lines: 148 · tests: pane_stars_test.go
- purpose: starAlias is NOUN.star: the standard set of starred, toggled; a caller's --value sets it instead (ochazuke v0.55: the toggle preset yields to a value, Parame...
- methods: App(6)
- leans on: main.go(8), notices.go(3), panekeys.go(1), panes.go(1)

### cmd/mtm/pane_store.go

- lines: 355 · tests: pane_store_test.go
- purpose: paneProps are a pane's props: the key (the window id); its name, the label (the display name shown when it is empty); its owner (project, move writes it) and...
- methods: App(14)
- leans on: main.go(19), panes.go(11), notices.go(9), panekeys.go(2)

### cmd/mtm/pane_task.go

- lines: 358 · tests: pane_task_test.go
- purpose: paneRef is a pane named by its row id (pane:session/window) or its window id: the loaded pane when there is one, else the window (and the session the row id ...
- methods: App(9)
- leans on: main.go(11), notices.go(6), panes.go(5), tasks.go(3)

### cmd/mtm/pane_view.go

- lines: 204 · tests: pane_view_test.go
- purpose: paneViewOf is the view the pane opens on and its split, each from the pane's own, else its harness profile's, else the settings.
- methods: App(7)
- leans on: main.go(8), panes.go(4), notices.go(3), panes_detail.go(3)

### cmd/mtm/panekeys.go

- lines: 264 · tests: panekeys_test.go
- purpose: paneKeyVerdict is what becomes of one key on its way to send-keys.
- methods: App(1)
- leans on: notices.go(7), main.go(2), panes.go(1)

### cmd/mtm/panes.go

- lines: 1634 · tests: panes_test.go
- purpose: paneCells is a pane row's cells in paneColumns order; home shortens the cwd to ~.
- methods: App(26), explained(1), liveStream(14), pane(2), tuios(11)
- leans on: main.go(32), notices.go(26), launch.go(11), panes_detail.go(8)

### cmd/mtm/panes_detail.go

- lines: 1448
- purpose: treeWidth is the tree's share of a side-by-side page: about 30%, never under 32 cells, so a pane's glyph, name and short state fit.
- methods: App(42), pane(3)
- leans on: panes.go(50), main.go(46), notices.go(42), keys.go(11)

### cmd/mtm/panes_events.go

- lines: 273 · tests: panes_events_test.go
- purpose: paneEventInbox keeps the latest payload per event kind/window until the loop takes it.
- methods: App(7), paneEventInbox(2)
- leans on: notices.go(9), main.go(8), panes.go(8), panes_tree.go(4)

### cmd/mtm/panes_group.go

- lines: 101 · tests: panes_group_test.go
- purpose: paneGrouping is the panes.group setting, the default when unset.
- methods: App(5)
- leans on: main.go(6)

### cmd/mtm/panes_leader.go

- lines: 109 · tests: panes_leader_test.go
- purpose: bindPageLeader binds the page's leader rows to the current leader and drops the rows of the one they were bound to before; it observes the terminal-leader se...
- methods: App(4)
- leans on: main.go(5), notices.go(2), panes.go(2)

### cmd/mtm/panes_panels.go

- lines: 411 · tests: panes_panels_test.go
- purpose: panelText is a panel's title and the line an empty one shows, by section.
- methods: App(12), pane(4), stateWords(3)
- leans on: main.go(13), launch.go(12), panes_sections.go(8), notices.go(7)

### cmd/mtm/panes_render_cache.go

- lines: 56 · tests: panes_render_cache_test.go
- purpose: Each visible panel keeps one exact content/geometry/style snapshot.
- methods: App(2)
- leans on: main.go(3), panes_sections.go(1)

### cmd/mtm/panes_sections.go

- lines: 870 · tests: panes_sections_test.go
- purpose: paneSectionSettings are the sections' settings.
- methods: App(21), paneSection(1), paneView(1)
- leans on: notices.go(32), panes.go(27), main.go(22), panes_tree.go(8)

### cmd/mtm/panes_tree.go

- lines: 466
- purpose: isAgent is the old app's rule: a pane with a harness, or one whose agent state is anything but none, is an agent's pane.
- methods: App(1), pane(6), paneGroup(1), paneProject(1), paneView(1)
- leans on: panes.go(21), notices.go(14), panes_group.go(4), main.go(2)

### cmd/mtm/path_sources.go

- lines: 257 · tests: path_sources_test.go
- purpose: dirParam is a directory parameter: a folder through the path chooser; isNew lets it name a folder not there yet (attached and flagged missing).
- methods: App(8)
- leans on: main.go(10), notices.go(9), directories.go(2), panes.go(1)

### cmd/mtm/pin.go

- lines: 334 · tests: pin_test.go
- purpose: openPinPicker opens the pin chooser: projects most recently pinned first, then by name, each with its directory; "no pin (unpin)" fixed at the bottom, which ...
- methods: App(14)
- leans on: main.go(17), notices.go(8), panes.go(4), directories.go(1)

### cmd/mtm/privacy.go

- lines: 29
- purpose: Privacy verbs on both nouns (the tasks plan, Owner decision 6): a project is private until it is marked family, and a task's private flag keeps it and its su...
- methods: App(1)
- leans on: main.go(2), notices.go(1)

### cmd/mtm/project.go

- lines: 361
- purpose: editProject is project.edit: without arguments it opens the editor on the project; with name, color, glyph, harness or preset it is the standard set of those...
- methods: App(20)
- leans on: main.go(25), notices.go(11), directories.go(7), editor.go(7)

### cmd/mtm/project_store.go

- lines: 244 · tests: project_store_test.go
- purpose: projectProps are the project's props: the key; the name (the label; an empty one shows the default directory); the theme's colour and glyph; the launch defau...
- methods: App(8)
- leans on: main.go(11), notices.go(9), task_store.go(8), directories.go(2)

### cmd/mtm/prompt.go

- lines: 90
- purpose: question is the open prompt's commit.
- methods: App(4)
- leans on: main.go(8), version.go(5), notices.go(3)

### cmd/mtm/prompt_attach.go

- lines: 399 · tests: prompt_attach_test.go
- purpose: promptAttachment is one image represented by a chip in an input.
- methods: App(8), imageAttachments(3), promptAttachment(1), promptBox(1)
- leans on: main.go(11), notices.go(11), prompt_box.go(4), clipboard.go(3)

### cmd/mtm/prompt_box.go

- lines: 939 · tests: prompt_box_test.go
- purpose: promptBox is the box's state.
- methods: App(26), promptTarget(3), tuios(4)
- leans on: main.go(31), panes.go(25), notices.go(24), launch.go(14)

### cmd/mtm/prompt_drafts.go

- lines: 116
- purpose: promptDraft is one pane's unsent prompt: what its box held when the box last showed another pane, with the input's undo tree and the images attached to it.
- methods: App(4)
- leans on: main.go(5), panes.go(1), prompt_attach.go(1)

### cmd/mtm/quicknote.go

- lines: 244 · tests: quicknote_test.go
- purpose: quickNote is the open quick note: its id and owner, what was last written, and the typing state the debounce reads.
- methods: App(10)
- leans on: main.go(15), owner.go(6), editor.go(5), notices.go(3)

### cmd/mtm/relations.go

- lines: 125
- purpose: relOwner is the owner a reverse verb hands a relation: the target row as a task (its project and id), a pane (its window, session and the pane's own owner) o...
- methods: App(7)
- leans on: main.go(9), notices.go(5), context.go(2), inbox.go(2)

### cmd/mtm/reminder_notify.go

- lines: 221 · tests: reminder_notify_test.go
- purpose: notificationWork snapshots runtime-owned configuration on the loop.
- methods: App(7)
- leans on: main.go(9), notices.go(9), reminders.go(3), schedules.go(3)

### cmd/mtm/reminders.go

- lines: 415 · tests: reminders_test.go
- methods: App(15)
- leans on: main.go(20), notices.go(16), reminder_notify.go(3), editor.go(2)

### cmd/mtm/rename.go

- lines: 186 · tests: rename_test.go
- purpose: renamer renames sessions and windows on the daemon; tests replace it.
- methods: App(9)
- leans on: main.go(12), notices.go(6), panes.go(5), editor.go(2)

### cmd/mtm/review_store.go

- lines: 167 · tests: review_store_test.go
- purpose: reviewProps are a review's props: the key; the pane (the label) and its owner, harness and session, read-only; the status and the answer; and the read-only c...
- methods: App(3)
- leans on: notices.go(7), reviews.go(6), main.go(4), panes.go(3)

### cmd/mtm/reviews.go

- lines: 719 · tests: reviews_test.go
- purpose: defineReviews adds the review noun and the answer area's noun.
- methods: App(28), paneReview(3)
- leans on: main.go(32), notices.go(24), launch.go(5), editor.go(4)

### cmd/mtm/runs.go

- lines: 502 · tests: runs_test.go
- purpose: commandRun is one exec run: what ran and what it said so far.
- methods: App(17), commandRun(1), runState(6)
- leans on: main.go(75), notices.go(12), launch.go(5), detail_links.go(2)

### cmd/mtm/saved.go

- lines: 333 · tests: saved_test.go
- purpose: Current TUIOS versions may omit launch argv entirely.
- methods: App(9)
- leans on: notices.go(15), main.go(14), launch.go(7), panes.go(3)

### cmd/mtm/saved_detail.go

- lines: 124 · tests: saved_detail_test.go
- purpose: savedSubject is the detail's saved pane (its id), text and links.
- methods: App(5)
- leans on: main.go(6), notices.go(5), detail_links.go(4), directories.go(2)

### cmd/mtm/saved_store.go

- lines: 276 · tests: saved_store_test.go
- purpose: savedProps are a saved record's props: the key; its name (the label); its project (move writes it) and task, both filled from the focus by create; the recipe...
- methods: App(6)
- leans on: notices.go(9), main.go(7), task_store.go(4), tasks.go(1)

### cmd/mtm/schedule_edit.go

- lines: 441
- purpose: scheduleParams are add's and edit's parameters (edit's name is the row's).
- methods: App(10)
- leans on: notices.go(16), main.go(13), schedules.go(4), editor.go(1)

### cmd/mtm/schedule_notify.go

- lines: 81
- purpose: Reconcile persisted schedule state, not lossy runner completion messages.
- leans on: notices.go(3), main.go(1)

### cmd/mtm/schedule_prompts.go

- lines: 210 · tests: schedule_prompts_test.go
- purpose: promptEnv is what a prompt run reads that lives on the UI loop: the harness registry and the default harness.
- methods: App(4)
- leans on: launch.go(7), main.go(7), notices.go(3), owner.go(1)

### cmd/mtm/schedule_store.go

- lines: 406 · tests: schedule_store_test.go
- purpose: scheduleProps are a schedule's props: the key; the fields edit writes (strings, as its parameters are; enabled one of on, off, auto); the interval; and the r...
- methods: App(12)
- leans on: notices.go(16), main.go(14), schedule_edit.go(10), schedules.go(8)

### cmd/mtm/schedules.go

- lines: 844 · tests: schedules_test.go
- purpose: scheduleState is the feature's state: the rows as configured, the runner, the page's list and detail, and the TUI's timers.
- methods: App(28), scheduleView(4)
- leans on: main.go(35), notices.go(16), detail_links.go(3), inbox.go(3)

### cmd/mtm/segments.go

- lines: 37 · tests: segments_test.go
- purpose: addSegments registers the system segments, on, the disk one reading the data directory.
- methods: App(2)
- leans on: main.go(4), extensions.go(1), owner.go(1), settings.go(1)

### cmd/mtm/session_store.go

- lines: 230 · tests: session_store_test.go
- purpose: sessionProps are a session's props: the key (the daemon's session id, else its name), the name (the label), the owning project, and how many panes it holds.
- methods: App(8)
- leans on: main.go(10), notices.go(10), panes.go(3), task_store.go(2)

### cmd/mtm/settings.go

- lines: 223 · tests: settings_test.go
- purpose: migrateSettingAliases rewrites deprecated names once through Settings.Set, which preserves unrelated settings, prefers a canonical value and removes its alia...
- methods: App(7)
- leans on: main.go(11), notices.go(5), inbox_notify.go(4), side_view.go(4)

### cmd/mtm/setup.go

- lines: 89 · tests: setup_test.go
- purpose: defineSetup adds the setup noun.
- methods: App(7)
- leans on: main.go(17), notices.go(2)

### cmd/mtm/side.go

- lines: 652 · tests: side_test.go
- purpose: sideDriver opens a side pane in its parent's context: the parent's session and workspace, the folder the parent is in now (sideCwd) and the environment the p...
- methods: App(10), sideDriver(1)
- leans on: panes.go(24), detail_links.go(21), notices.go(21), main.go(18)

### cmd/mtm/side_view.go

- lines: 405 · tests: side_view_test.go
- purpose: showSide changes only the secondary stream; the parent's lease stays live.
- methods: App(14), sideState(2)
- leans on: panes.go(18), main.go(16), notices.go(8), settings.go(2)

### cmd/mtm/sides_config.go

- lines: 368
- purpose: sidePatch follows harnesses.json: omitted fields keep the earlier row's values; explicit empty fields replace them (notably key: "").
- methods: App(15)
- leans on: main.go(17), notices.go(16), extensions.go(1), panes_detail.go(1)

### cmd/mtm/store_tables.go

- lines: 108 · tests: store_tables_test.go
- purpose: storeTables is store.tables and store-table.list, read off the loop.
- methods: App(3)
- leans on: main.go(5), notices.go(2), owner.go(1)

### cmd/mtm/story.go

- lines: 527
- purpose: storyShell runs `story ...` before mtm opens any database: the words that start with story run the host as an oden command named story, and mtm's own options...
- leans on: notices.go(15), main.go(13), storyfakes.go(6), panes.go(3)

### cmd/mtm/story_lanes.go

- lines: 59
- purpose: buildScopedLanes mounts a real extension page backed by a temp executable.
- leans on: extensions.go(2), main.go(2), story.go(1)

### cmd/mtm/story_notify.go

- lines: 31
- leans on: main.go(2), inbox_notify.go(1), notify.go(1), story.go(1)

### cmd/mtm/story_off.go

- lines: 10
- purpose: storyShell handles nothing in a normal build: only the tester (-tags monogatari, story.go) has mtm story, so the shipped binary links no monogatari and mtm s...
- leans on: main.go(1)

### cmd/mtm/story_reminders.go

- lines: 38
- purpose: Only the story fixture owns this clock control; no shipped verb changes time.
- leans on: notices.go(3), main.go(2), story.go(2)

### cmd/mtm/storyfakes.go

- lines: 193
- purpose: storyPanes is a story's TUIOS daemon: the fixture's panes, a screen per window, no real process behind any of them.
- methods: storyClipboard(1), storyLauncher(3), storyPanes(17), storyStream(10), storySystem(3)
- leans on: panes.go(21), notices.go(6), clipboard.go(2), context_turns.go(2)

### cmd/mtm/stream_reconnect.go

- lines: 180 · tests: stream_reconnect_test.go
- purpose: paneConn is one stream-pane connection (*daemon.PaneStream).
- methods: App(2), liveStream(6)
- leans on: panes.go(9), main.go(3), notices.go(1)

### cmd/mtm/task_notes.go

- lines: 64
- purpose: syncOutlineNotes pushes the selected task's notes into an ordinary note list.
- methods: App(2)
- leans on: outline.go(4), main.go(3), note_store.go(1), notices.go(1)

### cmd/mtm/task_store.go

- lines: 438 · tests: task_store_test.go
- purpose: taskProps are the task's props: the key; the title (the label); the status, one of work.Statuses (a legacy value is read as it is); the project that owns the...
- methods: App(9)
- leans on: notices.go(18), main.go(10)

### cmd/mtm/tasks.go

- lines: 488
- purpose: isTaskList reports whether id is the Work page's task tree or its list.
- methods: App(16)
- leans on: main.go(20), notices.go(16), actor.go(5), editor.go(4)

### cmd/mtm/turn_agents.go

- lines: 757 · tests: turn_agents_test.go
- purpose: subagents is the Turns tab's subagent state, the loop's own; logs is safe for the jobs that read off the loop.
- methods: App(14), paneUI(1), subagents(3)
- leans on: notices.go(25), main.go(19), turn_cards.go(15), panes.go(14)

### cmd/mtm/turn_cards.go

- lines: 893 · tests: turn_cards_test.go
- purpose: turnCards reports whether the Turns tab draws cards (turns.style).
- methods: App(26)
- leans on: main.go(36), notices.go(27), turns_model.go(16), panes_detail.go(10)

### cmd/mtm/turn_status.go

- lines: 197 · tests: turn_status_test.go
- purpose: turnStatus is how a card shows one status kind.
- methods: App(1)
- leans on: main.go(2), notices.go(2)

### cmd/mtm/turn_wrap.go

- lines: 273 · tests: turn_wrap_test.go
- purpose: turnFill is what a turns tree was last filled with, so a new width re-wraps it without reading the journal again.
- methods: App(5)
- leans on: turns.go(7), main.go(6), notices.go(6), launch.go(5)

### cmd/mtm/turns.go

- lines: 478
- purpose: addTurns adds the Turns tab's tree and the turn noun.
- methods: App(13)
- leans on: notices.go(18), main.go(14), launch.go(5), turns_model.go(5)

### cmd/mtm/turns_model.go

- lines: 1315 · tests: turns_model_test.go
- purpose: paneTurns is what mtm has read of one pane's turns.
- methods: App(29), paneCapturer(1), tuios(2), turnBody(4)
- leans on: notices.go(39), main.go(37), panes.go(28), turns.go(13)

### cmd/mtm/turns_refresh.go

- lines: 177 · tests: turns_refresh_test.go
- purpose: turnsRetryAfter is the wait after the tries-th miss (tries from 1).
- methods: App(6), resolutions(1)
- leans on: turns_model.go(13), main.go(9), panes.go(3)

### cmd/mtm/version.go

- lines: 51 · tests: version_test.go
- purpose: init lays the linker stamps over ochazuke's, so the docs viewer's header names the same build as --version.
- leans on: main.go(1), notices.go(1)

### cmd/mtm/vim.go

- lines: 469 · tests: vim_test.go
- purpose: vimRowsWith is the rows in scope, each session's state from state.
- methods: App(12)
- leans on: main.go(22), notices.go(11), launch.go(7), panes.go(6)

### cmd/mtm/vim_detail.go

- lines: 83 · tests: vim_detail_test.go
- purpose: vimSubject is the detail's session (its id), text and links.
- methods: App(4)
- leans on: main.go(5), detail_links.go(4), directories.go(3), notices.go(3)

### cmd/mtm/vim_store.go

- lines: 181 · tests: vim_store_test.go
- purpose: vimProps are a Neovim session's props: the key; its name (the label); its project (move writes it) and task; and the read-only rest.
- methods: App(5)
- leans on: main.go(6), notices.go(6), panes.go(1), side.go(1)

### cmd/mtm/workpage.go

- lines: 109 · tests: workpage_test.go
- purpose: renderWorkWide draws the wide Work page.
- methods: App(3)
- leans on: main.go(5), notices.go(4), panes.go(1), pin.go(1)

## Package internal/actionitems

Go package `internal/actionitems`: 2 source files, 3 test files.

### internal/actionitems/actionitems.go

- lines: 438 · tests: actionitems_test.go
- purpose: Item is one row.
- types: Item, Spec, Owner, Store, Counts
- methods: Item(5), Store(15)
- funcs: Open

### internal/actionitems/notify.go

- lines: 127 · tests: notify_test.go
- purpose: NotifyOptions is a snapshot shared by the headless verb and the TUI worker.
- types: NotifyOptions
- methods: NotifyOptions(1), Store(3)
- leans on: actionitems.go(18)

## Package internal/compose

Go package `internal/compose`: 4 source files, 1 test files.

### internal/compose/commands.go

- lines: 126
- purpose: Command is one slash command a harness takes, without its slash.
- types: Command
- methods: Sources(2)
- leans on: skills.go(28)

### internal/compose/files.go

- lines: 182
- purpose: Files lists dir's files and directories, relative to it.
- types: Match
- methods: Sources(1)
- funcs: Rank
- leans on: skills.go(3), commands.go(1)

### internal/compose/skills.go

- lines: 455
- purpose: Harness is compose's name for a tuios harness id ("claude-code" is Claude), "" for a harness it does not know.
- types: Skill, Env, Sources
- methods: Sources(4)
- funcs: Harness, DefaultEnv, NewSources, ParseOAISkills
- leans on: commands.go(3)

### internal/compose/syntax.go

- lines: 28
- purpose: SkillInvocation is how the harness's prompt names a skill, the text the prompt box inserts when a skill is accepted:    - omp: /skill:<name> (a namespaced sk...
- funcs: SkillInvocation
- leans on: skills.go(3)

## Package internal/contextitems

Go package `internal/contextitems`: 6 source files, 5 test files.

### internal/contextitems/extract.go

- lines: 386 · tests: extract_test.go
- purpose: Join strips terminal controls and joins soft-wrapped lines: a line that fills width (the pane's columns) exactly and ends mid-token continues on the next, so...
- types: StatFunc (func)
- methods: mline(4)
- funcs: Join, Extract, Target
- leans on: spans.go(10), store.go(9), wrap.go(2)

### internal/contextitems/origins.go

- lines: 54 · tests: origins_test.go
- purpose: Origin classifies stored provenance, never the item's text or kind.
- methods: Item(2), Source(1)
- leans on: store.go(4)

### internal/contextitems/osc8.go

- lines: 110
- purpose: stripLinks is ansi.Strip(row) and, per byte of it, the hyperlink the byte is drawn in ("" for none): the target and params, as one string.
- leans on: extract.go(5), spans.go(3)

### internal/contextitems/spans.go

- lines: 179 · tests: spans_test.go
- purpose: Cell is the run of screen cells a part of a span covers: one row, from column Col up to (not including) End.
- types: Cell, Span, SpanOptions
- funcs: ExtractSpans, CachedStat
- leans on: extract.go(14), store.go(10), osc8.go(3)

### internal/contextitems/store.go

- lines: 286 · tests: store_test.go
- purpose: Item is one row.
- types: Item, Source, Provenance, Owner, Store
- methods: Item(2), Store(8)
- funcs: Open

### internal/contextitems/wrap.go

- lines: 363 · tests: wrap_test.go
- purpose: rowShape is one screen row's layout: its left border (0 for none), where its text starts and ends (cells; end exclusive, trailing spaces and border left out)...
- methods: layout(1)
- leans on: extract.go(31)

## Package internal/daemon

Go package `internal/daemon`: 7 source files, 2 test files.

### internal/daemon/client.go

- lines: 383
- purpose: Client talks to one daemon socket.
- types: Client, Error, Presence
- methods: Client(6), Error(1), Presence(1), connection(1)
- funcs: New, SocketFor, ForServer
- leans on: event.go(5), verbs.go(5), hosts.go(3), server.go(1)

### internal/daemon/event.go

- lines: 28
- purpose: Event is one line from the daemon's subscribe stream, with the protocol's fixed fields lifted out and everything else kept in Data.
- types: Event
- leans on: verbs.go(1)

### internal/daemon/hosts.go

- lines: 82
- purpose: Qualify keeps local identities unchanged and remote identities collision-free.
- types: HostReport, HostReports
- funcs: WithHost, Host, Qualify, SplitIdentity

### internal/daemon/launch.go

- lines: 93
- purpose: LaunchWorkspace creates only a missing session, never a placeholder window.
- funcs: LaunchWorkspace
- leans on: hosts.go(2), client.go(1), verbs.go(1)

### internal/daemon/server.go

- lines: 129 · tests: server_test.go
- purpose: StartFunc starts the TUIOS server named name and returns at once; the channel receives the start command's exit (nil when it detached cleanly).
- types: StartFunc (func)
- methods: Client(4)
- funcs: StartServer
- leans on: client.go(4)

### internal/daemon/stream.go

- lines: 213
- types: StreamParams, StreamInfo, Frame, PaneStream
- methods: Client(1), PaneStream(6)
- leans on: client.go(17), verbs.go(4), hosts.go(3)

### internal/daemon/verbs.go

- lines: 409
- purpose: Hello is the daemon's handshake answer: its version, the protocol it speaks, its pid and how many sessions it holds.
- types: Session, Pane, Hello, StartAgentParams, StartAgentResult, NewWindowParams, NewWindowResult, CaptureParams, CaptureResult, SendTextParams, SendKeysParams, QueuePromptParams, QueuePromptResult, ActivityParams, ActivityEntry, ActivityResult, TranscriptParams, TranscriptEntry, TranscriptResult, HistoryParams, CommandEntry, WaitForParams
- methods: Client(15)
- funcs: Resize
- leans on: client.go(18)

## Package internal/draft

Go package `internal/draft`: 1 source files, 1 test files.

### internal/draft/draft.go

- lines: 110 · tests: draft_test.go
- purpose: New uses the directory containing the database, not the database filename.
- types: Store, Entry
- methods: Store(5)
- funcs: New

## Package internal/drive

Go package `internal/drive`: 1 source files, 1 test files.

### internal/drive/drive.go

- lines: 153 · tests: drive_test.go
- purpose: Check reports a value that is no drive; "" (the row's default) is fine.
- types: Command
- funcs: Check, Of, Available, Stream

## Package internal/harness

Go package `internal/harness`: 6 source files, 4 test files.

### internal/harness/harness.go

- lines: 425 · tests: harness_test.go
- purpose: Harness is one agent CLI as data.
- types: Harness, Ready, Registry
- methods: Harness(6), Ready(1), Registry(5)
- funcs: Builtins, NewRegistry, Load, Merge
- leans on: promptbox.go(9), view.go(7), settings.go(4), session.go(2)

### internal/harness/launch.go

- lines: 176
- purpose: Window is a pane to open: the session it goes in, its name, its working directory and the argv it runs (none: a shell).
- types: Window, Driver (iface), Spec, Result, Launcher
- methods: Launcher(1)
- funcs: NewLauncher, Sleep
- leans on: harness.go(8)

### internal/harness/promptbox.go

- lines: 76
- purpose: PromptBox is how mtm's prompt box writes to a pane running the harness: {"deliver": "queue", "submit": "enter"}.
- types: PromptBox
- methods: Harness(1), PromptBox(2)
- leans on: harness.go(1)

### internal/harness/session.go

- lines: 152 · tests: session_test.go
- purpose: SessionSource says where a harness records the session a running agent writes now, so a pane's live session is found from the harness's own files rather than...
- types: SessionSource
- methods: SessionSource(2)

### internal/harness/settings.go

- lines: 243 · tests: settings_test.go
- purpose: Setting is how one per-session setting is changed inside a running harness: typed (Command, submitted like a prompt) or stepped (Key, sent once per step thro...
- types: Setting
- methods: Harness(4), Setting(5)
- leans on: harness.go(4)

### internal/harness/view.go

- lines: 63 · tests: view_test.go
- purpose: View is how mtm's pane detail opens a pane running the harness: {"open": "both", "split": 60}.
- types: View
- methods: View(1)
- funcs: CheckView, CheckSplit

## Package internal/hints

Go package `internal/hints`: 2 source files, 2 test files.

### internal/hints/labels.go

- lines: 195
- purpose: NormalizeAlphabet makes a label alphabet usable: lowercase ASCII letters only, each once, in the order given.
- types: Target
- funcs: NormalizeAlphabet, Labels, KeyLabels, Assign, Distance

### internal/hints/matcher.go

- lines: 114
- purpose: Action is what completing a label does.
- types: Action, State, Result, Matcher
- methods: Matcher(4)
- funcs: NewMatcher

## Package internal/history

Go package `internal/history`: 4 source files, 2 test files.

### internal/history/history.go

- lines: 319 · tests: history_test.go
- purpose: Entry is one journal row (matomeru-old's history.Entry, field for field).
- types: Entry, Provenance, Owner (func), Journal, Activity, Pane
- methods: Journal(6)
- funcs: Open, FromActivity, Notification, NewID
- leans on: turns.go(5), project.go(3), marks.go(1)

### internal/history/marks.go

- lines: 63
- purpose: Mark is one review mark (matomeru-old's reviews.ReviewMark): the turn's prompt entry id and when it was first opened.
- types: Mark
- methods: Journal(2)
- leans on: history.go(10), project.go(1)

### internal/history/project.go

- lines: 215
- purpose: Response is one answer the agent gave inside a turn.
- types: Response, PaneTurn
- methods: Journal(1), PaneTurn(2)
- leans on: turns.go(11), history.go(10)

### internal/history/turns.go

- lines: 137
- purpose: Turn is one delivered user request through the agent's final answer, as a capture hook reports it (matomeru-old's history.Turn).
- types: Turn
- methods: Journal(3)
- leans on: history.go(22), project.go(4)

## Package internal/locate

Go package `internal/locate`: 2 source files, 1 test files.

### internal/locate/locate.go

- lines: 505 · tests: locate_test.go
- purpose: Pane is what the locator needs of a listed pane.
- types: Pane, Found, Proc, System (iface), Locator
- methods: Locator(11)
- funcs: New

### internal/locate/ps.go

- lines: 121
- purpose: PS is the System of this machine: ps for starts, terminals and command lines, the environment from /proc/<pid>/environ where there is one, else ps eww.
- types: PS
- methods: PS(2)
- leans on: locate.go(6)

## Package internal/mcp

Go package `internal/mcp`: 3 source files, 1 test files.

### internal/mcp/harness.go

- lines: 90
- purpose: Command is how a harness starts the server: this binary, the flags that name the same database and daemon, then mcp serve.
- types: Command, HarnessConfig
- funcs: Harnesses, Stdio, Config

### internal/mcp/mcp.go

- lines: 43 · tests: mcp_test.go
- purpose: Identity is who is talking: the pane and what it is assigned to.
- types: Identity
- funcs: IdentityFromEnv

### internal/mcp/tools.go

- lines: 68
- purpose: Aliases preserves the eight legacy tools and their argument schemas over run.
- funcs: Aliases

## Package internal/notify

Go package `internal/notify`: 1 source files, 1 test files.

### internal/notify/notify.go

- lines: 91 · tests: notify_test.go
- purpose: Sender is a configuration snapshot.
- types: Sender
- methods: Sender(1)
- funcs: Terminal, Commands

## Package internal/panesignals

Go package `internal/panesignals`: 2 source files, 2 test files.

### internal/panesignals/commands.go

- lines: 333 · tests: commands_test.go
- purpose: Finished is one finished command.
- types: Finished, CommandRule, Shell, Marks
- methods: CommandRule(2), Finished(2), Marks(1)
- funcs: CommandKey, CommandWhy, Span, CommandExtra, CommandFailed, ReconcileCommands
- leans on: panesignals.go(14)

### internal/panesignals/panesignals.go

- lines: 198 · tests: panesignals_test.go
- purpose: Pane is what the rules read of a pane.
- types: Pane, Signal, Plan
- methods: Pane(1), Plan(2)
- funcs: Key, Why, Reconcile
- leans on: commands.go(3)

## Package internal/reminders

Go package `internal/reminders`: 2 source files, 3 test files.

### internal/reminders/notify.go

- lines: 155 · tests: notify_test.go
- purpose: Finish re-reads the day so a concurrent checkoff is never overwritten.
- types: Claim, NotifyStatus, Send (func)
- methods: Store(5)
- leans on: store.go(27)

### internal/reminders/store.go

- lines: 308 · tests: store_test.go
- purpose: Rule applies from a local date until the next rule.
- types: Rule, Reminder, Day, HistoryDay, Store
- methods: Reminder(2), Rule(1), Store(8)
- funcs: ParseDays, CheckTime, Open
- leans on: notify.go(1)

## Package internal/schedules

Go package `internal/schedules`: 3 source files, 3 test files.

### internal/schedules/defs.go

- lines: 155
- purpose: Stored are the stored rows by name order.
- methods: Runner(4)
- funcs: Layer
- leans on: schedules.go(34)

### internal/schedules/schedules.go

- lines: 1053 · tests: schedules_test.go
- purpose: Duration is a time.Duration written as "5m" in JSON.
- types: Duration, Agent, Def, State, Record, Launched, ExecFunc (func), Executor (func), Runner
- methods: Def(12), Duration(2), Record(1), Runner(19), State(2)
- funcs: Defaults, Load, Merge, ValidName, Delay, New, RealExec
- leans on: when.go(20), defs.go(2)

### internal/schedules/when.go

- lines: 406 · tests: when_test.go
- purpose: When is a schedule's timing, as Def holds it.
- types: When
- methods: When(5), cron(2)
- funcs: ParseWhen, Span, ParseBound
- leans on: schedules.go(8)

## Package internal/setup

Go package `internal/setup`: 2 source files, 1 test files.

### internal/setup/hooks.go

- lines: 247
- purpose: object is a JSON object with its keys in their order.
- methods: object(3)
- leans on: setup.go(5)

### internal/setup/setup.go

- lines: 347 · tests: setup_test.go
- purpose: Definition is one installable item.
- types: Definition, Options, Item, Plan
- methods: Options(8)
- funcs: Items, IDs, Extension
- leans on: hooks.go(3)

## Package internal/turns

Go package `internal/turns`: 8 source files, 7 test files.

### internal/turns/activity.go

- lines: 291 · tests: activity_test.go
- purpose: Activity is one child activity of a pane: a subagent, as far as its parent's log and its meta say.
- types: Activity, ActivityLog, ActivitySource (iface), ActivityReader (iface)
- methods: Activity(1), ActivityLog(2), ClaudeLog(1), activityParser(4)
- funcs: NewActivityLog, ActivitySourceOf
- leans on: agent.go(17), claude.go(13), turn.go(11)

### internal/turns/agent.go

- lines: 588 · tests: agent_test.go
- purpose: AgentState is how far a subagent got.
- types: AgentState, Agent, Notice, AgentMeta, AgentMetas, AgentTranscript, AgentLogs
- methods: Agent(5), AgentLogs(2), AgentMetas(1), metaDir(1), notification(2)
- funcs: IsSpawnTool, SubagentDir
- leans on: turn.go(27), claude.go(8)

### internal/turns/claude.go

- lines: 897 · tests: claude_test.go
- purpose: claudeRecord is the part of a record the parser reads.
- types: ClaudeParser, ClaudeLog, Log (iface), LogChain, ClaudeChain
- methods: ClaudeLog(5), ClaudeParser(19), LogChain(6), tail(1)
- funcs: NewClaudeLog, ClaudeLogCwd, ClaudeProjects, ClaudeProjectDir, FindClaudeLog
- leans on: turn.go(42), agent.go(39), settings.go(1)

### internal/turns/claude_resolve.go

- lines: 97 · tests: claude_resolve_test.go
- purpose: ClaudeLookup is what ResolveClaudeLog needs to find the transcript of a pane's current Claude Code session.
- types: ClaudeLookup
- funcs: ResolveClaudeLog
- leans on: claude.go(7)

### internal/turns/omp.go

- lines: 414 · tests: omp_test.go
- purpose: ompEntry is the part of an entry the parser reads.
- types: OmpParser, OmpLog
- methods: OmpLog(3), OmpParser(4), ompBuild(4)
- funcs: NewOmpLog, OmpSessions, FindOmpLog, IsNoLog
- leans on: turn.go(38), claude.go(9), settings.go(1)

### internal/turns/settings.go

- lines: 456 · tests: settings_test.go
- purpose: Live is a session's settings as its own log says them now, each empty (or zero) when the log states none.
- types: Live
- methods: ClaudeLog(1), ClaudeParser(1), Live(6), LogChain(1), OmpLog(1), OmpParser(1)
- funcs: ReadCodexSettings, CodexSessions, FindCodexLog
- leans on: claude.go(7), turn.go(6), omp.go(2)

### internal/turns/shell.go

- lines: 277 · tests: shell_test.go
- purpose: ShellTurns are a pane's commands as turns, oldest first.
- types: Capturer (iface)
- methods: captureStream(3)
- funcs: ShellTurns, ShellOutput
- leans on: turn.go(17)

### internal/turns/turn.go

- lines: 155
- purpose: Source is where a turn was read from.
- types: Source, State, StepKind, Message, Step, Command, Turn
- methods: Turn(2)
- funcs: Chain
- leans on: agent.go(11)

## Package internal/work

Go package `internal/work`: 20 source files, 19 test files.

### internal/work/actor.go

- lines: 371 · tests: actor_test.go
- purpose: Actor is who made a write: the owner, an agent (with the pane it runs in, its tuios session, the task its pane is on, its harness and model), or unknown (a t...
- types: Actor, AgentPolicy, RefusedError, TaskLookup (func), PaneRelation
- methods: Actor(3), RefusedError(1), Task(2)
- funcs: Owner, ParseActor, DefaultAgentPolicy, IsRefused, TaskProject, TaskUnder, TaskChanges, MayChangeTask, StampTask, MayChangePane, MayShareProject, MergePolicy
- leans on: work.go(23), privacy.go(2)

### internal/work/actor_store.go

- lines: 104 · tests: actor_store_test.go
- purpose: SetActor makes every later write run as a; a zero actor is the owner.
- methods: Store(8)
- leans on: actor.go(20), work.go(17), companions.go(1), saved.go(1)

### internal/work/companions.go

- lines: 101 · tests: companions_test.go
- purpose: Companion links a named side window to its parent pane.
- types: Companion
- methods: Store(4)
- leans on: work.go(5)

### internal/work/convert.go

- lines: 834 · tests: convert_test.go
- purpose: RowChange is one row a conversion wrote: its stored JSON before and after (nil when the row did not exist, or no longer does).
- types: RowChange, Conversion
- methods: Conversion(1), Store(7), convertTx(5), taskIndex(2)
- leans on: work.go(42)

### internal/work/delete.go

- lines: 156 · tests: delete_test.go
- purpose: DeletionImpact and Delete use the same ownership walk.
- types: DeleteCounts
- methods: DeleteCounts(1), Store(3)
- leans on: work.go(6)

### internal/work/ext_rows.go

- lines: 47
- purpose: ExtRowOwner is the project an extension's row was created in: mtm records it when one of the extension's verbs answers a new row while a project is in contex...
- types: ExtRowOwner
- methods: Store(2)
- funcs: ExtRowKey
- leans on: work.go(3)

### internal/work/notes.go

- lines: 141 · tests: notes_test.go
- purpose: Note retains unknown legacy fields, including provenance, across edits.
- types: Note
- methods: Note(4), Store(6)
- leans on: work.go(10), actor.go(7)

### internal/work/owners.go

- lines: 236 · tests: owners_test.go
- purpose: Assignment's presence, even with empty ownership, overrides session inheritance.
- types: Assignment, SessionOwner
- methods: Store(11)
- leans on: work.go(17), reviews.go(1)

### internal/work/privacy.go

- lines: 68 · tests: privacy_test.go
- purpose: ValidPrivacy reports whether v is a privacy a project can be set to.
- funcs: ValidPrivacy, PrivacyOf, TaskPrivate, Exports
- leans on: work.go(5)

### internal/work/privacy_store.go

- lines: 59
- purpose: SetProjectPrivacy sets a project's privacy (project make-private and make-family).
- methods: Store(4)
- leans on: work.go(12), privacy.go(4)

### internal/work/recency.go

- lines: 58
- purpose: DirRecent records usage of a cleaned path, independently of project ownership.
- types: DirRecent
- methods: Store(3)
- leans on: work.go(5)

### internal/work/reviews.go

- lines: 137
- purpose: Review is one row of the reviews table: the result of one launched agent, tied to its project (empty for a launch with no project), the task it was launched ...
- types: Review
- methods: Store(8)
- leans on: work.go(14)

### internal/work/saved.go

- lines: 93 · tests: saved_test.go
- purpose: PaneRecipe records the request confirmed by the daemon, not a foreground process guessed later.
- types: PaneRecipe, SavedPane
- methods: Store(6)
- leans on: work.go(12), actor.go(1)

### internal/work/sessions.go

- lines: 58 · tests: sessions_test.go
- purpose: PaneSessions are the agent sessions a pane has run, oldest first, keyed by the TUIOS window id: tuios reports only the current one (list-agents' agent_sessio...
- types: PaneSessions
- methods: Store(3)
- leans on: work.go(5)

### internal/work/stars.go

- lines: 80 · tests: stars_test.go
- purpose: PaneStar marks a pane the person starred.
- types: PaneStar
- methods: Store(3)
- leans on: work.go(5)

### internal/work/subtree.go

- lines: 227 · tests: subtree_test.go
- purpose: taskForest is every task row by id with its children in board order.
- methods: Store(6), taskForest(1)
- leans on: work.go(30)

### internal/work/trees.go

- lines: 133 · tests: trees_test.go
- purpose: AddTree attaches a checkout without claiming exclusive ownership of its path.
- methods: Store(5)
- leans on: work.go(17)

### internal/work/views.go

- lines: 51
- purpose: PaneView is a pane's own view in mtm's pane detail (pane.view): which tab or layout it opens on (turns, terminal or both) and, for both, the percent of the w...
- types: PaneView
- methods: Store(2)
- leans on: work.go(4)

### internal/work/vim.go

- lines: 99 · tests: vim_test.go
- purpose: VimSession keeps the legacy vim_sessions shape and unknown fields intact.
- types: VimSession
- methods: Store(5), VimSession(2)
- leans on: work.go(7)

### internal/work/work.go

- lines: 373 · tests: work_test.go
- purpose: Project is one row of the projects table.
- types: Project, Theme, Tree, ProjectSettings, Task, Store
- methods: Store(15)
- funcs: Open, NormalStatus, ValidStatus, StatusGlyph, StatusRank, NewID, Now
- leans on: actor.go(21)

## Package store

Go package `store`: 5 source files, 5 test files.

### store/append.go

- lines: 69 · tests: append_test.go
- purpose: SequencedRow is an immutable row and its database insertion sequence.
- types: SequencedRow, JSONMatch
- methods: Store(2)
- leans on: store.go(4)

### store/doc.go

- lines: 32

### store/extension.go

- lines: 122 · tests: extension_test.go
- purpose: ValidExtensionName reports whether name may name an extension database: lowercase letters, digits and single dashes between them, at most 64 bytes.
- methods: Store(1)
- funcs: ValidExtensionName, ExtensionPath, OpenExtension
- leans on: store.go(6)

### store/migrate.go

- lines: 126 · tests: migrate_test.go
- purpose: Rewrite turns one row of the old format into the new one.
- types: Rewrite (func)
- methods: Store(1)
- leans on: store.go(9)

### store/store.go

- lines: 394 · tests: store_test.go
- purpose: Table describes ownership and the version of a unit's JSON row format.
- types: Table, Store, Tx, TableInfo, Entry
- methods: Store(10), Tx(7)
- funcs: Open, OpenReadOnly
- leans on: extension.go(1)

## Symbol owners

Exported type -> file.

- cmd/mtm.App -> cmd/mtm/main.go
- cmd/mtm.Options -> cmd/mtm/main.go
- cmd/mtm.Owner -> cmd/mtm/owner.go
- internal/actionitems.Counts -> internal/actionitems/actionitems.go
- internal/actionitems.Item -> internal/actionitems/actionitems.go
- internal/actionitems.NotifyOptions -> internal/actionitems/notify.go
- internal/actionitems.Owner -> internal/actionitems/actionitems.go
- internal/actionitems.Spec -> internal/actionitems/actionitems.go
- internal/actionitems.Store -> internal/actionitems/actionitems.go
- internal/compose.Command -> internal/compose/commands.go
- internal/compose.Env -> internal/compose/skills.go
- internal/compose.Match -> internal/compose/files.go
- internal/compose.Skill -> internal/compose/skills.go
- internal/compose.Sources -> internal/compose/skills.go
- internal/contextitems.Cell -> internal/contextitems/spans.go
- internal/contextitems.Item -> internal/contextitems/store.go
- internal/contextitems.Owner -> internal/contextitems/store.go
- internal/contextitems.Provenance -> internal/contextitems/store.go
- internal/contextitems.Source -> internal/contextitems/store.go
- internal/contextitems.Span -> internal/contextitems/spans.go
- internal/contextitems.SpanOptions -> internal/contextitems/spans.go
- internal/contextitems.StatFunc -> internal/contextitems/extract.go
- internal/contextitems.Store -> internal/contextitems/store.go
- internal/daemon.ActivityEntry -> internal/daemon/verbs.go
- internal/daemon.ActivityParams -> internal/daemon/verbs.go
- internal/daemon.ActivityResult -> internal/daemon/verbs.go
- internal/daemon.CaptureParams -> internal/daemon/verbs.go
- internal/daemon.CaptureResult -> internal/daemon/verbs.go
- internal/daemon.Client -> internal/daemon/client.go
- internal/daemon.CommandEntry -> internal/daemon/verbs.go
- internal/daemon.Error -> internal/daemon/client.go
- internal/daemon.Event -> internal/daemon/event.go
- internal/daemon.Frame -> internal/daemon/stream.go
- internal/daemon.Hello -> internal/daemon/verbs.go
- internal/daemon.HistoryParams -> internal/daemon/verbs.go
- internal/daemon.HostReport -> internal/daemon/hosts.go
- internal/daemon.HostReports -> internal/daemon/hosts.go
- internal/daemon.NewWindowParams -> internal/daemon/verbs.go
- internal/daemon.NewWindowResult -> internal/daemon/verbs.go
- internal/daemon.Pane -> internal/daemon/verbs.go
- internal/daemon.PaneStream -> internal/daemon/stream.go
- internal/daemon.Presence -> internal/daemon/client.go
- internal/daemon.QueuePromptParams -> internal/daemon/verbs.go
- internal/daemon.QueuePromptResult -> internal/daemon/verbs.go
- internal/daemon.SendKeysParams -> internal/daemon/verbs.go
- internal/daemon.SendTextParams -> internal/daemon/verbs.go
- internal/daemon.Session -> internal/daemon/verbs.go
- internal/daemon.StartAgentParams -> internal/daemon/verbs.go
- internal/daemon.StartAgentResult -> internal/daemon/verbs.go
- internal/daemon.StartFunc -> internal/daemon/server.go
- internal/daemon.StreamInfo -> internal/daemon/stream.go
- internal/daemon.StreamParams -> internal/daemon/stream.go
- internal/daemon.TranscriptEntry -> internal/daemon/verbs.go
- internal/daemon.TranscriptParams -> internal/daemon/verbs.go
- internal/daemon.TranscriptResult -> internal/daemon/verbs.go
- internal/daemon.WaitForParams -> internal/daemon/verbs.go
- internal/draft.Entry -> internal/draft/draft.go
- internal/draft.Store -> internal/draft/draft.go
- internal/drive.Command -> internal/drive/drive.go
- internal/harness.Driver -> internal/harness/launch.go
- internal/harness.Harness -> internal/harness/harness.go
- internal/harness.Launcher -> internal/harness/launch.go
- internal/harness.PromptBox -> internal/harness/promptbox.go
- internal/harness.Ready -> internal/harness/harness.go
- internal/harness.Registry -> internal/harness/harness.go
- internal/harness.Result -> internal/harness/launch.go
- internal/harness.SessionSource -> internal/harness/session.go
- internal/harness.Setting -> internal/harness/settings.go
- internal/harness.Spec -> internal/harness/launch.go
- internal/harness.View -> internal/harness/view.go
- internal/harness.Window -> internal/harness/launch.go
- internal/hints.Action -> internal/hints/matcher.go
- internal/hints.Matcher -> internal/hints/matcher.go
- internal/hints.Result -> internal/hints/matcher.go
- internal/hints.State -> internal/hints/matcher.go
- internal/hints.Target -> internal/hints/labels.go
- internal/history.Activity -> internal/history/history.go
- internal/history.Entry -> internal/history/history.go
- internal/history.Journal -> internal/history/history.go
- internal/history.Mark -> internal/history/marks.go
- internal/history.Owner -> internal/history/history.go
- internal/history.Pane -> internal/history/history.go
- internal/history.PaneTurn -> internal/history/project.go
- internal/history.Provenance -> internal/history/history.go
- internal/history.Response -> internal/history/project.go
- internal/history.Turn -> internal/history/turns.go
- internal/locate.Found -> internal/locate/locate.go
- internal/locate.Locator -> internal/locate/locate.go
- internal/locate.PS -> internal/locate/ps.go
- internal/locate.Pane -> internal/locate/locate.go
- internal/locate.Proc -> internal/locate/locate.go
- internal/locate.System -> internal/locate/locate.go
- internal/mcp.Command -> internal/mcp/harness.go
- internal/mcp.HarnessConfig -> internal/mcp/harness.go
- internal/mcp.Identity -> internal/mcp/mcp.go
- internal/notify.Sender -> internal/notify/notify.go
- internal/panesignals.CommandRule -> internal/panesignals/commands.go
- internal/panesignals.Finished -> internal/panesignals/commands.go
- internal/panesignals.Marks -> internal/panesignals/commands.go
- internal/panesignals.Pane -> internal/panesignals/panesignals.go
- internal/panesignals.Plan -> internal/panesignals/panesignals.go
- internal/panesignals.Shell -> internal/panesignals/commands.go
- internal/panesignals.Signal -> internal/panesignals/panesignals.go
- internal/reminders.Claim -> internal/reminders/notify.go
- internal/reminders.Day -> internal/reminders/store.go
- internal/reminders.HistoryDay -> internal/reminders/store.go
- internal/reminders.NotifyStatus -> internal/reminders/notify.go
- internal/reminders.Reminder -> internal/reminders/store.go
- internal/reminders.Rule -> internal/reminders/store.go
- internal/reminders.Send -> internal/reminders/notify.go
- internal/reminders.Store -> internal/reminders/store.go
- internal/schedules.Agent -> internal/schedules/schedules.go
- internal/schedules.Def -> internal/schedules/schedules.go
- internal/schedules.Duration -> internal/schedules/schedules.go
- internal/schedules.ExecFunc -> internal/schedules/schedules.go
- internal/schedules.Executor -> internal/schedules/schedules.go
- internal/schedules.Launched -> internal/schedules/schedules.go
- internal/schedules.Record -> internal/schedules/schedules.go
- internal/schedules.Runner -> internal/schedules/schedules.go
- internal/schedules.State -> internal/schedules/schedules.go
- internal/schedules.When -> internal/schedules/when.go
- internal/setup.Definition -> internal/setup/setup.go
- internal/setup.Item -> internal/setup/setup.go
- internal/setup.Options -> internal/setup/setup.go
- internal/setup.Plan -> internal/setup/setup.go
- internal/turns.Activity -> internal/turns/activity.go
- internal/turns.ActivityLog -> internal/turns/activity.go
- internal/turns.ActivityReader -> internal/turns/activity.go
- internal/turns.ActivitySource -> internal/turns/activity.go
- internal/turns.Agent -> internal/turns/agent.go
- internal/turns.AgentLogs -> internal/turns/agent.go
- internal/turns.AgentMeta -> internal/turns/agent.go
- internal/turns.AgentMetas -> internal/turns/agent.go
- internal/turns.AgentState -> internal/turns/agent.go
- internal/turns.AgentTranscript -> internal/turns/agent.go
- internal/turns.Capturer -> internal/turns/shell.go
- internal/turns.ClaudeChain -> internal/turns/claude.go
- internal/turns.ClaudeLog -> internal/turns/claude.go
- internal/turns.ClaudeLookup -> internal/turns/claude_resolve.go
- internal/turns.ClaudeParser -> internal/turns/claude.go
- internal/turns.Command -> internal/turns/turn.go
- internal/turns.Live -> internal/turns/settings.go
- internal/turns.Log -> internal/turns/claude.go
- internal/turns.LogChain -> internal/turns/claude.go
- internal/turns.Message -> internal/turns/turn.go
- internal/turns.Notice -> internal/turns/agent.go
- internal/turns.OmpLog -> internal/turns/omp.go
- internal/turns.OmpParser -> internal/turns/omp.go
- internal/turns.Source -> internal/turns/turn.go
- internal/turns.State -> internal/turns/turn.go
- internal/turns.Step -> internal/turns/turn.go
- internal/turns.StepKind -> internal/turns/turn.go
- internal/turns.Turn -> internal/turns/turn.go
- internal/work.Actor -> internal/work/actor.go
- internal/work.AgentPolicy -> internal/work/actor.go
- internal/work.Assignment -> internal/work/owners.go
- internal/work.Companion -> internal/work/companions.go
- internal/work.Conversion -> internal/work/convert.go
- internal/work.DeleteCounts -> internal/work/delete.go
- internal/work.DirRecent -> internal/work/recency.go
- internal/work.ExtRowOwner -> internal/work/ext_rows.go
- internal/work.Note -> internal/work/notes.go
- internal/work.PaneRecipe -> internal/work/saved.go
- internal/work.PaneRelation -> internal/work/actor.go
- internal/work.PaneSessions -> internal/work/sessions.go
- internal/work.PaneStar -> internal/work/stars.go
- internal/work.PaneView -> internal/work/views.go
- internal/work.Project -> internal/work/work.go
- internal/work.ProjectSettings -> internal/work/work.go
- internal/work.RefusedError -> internal/work/actor.go
- internal/work.Review -> internal/work/reviews.go
- internal/work.RowChange -> internal/work/convert.go
- internal/work.SavedPane -> internal/work/saved.go
- internal/work.SessionOwner -> internal/work/owners.go
- internal/work.Store -> internal/work/work.go
- internal/work.Task -> internal/work/work.go
- internal/work.TaskLookup -> internal/work/actor.go
- internal/work.Theme -> internal/work/work.go
- internal/work.Tree -> internal/work/work.go
- internal/work.VimSession -> internal/work/vim.go
- store.Entry -> store/store.go
- store.JSONMatch -> store/append.go
- store.Rewrite -> store/migrate.go
- store.SequencedRow -> store/append.go
- store.Store -> store/store.go
- store.Table -> store/store.go
- store.TableInfo -> store/store.go
- store.Tx -> store/store.go
