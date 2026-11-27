package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

const (
	homeMaxDecisions = 6
	homeMaxNewItems  = 8
	homeMaxPanes     = 6
	homeMaxPrompts   = 6
	homeMaxQuestions = 6
	homeMaxOfficial  = 4
	homeMaxLanes     = 4
)

type projectHomeTarget struct {
	FocusID     string
	Group       string
	Label       string
	Ref         *core.Ref
	PaneID      string
	ContextItem *CollectedContextItem
	Unread      bool
	OpenQ       bool
}

// projectHomeDashboardBindings returns data-driven key bindings for interactive
// items inside the Project Page Home dashboard.
func projectHomeDashboardBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		when(bind(n, views.ScopeProjectContent, "enter", CmdProjectContentOpen, "open item"), "facts.homeItem"),
		when(bind(n, views.ScopeProjectContent, "a", CmdAnswer, "answer decision"), "facts.homeItem && facts.openQuestion"),
		when(bind(n, views.ScopeProjectContent, "r", CmdProjectNavMarkRead, "mark read"), "facts.homeItem && facts.unread"),
		when(bind(n, views.ScopeProjectContent, "R", CmdProjectNavMarkSectionRead, "mark all read"), "facts.section == \"home\" && facts.projectUnread"),
	}
}

func (a *App) isProjectHomeFocus(focusID string) bool {
	return strings.HasPrefix(focusID, views.ProjectHomeItemPrefix)
}

func (pc *ProjectController) resetHomeTargets(projKey string) {
	if pc == nil || projKey == "" {
		return
	}
	if pc.homeTargets == nil {
		pc.homeTargets = map[string]map[string]projectHomeTarget{}
	}
	pc.homeTargets[projKey] = map[string]projectHomeTarget{}
}

func (pc *ProjectController) registerHomeTarget(projKey string, t projectHomeTarget) {
	if pc == nil || projKey == "" || t.FocusID == "" {
		return
	}
	if pc.homeTargets == nil {
		pc.homeTargets = map[string]map[string]projectHomeTarget{}
	}
	if pc.homeTargets[projKey] == nil {
		pc.homeTargets[projKey] = map[string]projectHomeTarget{}
	}
	pc.homeTargets[projKey][t.FocusID] = t
}

func (pc *ProjectController) homeTarget(projKey, focusID string) (projectHomeTarget, bool) {
	if pc == nil || pc.homeTargets == nil || projKey == "" || focusID == "" {
		return projectHomeTarget{}, false
	}
	t, ok := pc.homeTargets[projKey][focusID]
	return t, ok
}

func (a *App) projectHomeCacheKey(ws projectWorkspaceData, interactive bool) string {
	return fmt.Sprintf("%s|%d|%d|%s|%t|%d|%s|%t|%t|%t|%d",
		ws.projectKey, ws.cacheRev, ws.cacheCtxGen, a.projectPanesHomeTelemetryFP(ws.panes),
		a.nav.showArchived, a.navUnreadCutoff.UnixNano(), ws.cacheRailKey,
		a.projectHomeDashboardEnabled(), a.projectHomeQueueProgressEnabled(),
		interactive, a.now().Unix()/60,
	)
}

// buildProjectHomeSection projects Project Page Home either as the interactive
// important-info dashboard (default) or as the compact legacy summary when
// project.homeDashboard is false.
func (a *App) buildProjectHomeSection(ws projectWorkspaceData, interactive bool) ui.ViewNode {
	if !a.projectHomeDashboardEnabled() {
		return a.projectSectionHomeLegacy(ws, interactive)
	}
	pc := a.projectCtrl()
	slot := fmt.Sprintf("%s|%t", ws.projectKey, interactive)
	key := a.projectHomeCacheKey(ws, interactive)
	if cached, ok := pc.homeCache[slot]; ok && cached.key == key {
		if interactive {
			if pc.homeTargets == nil {
				pc.homeTargets = map[string]map[string]projectHomeTarget{}
			}
			pc.homeTargets[ws.projectKey] = cached.targets
		}
		return cached.node
	}
	node := a.buildProjectHomeSectionUncached(ws, interactive)
	if pc.homeCache == nil {
		pc.homeCache = map[string]cachedProjectHome{}
	}
	pc.homeCache[slot] = cachedProjectHome{
		key:     key,
		node:    node,
		targets: pc.homeTargets[ws.projectKey],
	}
	pc.homeBuilds++
	return node
}

func (a *App) buildProjectHomeSectionUncached(ws projectWorkspaceData, interactive bool) ui.ViewNode {
	if interactive {
		a.projectCtrl().resetHomeTargets(ws.projectKey)
	}

	now := a.now()
	paneSchema := a.schema(data.KindPane)
	paneTitles := make(map[string]string, len(ws.panes))
	for _, p := range ws.panes {
		paneTitles[p.ID] = views.Title(paneSchema, p)
	}

	desc, meta := a.projectHomeMetaLines(ws)
	groups := []views.ProjectHomeGroup{
		a.homeDecisionsGroup(ws, paneTitles, now, interactive),
		a.homeNewGroup(ws, now, interactive),
		a.homePanesGroup(ws, now, interactive),
		a.homePromptsGroup(ws, paneTitles, now, interactive),
		a.homeRecentQuestionsGroup(ws, now, interactive),
		a.homeOfficialContextGroup(ws, interactive),
		a.homeActiveLanesGroup(ws, interactive),
		a.homePortalSettingsGroup(ws, interactive),
	}

	badges := make([]ui.Badge, 0, 7)
	if ws.openQuestions > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("? %d decisions", ws.openQuestions), Tone: "warning"})
	}
	if ws.unreadTotal > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("● %d new", ws.unreadTotal), Tone: "warning"})
	}
	if ws.needsYouPanes > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("! %d needs you", ws.needsYouPanes), Tone: "error"})
	} else if ws.runningPanes > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("▶ %d running", ws.runningPanes), Tone: "info"})
	}
	badges = append(badges,
		ui.Badge{Text: fmt.Sprintf("%d panes", len(ws.panes))},
		ui.Badge{Text: fmt.Sprintf("%d tasks", len(ws.tasks))},
	)
	if ws.officialCount > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("★ %d official", ws.officialCount), Tone: "accent"})
	}

	counts1 := fmt.Sprintf("Tasks: %d · Panes: %d · Lanes: %d active (%d total) · Pages: %d",
		len(ws.tasks), len(ws.panes), len(ws.activeLanes), len(ws.allLanes), len(ws.pages))
	counts2 := fmt.Sprintf("Docs: %d · Notes: %d · Questions: %d (%d open) · Commands: %d · Worktrees: %d",
		len(ws.documents), len(ws.notes), len(ws.questions), ws.openQuestions, len(ws.commands), len(ws.worktrees))

	scrollKey := ""
	if ws.projRec != nil {
		scrollKey = data.RefText(ws.projRec.Ref())
	}
	return views.ProjectHomeView(views.ProjectHomeSpec{
		Title: "Home", Description: desc, Meta: meta, ScrollKey: scrollKey,
		CountsLine1: counts1, CountsLine2: counts2,
		Badges: badges, Groups: groups,
		QueueProgress: a.projectQueueProgress(ws.projectID),
		Interactive:   interactive, ProjectUnread: ws.unreadTotal > 0,
		Facts: map[string]any{"section": "home", "homeDashboard": true, "projectUnread": ws.unreadTotal > 0},
	})
}

func (a *App) projectHomeMetaLines(ws projectWorkspaceData) (desc, meta string) {
	if ws.projRec == nil {
		return "", "Panes not assigned to any project task (L associates a pane)"
	}
	desc = strings.TrimSpace(ws.projRec.Text("description"))
	var parts []string
	if len(ws.projRepoLabels) > 0 {
		parts = append(parts, "repo: "+strings.Join(ws.projRepoLabels, ", "))
	}
	if dir := strings.TrimSpace(ws.projRec.Text("defaultDir")); dir != "" {
		parts = append(parts, "dir: "+dir)
	}
	return desc, strings.Join(parts, " · ")
}

func (a *App) homeDecisionsGroup(ws projectWorkspaceData, paneTitles map[string]string, now time.Time, interactive bool) views.ProjectHomeGroup {
	var open []data.Record
	for _, q := range ws.questions {
		if strings.TrimSpace(q.Text("status")) == "open" {
			open = append(open, q)
		}
	}
	if len(open) == 0 {
		return views.ProjectHomeGroup{Key: "decisions"}
	}
	schema, pc := a.schema(data.KindQuestion), a.projectCtrl()
	limit := min(len(open), homeMaxDecisions)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		q := open[i]
		ref := q.Ref()
		title := views.Title(schema, q)
		unread := a.isNavRecordUnread(data.KindQuestion, q, &ws)
		qk := strings.TrimSpace(q.Text("questionKind"))
		if qk == "" {
			qk = "decision"
		}
		sub := ""
		if pRef, ok := q.RefField("pane"); ok {
			sub = paneTitles[pRef.ID]
		}
		focusID := views.ProjectHomeItemPrefix + "decisions." + q.ID
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: "?", Badge: "[" + qk + "]", Title: clip(title, 60), Subtitle: clip(sub, 24),
			Age: relativeTime(now, q.UpdatedAt), Tone: "warning", Unread: unread, Ref: &ref,
			Facts: map[string]any{"openQuestion": true, "recordKind": data.KindQuestion},
		})
		if interactive {
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "decisions", Label: title, Ref: &ref, Unread: unread, OpenQ: true,
			})
		}
	}
	return views.ProjectHomeGroup{
		Key: "decisions", Title: "Unanswered Decisions", Icon: "?", Tone: "warning",
		Count: len(open), MoreCount: max(0, len(open)-limit), Items: items,
	}
}

func (a *App) homeNewGroup(ws projectWorkspaceData, now time.Time, interactive bool) views.ProjectHomeGroup {
	type unreadEntry struct {
		kind, badge, sub string
		rec              data.Record
		ts               time.Time
	}
	seen := map[core.Ref]bool{}
	var all []unreadEntry
	collect := func(kind, badge string, recs []data.Record, subFn func(data.Record) string) {
		for _, rec := range recs {
			ref := rec.Ref()
			if seen[ref] || !a.isNavRecordUnread(kind, rec, &ws) {
				continue
			}
			seen[ref] = true
			ts := rec.UpdatedAt
			if ts.IsZero() {
				ts = rec.CreatedAt
			}
			sub := ""
			if subFn != nil {
				sub = strings.TrimSpace(subFn(rec))
			}
			all = append(all, unreadEntry{kind: kind, badge: badge, sub: sub, rec: rec, ts: ts})
		}
	}
	fieldSub := func(field string) func(data.Record) string {
		return func(r data.Record) string { return r.Text(field) }
	}
	collect(data.KindPage, "[page]", ws.pages, fieldSub("pageKind"))
	collect(data.KindTask, "[task]", ws.tasks, fieldSub("workStatus"))
	collect(data.KindNote, "[note]", ws.notes, fieldSub("authorKind"))
	collect(data.KindDocument, "[doc]", ws.documents, nil)
	collect(data.KindQuestion, "[question]", ws.questions, fieldSub("questionKind"))
	collect(data.KindLane, "[lane]", ws.allLanes, fieldSub("laneStatus"))
	collect(data.KindPane, "[pane]", ws.panes, func(r data.Record) string {
		st, _, pending := a.effectiveHookState(r.ID)
		if len(pending) > 0 {
			return "needs approval"
		}
		if st != "" {
			return st
		}
		return r.Text("runStatus")
	})
	collect(data.KindCommand, "[cmd]", ws.commands, nil)
	if ctxRecs := unreadContextRecordsForHome(ws); len(ctxRecs) > 0 {
		collect(data.KindContext, "[ctx]", ctxRecs, fieldSub("contextKind"))
	}
	if len(all) == 0 {
		return views.ProjectHomeGroup{Key: "new"}
	}

	slices.SortStableFunc(all, func(x, y unreadEntry) int {
		if cmp := y.ts.Compare(x.ts); cmp != 0 {
			return cmp
		}
		return strings.Compare(data.RefText(x.rec.Ref()), data.RefText(y.rec.Ref()))
	})

	pc, limit := a.projectCtrl(), min(len(all), homeMaxNewItems)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		ent := all[i]
		ref := ent.rec.Ref()
		title := strings.TrimSpace(views.Title(a.schema(ent.kind), ent.rec))
		if title == "" {
			title = ent.rec.ID
		}
		openQ := ent.kind == data.KindQuestion && strings.TrimSpace(ent.rec.Text("status")) == "open"
		facts := map[string]any{"recordKind": ent.kind}
		if openQ {
			facts["openQuestion"] = true
		}
		focusID := views.ProjectHomeItemPrefix + "new." + ent.kind + "." + ent.rec.ID
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Badge: ent.badge, Title: clip(title, 54), Subtitle: clip(ent.sub, 20),
			Age: relativeTime(now, ent.ts), Tone: "warning", Unread: true, Ref: &ref, Facts: facts,
		})
		if interactive {
			paneID := ""
			if ent.kind == data.KindPane {
				paneID = ent.rec.ID
			}
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "new", Label: title, Ref: &ref, PaneID: paneID, Unread: true, OpenQ: openQ,
			})
		}
	}
	return views.ProjectHomeGroup{
		Key: "new", Title: "New / Unread", Icon: "●", Tone: "warning",
		Count: len(all), MoreCount: max(0, len(all)-limit), Items: items,
	}
}

func (a *App) homePanesGroup(ws projectWorkspaceData, now time.Time, interactive bool) views.ProjectHomeGroup {
	if len(ws.panes) == 0 {
		return views.ProjectHomeGroup{Key: "panes"}
	}
	type paneEntry struct {
		rec                             data.Record
		rank                            int
		icon, tone, stateLabel, harness string
		ts                              time.Time
		unread, hasPending              bool
	}
	entries := make([]paneEntry, 0, len(ws.panes))
	for _, p := range ws.panes {
		hookState, pt, pending := a.effectiveHookState(p.ID)
		effState := hookState
		if effState == "" {
			effState = strings.ToLower(strings.TrimSpace(p.Text("runStatus")))
		}
		unread, hasPending := a.isNavRecordUnread(data.KindPane, p, &ws), len(pending) > 0
		rank, icon, tone, stateLabel := 2, "·", "", effState
		if stateLabel == "" {
			stateLabel = "idle"
		}
		switch {
		case hasPending:
			rank, icon, tone, stateLabel = 0, "!", "error", "needs approval"
		case effState == daemon.StateNeedsYou || effState == "waiting":
			rank, icon, tone, stateLabel = 0, "!", "error", "needs you"
		case effState == daemon.StateRunning || effState == "working":
			rank, icon, tone, stateLabel = 1, "▶", "info", "running"
		case unread:
			tone = "warning"
		}
		harness := strings.TrimSpace(p.Text("harness"))
		if harness == "" && pt != nil {
			harness = strings.TrimSpace(pt.Harness)
		}
		ts := p.Time("promptedAt")
		if ts.IsZero() {
			ts = p.UpdatedAt
		}
		entries = append(entries, paneEntry{
			rec: p, rank: rank, icon: icon, tone: tone, stateLabel: stateLabel,
			harness: harness, ts: ts, unread: unread, hasPending: hasPending,
		})
	}
	slices.SortStableFunc(entries, func(x, y paneEntry) int {
		if x.rank != y.rank {
			return x.rank - y.rank
		}
		if cmp := y.ts.Compare(x.ts); cmp != 0 {
			return cmp
		}
		if cmp := y.rec.UpdatedAt.Compare(x.rec.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(x.rec.ID, y.rec.ID)
	})

	schema, pc, limit := a.schema(data.KindPane), a.projectCtrl(), min(len(entries), homeMaxPanes)
	items := make([]views.ProjectHomeItem, 0, limit)
	groupTone := "info"
	if ws.needsYouPanes > 0 {
		groupTone = "error"
	}
	for i := range limit {
		ent := entries[i]
		ref, title := ent.rec.Ref(), views.Title(schema, ent.rec)
		badge := ""
		if ent.harness != "" {
			badge = "[" + ent.harness + "]"
		}
		sub := ent.stateLabel
		if ws.unreadPagePanes != nil && ws.unreadPagePanes[ent.rec.ID] {
			sub += " · unread pages"
		}
		focusID := views.ProjectHomeItemPrefix + "panes." + ent.rec.ID
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: ent.icon, Badge: badge, Title: clip(title, 44), Subtitle: sub,
			Age: relativeTime(now, ent.ts), Tone: ent.tone, Unread: ent.unread, Ref: &ref,
			Facts: map[string]any{"recordKind": data.KindPane, "hasPendingApproval": ent.hasPending},
		})
		if interactive {
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "panes", Label: title, Ref: &ref, PaneID: ent.rec.ID, Unread: ent.unread,
			})
		}
	}
	return views.ProjectHomeGroup{
		Key: "panes", Title: "Panes", Icon: "▣", Tone: groupTone,
		Count: len(entries), MoreCount: max(0, len(entries)-limit), Items: items,
	}
}

func (a *App) homePromptsGroup(ws projectWorkspaceData, paneTitles map[string]string, now time.Time, interactive bool) views.ProjectHomeGroup {
	if len(ws.panes) == 0 {
		return views.ProjectHomeGroup{Key: "prompts"}
	}
	type promptEntry struct {
		paneID, paneTitle, prompt string
		at                        time.Time
		seq                       int
	}
	var raw []promptEntry
	seq := 0
	for _, p := range ws.panes {
		pt := a.paneTelemetry(p.ID)
		if pt == nil || len(pt.RecentTurns) == 0 {
			continue
		}
		pTitle := paneTitles[p.ID]
		for _, ht := range pt.RecentTurns {
			prompt := homeSingleLine(ht.Prompt)
			if prompt == "" {
				continue
			}
			seq++
			raw = append(raw, promptEntry{paneID: p.ID, paneTitle: pTitle, prompt: prompt, at: ht.At, seq: seq})
		}
	}
	if len(raw) == 0 {
		return views.ProjectHomeGroup{Key: "prompts"}
	}
	slices.SortStableFunc(raw, func(x, y promptEntry) int {
		if cmp := y.at.Compare(x.at); cmp != 0 {
			return cmp
		}
		if x.seq != y.seq {
			return y.seq - x.seq
		}
		return strings.Compare(x.paneID, y.paneID)
	})
	dedup := make([]promptEntry, 0, len(raw))
	for _, ent := range raw {
		if len(dedup) > 0 && dedup[len(dedup)-1].prompt == ent.prompt {
			continue
		}
		dedup = append(dedup, ent)
	}
	pc, limit := a.projectCtrl(), min(len(dedup), homeMaxPrompts)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		ent := dedup[i]
		ref := core.Ref{Kind: data.KindPane, ID: ent.paneID}
		focusID := fmt.Sprintf("%sprompts.%s.%d", views.ProjectHomeItemPrefix, ent.paneID, i)
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: "❯", Title: clip(ent.prompt, 56), Subtitle: clip(ent.paneTitle, 24),
			Age: relativeTime(now, ent.at), Tone: "accent", Ref: &ref,
			Facts: map[string]any{"recordKind": data.KindPane, "promptItem": true},
		})
		if interactive {
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "prompts", Label: clip(ent.prompt, 40), Ref: &ref, PaneID: ent.paneID,
			})
		}
	}
	return views.ProjectHomeGroup{
		Key: "prompts", Title: "Recent Prompts", Icon: "❯", Tone: "accent",
		Count: len(dedup), MoreCount: max(0, len(dedup)-limit), Items: items,
	}
}

func (a *App) homeRecentQuestionsGroup(ws projectWorkspaceData, now time.Time, interactive bool) views.ProjectHomeGroup {
	type qEntry struct {
		rec data.Record
		ts  time.Time
	}
	var answered []qEntry
	for _, q := range ws.questions {
		if strings.TrimSpace(q.Text("status")) != "answered" {
			continue
		}
		ts := q.Time("answeredAt")
		if ts.IsZero() {
			ts = q.UpdatedAt
		}
		answered = append(answered, qEntry{rec: q, ts: ts})
	}
	if len(answered) == 0 {
		return views.ProjectHomeGroup{Key: "questions"}
	}
	slices.SortStableFunc(answered, func(x, y qEntry) int {
		if cmp := y.ts.Compare(x.ts); cmp != 0 {
			return cmp
		}
		if cmp := y.rec.UpdatedAt.Compare(x.rec.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(x.rec.ID, y.rec.ID)
	})
	schema, pc, limit := a.schema(data.KindQuestion), a.projectCtrl(), min(len(answered), homeMaxQuestions)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		q := answered[i].rec
		ref, title := q.Ref(), views.Title(schema, q)
		badge, tone := "[decided]", "muted"
		if strings.EqualFold(strings.TrimSpace(q.Text("questionKind")), "qa") {
			badge, tone = "[Q&A]", "accent"
		}
		focusID := views.ProjectHomeItemPrefix + "questions." + q.ID
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: "?", Badge: badge, Title: clip(title, 48),
			Subtitle: clip(homeSingleLine(q.Text("answer")), 40),
			Age:      relativeTime(now, answered[i].ts), Tone: tone, Ref: &ref,
			Facts: map[string]any{"recordKind": data.KindQuestion},
		})
		if interactive {
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "questions", Label: title, Ref: &ref,
			})
		}
	}
	return views.ProjectHomeGroup{
		Key: "questions", Title: "Recent Questions", Icon: "?", Tone: "muted",
		Count: len(answered), MoreCount: max(0, len(answered)-limit), Items: items,
	}
}

func (a *App) homeOfficialContextGroup(ws projectWorkspaceData, interactive bool) views.ProjectHomeGroup {
	if len(ws.officialItems) == 0 {
		return views.ProjectHomeGroup{Key: "official"}
	}
	pc, limit := a.projectCtrl(), min(len(ws.officialItems), homeMaxOfficial)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		it := ws.officialItems[i]
		label := strings.TrimSpace(it.Label)
		if label == "" {
			label = it.Value
		}
		sub := ""
		if it.Value != "" && it.Value != label {
			sub = clip(it.Value, 40)
		}
		var ref *core.Ref
		focusID := fmt.Sprintf("%sofficial.%d", views.ProjectHomeItemPrefix, i)
		if it.RecordID != "" {
			r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
			ref = &r
			focusID = views.ProjectHomeItemPrefix + "official." + it.RecordID
		}
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: "★", Badge: "[" + it.Kind + "]", Title: clip(label, 42), Subtitle: sub,
			Tone: "accent", Ref: ref, Facts: map[string]any{"contextItem": true, "official": true},
		})
		if interactive {
			cp := it
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "official", Label: label, Ref: ref, ContextItem: &cp,
			})
		}
	}
	more := max(0, len(ws.officialItems)-limit)
	return views.ProjectHomeGroup{
		Key: "official", Title: "Official Context", Icon: "★", Tone: "accent",
		Count: len(ws.officialItems), MoreCount: more, MoreHint: fmt.Sprintf("+%d more in Context", more), Items: items,
	}
}

func (a *App) homeActiveLanesGroup(ws projectWorkspaceData, interactive bool) views.ProjectHomeGroup {
	if len(ws.activeLanes) == 0 {
		return views.ProjectHomeGroup{Key: "lanes"}
	}
	schema, pc, limit := a.schema(data.KindLane), a.projectCtrl(), min(len(ws.activeLanes), homeMaxLanes)
	items := make([]views.ProjectHomeItem, 0, limit)
	for i := range limit {
		l := ws.activeLanes[i]
		ref, title := l.Ref(), views.Title(schema, l)
		unread := a.isNavRecordUnread(data.KindLane, l, &ws)
		badge := ""
		if st := strings.TrimSpace(l.Text("laneStatus")); st != "" {
			badge = "[" + st + "]"
		}
		focusID := views.ProjectHomeItemPrefix + "lanes." + l.ID
		items = append(items, views.ProjectHomeItem{
			ID: focusID, Icon: "⎇", Badge: badge, Title: clip(title, 56), Tone: "accent", Unread: unread, Ref: &ref,
			Facts: map[string]any{"recordKind": data.KindLane},
		})
		if interactive {
			pc.registerHomeTarget(ws.projectKey, projectHomeTarget{
				FocusID: focusID, Group: "lanes", Label: title, Ref: &ref, Unread: unread,
			})
		}
	}
	more := max(0, len(ws.activeLanes)-limit)
	return views.ProjectHomeGroup{
		Key: "lanes", Title: "Active Lanes", Icon: "⎇", Tone: "muted",
		Count: len(ws.activeLanes), MoreCount: more, MoreHint: fmt.Sprintf("+%d more lanes", more), Items: items,
	}
}

func homeSingleLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func unreadContextRecordsForHome(ws projectWorkspaceData) []data.Record {
	if len(ws.reviewContext) == 0 || len(ws.contextRecords) == 0 {
		return nil
	}
	out := make([]data.Record, 0, len(ws.reviewContext))
	for _, it := range ws.reviewContext {
		if rec, ok := ws.contextRecords[it.RecordID]; ok {
			out = append(out, rec)
		}
	}
	return out
}

// --- Interactive Home item commands (enter, r, R) ------------------------------

func (a *App) projectHomeItemOpen(s core.State, _ core.Command) core.StepResult {
	focus, pc := s.ActiveFrame().FocusID, a.projectCtrl()
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	target, ok := pc.homeTarget(projKey, focus)
	if !ok {
		_ = a.View(s)
		target, ok = pc.homeTarget(projKey, focus)
	}
	if !ok {
		return core.Next(s)
	}

	if a.autoMarkRecordRead(target.Ref, target.Unread) {
		a.invalidateProjectUnreadCaches(projKey)
	}

	if target.OpenQ || target.Group == "decisions" {
		if target.Ref != nil && target.Ref.Kind == data.KindQuestion {
			rec, err := a.Store.Get(*target.Ref)
			if err != nil {
				a.fail("answer", err)
				return core.Next(s)
			}
			return core.Next(a.openAnswerFormForQuestionRecord(s, *rec))
		}
	}

	if target.Group == "questions" || (target.Ref != nil && target.Ref.Kind == data.KindQuestion) {
		if target.Ref != nil {
			rec, err := a.Store.Get(*target.Ref)
			if err != nil {
				a.fail("open question", err)
				return core.Next(s)
			}
			if pageRef, hasPage := rec.RefField("page"); hasPage && pageRef.Kind == data.KindPage && pageRef.ID != "" {
				if page, perr := a.Store.Get(pageRef); perr == nil && page != nil {
					return a.projectOpenInteractivePage(s, projKey, *page, nil, target.FocusID)
				}
			}
			return core.Next(a.show(s, *target.Ref))
		}
	}

	if target.Group == "panes" || target.Group == "prompts" || (target.Ref != nil && target.Ref.Kind == data.KindPane) {
		paneID := target.PaneID
		if paneID == "" && target.Ref != nil {
			paneID = target.Ref.ID
		}
		if paneID != "" {
			enterLive := a.railEnterLiveEnabled()
			if a.projectChildPaneViewEnabled() {
				return core.Next(a.openProjectChildPaneInPlace(s, projKey, paneID, target.FocusID, enterLive))
			}
			return core.Next(a.jumpToPane(s, paneID, enterLive))
		}
	}

	if target.Ref != nil && target.Ref.Kind == data.KindPage {
		page, err := a.Store.Get(*target.Ref)
		if err != nil {
			a.fail("open page", err)
			return core.Next(s)
		}
		return a.projectOpenInteractivePage(s, projKey, *page, nil, target.FocusID)
	}

	if target.Group == "official" && target.ContextItem != nil {
		if target.Ref != nil && target.Ref.Kind == data.KindContext {
			return core.Next(a.show(s, *target.Ref))
		}
		return a.openCollectedContextItem(s, *target.ContextItem, nil)
	}

	if target.Ref != nil {
		return core.Next(a.show(s, *target.Ref))
	}
	return core.Next(s)
}

func (a *App) projectMarkRead(s core.State, c core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	if tbl := a.nav.tables[focus]; tbl != nil {
		if row, ok := tbl.FocusedRow(); ok && row.Ref != nil && row.Unread && a.markRecordRead(*row.Ref) {
			a.invalidateProjectUnreadCaches("")
			root := a.View(s)
			if root.Find(focus) == nil {
				s = withFocus(s, projectContextTable)
			}
			a.say("marked read")
		}
		return core.Next(s)
	}
	if !a.isProjectHomeFocus(focus) {
		return a.projectNavMarkRead(s, c)
	}
	projKey, pc := a.activeProjectWorkspaceKey(s), a.projectCtrl()
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	target, ok := pc.homeTarget(projKey, focus)
	if !ok {
		_ = a.View(s)
		target, ok = pc.homeTarget(projKey, focus)
	}
	if !ok || target.Ref == nil {
		return core.Next(s)
	}
	if a.markRecordRead(*target.Ref) {
		a.invalidateProjectUnreadCaches(projKey)
		s = a.settleHomeFocusAfterRead(s, focus)
		a.say("marked %s read", target.Label)
	} else {
		a.say("%s is already read", target.Label)
	}
	return core.Next(s)
}

func (a *App) projectMarkSectionRead(s core.State, c core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	if secKey, _ := projectNavFocusSection(focus); secKey != "" {
		return a.projectNavMarkSectionRead(s, c)
	}
	if a.isProjectHomeFocus(focus) || focus == projectHomeSummaryID {
		ws, ok := a.resolveProjectNavWorkspace(s)
		if !ok {
			return core.Next(s)
		}
		if n := a.markAllProjectNavRead(ws); n > 0 {
			s = a.settleHomeFocusAfterRead(s, focus)
			a.say("marked %d items read in %s", n, ws.groupName)
		} else {
			a.say("nothing unread in %s", ws.groupName)
		}
		return core.Next(s)
	}
	return a.projectNavMarkSectionRead(s, c)
}

func (a *App) settleHomeFocusAfterRead(s core.State, prevFocus string) core.State {
	a.viewMemo.gen++
	root := a.View(s)
	if root.Find(prevFocus) != nil {
		return s
	}
	for _, n := range a.focusables(s) {
		if n.region == "project.content" {
			return withFocus(s, n.id)
		}
	}
	if fallback := projectNavPrefix + "home"; root.Find(fallback) != nil {
		return withFocus(s, fallback)
	}
	return s
}

// --- Legacy opt-out Home summary (project.homeDashboard = false) ---------------

func (a *App) projectSectionHomeLegacy(ws projectWorkspaceData, interactive bool) ui.ViewNode {
	var rows []ui.ViewNode
	desc, meta := a.projectHomeMetaLines(ws)
	if desc != "" {
		rows = append(rows, line("project.home.desc", " "+desc, ""))
	}
	if meta != "" {
		rows = append(rows, line("project.home.meta", " "+meta, "muted"))
	}
	rows = append(rows,
		line("project.home.gap1", "", ""),
		line("project.home.counts.hdr", " WORKSPACE COUNTS", "muted"),
		line("project.home.counts.1", fmt.Sprintf(" Tasks: %d · Panes: %d · Lanes: %d active (%d total) · Pages: %d",
			len(ws.tasks), len(ws.panes), len(ws.activeLanes), len(ws.allLanes), len(ws.pages)), ""),
		line("project.home.counts.2", fmt.Sprintf(" Docs: %d · Notes: %d · Questions: %d (%d open) · Commands: %d · Worktrees: %d",
			len(ws.documents), len(ws.notes), len(ws.questions), ws.openQuestions, len(ws.commands), len(ws.worktrees)), ""),
		line("project.home.gap2", "", ""),
		line("project.home.official.hdr", fmt.Sprintf(" ★ OFFICIAL PROJECT CONTEXT (AUTO-INCLUDED · %d)", len(ws.officialItems)), "accent"),
	)
	if len(ws.officialItems) == 0 {
		rows = append(rows, line("project.home.official.empty", " None yet · press N to add official context, or p in Context to promote", "muted"))
	} else {
		limit := min(len(ws.officialItems), 6)
		for i := range limit {
			it := ws.officialItems[i]
			label := strings.TrimSpace(it.Label)
			if label == "" {
				label = it.Value
			}
			entry := fmt.Sprintf(" ★ [%s] %s", it.Kind, clip(label, 42))
			if it.Value != "" && it.Value != label {
				entry += " · " + clip(it.Value, 44)
			}
			rows = append(rows, line(fmt.Sprintf("project.home.official.%d", i), entry, ""))
		}
		if len(ws.officialItems) > limit {
			rows = append(rows, line("project.home.official.more", fmt.Sprintf(" +%d more in Context", len(ws.officialItems)-limit), "muted"))
		}
	}
	if len(ws.activeLanes) > 0 {
		rows = append(rows, line("project.home.gap3", "", ""), line("project.home.lanes.hdr", " ACTIVE LANES", "muted"))
		for i, l := range ws.activeLanes {
			if i >= 4 {
				rows = append(rows, line("project.home.lanes.more", fmt.Sprintf(" +%d more lanes", len(ws.activeLanes)-4), "muted"))
				break
			}
			rows = append(rows, line(fmt.Sprintf("project.home.lane.%d", i),
				fmt.Sprintf(" [%s] %s", l.Text("laneStatus"), views.Title(a.schema(data.KindLane), l)), "accent"))
		}
	}
	if ws.openQuestions > 0 {
		rows = append(rows, line("project.home.gap4", "", ""),
			line("project.home.q.hdr", fmt.Sprintf(" OPEN QUESTIONS & DECISIONS (%d)", ws.openQuestions), "warning"))
		shown := 0
		for _, q := range ws.questions {
			if q.Text("status") != "open" || shown >= 3 {
				continue
			}
			rows = append(rows, line(fmt.Sprintf("project.home.q.%d", shown), " ? "+clip(views.Title(a.schema(data.KindQuestion), q), 72), "warning"))
			shown++
		}
	}
	return ui.ViewNode{
		ID: "project.home", Kind: ui.KindPanel, Scope: views.ScopeProjectContent, Title: "Home", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d tasks", len(ws.tasks))}, {Text: fmt.Sprintf("%d context", len(ws.contextItems)), Tone: "accent"}},
		Facts:  map[string]any{"section": "home", "projectUnread": ws.unreadTotal > 0},
		Children: []ui.ViewNode{{
			ID: projectHomeSummaryID, Kind: ui.KindVStack, Scope: views.ScopeProjectContent,
			Focusable: interactive, Facts: map[string]any{"section": "home", "projectUnread": ws.unreadTotal > 0}, Children: rows,
		}},
	}
}
