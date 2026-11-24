package app

import (
	"context"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Commands and modal scope for Global Pane & Chat-Log Search.
const (
	CmdPaneSearch               = "search.panes"
	CmdPaneSearchAttach         = "search.panes.attach"
	CmdPaneSearchYank           = "search.panes.yank"
	CmdPaneSearchSourceCycle    = "search.panes.source.cycle"
	CmdPaneSearchArchivedToggle = "search.panes.archived.toggle"

	paneSearchModalID         = "pane.search"
	defaultPaneSearchMaxLogKB = 2048
)

func paneSearchSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdPaneSearch,
			Description: "Search across all panes: live scrollback, chat turns, SQLite records, and agent transcripts",
			Params: []core.ParamSpec{
				{Name: "query", Type: core.ParamString, Description: "initial search query"},
				{Name: "source", Type: core.ParamString, Enum: []string{"all", "scrollback", "chat", "records"}, Default: "all", Description: "source filter"},
				{Name: "includeArchived", Type: core.ParamBool, Default: false, Description: "include archived panes"},
			},
		},
		{ID: CmdPaneSearchAttach, Description: "Jump to the focused pane and attach its live terminal", Meta: map[string]any{"internal": true}},
		{ID: CmdPaneSearchYank, Description: "Yank the top matching snippet for the focused pane", Meta: map[string]any{"internal": true}},
		{ID: CmdPaneSearchSourceCycle, Description: "Cycle the pane search source filter (all -> scrollback -> chat -> records)", Meta: map[string]any{"internal": true}},
		{ID: CmdPaneSearchArchivedToggle, Description: "Toggle archived panes in global pane search", Meta: map[string]any{"internal": true}},
	}
}

func paneSearchBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, "", "F", CmdPaneSearch, "find in panes"),
		bind(ModePicker, paneSearchModalID, "o", CmdPaneSearchAttach, "open live terminal"),
		bind(ModePicker, paneSearchModalID, "y", CmdPaneSearchYank, "yank snippet"),
		bind(ModePicker, paneSearchModalID, "s", CmdPaneSearchSourceCycle, "cycle source"),
		bind(ModePicker, paneSearchModalID, "a", CmdPaneSearchArchivedToggle, "toggle archived"),
	}
}

func (a *App) paneSearchIncludeTranscripts() bool {
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		return a.cfg.Settings.Bool(SetSearchPanesIncludeTranscripts, true)
	}
	return true
}

func (a *App) paneSearchIncludeArchived() bool {
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		return a.cfg.Settings.Bool(SetSearchPanesIncludeArchived, false)
	}
	return false
}

func (a *App) paneSearchMaxLogBytes() int64 {
	kb := defaultPaneSearchMaxLogKB
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		if v := a.cfg.Settings.Int(SetSearchPanesMaxLogKB, defaultPaneSearchMaxLogKB); v > 0 {
			kb = v
		}
	}
	return int64(kb) * 1024
}

func (a *App) paneSearchBuildOpts(includeArchived, includeTranscripts bool) panesearch.BuildOptions {
	frames := map[string]panesearch.LiveFrame{}
	fallback := map[string][]string{}
	turns := map[string][]panesearch.Turn{}
	transcriptPaths := map[string]string{}
	sessionIDs := map[string]string{}

	if res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: includeArchived}); err == nil {
		for _, pane := range res.Items {
			id := pane.ID
			if a.panes.Host != nil {
				if fr, ok := a.panes.Host.Snapshot(id); ok {
					frames[id] = panesearch.LiveFrame{Lines: fr.History(), Revision: fr.Revision}
				} else if fr, ok := a.rt.live.frames[id]; ok {
					frames[id] = panesearch.LiveFrame{Lines: fr.History(), Revision: fr.Revision}
				}
			} else if fr, ok := a.rt.live.frames[id]; ok {
				frames[id] = panesearch.LiveFrame{Lines: fr.History(), Revision: fr.Revision}
			}
			if _, hasFrame := frames[id]; !hasFrame {
				if buf := a.panes.term[id]; buf != nil && len(buf.lines) > 0 {
					fallback[id] = append([]string(nil), buf.lines...)
				}
			}
			if pt := a.paneTelemetry(id); pt != nil {
				if pt.TranscriptPath != "" {
					transcriptPaths[id] = pt.TranscriptPath
				}
				if pt.SessionID != "" {
					sessionIDs[id] = pt.SessionID
				}
				if len(pt.RecentTurns) > 0 {
					list := make([]panesearch.Turn, len(pt.RecentTurns))
					for i, ht := range pt.RecentTurns {
						list[i] = panesearch.Turn{
							Prompt:   ht.Prompt,
							Response: ht.Response,
							Summary:  ht.Summary,
							At:       ht.At,
						}
					}
					turns[id] = list
				}
			}
		}
	}

	if a.panes.searchCache == nil {
		a.panes.searchCache = panesearch.NewCache()
	}
	return panesearch.BuildOptions{
		IncludeArchived:    includeArchived,
		IncludeTranscripts: includeTranscripts,
		MaxLogBytes:        a.paneSearchMaxLogBytes(),
		Frames:             frames,
		FallbackLines:      fallback,
		Turns:              turns,
		TranscriptPaths:    transcriptPaths,
		SessionIDs:         sessionIDs,
		Cache:              a.panes.searchCache,
	}
}

func (a *App) paneSearch(s core.State, c core.Command) core.StepResult {
	source, ok := panesearch.NormalizeSource(c.Arg("source"))
	if !ok {
		source = panesearch.SourceAll
	}
	includeArchived := a.paneSearchIncludeArchived()
	if raw, has := c.Args["includeArchived"]; has {
		if b, isBool := raw.(bool); isBool {
			includeArchived = b
		} else if str, isStr := raw.(string); isStr {
			includeArchived = strings.EqualFold(strings.TrimSpace(str), "true")
		}
	}
	includeTranscripts := a.paneSearchIncludeTranscripts()
	initialQuery := strings.TrimSpace(c.Arg("query"))

	// In live mode, hydrate live scrollback + SQLite records immediately and
	// load disk transcripts asynchronously so opening F never blocks the UI.
	syncTranscripts := includeTranscripts && !a.opts.Live
	docs, err := panesearch.BuildDocuments(a.Store, a.paneSearchBuildOpts(includeArchived, syncTranscripts))
	if err != nil {
		a.fail("search panes", err)
		return core.Next(s)
	}

	a.panes.searchGen++
	gen := a.panes.searchGen
	hydrating := a.opts.Live && includeTranscripts

	state := &paneSearchState{
		source:             source,
		includeArchived:    includeArchived,
		includeTranscripts: includeTranscripts,
		hydrating:          hydrating,
		docs:               docs,
	}

	pm := picker.New("Find in Panes", state.pickerItems())
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "search panes, scrollback, chat, records · tab mode · s source · a archived · o attach · y yank"
	if initialQuery != "" {
		pm.SetQuery(initialQuery)
	}
	pm.Rank = func(it picker.Item, lowerQuery string) int {
		doc, ok := it.Data.(panesearch.Document)
		if !ok {
			return 0
		}
		score := panesearch.ScoreDocument(doc, lowerQuery, pm.MatchMode, state.source)
		if score == 0 {
			return 1_000_000
		}
		return -score
	}
	pm.PreviewFn = func(it picker.Item) *ui.ViewNode {
		doc, ok := it.Data.(panesearch.Document)
		if !ok {
			return nil
		}
		return renderPaneSearchPreview(doc, pm.Query, pm.MatchMode, state.source, state.hydrating)
	}

	startAsyncHydrate := func(targetGen uint64, archived bool) {
		if !a.opts.Live || !includeTranscripts {
			return
		}
		opts := a.paneSearchBuildOpts(archived, true)
		a.background(func(_ context.Context) func(core.State) core.State {
			hydrated, herr := panesearch.BuildDocuments(a.Store, opts)
			return func(st core.State) core.State {
				if herr != nil || a.panes.searchGen != targetGen {
					return st
				}
				mod := a.activePicker(paneSearchModalID)
				if mod == nil {
					return st
				}
				state.docs = hydrated
				state.hydrating = false
				mod.SetItems(state.pickerItems())
				a.viewMemo.gen++
				return st
			}
		})
	}

	var mod *modal.PickerModal
	mod = a.newPickerModal(pm, modal.PickerOptions{
		ID:            paneSearchModalID,
		Scope:         paneSearchModalID,
		Title:         "Find in Panes",
		StartInFilter: true,
		WidthPct:      90,
		HeightPct:     82,
		PageSize:      12,
		Badges: func(p *picker.Model) []ui.Badge {
			badges := []ui.Badge{
				{Text: "source:" + string(state.source), Tone: "accent"},
				{Text: string(p.MatchMode), Tone: "muted"},
			}
			if state.includeArchived {
				badges = append(badges, ui.Badge{Text: "+archived", Tone: "warning"})
			}
			if state.hydrating {
				badges = append(badges, ui.Badge{Text: "indexing logs…", Tone: "info"})
			}
			return badges
		},
		OnConfirm: func(picked picker.Item) modal.Result {
			doc, ok := picked.Data.(panesearch.Document)
			if !ok {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			a.afterModal(func(st core.State) core.State {
				return a.jumpToPane(st, doc.PaneID, false)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
		OnCommand: func(p *picker.Model, cmd core.Command) (modal.Result, bool) {
			switch cmd.ID {
			case CmdPaneSearchAttach:
				picked, ok := p.Focused()
				if !ok {
					return modal.Result{Handled: true, Status: "no pane matches", SetStatus: true}, true
				}
				doc, ok := picked.Data.(panesearch.Document)
				if !ok {
					return modal.Result{Handled: true}, true
				}
				a.afterModal(func(st core.State) core.State {
					return a.jumpToPane(st, doc.PaneID, true)
				})
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true

			case CmdPaneSearchYank:
				picked, ok := p.Focused()
				if !ok {
					return modal.Result{Handled: true, Status: "no pane matches", SetStatus: true}, true
				}
				doc, ok := picked.Data.(panesearch.Document)
				if !ok {
					return modal.Result{Handled: true}, true
				}
				text, what := topPaneSearchYank(doc, p.Query, p.MatchMode, state.source)
				a.yank(text, what)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true

			case CmdPaneSearchSourceCycle:
				state.source = panesearch.CycleSource(state.source)
				mod.SetItems(state.pickerItems())
				return modal.Result{
					Handled:   true,
					Mode:      mod.Mode(),
					Status:    fmt.Sprintf("pane search source: %s", state.source),
					SetStatus: true,
				}, true

			case CmdPaneSearchArchivedToggle:
				state.includeArchived = !state.includeArchived
				a.panes.searchGen++
				nextGen := a.panes.searchGen
				syncT := state.includeTranscripts && !a.opts.Live
				if updated, uerr := panesearch.BuildDocuments(a.Store, a.paneSearchBuildOpts(state.includeArchived, syncT)); uerr == nil {
					state.docs = updated
					state.hydrating = a.opts.Live && state.includeTranscripts
					mod.SetItems(state.pickerItems())
					startAsyncHydrate(nextGen, state.includeArchived)
				}
				label := "active panes only"
				if state.includeArchived {
					label = "including archived panes"
				}
				return modal.Result{
					Handled:   true,
					Mode:      mod.Mode(),
					Status:    "pane search: " + label,
					SetStatus: true,
				}, true
			}
			return modal.Result{}, false
		},
	})

	startAsyncHydrate(gen, includeArchived)
	a.say("enter jump · o live terminal · y yank snippet · s source · a archived · tab match mode")
	return core.Next(a.pushModal(s, mod))
}

type paneSearchState struct {
	source             panesearch.Source
	includeArchived    bool
	includeTranscripts bool
	hydrating          bool
	docs               []panesearch.Document
}

func (st *paneSearchState) pickerItems() []picker.Item {
	items := make([]picker.Item, 0, len(st.docs))
	for _, doc := range st.docs {
		kws := doc.Keywords(st.source)
		if st.source != panesearch.SourceAll && len(kws) == 0 {
			continue
		}
		var subParts []string
		if doc.Project != "" {
			subParts = append(subParts, doc.Project)
		}
		if doc.Harness != "" {
			subParts = append(subParts, doc.Harness)
		}
		if doc.Cwd != "" {
			subParts = append(subParts, doc.Cwd)
		}
		var badges []ui.Badge
		if doc.RunStatus != "" {
			badges = append(badges, ui.Badge{Text: doc.RunStatus, Tone: paneStatusTone(doc.RunStatus)})
		}
		if doc.Archived {
			badges = append(badges, ui.Badge{Text: "archived", Tone: "muted"})
		}
		ref := doc.Pane.Ref()
		items = append(items, picker.Item{
			ID:       doc.PaneID,
			Title:    doc.Title,
			Subtitle: strings.Join(subParts, " · "),
			Badges:   badges,
			Ref:      &ref,
			Scope:    paneSearchModalID,
			Keywords: kws,
			Data:     doc,
		})
	}
	return items
}

func paneStatusTone(status string) string {
	switch status {
	case "needs_you":
		return "warning"
	case "running":
		return "success"
	case "dead":
		return "error"
	default:
		return "muted"
	}
}

func topPaneSearchYank(doc panesearch.Document, query string, mode traits.MatchMode, src panesearch.Source) (string, string) {
	matcher, _ := traits.CompileFilter(query, mode, traits.FilterConfig{})
	if res, ok := panesearch.MatchDocument(doc, matcher, src, 1); ok && len(res.Snippets) > 0 {
		sn := res.Snippets[0]
		return sn.Line, sn.Badge + " snippet"
	}
	if doc.Cwd != "" {
		return doc.Cwd, "pane cwd"
	}
	return doc.Title, "pane title"
}

func renderPaneSearchPreview(doc panesearch.Document, query string, mode traits.MatchMode, src panesearch.Source, hydrating bool) *ui.ViewNode {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", doc.Title)
	if doc.Harness != "" {
		fmt.Fprintf(&b, "  [%s]", doc.Harness)
	}
	if doc.RunStatus != "" {
		fmt.Fprintf(&b, "  (%s)", doc.RunStatus)
	}
	b.WriteByte('\n')
	if doc.Project != "" {
		fmt.Fprintf(&b, "Project: %s", doc.Project)
		if doc.TaskTitle != "" && doc.TaskTitle != doc.Project {
			fmt.Fprintf(&b, " › %s", doc.TaskTitle)
		}
		b.WriteByte('\n')
	}
	if doc.Cwd != "" {
		fmt.Fprintf(&b, "Cwd: %s\n", doc.Cwd)
	}
	if doc.TranscriptPath != "" {
		fmt.Fprintf(&b, "Log: %s\n", doc.TranscriptPath)
	}
	b.WriteByte('\n')

	matcher, errMsg := traits.CompileFilter(query, mode, traits.FilterConfig{})
	if errMsg != "" {
		fmt.Fprintf(&b, "Invalid regex: %s\n\n", errMsg)
	}
	res, ok := panesearch.MatchDocument(doc, matcher, src, 8)
	if !ok || len(res.Snippets) == 0 {
		if hydrating {
			b.WriteString("Indexing external session transcripts…\n")
		} else {
			b.WriteString("No matching excerpts in selected source.\n")
		}
	} else {
		if strings.TrimSpace(query) != "" {
			fmt.Fprintf(&b, "Matches (%d, score %d · source: %s):\n\n", res.MatchCount, res.Score, src)
		} else {
			fmt.Fprintf(&b, "Recent excerpts (source: %s):\n\n", src)
		}
		for _, sn := range res.Snippets {
			header := "[" + sn.Badge + "]"
			if sn.Label != "" {
				header += " " + clip(sn.Label, 48)
			}
			b.WriteString(header + "\n")
			for _, prev := range sn.Before {
				fmt.Fprintf(&b, "  %s\n", clip(prev, 96))
			}
			fmt.Fprintf(&b, "▸ %s\n", clip(sn.Line, 96))
			for _, next := range sn.After {
				fmt.Fprintf(&b, "  %s\n", clip(next, 96))
			}
			b.WriteByte('\n')
		}
	}
	return &ui.ViewNode{
		ID:    "pane.search.preview.text",
		Kind:  ui.KindText,
		Props: map[string]any{"text": strings.TrimRight(b.String(), "\n")},
	}
}
