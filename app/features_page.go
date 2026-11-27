package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/input"
)

// Space, widget, task-root, and command constants for the self-hosted Features page.
const (
	SpaceFeatures = "features"

	featuresCaptureTree  = "features.capture.tree"
	featuresCapturePanel = "features.capture.panel"
	featuresCaptureTitle = "Quick Capture (Features Wishlist) · n/o add · b clean-slate · tab indent"

	FeaturesWishlistTitle = "Features Wishlist"
	FeaturesGroupsTitle   = "Organized Feature Groups"

	CmdFeaturesNavSelect      = "features.nav.select"
	CmdFeaturesNavGroupToggle = "features.nav.group.toggle"
	CmdFeaturesQuickCapture   = "features.capture.add"
	CmdFeaturesCaptureFocus   = "features.capture.focus"
	CmdFeaturesCaptureToggle  = "features.capture.expand"
	CmdFeaturesItemOpen       = "features.item.open"
	CmdFeaturesSuggestProp    = "features.suggest.prop"
	CmdFeaturesSuggestView    = "features.suggest.view"
	CmdFeaturesSuggestConcept = "features.suggest.concept"
)

// FeaturesController owns cached local-checkout detection, left-nav selection
// and collapse state, inline raw note editing, and Quick Capture expansion for the Features page.
type FeaturesController struct {
	activeKey        string
	collapsedGroups  map[string]bool
	captureExpanded  bool
	noteInput        *input.Model
	noteEditing      bool
	noteGen          int
	detectCached     bool
	detectEnabledKey string
	detectRepoPath   string
	detectActive     bool
	detectResolved   string
}

func newFeaturesController() *FeaturesController {
	return &FeaturesController{collapsedGroups: map[string]bool{}}
}

func (a *App) featuresCtrl() *FeaturesController {
	if a.features == nil {
		a.features = newFeaturesController()
	}
	return a.features
}

func (fc *FeaturesController) invalidateDetection() {
	if fc == nil {
		return
	}
	fc.detectCached = false
	fc.detectEnabledKey = ""
	fc.detectRepoPath = ""
	fc.detectActive = false
	fc.detectResolved = ""
}

func (fc *FeaturesController) activeItemKey() string {
	if fc != nil && strings.TrimSpace(fc.activeKey) != "" {
		return fc.activeKey
	}
	return "page:" + SpacePanes
}

func (a *App) featuresViewKey(s core.State) string {
	active, repoPath := a.detectMatomeruCheckout()
	if !active && s.ActivePage != SpaceFeatures {
		return "0"
	}
	if s.ActivePage != SpaceFeatures {
		return "1:" + repoPath
	}
	fc := a.featuresCtrl()
	var collapsed []string
	for k, v := range fc.collapsedGroups {
		if v {
			collapsed = append(collapsed, k)
		}
	}
	slices.Sort(collapsed)
	return fmt.Sprintf("1:%s:%s:%t:%t:%t:%t:%d:%s:%q:%s",
		repoPath, fc.activeItemKey(), fc.captureExpanded,
		a.featuresCaptureDockedEnabled(), fc.noteEditing,
		a.isCleanSlateActive(data.KindTask, featuresCaptureTree),
		fc.noteGen,
		strings.Join(collapsed, ","),
		a.filterQuery(views.FeaturesNavFilterID), a.filterMode(views.FeaturesNavFilterID),
	)
}

func filterFeaturesNavGroups(groups []views.FeaturesNavGroup, query string, mode traits.MatchMode) []views.FeaturesNavGroup {
	if strings.TrimSpace(query) == "" {
		return groups
	}
	mm, _ := traits.CompileMultiTerm(query, mode, traits.FilterConfig{})
	var out []views.FeaturesNavGroup
	for _, g := range groups {
		var kept []views.FeaturesNavItem
		for _, it := range g.Items {
			text := traits.FormatFilterableText("group", g.Title, "section", it.Section, "label", it.Label, "key", it.Key, "badge", it.Badge)
			if mm.Match(text) {
				kept = append(kept, it)
			}
		}
		if len(kept) == 0 {
			continue
		}
		cp := g
		cp.Items = kept
		cp.Collapsed = false
		out = append(out, cp)
	}
	return out
}

// --- Nav & Detail Projection ----------------------------------------------------

func (a *App) buildFeaturesNav(snap featuresSnapshot, activeKey string) ([]views.FeaturesNavGroup, map[string]bool) {
	fc := a.featuresCtrl()
	validKeys := map[string]bool{}

	// 1. By Page: live registered spaces + catalog entries.
	var pageKeys []string
	addPage := func(k string) {
		if k = strings.TrimSpace(k); k != "" && !slices.Contains(pageKeys, k) {
			pageKeys = append(pageKeys, k)
		}
	}
	for _, sp := range spaces {
		addPage(sp.id)
	}
	if a.projectsTopNavTabEnabled() {
		addPage(SpaceProjects)
	}
	addPage(SpaceFeatures)
	for _, cat := range defaultFeaturesPageCatalog() {
		addPage(cat.Key)
	}

	pageItems := make([]views.FeaturesNavItem, 0, len(pageKeys))
	for _, pk := range pageKeys {
		cat, hasCat := lookupFeaturesPageCatalog(pk)
		count := 0
		for _, g := range snap.featureGroups {
			if a.recordMatchesTarget(g, "targetPages", pk, cat.Keywords, true) {
				count++
			}
		}
		for _, rt := range snap.rawThoughts {
			if a.recordMatchesTarget(rt, "targetPages", pk, cat.Keywords, false) {
				count++
			}
		}
		itemKey := "page:" + pk
		validKeys[itemKey] = true
		tone := ""
		if !hasCat {
			tone = "muted"
		}
		pageItems = append(pageItems, views.FeaturesNavItem{
			Key:     itemKey,
			Section: "pages",
			Label:   firstNonEmpty(cat.Title, views.Label(pk)),
			Icon:    firstNonEmpty(cat.Icon, "◫"),
			Tone:    tone,
			Count:   count,
			Active:  activeKey == itemKey,
		})
	}
	if a.featuresItemizationEnabled() {
		for _, component := range featuresComponentCatalog {
			itemKey := "component:" + component.Key
			validKeys[itemKey] = true
			pageItems = append(pageItems, views.FeaturesNavItem{
				Key: itemKey, Section: "pages", Label: component.View.Name,
				Icon: "·", Active: activeKey == itemKey,
			})
		}
	}

	// 2. By Noun: live schemas merged with catalog (including planned nouns).
	liveByKind := map[string]data.Schema{}
	var liveSchemas []data.Schema
	if a.Store != nil {
		liveSchemas = a.Store.Registry().List()
		for _, sc := range liveSchemas {
			liveByKind[sc.Kind] = sc
		}
	}
	addedKinds := map[string]bool{}
	var nounItems []views.FeaturesNavItem
	appendNoun := func(kind string) {
		if kind == "" || addedKinds[kind] {
			return
		}
		addedKinds[kind] = true
		sc, isLive := liveByKind[kind]
		cat, hasCat := lookupFeaturesNounCatalog(kind)
		planned := !isLive && (!hasCat || cat.Planned)
		label := cat.Title
		if isLive {
			label = firstNonEmpty(sc.Plural, sc.Title, label)
		}
		impl, _ := domainVerbsForNoun(kind, snap.specs, snap.keysByID)
		badge, tone := "", ""
		if planned {
			badge, tone = "planned", "warning"
		} else if a.isExternalKind(kind) {
			badge, tone = "ext", "info"
		}
		itemKey := "noun:" + kind
		validKeys[itemKey] = true
		nounItems = append(nounItems, views.FeaturesNavItem{
			Key:     itemKey,
			Section: "nouns",
			Label:   firstNonEmpty(label, views.Label(kind)),
			Icon:    firstNonEmpty(cat.Icon, "◆"),
			Badge:   badge,
			Tone:    tone,
			Count:   len(impl),
			Planned: planned,
			Active:  activeKey == itemKey,
		})
	}
	for _, cat := range defaultFeaturesNounCatalog() {
		appendNoun(cat.Kind)
	}
	slices.SortFunc(liveSchemas, func(x, y data.Schema) int { return strings.Compare(x.Kind, y.Kind) })
	for _, sc := range liveSchemas {
		appendNoun(sc.Kind)
	}

	// 3. Organized Feature Groups.
	taskSchema := a.schema(data.KindTask)
	groupItems := make([]views.FeaturesNavItem, 0, len(snap.featureGroups))
	for _, g := range snap.featureGroups {
		itemKey := "group:" + g.ID
		status := g.Text("workStatus")
		validKeys[itemKey] = true
		groupItems = append(groupItems, views.FeaturesNavItem{
			Key:     itemKey,
			Section: "groups",
			Label:   views.Title(taskSchema, g),
			Icon:    "◈",
			Badge:   status,
			Tone:    statusToneFor(status),
			Count:   len(a.groupLinkedLanes(g, snap.lanes)) + len(a.groupProvenanceRawThoughts(g)),
			Active:  activeKey == itemKey,
		})
	}

	return []views.FeaturesNavGroup{
		{Key: "pages", Title: "By Page", Collapsed: fc.collapsedGroups["pages"], Items: pageItems},
		{Key: "nouns", Title: "By Noun", Collapsed: fc.collapsedGroups["nouns"], Items: nounItems},
		{Key: "groups", Title: "Organized Groups", Collapsed: fc.collapsedGroups["groups"], Items: groupItems},
		a.featuresConceptNav(snap.concepts, activeKey, validKeys),
	}, validKeys
}

func (a *App) buildFeaturesDetail(snap featuresSnapshot, activeKey string) views.FeaturesDetailSpec {
	detail := a.buildFeaturesDetailBody(snap, activeKey)
	detail.NoteBox = a.featuresNoteBoxSpec(activeKey, detail.Title)
	return detail
}

func (a *App) buildFeaturesDetailBody(snap featuresSnapshot, activeKey string) views.FeaturesDetailSpec {
	itemized := a.featuresItemizationEnabled()
	switch {
	case strings.HasPrefix(activeKey, "page:"):
		pk := strings.TrimPrefix(activeKey, "page:")
		cat, _ := lookupFeaturesPageCatalog(pk)
		var linkedGroups, thoughts []views.FeaturesLinkedItem
		nounKinds := slices.Clone(featuresPageNouns(pk))
		for _, g := range snap.featureGroups {
			if a.recordMatchesTarget(g, "targetPages", pk, cat.Keywords, true) {
				linkedGroups = append(linkedGroups, a.linkedItemFromRecord(g))
				if containsFold(stringListField(g, "targetPages"), pk) {
					nounKinds = append(nounKinds, stringListField(g, "targetNouns")...)
				}
			}
		}
		for _, rt := range snap.rawThoughts {
			if a.recordMatchesTarget(rt, "targetPages", pk, cat.Keywords, false) {
				thoughts = append(thoughts, a.linkedItemFromRecord(rt))
			}
		}
		detail := views.FeaturesDetailSpec{
			Kind:          "page",
			Key:           activeKey,
			Title:         firstNonEmpty(cat.Title, views.Label(pk)),
			Subtitle:      "Page · " + pk,
			Summary:       firstNonEmpty(cat.Summary, fmt.Sprintf("Registered Matomeru space %q.", pk)),
			Badges:        []ui.Badge{{Text: "page", Tone: "info"}},
			Highlights:    slices.Clone(cat.Highlights),
			LinkedGroups:  linkedGroups,
			RawThoughts:   thoughts,
			ItemizedNouns: a.featuresItemizedNouns(nounKinds, snap.concepts),
			Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
				return featuresConceptTargets(rec, pk, activeKey)
			}),
			CanSuggestView: itemized,
		}
		if itemized {
			detail.Views = featuresViewsWithSuggestions(featuresPageViews(pk, detail), snap.concepts, pk, activeKey)
		}
		return detail

	case strings.HasPrefix(activeKey, "component:"):
		key := strings.TrimPrefix(activeKey, "component:")
		if component, ok := lookupFeaturesComponent(key); itemized && ok {
			page, _ := lookupFeaturesPageCatalog(component.Page)
			var thoughts []views.FeaturesLinkedItem
			for _, rt := range snap.rawThoughts {
				if a.recordMatchesTarget(rt, "targetComponents", key, nil, false) {
					thoughts = append(thoughts, a.linkedItemFromRecord(rt))
				}
			}
			return views.FeaturesDetailSpec{
				Kind: "component", Key: activeKey, Title: component.View.Name, Subtitle: "Component · " + page.Title,
				RawThoughts:   thoughts,
				ItemizedNouns: a.featuresItemizedNouns(component.Nouns, snap.concepts),
				Views:         featuresViewsWithSuggestions([]views.FeaturesViewRow{component.View}, snap.concepts, key, activeKey),
				Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
					return featuresConceptTargets(rec, key, activeKey)
				}),
				CanSuggestView: true,
			}
		}

	case strings.HasPrefix(activeKey, "noun:"):
		kind := strings.TrimPrefix(activeKey, "noun:")
		sc, isLive := data.Schema{}, false
		if a.Store != nil {
			sc, isLive = a.Store.Registry().Get(kind)
		}
		cat, _ := lookupFeaturesNounCatalog(kind)
		title := cat.Title
		if isLive {
			title = firstNonEmpty(sc.Plural, sc.Title, title)
		}
		summary := cat.Summary
		if summary == "" && isLive {
			summary = fmt.Sprintf("Live registered noun schema %q with %d fields.", sc.Kind, len(sc.Fields))
		}
		impl, plannedVerbs := domainVerbsForNoun(kind, snap.specs, snap.keysByID)
		var badges []ui.Badge
		if !isLive {
			badges = append(badges, ui.Badge{Text: "planned", Tone: "warning"})
		} else {
			badges = append(badges, ui.Badge{Text: "live schema", Tone: "success"})
			if a.isExternalKind(kind) {
				badges = append(badges, ui.Badge{Text: "extension", Tone: "info"})
			}
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d records", a.schemaCount(kind)), Tone: "muted"})
		}
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d verbs", len(impl)), Tone: "accent"})
		if len(plannedVerbs) > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d planned", len(plannedVerbs)), Tone: "warning"})
		}
		highlights := slices.Clone(cat.Highlights)
		if len(highlights) == 0 && isLive && len(sc.Fields) > 0 {
			names := make([]string, 0, min(len(sc.Fields), 8))
			for i := 0; i < len(sc.Fields) && i < 8; i++ {
				names = append(names, sc.Fields[i].Name)
			}
			highlights = append(highlights, "Schema fields: "+strings.Join(names, ", "))
		}
		var linkedGroups, thoughts []views.FeaturesLinkedItem
		for _, g := range snap.featureGroups {
			if a.recordMatchesTarget(g, "targetNouns", kind, cat.Keywords, false) {
				linkedGroups = append(linkedGroups, a.linkedItemFromRecord(g))
			}
		}
		for _, rt := range snap.rawThoughts {
			if a.recordMatchesTarget(rt, "targetNouns", kind, cat.Keywords, false) {
				thoughts = append(thoughts, a.linkedItemFromRecord(rt))
			}
		}
		var props []views.FeaturesPropRow
		if itemized {
			props = featuresPropRows(sc, kind, snap.concepts)
		}
		return views.FeaturesDetailSpec{
			Kind:             "noun",
			Key:              activeKey,
			Title:            firstNonEmpty(title, views.Label(kind)),
			Subtitle:         "Noun · " + kind,
			Summary:          summary,
			Badges:           badges,
			Highlights:       highlights,
			ImplementedVerbs: impl,
			PlannedVerbs:     plannedVerbs,
			LinkedGroups:     linkedGroups,
			RawThoughts:      thoughts,
			Props:            props,
			Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
				return featuresConceptTargets(rec, kind, activeKey)
			}),
			CanSuggestProp: itemized,
		}

	case strings.HasPrefix(activeKey, "group:"):
		gid := strings.TrimPrefix(activeKey, "group:")
		for _, g := range snap.featureGroups {
			if g.ID != gid {
				continue
			}
			taskSchema := a.schema(data.KindTask)
			var tags []string
			for _, p := range stringListField(g, "targetPages") {
				tags = append(tags, "page:"+p)
			}
			for _, n := range stringListField(g, "targetNouns") {
				tags = append(tags, "noun:"+n)
			}
			return views.FeaturesDetailSpec{
				Kind:               "group",
				Key:                activeKey,
				Title:              views.Title(taskSchema, g),
				Subtitle:           "Organized Feature Group · " + data.RefText(g.Ref()),
				Summary:            firstNonEmpty(strings.TrimSpace(g.Text("description")), "Organized Feature Group tracking acceptance criteria, provenance thoughts, and execution lanes."),
				Badges:             views.StatusBadges(taskSchema, g),
				AcceptanceCriteria: groupAcceptanceCriteria(g),
				TargetTags:         tags,
				LinkedLanes:        a.groupLinkedLanes(g, snap.lanes),
				RawThoughts:        a.groupProvenanceRawThoughts(g),
				ItemizedNouns:      a.featuresItemizedNouns(stringListField(g, "targetNouns"), snap.concepts),
				Concepts:           a.featuresGroupConcepts(snap.concepts, g),
			}
		}

	case strings.HasPrefix(activeKey, "concept:"):
		kind := strings.TrimPrefix(activeKey, "concept:")
		var thoughts []views.FeaturesLinkedItem
		for _, rt := range snap.rawThoughts {
			if a.recordMatchesTarget(rt, "targetConcepts", kind, nil, false) {
				thoughts = append(thoughts, a.linkedItemFromRecord(rt))
			}
		}
		return views.FeaturesDetailSpec{
			Kind: "concept", Key: activeKey, Title: views.Label(kind) + " concepts",
			RawThoughts: thoughts,
			Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
				return rec.Text(data.FieldConceptKind) == kind
			}),
		}
	}
	return views.FeaturesDetailSpec{
		Kind:     "page",
		Key:      activeKey,
		Title:    "Features",
		Subtitle: "Select an item from the left rail.",
	}
}

func (a *App) featuresSuggestProp(s core.State, _ core.Command) core.StepResult {
	return a.featuresSuggestItem(s, data.ConceptKindProp, "Suggest prop")
}

func (a *App) featuresSuggestView(s core.State, _ core.Command) core.StepResult {
	return a.featuresSuggestItem(s, data.ConceptKindView, "Suggest view")
}

func (a *App) featuresSuggestItem(s core.State, kind, title string) core.StepResult {
	if !a.featuresPageActive() || !a.featuresItemizationEnabled() || s.ActivePage != SpaceFeatures {
		a.say("open Features with itemization enabled to suggest a %s", kind)
		return core.Next(s)
	}
	section, target, _ := strings.Cut(a.featuresCtrl().activeItemKey(), ":")
	if target == "" || (kind == data.ConceptKindProp && section != "noun") ||
		(kind == data.ConceptKindView && section != "page" && section != "component") {
		a.say("select a Features target for this %s", kind)
		return core.Next(s)
	}
	return a.create(s, core.Command{ID: views.CmdNew, Args: map[string]any{
		"kind": data.KindConcept, "title": title + " for " + target,
		"fields": []string{"title", "summary", "body"},
		"defaults": map[string]any{
			data.FieldConceptKind:       kind,
			data.FieldConceptTargetKind: target,
			data.FieldConceptStatus:     data.ConceptStatusProposed,
		},
	}})
}

func (a *App) featuresSuggestConcept(s core.State, _ core.Command) core.StepResult {
	if !a.featuresPageActive() {
		a.say("Features is inactive")
		return core.Next(s)
	}
	defaults := map[string]any{}
	if s.ActivePage == SpaceFeatures {
		section, target, _ := strings.Cut(a.featuresCtrl().activeItemKey(), ":")
		switch section {
		case "page", "noun":
			defaults[data.FieldConceptKind] = section
			defaults[data.FieldConceptTargetKind] = target
		case "component":
			defaults[data.FieldConceptKind] = data.ConceptKindView
			defaults[data.FieldConceptTargetKind] = target
		case "group":
			defaults[data.FieldConceptTargetKind] = target
		case "concept":
			defaults[data.FieldConceptKind] = target
		}
	}
	return a.create(s, core.Command{ID: views.CmdNew, Args: map[string]any{
		"kind": data.KindConcept, "title": "Track concept", "defaults": defaults,
		"fields": []string{"title", data.FieldConceptKind, data.FieldConceptTargetKind, data.FieldConceptStatus, "summary", "body"},
	}})
}

// featuresSpace projects the complete self-hosted Features page view tree.
func (a *App) featuresSpace(s core.State) ui.ViewNode {
	_, repoPath := a.detectMatomeruCheckout()
	fc := a.featuresCtrl()
	snap := a.collectFeaturesSnapshot()

	activeKey := fc.activeItemKey()
	if s.ActivePage == SpaceFeatures {
		focus := s.Pages[SpaceFeatures].FocusID
		if !strings.HasPrefix(focus, views.FeaturesGroupPrefix) {
			if k, ok := strings.CutPrefix(focus, views.FeaturesNavPrefix); ok && k != "" {
				activeKey = k
			}
		}
	}
	navGroups, validKeys := a.buildFeaturesNav(snap, activeKey)
	if !validKeys[activeKey] {
		activeKey = "page:" + SpacePanes
		if !validKeys[activeKey] {
			for _, g := range navGroups {
				if len(g.Items) > 0 {
					activeKey = g.Items[0].Key
					break
				}
			}
		}
		navGroups, _ = a.buildFeaturesNav(snap, activeKey)
	}
	fc.activeKey = activeKey
	navQuery := a.filterQuery(views.FeaturesNavFilterID)
	navGroups = filterFeaturesNavGroups(navGroups, navQuery, a.filterMode(views.FeaturesNavFilterID))

	docked := a.featuresCaptureDockedEnabled()
	var captureNode ui.ViewNode
	if docked {
		captureNode = a.featuresCaptureNode(snap.wishlistRoot)
	}

	return views.FeaturesPageView(views.FeaturesPageSpec{
		RepoPath:        repoPath,
		ActiveKey:       activeKey,
		NavRatio:        0.24,
		NavFilterQuery:  navQuery,
		NavGroups:       navGroups,
		Detail:          a.buildFeaturesDetail(snap, activeKey),
		CaptureNode:     captureNode,
		ShowCapture:     docked,
		CaptureExpanded: docked && fc.captureExpanded,
		Facts: map[string]any{
			"featuresPageActive":    true,
			"featuresActiveKey":     activeKey,
			"captureExpanded":       docked && fc.captureExpanded,
			"featuresCaptureDocked": docked,
		},
	})
}
