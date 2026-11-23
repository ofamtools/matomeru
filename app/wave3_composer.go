package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
)

const (
	ComposerModeSend      = "send"
	ComposerModeQueue     = "queue"
	ComposerModeInterrupt = "interrupt"

	defaultInterruptDelay = 120 * time.Millisecond
	composerMaxFiles      = 500
	composerMaxDepth      = 4
	composerFileCacheTTL  = 15 * time.Second
)

var composerDeliveryModes = []string{ComposerModeSend, ComposerModeQueue, ComposerModeInterrupt}

var builtinSlashCommands = []composerCompletion{
	{Trigger: "/", Label: "/plan", Insert: "/plan", Detail: "step-by-step planning", Kind: "command"},
	{Trigger: "/", Label: "/compact", Insert: "/compact", Detail: "compact conversation context", Kind: "command"},
	{Trigger: "/", Label: "/clear", Insert: "/clear", Detail: "clear conversation", Kind: "command"},
	{Trigger: "/", Label: "/review", Insert: "/review", Detail: "review changes", Kind: "command"},
	{Trigger: "/", Label: "/help", Insert: "/help", Detail: "show available commands", Kind: "command"},
}

type composerCompletion struct {
	Trigger string // "@", "$", "/"
	Label   string
	Insert  string
	Detail  string
	Kind    string // "file", "skill", "agent", "command"
}

type cachedFileList struct {
	files    []string
	loadedAt time.Time
	loading  bool
}

type composerState struct {
	paneID      string
	paneTitle   string
	cwd         string
	in          *input.Model
	delivery    string
	draftKey    string
	draftIdx    int // -1 = live buffer, 0..N-1 = draft from DraftStore
	liveStash   string
	returnMode  string
	returnFocus string

	compTrigger string
	compStart   int // rune offset of trigger character
	compQuery   string
	compItems   []composerCompletion
	compSel     int
	compOpen    bool
}

func composerDraftKey(paneID string) string {
	return "composer:" + paneID
}

func bracketedPasteBytes(text string) []byte {
	return []byte("\x1b[200~" + text + "\x1b[201~\r")
}

// SetComposerSkills sets cached skills/agents for composer $ and / completion
// (useful for testing or external refresh without blocking the UI).
func (a *App) SetComposerSkills(items []composerCompletion) {
	a.aiSkillsMu.Lock()
	a.aiSkills = slices.Clone(items)
	a.aiSkillsLoaded = true
	a.aiSkillsLoading = false
	a.aiSkillsMu.Unlock()
	if a.composer != nil {
		a.updateComposerCompletions()
	}
	a.gen++
}

func (a *App) queuedPromptCount(paneID string) int {
	a.queuedMu.Lock()
	defer a.queuedMu.Unlock()
	return len(a.queuedPrompts[paneID])
}

func (a *App) queuedPromptsFor(paneID string) []string {
	a.queuedMu.Lock()
	defer a.queuedMu.Unlock()
	return slices.Clone(a.queuedPrompts[paneID])
}

// paneCompose opens the on-demand multi-line prompt composer overlay for the
// selected pane.
func (a *App) paneCompose(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	in := input.New("", true)
	in.HandleKey("i") // start in insert mode ready to draft immediately

	curFrame := s.ActiveFrame()
	cs := &composerState{
		paneID:      pane.ID,
		paneTitle:   views.Title(a.schema(data.KindPane), *pane),
		cwd:         expandHome(pane.Text("cwd")),
		in:          in,
		delivery:    ComposerModeSend,
		draftKey:    composerDraftKey(pane.ID),
		draftIdx:    -1,
		returnMode:  curFrame.Mode,
		returnFocus: curFrame.FocusID,
	}
	a.composer = cs

	// Kick off non-blocking async prefetch for @file and $skill / /command.
	if cs.cwd != "" {
		a.ensureComposerFilesAsync(cs.cwd)
	}
	a.ensureAICatalogAsync()

	a.say("composer (%s) · ctrl+s submits · shift+tab mode · ctrl+p/n drafts · @file $skill /cmd", cs.delivery)
	return core.Next(openModal(s, modalComposer, ModeComposer, modalComposer+".input"))
}

// saveComposerDraft saves non-empty prompt text into the persistent DraftStore.
func (a *App) saveComposerDraft(cs *composerState) bool {
	if cs == nil || cs.in == nil || cs.draftKey == "" {
		return false
	}
	val := strings.TrimSpace(cs.in.Value())
	if val == "" {
		return false
	}
	if a.drafts == nil {
		a.drafts = form.NewDraftStore()
	}
	label := clip(strings.ReplaceAll(val, "\n", " "), 40)
	if !a.drafts.Save(cs.draftKey, map[string]any{"prompt": cs.in.Value(), "mode": cs.delivery}, label, a.now()) {
		return false
	}
	a.saveDrafts()
	return true
}

func setInputText(in *input.Model, value string) *input.Model {
	fresh := input.New(value, true)
	fresh.HandleKey("i")
	// Move cursor to end of text.
	rs := []rune(value)
	if len(rs) > 0 {
		fresh = input.New("", true)
		fresh.HandleKey("i")
		fresh.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
	}
	return fresh
}

func (a *App) composerCycleDraft(delta int) {
	cs := a.composer
	if cs == nil || a.drafts == nil {
		return
	}
	drafts := a.drafts.List(cs.draftKey)
	if len(drafts) == 0 {
		a.say("no saved drafts for this pane")
		return
	}
	if cs.draftIdx < 0 {
		if delta < 0 {
			a.say("already on current draft")
			return
		}
		cs.liveStash = cs.in.Value()
		cs.draftIdx = 0
	} else {
		nextIdx := cs.draftIdx + delta
		if nextIdx < 0 {
			cs.draftIdx = -1
			cs.in = setInputText(cs.in, cs.liveStash)
			a.updateComposerCompletions()
			a.say("back to current edits")
			return
		}
		if nextIdx >= len(drafts) {
			nextIdx = len(drafts) - 1
		}
		cs.draftIdx = nextIdx
	}
	d := drafts[cs.draftIdx]
	text := data.Text(d.Values["prompt"])
	if m := data.Text(d.Values["mode"]); slices.Contains(composerDeliveryModes, m) {
		cs.delivery = m
	}
	cs.in = setInputText(cs.in, text)
	a.updateComposerCompletions()
	a.say("draft %d of %d", cs.draftIdx+1, len(drafts))
}

func (a *App) composerCycleDeliveryMode() {
	cs := a.composer
	if cs == nil {
		return
	}
	i := slices.Index(composerDeliveryModes, cs.delivery)
	cs.delivery = composerDeliveryModes[(i+1)%len(composerDeliveryModes)]
	a.say("delivery mode: %s", cs.delivery)
}

// composerCommand handles all commands while modalComposer is open in ModeComposer.
func (a *App) composerCommand(s core.State, c core.Command) core.StepResult {
	cs := a.composer
	if cs == nil || topModal(s) != modalComposer {
		return core.Next(s)
	}

	switch c.ID {
	case CmdComposerCancel:
		if cs.compOpen {
			cs.compOpen = false
			cs.compItems = nil
			return core.Next(s)
		}
		saved := a.saveComposerDraft(cs)
		a.composer = nil
		if saved {
			a.say("composer closed · draft saved (ctrl+p restores)")
		} else {
			a.say("composer closed")
		}
		return core.Next(closeModal(s, modalComposer))

	case CmdComposerSubmit:
		text := strings.TrimSpace(cs.in.Value())
		if text == "" {
			a.say("prompt is empty")
			return core.Next(s)
		}
		pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: cs.paneID})
		if err != nil {
			a.fail("composer pane", err)
			return core.Next(s)
		}
		mode := cs.delivery
		if cs.draftIdx >= 0 && a.drafts != nil {
			if a.drafts.Remove(cs.draftKey, cs.draftIdx) {
				a.saveDrafts()
			}
		}
		a.composer = nil
		s = closeModal(s, modalComposer)
		a.deliverComposerPrompt(*pane, cs.in.Value(), mode)
		return core.Next(s)

	case CmdComposerModeCycle:
		a.composerCycleDeliveryMode()
		return core.Next(s)

	case CmdComposerComplete:
		if cs.compOpen && len(cs.compItems) > 0 {
			a.acceptComposerCompletion()
			return core.Next(s)
		}
		a.composerCycleDeliveryMode()
		return core.Next(s)

	case CmdComposerEnter:
		if cs.compOpen && len(cs.compItems) > 0 {
			a.acceptComposerCompletion()
			return core.Next(s)
		}
		cs.in.HandleKey("enter")
		a.updateComposerCompletions()
		return core.Next(s)

	case CmdComposerInsert:
		txt := c.Arg("text")
		if txt != "" {
			cs.in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": txt}})
			a.updateComposerCompletions()
		}
		return core.Next(s)

	case CmdComposerBackspace:
		cs.in.HandleKey("backspace")
		a.updateComposerCompletions()
		return core.Next(s)

	case CmdComposerUp:
		if cs.compOpen && len(cs.compItems) > 0 {
			cs.compSel = (cs.compSel - 1 + len(cs.compItems)) % len(cs.compItems)
			return core.Next(s)
		}
		a.composerMoveVertical(-1)
		return core.Next(s)

	case CmdComposerDown:
		if cs.compOpen && len(cs.compItems) > 0 {
			cs.compSel = (cs.compSel + 1) % len(cs.compItems)
			return core.Next(s)
		}
		a.composerMoveVertical(1)
		return core.Next(s)

	case CmdComposerDraftPrev:
		if cs.compOpen && len(cs.compItems) > 0 {
			cs.compSel = (cs.compSel - 1 + len(cs.compItems)) % len(cs.compItems)
			return core.Next(s)
		}
		a.composerCycleDraft(1)
		return core.Next(s)

	case CmdComposerDraftNext:
		if cs.compOpen && len(cs.compItems) > 0 {
			cs.compSel = (cs.compSel + 1) % len(cs.compItems)
			return core.Next(s)
		}
		a.composerCycleDraft(-1)
		return core.Next(s)

	default:
		if input.IsReadlineCommand(c.ID) {
			cs.in.HandleCommand(c)
			a.updateComposerCompletions()
		}
		return core.Next(s)
	}
}

func (a *App) composerMoveVertical(delta int) {
	cs := a.composer
	if cs == nil || cs.in == nil {
		return
	}
	// Briefly step via normal-mode vertical motion then return to insert mode.
	cs.in.HandleKey("esc")
	if delta < 0 {
		cs.in.HandleKey("k")
	} else {
		cs.in.HandleKey("j")
	}
	cs.in.HandleKey("i")
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
	if a.Host != nil && a.hasSession(pane.ID) {
		if err := a.Host.Write(pane.ID, bracketedPasteBytes(text)); err != nil {
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
	a.queuedMu.Lock()
	if a.queuedPrompts == nil {
		a.queuedPrompts = map[string][]string{}
	}
	a.queuedPrompts[pane.ID] = append(a.queuedPrompts[pane.ID], text)
	count := len(a.queuedPrompts[pane.ID])
	a.queuedMu.Unlock()

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
	a.queuedMu.Lock()
	list := a.queuedPrompts[paneID]
	if len(list) == 0 {
		a.queuedMu.Unlock()
		return 0
	}
	delete(a.queuedPrompts, paneID)
	a.queuedMu.Unlock()

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
	a.queuedMu.Lock()
	ids := make([]string, 0, len(a.queuedPrompts))
	for id, list := range a.queuedPrompts {
		if len(list) > 0 {
			ids = append(ids, id)
		}
	}
	a.queuedMu.Unlock()
	delivered := 0
	for _, id := range ids {
		delivered += a.flushQueuedPrompts(id)
	}
	return delivered
}

func (a *App) deliverPromptInterrupt(pane data.Record, text string) {
	a.escapes++
	if a.Host != nil && a.hasSession(pane.ID) {
		if err := a.Host.Write(pane.ID, []byte{0x1b}); err != nil {
			a.fail("interrupt pane", err)
			return
		}
		delay := a.interruptDelay
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
			_ = a.Host.Write(pane.ID, bracketedPasteBytes(text))
			return func(s core.State) core.State {
				if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
					a.fail("record prompt", err)
				}
				a.harvestPrompt(pane, text)
				a.say("interrupted and sent prompt to %s", views.Title(a.schema(data.KindPane), pane))
				a.gen++
				return s
			}
		})
		return
	}
	// Synthetic pane: record immediately.
	a.deliverPromptNow(pane, text, "interrupted and sent")
}

// --- Async @file / $skill / /command completion --------------------------------

func detectComposerTrigger(value string, cursor int) (trigger string, start int, query string, ok bool) {
	rs := []rune(value)
	ci := min(max(cursor, 0), len(rs))
	// Scan backward to whitespace or start of text.
	i := ci
	for i > 0 && !unicode.IsSpace(rs[i-1]) {
		i--
	}
	if i >= ci {
		return "", 0, "", false
	}
	first := rs[i]
	if first != '@' && first != '$' && first != '/' {
		return "", 0, "", false
	}
	// "/" only triggers at start of line or after whitespace (avoid matching paths like foo/bar).
	return string(first), i, string(rs[i+1 : ci]), true
}

func (a *App) updateComposerCompletions() {
	cs := a.composer
	if cs == nil || cs.in == nil {
		return
	}
	trig, start, query, ok := detectComposerTrigger(cs.in.Value(), cs.in.Cursor())
	if !ok {
		cs.compTrigger, cs.compStart, cs.compQuery, cs.compItems, cs.compSel, cs.compOpen = "", 0, "", nil, 0, false
		return
	}
	cs.compTrigger, cs.compStart, cs.compQuery = trig, start, query

	var candidates []composerCompletion
	switch trig {
	case "@":
		if cs.cwd != "" {
			a.ensureComposerFilesAsync(cs.cwd)
			candidates = a.cachedFileCompletions(cs.cwd)
		}
	case "$":
		a.ensureAICatalogAsync()
		candidates = a.cachedSkillCompletions("$")
	case "/":
		a.ensureAICatalogAsync()
		candidates = a.cachedSkillCompletions("/")
	}

	lq := strings.ToLower(query)
	var filtered []composerCompletion
	for _, c := range candidates {
		if lq == "" || strings.Contains(strings.ToLower(c.Label), lq) || strings.Contains(strings.ToLower(c.Detail), lq) {
			filtered = append(filtered, c)
			if len(filtered) >= 12 {
				break
			}
		}
	}
	cs.compItems = filtered
	cs.compOpen = len(filtered) > 0
	if cs.compSel >= len(filtered) {
		cs.compSel = 0
	}
}

func (a *App) acceptComposerCompletion() {
	cs := a.composer
	if cs == nil || !cs.compOpen || len(cs.compItems) == 0 {
		return
	}
	item := cs.compItems[min(max(cs.compSel, 0), len(cs.compItems)-1)]
	rs := []rune(cs.in.Value())
	ci := min(max(cs.in.Cursor(), 0), len(rs))
	start := min(max(cs.compStart, 0), ci)
	replacement := item.Insert + " "
	nextText := string(rs[:start]) + replacement + string(rs[ci:])
	nextCursor := start + len([]rune(replacement))

	fresh := input.New("", true)
	fresh.HandleKey("i")
	fresh.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": nextText}})
	// Position cursor right after replacement if there was trailing text.
	if tailLen := len([]rune(nextText)) - nextCursor; tailLen > 0 {
		for range tailLen {
			fresh.HandleCommand(core.Command{ID: input.CmdCharLeft})
		}
	}
	cs.in = fresh
	cs.compOpen = false
	cs.compItems = nil
	cs.compSel = 0
}

func (a *App) cachedFileCompletions(cwd string) []composerCompletion {
	a.composerFilesMu.RLock()
	entry := a.composerFiles[cwd]
	a.composerFilesMu.RUnlock()
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
	a.composerFilesMu.Lock()
	if a.composerFiles == nil {
		a.composerFiles = map[string]cachedFileList{}
	}
	entry := a.composerFiles[cwd]
	if entry.loading || (!entry.loadedAt.IsZero() && now.Sub(entry.loadedAt) < composerFileCacheTTL) {
		a.composerFilesMu.Unlock()
		return
	}
	entry.loading = true
	a.composerFiles[cwd] = entry
	a.composerFilesMu.Unlock()

	a.background(func(ctx context.Context) func(core.State) core.State {
		files := scanComposerFiles(ctx, cwd, composerMaxFiles, composerMaxDepth)
		return func(s core.State) core.State {
			a.composerFilesMu.Lock()
			a.composerFiles[cwd] = cachedFileList{files: files, loadedAt: a.now(), loading: false}
			a.composerFilesMu.Unlock()
			if a.composer != nil && a.composer.cwd == cwd {
				a.updateComposerCompletions()
				a.gen++
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
	a.aiSkillsMu.RLock()
	skills := slices.Clone(a.aiSkills)
	a.aiSkillsMu.RUnlock()

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
	a.aiSkillsMu.Lock()
	if a.aiSkillsLoaded || a.aiSkillsLoading {
		a.aiSkillsMu.Unlock()
		return
	}
	a.aiSkillsLoading = true
	a.aiSkillsMu.Unlock()

	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	storeBinary := ""
	if a.Store != nil {
		if rec, err := a.Store.GetByField(data.KindExtension, "name", "o-ai"); err == nil && rec != nil {
			storeBinary = rec.Text("binary")
		}
	}
	cache := a.readCache

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

		// 1. Read from ReadCache first when available.
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

		// 2. Otherwise query o-ai asynchronously without ever blocking the UI loop.
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
			a.aiSkillsMu.Lock()
			a.aiSkills = discovered
			a.aiSkillsLoaded = true
			a.aiSkillsLoading = false
			a.aiSkillsMu.Unlock()
			if a.composer != nil {
				a.updateComposerCompletions()
				a.gen++
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

// composerModal renders the prompt composer overlay with multi-line input,
// delivery mode tabs, draft badge, and @file/$skill//command completion drawer.
func (a *App) composerModal() ui.ViewNode {
	cs := a.composer
	if cs == nil {
		return overlay(modalComposer, "Compose Prompt", 78, 60, text("", "no composer active", "muted"))
	}

	var modeParts []string
	for _, m := range composerDeliveryModes {
		if m == cs.delivery {
			modeParts = append(modeParts, "[● "+m+"]")
		} else {
			modeParts = append(modeParts, "  "+m+" ")
		}
	}
	modeBar := line(modalComposer+".modes", " Delivery: "+strings.Join(modeParts, "  ")+"   (shift+tab cycles)", "accent")

	inNode := cs.in.View(modalComposer + ".input")
	inNode.Border = true
	inNode.Title = "Prompt"
	inNode.Props["placeholder"] = "Type prompt… (@file, $skill, /command · enter newline · ctrl+s submit)"
	inNode.Box = layout.Constraints{Flex: 1, MinH: 6}

	children := []ui.ViewNode{modeBar, inNode}

	if cs.compOpen && len(cs.compItems) > 0 {
		var compLines []ui.ViewNode
		for i, item := range cs.compItems {
			prefix := "  "
			tone := ""
			if i == cs.compSel {
				prefix = "▸ "
				tone = "accent"
			}
			lbl := fmt.Sprintf("%s%-24s %s", prefix, item.Label, clip(item.Detail, 36))
			compLines = append(compLines, line(fmt.Sprintf("%s.comp.%d", modalComposer, i), lbl, tone))
		}
		compPanel := ui.ViewNode{
			ID:       modalComposer + ".completions",
			Kind:     ui.KindPanel,
			Title:    fmt.Sprintf("Completions (%s)", cs.compTrigger),
			Border:   true,
			Box:      layout.Constraints{FixedH: min(len(compLines)+2, 8)},
			Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: compLines}},
		}
		children = append(children, compPanel)
	}

	helpBar := line(modalComposer+".help", " ctrl+s send/submit · shift+tab mode · ctrl+p/n drafts · tab complete · esc close & save draft", "muted")
	children = append(children, helpBar)

	var badges []ui.Badge
	badges = append(badges, ui.Badge{Text: cs.delivery, Tone: "accent"})
	if qn := a.queuedPromptCount(cs.paneID); qn > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d queued", qn), Tone: "warning"})
	}
	if a.drafts != nil {
		if n := len(a.drafts.List(cs.draftKey)); n > 0 {
			if cs.draftIdx >= 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("draft %d/%d", cs.draftIdx+1, n), Tone: "warning"})
			} else {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d drafts", n), Tone: "info"})
			}
		}
	}

	ov := overlay(modalComposer, "Compose · "+cs.paneTitle, 80, 70, ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: children})
	ov.Scope = "composer"
	ov.Badges = badges
	return ov
}
