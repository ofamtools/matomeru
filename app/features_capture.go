package app

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

const matomeruGoModNeedle = "module ofam.dev/matomeru"

// --- Conditional Checkout Detection ---------------------------------------------

func expandHomePath(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "" || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

func (a *App) featuresHomeDir() string {
	if a.opts.FS != nil {
		home, _ := a.opts.FS.HomeDir()
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}

func (a *App) probeMatomeruDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	goMod := filepath.Join(dir, "go.mod")
	if a.opts.FS != nil {
		entries, err := a.opts.FS.ReadDir(dir)
		if err != nil {
			return false
		}
		hasGoMod := false
		for _, e := range entries {
			if !e.IsDir && e.Name == "go.mod" {
				hasGoMod = true
				break
			}
		}
		if !hasGoMod {
			return false
		}
		if a.opts.ReadFile != nil {
			if b, rerr := a.opts.ReadFile(goMod); rerr == nil {
				return strings.Contains(string(b[:min(len(b), 512)]), matomeruGoModNeedle)
			}
		}
		preview, perr := a.opts.FS.ReadPreview(goMod, 512)
		return perr == nil && strings.Contains(preview, matomeruGoModNeedle)
	}
	if a.opts.ReadFile != nil {
		b, err := a.opts.ReadFile(goMod)
		return err == nil && strings.Contains(string(b[:min(len(b), 512)]), matomeruGoModNeedle)
	}
	f, err := os.Open(goMod)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 512))
	return err == nil && strings.Contains(string(b), matomeruGoModNeedle)
}

func (a *App) findMatomeruCheckout(repoPath string) (bool, string) {
	home := a.featuresHomeDir()
	if repoPath = strings.TrimSpace(repoPath); repoPath != "" {
		cand := expandHomePath(repoPath, home)
		if a.probeMatomeruDir(cand) {
			return true, cand
		}
		return false, ""
	}
	if home == "" {
		return false, ""
	}
	for _, rel := range [][]string{{"proj", "matomeru"}, {"src", "matomeru"}, {"code", "matomeru"}, {"matomeru"}} {
		cand := filepath.Join(append([]string{home}, rel...)...)
		if a.probeMatomeruDir(cand) {
			return true, cand
		}
	}
	return false, ""
}

// detectMatomeruCheckout resolves whether the self-hosted Features page is
// active and the detected (or configured) local Matomeru checkout path.
func (a *App) detectMatomeruCheckout() (bool, string) {
	if a == nil {
		return false, ""
	}
	enabled, repoPath := SelfHostedFeaturesAuto, ""
	if a.cfg != nil && a.cfg.Settings != nil {
		enabled = strings.ToLower(strings.TrimSpace(a.cfg.Settings.String(SetSelfHostedFeaturesEnabled, SelfHostedFeaturesAuto)))
		repoPath = strings.TrimSpace(a.cfg.Settings.String(SetSelfHostedFeaturesRepoPath, ""))
	}
	if enabled == "" {
		enabled = SelfHostedFeaturesAuto
	}
	fc := a.featuresCtrl()
	if fc.detectCached && fc.detectEnabledKey == enabled && fc.detectRepoPath == repoPath {
		return fc.detectActive, fc.detectResolved
	}

	var active bool
	var resolved string
	switch enabled {
	case SelfHostedFeaturesOff:
		active, resolved = false, ""
	case SelfHostedFeaturesOn:
		active = true
		if found, p := a.findMatomeruCheckout(repoPath); found {
			resolved = p
		} else if repoPath != "" {
			resolved = expandHomePath(repoPath, a.featuresHomeDir())
		}
	default:
		active, resolved = a.findMatomeruCheckout(repoPath)
	}

	fc.detectCached = true
	fc.detectEnabledKey = enabled
	fc.detectRepoPath = repoPath
	fc.detectActive = active
	fc.detectResolved = resolved
	return active, resolved
}

func (a *App) featuresPageActive() bool {
	active, _ := a.detectMatomeruCheckout()
	return active
}

// --- Command Specs, Bindings & Handlers -----------------------------------------

func featuresPageSpecs() []core.CommandSpec {
	sh := func(id, desc string, tags ...string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: tags, Meta: map[string]any{"selfHosted": true}}
	}
	return []core.CommandSpec{
		{
			ID:          CmdFeaturesNavSelect,
			Description: "Activate the focused Features navigation item or toggle a group header",
			Params:      []core.ParamSpec{{Name: "key", Type: core.ParamString, Description: "optional nav item key"}},
			Meta:        map[string]any{"selfHosted": true},
		},
		{
			ID:          CmdFeaturesNavGroupToggle,
			Description: "Collapse or expand the focused Features navigation group",
			Params:      []core.ParamSpec{{Name: "group", Type: core.ParamString, Description: "optional nav group key (pages, nouns, groups)"}},
			Meta:        map[string]any{"selfHosted": true},
		},
		sh(CmdFeaturesQuickCapture, "Quick-capture a raw feature thought into the Features Wishlist outline", "editing"),
		sh(CmdFeaturesCaptureFocus, "Focus the Quick Capture (Features Wishlist) outline pane"),
		sh(CmdFeaturesCaptureToggle, "Toggle the Quick Capture pane between docked and expanded layout"),
		sh(CmdFeaturesItemOpen, "Open the focused linked feature group, lane, or raw thought in 2 Work"),
	}
}

func featuresPageBindings() []core.Binding {
	n, active := ModeNormal, "facts.featuresPageActive"
	return []core.Binding{
		when(bind(n, views.ScopeFeaturesNav, "enter", CmdFeaturesNavSelect, "open / toggle"), active),
		when(bind(n, views.ScopeFeaturesNav, "space", CmdFeaturesNavGroupToggle, "toggle group"), active),
		when(bind(n, views.ScopeFeaturesPage, "n", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesNav, "o", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesNav, "a", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesNav, "i", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesContent, "o", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesContent, "a", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesContent, "i", CmdFeaturesQuickCapture, "quick capture"), active),
		when(bind(n, views.ScopeFeaturesCapture, "o", CmdFeaturesQuickCapture, "quick capture"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesCapture, "a", CmdFeaturesQuickCapture, "quick capture"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesCapture, "i", CmdFeaturesQuickCapture, "quick capture"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesNav, "c", CmdFeaturesCaptureFocus, "focus capture"), active),
		when(bind(n, views.ScopeFeaturesContent, "c", CmdFeaturesCaptureFocus, "focus capture"), active),
		when(bind(n, views.ScopeFeaturesCapture, "c", CmdStatusCycle, "cycle status"), active+" && facts.hasStatus"),
		when(bind(n, views.ScopeFeaturesPage, "z", CmdFeaturesCaptureToggle, "expand capture"), active),
		when(bind(n, views.ScopeFeaturesContent, "enter", CmdFeaturesItemOpen, "open item"), active),
	}
}

func (a *App) registerFeaturesPageHandlers() {
	a.handle(CmdFeaturesNavSelect, a.featuresNavSelect)
	a.handle(CmdFeaturesNavGroupToggle, a.featuresNavGroupToggle)
	a.handle(CmdFeaturesQuickCapture, a.featuresQuickCapture)
	a.handle(CmdFeaturesCaptureFocus, a.featuresCaptureFocus)
	a.handle(CmdFeaturesCaptureToggle, a.featuresCaptureToggle)
	a.handle(CmdFeaturesItemOpen, a.featuresItemOpen)
}

// --- Quick Capture Outline & Handlers -------------------------------------------

func (a *App) inFeaturesCaptureTree(s *core.State) bool {
	return a != nil && s != nil && s.ActivePage == SpaceFeatures && s.ActiveFrame().FocusID == featuresCaptureTree && a.Modal() == ""
}

func (a *App) featuresWishlistTaskID() string {
	if rec := a.findFeaturesRootTask(FeaturesWishlistTitle); rec != nil {
		return rec.ID
	}
	return ""
}

func (a *App) ensureFeaturesWishlistTask() string {
	if id := a.featuresWishlistTaskID(); id != "" {
		return id
	}
	if a.Store == nil {
		return ""
	}
	rec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":       FeaturesWishlistTitle,
			"description": "Immutable raw feature thoughts captured from the self-hosted Features page.",
			"workStatus":  "active",
		},
	}, User)
	if err != nil || rec == nil {
		if err != nil {
			a.fail("create "+FeaturesWishlistTitle, err)
		}
		return ""
	}
	return rec.ID
}

func (a *App) findFeaturesRootTask(title string) *data.Record {
	if a.Store == nil || title == "" {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindTask, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(title))
	var fallback *data.Record
	for i := range res.Items {
		rec := &res.Items[i]
		if strings.ToLower(strings.TrimSpace(rec.Text("title"))) != want {
			continue
		}
		if _, hasParent := rec.RefField("parent"); !hasParent {
			return rec
		}
		if fallback == nil {
			fallback = rec
		}
	}
	return fallback
}

func (a *App) syncFeaturesCaptureTree(tm *tree.Model, wishlistID string) (data.Schema, []data.TreeNode) {
	schema, nodes := a.syncProjectTaskTree(tm, wishlistID, featuresCaptureTree)
	if cs := a.activeCleanSlate(data.KindTask, featuresCaptureTree); cs != nil && len(tm.Nodes) > 0 && tm.Nodes[0].ID == wishlistID {
		filtered := make([]tree.Node, 0, len(tm.Nodes[0].Children))
		for _, ch := range tm.Nodes[0].Children {
			if cs.roots != nil && cs.roots[ch.ID] {
				continue
			}
			filtered = append(filtered, ch)
		}
		tm.Nodes[0].Children = filtered
		tm.SetNodes(tm.Nodes)
		a.saveTreeFolded(data.KindTask, featuresCaptureTree, tm)
	}
	return schema, nodes
}

func (a *App) featuresCaptureTreeMove(_ core.State, c core.Command) {
	wid := a.featuresWishlistTaskID()
	if wid == "" {
		return
	}
	tm := a.trees.projectModel(wid)
	a.syncFeaturesCaptureTree(tm, wid)
	tm.HandleCommand(c)
	a.saveTreeFolded(data.KindTask, featuresCaptureTree, tm)
}

func (a *App) featuresCaptureNode(wishlistRoot *data.Record) ui.ViewNode {
	if wishlistRoot == nil {
		return ui.ViewNode{
			ID:     featuresCapturePanel,
			Kind:   ui.KindPanel,
			Scope:  views.ScopeFeaturesCapture,
			Title:  featuresCaptureTitle,
			Border: true,
			Badges: []ui.Badge{{Text: "0 thoughts", Tone: "muted"}},
			Facts:  map[string]any{"isTree": false, "empty": true, "featuresCapture": true},
			Children: []ui.ViewNode{{
				ID:        featuresCaptureTree,
				Kind:      ui.KindText,
				Scope:     views.ScopeFeaturesCapture,
				Focusable: true,
				Tone:      "muted",
				Facts:     map[string]any{"isTree": false, "empty": true, "featuresCapture": true},
				Props:     map[string]any{"text": "No wishlist thoughts yet. Press n or o to capture a raw thought, or b for clean slate."},
			}},
		}
	}

	wid := wishlistRoot.ID
	tm := a.trees.projectModel(wid)
	schema, _ := a.syncFeaturesCaptureTree(tm, wid)
	csActive := a.isCleanSlateActive(data.KindTask, featuresCaptureTree)

	var extra []ui.Badge
	if csActive {
		extra = append(extra, ui.Badge{Text: "clean slate", Tone: "accent"})
	}
	if a.nav.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeViewWith(schema, tm, featuresCapturePanel, featuresCaptureTree, views.ScopeIndex, a.stickyTreeBreadcrumbsEnabled(), extra...)
	panel.Title = featuresCaptureTitle

	subHoisted := len(tm.HoistStack) > 1 || (tm.Hoisted() && tm.HoistID() != wid)
	filtered := make([]ui.Badge, 0, len(panel.Badges))
	for _, b := range panel.Badges {
		if (strings.HasPrefix(b.Text, "hoist: ") && !subHoisted) || b.Text == "1 project" || b.Text == "0 projects" {
			continue
		}
		filtered = append(filtered, b)
	}
	panel.Badges = filtered

	applyFacts := func(n *ui.ViewNode) {
		if n.Facts == nil {
			n.Facts = map[string]any{}
		}
		n.Facts["isTree"] = true
		n.Facts["featuresCapture"] = true
		n.Facts["cleanSlate"] = csActive
		n.Facts["hoisted"] = subHoisted
	}
	applyFacts(&panel)
	if len(panel.Children) > 0 {
		applyFacts(&panel.Children[0])
	}
	return panel
}

func (a *App) featuresNavSelect(s core.State, c core.Command) core.StepResult {
	fc := a.featuresCtrl()
	if key := strings.TrimSpace(c.Arg("key")); key != "" {
		fc.activeKey = key
		return core.Next(withFocus(s, views.FeaturesNavPrefix+key))
	}
	focus := s.ActiveFrame().FocusID
	if groupKey, ok := strings.CutPrefix(focus, views.FeaturesGroupPrefix); ok && groupKey != "" {
		return a.toggleFeaturesNavGroup(s, groupKey)
	}
	if itemKey, ok := strings.CutPrefix(focus, views.FeaturesNavPrefix); ok && itemKey != "" {
		fc.activeKey = itemKey
		for _, nd := range a.focusables(s) {
			if nd.region == "features.content" {
				return core.Next(withFocus(s, nd.id))
			}
		}
		a.say("selected %s", itemKey)
	}
	return core.Next(s)
}

func (a *App) toggleFeaturesNavGroup(s core.State, groupKey string) core.StepResult {
	fc := a.featuresCtrl()
	if fc.collapsedGroups == nil {
		fc.collapsedGroups = map[string]bool{}
	}
	fc.collapsedGroups[groupKey] = !fc.collapsedGroups[groupKey]
	if fc.collapsedGroups[groupKey] {
		a.say("collapsed %s", groupKey)
	} else {
		a.say("expanded %s", groupKey)
	}
	return core.Next(s)
}

func (a *App) featuresNavGroupToggle(s core.State, c core.Command) core.StepResult {
	if g := strings.TrimSpace(c.Arg("group")); g != "" {
		return a.toggleFeaturesNavGroup(s, g)
	}
	focus := s.ActiveFrame().FocusID
	if groupKey, ok := strings.CutPrefix(focus, views.FeaturesGroupPrefix); ok && groupKey != "" {
		return a.toggleFeaturesNavGroup(s, groupKey)
	}
	if itemKey, ok := strings.CutPrefix(focus, views.FeaturesNavPrefix); ok && itemKey != "" {
		switch {
		case strings.HasPrefix(itemKey, "page:"):
			return a.toggleFeaturesNavGroup(s, "pages")
		case strings.HasPrefix(itemKey, "noun:"):
			return a.toggleFeaturesNavGroup(s, "nouns")
		case strings.HasPrefix(itemKey, "group:"):
			return a.toggleFeaturesNavGroup(s, "groups")
		}
	}
	a.say("focus a navigation group to toggle")
	return core.Next(s)
}

func (a *App) featuresQuickCapture(s core.State, _ core.Command) core.StepResult {
	wid := a.ensureFeaturesWishlistTask()
	if wid == "" {
		a.say("could not initialize %s", FeaturesWishlistTitle)
		return core.Next(s)
	}
	tm := a.trees.projectModel(wid)
	a.syncFeaturesCaptureTree(tm, wid)
	s = withPage(s, SpaceFeatures, featuresCaptureTree, ModeNormal)
	a.runTreeStartAdd(tm, data.KindTask, featuresCaptureTree, false, ModeNormal, false)
	return core.Next(withMode(s, ModeOutlineEdit))
}

func (a *App) featuresCaptureFocus(s core.State, _ core.Command) core.StepResult {
	a.say("focused quick capture · n/o add · b clean slate")
	return core.Next(withFocus(s, featuresCaptureTree))
}

func (a *App) featuresCaptureToggle(s core.State, _ core.Command) core.StepResult {
	fc := a.featuresCtrl()
	fc.captureExpanded = !fc.captureExpanded
	if fc.captureExpanded {
		a.say("quick capture expanded")
	} else {
		a.say("quick capture docked")
	}
	return core.Next(s)
}

func (a *App) featuresItemOpen(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("no linked item focused")
		return core.Next(s)
	}
	return core.Next(a.show(s, ref))
}

// --- Data Collection & Provenance Resolution ------------------------------------

type featuresSnapshot struct {
	wishlistRoot  *data.Record
	rawThoughts   []data.Record
	groupsRoot    *data.Record
	featureGroups []data.Record
	lanes         []data.Record
	specs         []core.CommandSpec
	keysByID      map[string]string
}

func (a *App) actionKeysMap() map[string]string {
	bindings := DefaultBindings()
	if a != nil && a.bind != nil && len(a.bind.bindings) > 0 {
		bindings = a.bind.bindings
	}
	grouped := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, b := range bindings {
		if b.Unbind || b.Command == "" || len(b.Keys) == 0 {
			continue
		}
		seq := strings.Join(b.Keys, " ")
		if b.Mode != "" && b.Mode != ModeNormal {
			seq = b.Mode + ": " + seq
		}
		if seen[b.Command] == nil {
			seen[b.Command] = map[string]bool{}
		}
		if !seen[b.Command][seq] {
			seen[b.Command][seq] = true
			grouped[b.Command] = append(grouped[b.Command], seq)
		}
	}
	out := make(map[string]string, len(grouped))
	for cmd, ks := range grouped {
		out[cmd] = strings.Join(ks, " / ")
	}
	return out
}

func collectDescendants(nodes []data.TreeNode) []data.Record {
	var out []data.Record
	var walk func(ns []data.TreeNode)
	walk = func(ns []data.TreeNode) {
		for _, nd := range ns {
			out = append(out, nd.Record)
			walk(nd.Children)
		}
	}
	walk(nodes)
	return out
}

func (a *App) collectFeaturesSnapshot() featuresSnapshot {
	snap := featuresSnapshot{specs: CommandSpecs(), keysByID: a.actionKeysMap()}
	if a.Store == nil {
		return snap
	}
	snap.wishlistRoot = a.findFeaturesRootTask(FeaturesWishlistTitle)
	snap.groupsRoot = a.findFeaturesRootTask(FeaturesGroupsTitle)
	allTree, _ := a.recordTreeNodes(data.KindTask)
	if snap.wishlistRoot != nil {
		if nd, ok := findProjectTreeNode(allTree, snap.wishlistRoot.ID); ok {
			snap.rawThoughts = collectDescendants(nd.Children)
		}
	}
	seenGroups := map[string]bool{}
	addGroup := func(rec data.Record) {
		if rec.ID == "" || seenGroups[rec.ID] ||
			(snap.wishlistRoot != nil && rec.ID == snap.wishlistRoot.ID) ||
			(snap.groupsRoot != nil && rec.ID == snap.groupsRoot.ID) {
			return
		}
		seenGroups[rec.ID] = true
		snap.featureGroups = append(snap.featureGroups, rec)
	}
	if snap.groupsRoot != nil {
		if nd, ok := findProjectTreeNode(allTree, snap.groupsRoot.ID); ok {
			for _, ch := range nd.Children {
				addGroup(ch.Record)
			}
		}
	}
	if allTasks, err := a.Store.Query(data.Query{Kind: data.KindTask, IncludeArchived: a.nav.showArchived}); err == nil {
		for _, rec := range allTasks.Items {
			if rec.Bool("featureGroup") {
				addGroup(rec)
			}
		}
	}
	if lanesRes, err := a.Store.Query(data.Query{Kind: data.KindLane, IncludeArchived: a.nav.showArchived}); err == nil {
		snap.lanes = lanesRes.Items
	}
	return snap
}

func stringListField(rec data.Record, field string) []string {
	v, ok := rec.Value(field)
	if !ok || v == nil {
		return nil
	}
	var out []string
	appendTrim := func(s string) {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	switch x := v.(type) {
	case []string:
		for _, s := range x {
			appendTrim(s)
		}
	case []any:
		for _, e := range x {
			appendTrim(data.Text(e))
		}
	case string:
		for _, part := range strings.Split(x, ",") {
			appendTrim(part)
		}
	}
	return out
}

func containsFold(list []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target != "" && slices.ContainsFunc(list, func(s string) bool { return strings.ToLower(strings.TrimSpace(s)) == target })
}

func textMatchesAny(text string, terms []string) bool {
	low := strings.ToLower(text)
	if low == "" {
		return false
	}
	for _, term := range terms {
		if t := strings.ToLower(strings.TrimSpace(term)); len(t) >= 2 && strings.Contains(low, t) {
			return true
		}
	}
	return false
}

func groupAcceptanceCriteria(rec data.Record) []string {
	if items := stringListField(rec, "acceptanceCriteria"); len(items) > 0 {
		return items
	}
	var out []string
	for _, rawLine := range strings.Split(rec.Text("description"), "\n") {
		trimmed := strings.TrimSpace(rawLine)
		for _, pfx := range []string{"- [ ] ", "- [x] ", "- [X] ", "* [ ] ", "* [x] ", "* [X] "} {
			if strings.HasPrefix(trimmed, pfx) {
				out = append(out, trimmed)
				break
			}
		}
	}
	return out
}

func statusToneFor(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "merged", "healthy", "ok":
		return "success"
	case "active", "running", "review", "in_progress":
		return "accent"
	case "blocked", "parked", "cancelled", "failed", "warning":
		return "warning"
	case "planned", "todo":
		return "info"
	default:
		return "muted"
	}
}

func (a *App) linkedItemFromRecord(rec data.Record) views.FeaturesLinkedItem {
	sc := a.schema(rec.Kind)
	ref := rec.Ref()
	status := firstNonEmpty(rec.Text("laneStatus"), rec.Text("workStatus"), rec.Text("runStatus"))
	sub := firstNonEmpty(strings.TrimSpace(rec.Text("summary")), strings.TrimSpace(rec.Text("description")), strings.TrimSpace(rec.Text("body")))
	if idx := strings.IndexByte(sub, '\n'); idx >= 0 {
		sub = strings.TrimSpace(sub[:idx])
	}
	return views.FeaturesLinkedItem{
		ID:       rec.ID,
		Title:    views.Title(sc, rec),
		Subtitle: clip(sub, 96),
		Status:   status,
		Tone:     statusToneFor(status),
		Ref:      &ref,
		Badges:   views.StatusBadges(sc, rec),
	}
}

func (a *App) groupProvenanceRawThoughts(group data.Record) []views.FeaturesLinkedItem {
	if a.Store == nil {
		return nil
	}
	seen := map[core.Ref]bool{group.Ref(): true}
	var items []views.FeaturesLinkedItem
	addRef := func(ref core.Ref) {
		if ref.ID == "" || seen[ref] || ref.Kind == data.KindLane {
			return
		}
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			seen[ref] = true
			items = append(items, a.linkedItemFromRecord(*rec))
		}
	}
	if rels, err := a.Store.Related(group.Ref(), data.RelContext, data.Outgoing); err == nil {
		for _, rel := range rels {
			addRef(rel.To)
		}
	}
	if rels, err := a.Store.Related(group.Ref(), data.RelAbout, data.Incoming); err == nil {
		for _, rel := range rels {
			addRef(rel.From)
		}
	}
	return items
}

func (a *App) groupLinkedLanes(group data.Record, allLanes []data.Record) []views.FeaturesLinkedItem {
	seen := map[string]bool{}
	var out []views.FeaturesLinkedItem
	addLane := func(rec data.Record) {
		if rec.ID != "" && !seen[rec.ID] {
			seen[rec.ID] = true
			out = append(out, a.linkedItemFromRecord(rec))
		}
	}
	for _, lane := range allLanes {
		if tRef, ok := lane.RefField("task"); ok && tRef == group.Ref() {
			addLane(lane)
		}
	}
	if a.Store != nil {
		if rels, err := a.Store.Related(group.Ref(), data.RelContext, data.Both); err == nil {
			for _, rel := range rels {
				if other := rel.Other(group.Ref()); other.Kind == data.KindLane {
					if rec, err := a.Store.Get(other); err == nil && rec != nil {
						addLane(*rec)
					}
				}
			}
		}
	}
	return out
}

func (a *App) recordMatchesTarget(rec data.Record, field, target string, keywords []string, checkRels bool) bool {
	if containsFold(stringListField(rec, field), target) {
		return true
	}
	if checkRels && a.Store != nil {
		for _, relKind := range []string{data.RelAbout, data.RelContext} {
			if rels, err := a.Store.Related(rec.Ref(), relKind, data.Both); err == nil {
				for _, rel := range rels {
					other := rel.Other(rec.Ref())
					if strings.EqualFold(other.ID, target) || strings.EqualFold(other.Kind, target) {
						return true
					}
				}
			}
		}
	}
	return textMatchesAny(rec.Text("title")+" "+rec.Text("description"), append([]string{target}, keywords...))
}
