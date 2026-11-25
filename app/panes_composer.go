package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func composerDraftKey(paneID string) string {
	return "composer:" + paneID
}

func bracketedPasteBytes(text string) []byte {
	return []byte("\x1b[200~" + text + "\x1b[201~\r")
}

func (a *App) queuedPromptCount(paneID string) int {
	a.panes.queuedMu.Lock()
	defer a.panes.queuedMu.Unlock()
	return len(a.panes.queuedPrompts[paneID])
}

func (a *App) queuedPromptsFor(paneID string) []string {
	a.panes.queuedMu.Lock()
	defer a.panes.queuedMu.Unlock()
	return slices.Clone(a.panes.queuedPrompts[paneID])
}

// paneCompose opens the on-demand multi-line prompt composer overlay for the
// selected pane, backed by the universal richComposerModal engine.
func (a *App) paneCompose(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	paneID := pane.ID
	paneTitle := views.Title(a.schema(data.KindPane), *pane)
	contextTaskID := ""
	if tRef, hasTask := pane.RefField("task"); hasTask && tRef.Kind == data.KindTask {
		contextTaskID = tRef.ID
	}

	cm := newRichComposerModal(a, richComposerSpec{
		ID:                "composer",
		Scope:             "composer",
		Title:             "Compose · " + paneTitle,
		InputTitle:        "Prompt",
		Placeholder:       "Type prompt… (@file, @ctx, @agent, $skill, /command · enter newline · ctrl+s submit)",
		Cwd:               pane.Text("cwd"),
		DraftKey:          composerDraftKey(paneID),
		PaneID:            paneID,
		ShowDeliveryModes: true,
		ContextTaskID:     contextTaskID,
		OnSubmit: func(text string, attachments []data.Attachment, mode ComposerMode) (tea.Cmd, error) {
			rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
			if err != nil {
				return nil, err
			}
			payload := formatPromptWithAttachments(text, attachments)
			paneCopy := *rec
			a.afterModal(func(st core.State) core.State {
				a.deliverComposerPrompt(paneCopy, payload, mode)
				return st
			})
			return nil, nil
		},
	})

	a.say("composer (%s) · ctrl+s submits · shift+tab mode · ctrl+p/n drafts · ctrl+v/f attach · @file $skill /cmd", cm.delivery)
	return core.Next(a.pushModal(s, cm))
}

// --- Delivery modes: send, queue, interrupt ------------------------------------

func (a *App) deliverComposerPrompt(pane data.Record, text, mode string) {
	switch mode {
	case ComposerModeQueue:
		a.enqueuePrompt(pane, text)
	case ComposerModeInterrupt:
		a.deliverPromptInterrupt(pane, text)
	default:
		a.deliverPromptNow(pane, text, "sent")
	}
}

func (a *App) deliverPromptNow(pane data.Record, text, verb string) {
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, bracketedPasteBytes(text)); err != nil {
			a.fail("send prompt", err)
			return
		}
	} else {
		buf := a.termOf(pane.ID)
		for _, ln := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			buf.lines = append(buf.lines, "❯ "+ln)
		}
		buf.lines = buf.lines[max(0, len(buf.lines)-200):]
	}
	if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
		a.fail("record prompt", err)
	}
	a.harvestPrompt(pane, text)
	a.say("%s prompt to %s", verb, views.Title(a.schema(data.KindPane), pane))
}

func (a *App) enqueuePrompt(pane data.Record, text string) {
	a.panes.queuedMu.Lock()
	if a.panes.queuedPrompts == nil {
		a.panes.queuedPrompts = map[string][]string{}
	}
	a.panes.queuedPrompts[pane.ID] = append(a.panes.queuedPrompts[pane.ID], text)
	count := len(a.panes.queuedPrompts[pane.ID])
	a.panes.queuedMu.Unlock()

	if a.isPaneReadyForQueue(pane.ID) {
		a.flushQueuedPrompts(pane.ID)
		return
	}
	a.say("queued prompt #%d for %s (delivers when idle/done)", count, views.Title(a.schema(data.KindPane), pane))
}

func (a *App) isPaneReadyForQueue(paneID string) bool {
	if len(a.pendingApprovalsFor(paneID)) > 0 {
		return false
	}
	pt := a.paneTelemetry(paneID)
	if pt == nil {
		return false
	}
	return pt.State == daemon.StateIdle || pt.State == daemon.StateDone
}

func (a *App) flushQueuedPrompts(paneID string) int {
	if !a.isPaneReadyForQueue(paneID) {
		return 0
	}
	a.panes.queuedMu.Lock()
	list := a.panes.queuedPrompts[paneID]
	if len(list) == 0 {
		a.panes.queuedMu.Unlock()
		return 0
	}
	delete(a.panes.queuedPrompts, paneID)
	a.panes.queuedMu.Unlock()

	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil {
		return 0
	}
	for _, prompt := range list {
		a.deliverPromptNow(*pane, prompt, "delivered queued")
	}
	return len(list)
}

func (a *App) flushAllQueuedPrompts() int {
	a.panes.queuedMu.Lock()
	ids := make([]string, 0, len(a.panes.queuedPrompts))
	for id, list := range a.panes.queuedPrompts {
		if len(list) > 0 {
			ids = append(ids, id)
		}
	}
	a.panes.queuedMu.Unlock()
	delivered := 0
	for _, id := range ids {
		delivered += a.flushQueuedPrompts(id)
	}
	return delivered
}

func (a *App) deliverPromptInterrupt(pane data.Record, text string) {
	a.panes.escapes++
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, []byte{0x1b}); err != nil {
			a.fail("interrupt pane", err)
			return
		}
		delay := a.panes.interruptDelay
		if delay <= 0 {
			delay = defaultInterruptDelay
		}
		a.say("interrupting %s…", views.Title(a.schema(data.KindPane), pane))
		a.background(func(ctx context.Context) func(core.State) core.State {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
			_ = a.panes.Host.Write(pane.ID, bracketedPasteBytes(text))
			return func(s core.State) core.State {
				if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
					a.fail("record prompt", err)
				}
				a.harvestPrompt(pane, text)
				a.say("interrupted and sent prompt to %s", views.Title(a.schema(data.KindPane), pane))
				a.viewMemo.gen++
				return s
			}
		})
		return
	}
	// Synthetic pane: record immediately.
	a.deliverPromptNow(pane, text, "interrupted and sent")
}

// --- Async @file and o-ai $skill / /command discovery caches -------------------

func (a *App) cachedFileCompletions(cwd string) []composerCompletion {
	a.panes.composerFilesMu.RLock()
	entry := a.panes.composerFiles[cwd]
	a.panes.composerFilesMu.RUnlock()
	out := make([]composerCompletion, len(entry.files))
	for i, rel := range entry.files {
		out[i] = composerCompletion{
			Trigger: "@",
			Label:   "@" + rel,
			Insert:  "@" + rel,
			Detail:  "file",
			Kind:    "file",
		}
	}
	return out
}

func (a *App) ensureComposerFilesAsync(cwd string) {
	if cwd == "" {
		return
	}
	now := a.now()
	a.panes.composerFilesMu.Lock()
	if a.panes.composerFiles == nil {
		a.panes.composerFiles = map[string]cachedFileList{}
	}
	entry := a.panes.composerFiles[cwd]
	if entry.loading || (!entry.loadedAt.IsZero() && now.Sub(entry.loadedAt) < composerFileCacheTTL) {
		a.panes.composerFilesMu.Unlock()
		return
	}
	entry.loading = true
	a.panes.composerFiles[cwd] = entry
	a.panes.composerFilesMu.Unlock()

	a.background(func(ctx context.Context) func(core.State) core.State {
		files := scanComposerFiles(ctx, cwd, composerMaxFiles, composerMaxDepth)
		return func(s core.State) core.State {
			a.panes.composerFilesMu.Lock()
			a.panes.composerFiles[cwd] = cachedFileList{files: files, loadedAt: a.now(), loading: false}
			a.panes.composerFilesMu.Unlock()
			if cm := a.activeRichComposer(); cm != nil && cm.cwd == cwd {
				cm.updateCompletions()
				a.viewMemo.gen++
			}
			return s
		}
	})
}

func scanComposerFiles(ctx context.Context, root string, maxFiles, maxDepth int) []string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	var out []string
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".hg": true, ".svn": true, ".cache": true,
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || len(out) >= maxFiles {
			return fs.SkipAll
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			depth := strings.Count(rel, string(filepath.Separator)) + 1
			if depth > maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(out)
	return out
}

func (a *App) cachedSkillCompletions(trigger string) []composerCompletion {
	a.panes.aiSkillsMu.RLock()
	skills := slices.Clone(a.panes.aiSkills)
	a.panes.aiSkillsMu.RUnlock()

	var out []composerCompletion
	seen := map[string]bool{}
	for _, s := range skills {
		name := strings.TrimLeft(s.Insert, "$/")
		if name == "" {
			continue
		}
		key := trigger + name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, composerCompletion{
			Trigger: trigger,
			Label:   key,
			Insert:  key,
			Detail:  s.Detail,
			Kind:    s.Kind,
		})
	}
	if trigger == "/" {
		for _, b := range builtinSlashCommands {
			if !seen[b.Label] {
				seen[b.Label] = true
				out = append(out, b)
			}
		}
	}
	return out
}

func (a *App) ensureAICatalogAsync() {
	a.panes.aiSkillsMu.Lock()
	if a.panes.aiSkillsLoaded || a.panes.aiSkillsLoading {
		a.panes.aiSkillsMu.Unlock()
		return
	}
	a.panes.aiSkillsLoading = true
	a.panes.aiSkillsMu.Unlock()

	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.cfg.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	storeBinary := ""
	if a.Store != nil {
		if rec, err := a.Store.GetByField(data.KindExtension, "name", "o-ai"); err == nil && rec != nil {
			storeBinary = rec.Text("binary")
		}
	}
	cache := a.ext.readCache

	a.background(func(ctx context.Context) func(core.State) core.State {
		var discovered []composerCompletion
		seen := map[string]bool{}
		add := func(name, detail, kind string) {
			name = strings.TrimSpace(name)
			if name == "" || seen[kind+":"+name] {
				return
			}
			seen[kind+":"+name] = true
			discovered = append(discovered, composerCompletion{
				Trigger: "$",
				Label:   "$" + name,
				Insert:  name,
				Detail:  detail,
				Kind:    kind,
			})
		}

		for _, spec := range []struct {
			kind  string
			label string
		}{{"ai.skill", "skill"}, {"ai.agent", "agent"}} {
			if cache != nil {
				if snap, ok := cache.Get(spec.kind); ok {
					for _, rec := range snap.Items {
						name := rec.Text("name")
						if name == "" {
							name = rec.Text("title")
						}
						if name == "" {
							name = rec.ID
						}
						detail := rec.Text("description")
						if detail == "" {
							detail = spec.label
						}
						add(name, detail, spec.label)
					}
				}
			}
		}

		if len(discovered) == 0 {
			if bin := findOAIBinary(dirs, storeBinary); bin != "" {
				for _, spec := range []struct {
					kind  string
					label string
				}{{"ai.skill", "skill"}, {"ai.agent", "agent"}} {
					pctx, cancel := context.WithTimeout(ctx, oaiDiscoverTimeout)
					raw, err := exec.CommandContext(pctx, bin, "query", "--kind", spec.kind).Output()
					cancel()
					if err != nil {
						continue
					}
					for _, item := range parseOAICatalogItems(raw, spec.label) {
						add(item.Insert, item.Detail, item.Kind)
					}
				}
			}
		}

		return func(s core.State) core.State {
			a.panes.aiSkillsMu.Lock()
			a.panes.aiSkills = discovered
			a.panes.aiSkillsLoaded = true
			a.panes.aiSkillsLoading = false
			a.panes.aiSkillsMu.Unlock()
			if cm := a.activeRichComposer(); cm != nil {
				cm.updateCompletions()
				a.viewMemo.gen++
			}
			return s
		}
	})
}

func parseOAICatalogItems(raw []byte, kindLabel string) []composerCompletion {
	var env struct {
		Items []struct {
			ID     string         `json:"id"`
			Name   string         `json:"name"`
			Title  string         `json:"title"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	out := make([]composerCompletion, 0, len(env.Items))
	for _, it := range env.Items {
		name := it.Name
		if name == "" && it.Fields != nil {
			name = data.Text(it.Fields["name"])
		}
		if name == "" && it.Fields != nil {
			name = data.Text(it.Fields["slug"])
		}
		if name == "" {
			name = it.Title
		}
		if name == "" {
			name = it.ID
		}
		detail := kindLabel
		if it.Fields != nil {
			if d := data.Text(it.Fields["description"]); d != "" {
				detail = d
			} else if d := data.Text(it.Fields["summary"]); d != "" {
				detail = d
			}
		}
		if name != "" {
			out = append(out, composerCompletion{
				Trigger: "$",
				Label:   "$" + name,
				Insert:  name,
				Detail:  detail,
				Kind:    kindLabel,
			})
		}
	}
	return out
}
