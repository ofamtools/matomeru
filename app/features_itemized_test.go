package app

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestFeaturesItemizationProjectsSchemasAndTrackedConcepts(t *testing.T) {
	a, d := newFeaturesTestApp(t, Options{FS: matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")})
	a.Store.Registry().Register(data.Schema{
		Kind: "blueprint", Title: "Blueprint", Plural: "Blueprints", SummaryField: "label",
		Fields: []data.FieldSpec{
			{Name: "label", Type: core.ParamString, Required: true},
			{Name: "format", Type: core.ParamString, Default: "short", Enum: []string{"short", "long"}, Description: "Output format"},
		},
	})
	makeRecord := func(kind string, fields map[string]any) *data.Record {
		t.Helper()
		rec, _, err := a.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	group := makeRecord(data.KindTask, map[string]any{
		"title": "Blueprint work", "featureGroup": true, "targetPages": []string{SpacePanes}, "targetNouns": []string{"blueprint"},
	})
	prop := makeRecord(data.KindConcept, map[string]any{
		"title": "compression", data.FieldConceptKind: data.ConceptKindProp, data.FieldConceptTargetKind: "blueprint",
		data.FieldConceptStatus: data.ConceptStatusProposed, "summary": "Enable compressed output",
	})
	pageView := makeRecord(data.KindConcept, map[string]any{
		"title": "Alternate rail", data.FieldConceptKind: data.ConceptKindView, data.FieldConceptTargetKind: SpacePanes,
		data.FieldConceptStatus: data.ConceptStatusProposed,
	})
	linked := makeRecord(data.KindConcept, map[string]any{"title": "Group idea"})
	if _, _, err := a.Store.Relate(data.RelateInput{From: linked.Ref(), Kind: data.RelAbout, To: group.Ref()}, User); err != nil {
		t.Fatal(err)
	}
	snap := a.collectFeaturesSnapshot()
	noun := a.buildFeaturesDetail(snap, "noun:blueprint")
	want := []views.FeaturesPropRow{
		{Name: "label", Type: "string", Role: data.FieldRoleTitle},
		{Name: "format", Type: "string", Role: data.FieldRoleProp, Default: "short", Enum: "short, long", Description: "Output format"},
		{Name: "compression", Description: "Enable compressed output", Suggested: true},
	}
	// The noun's own detail adds the universal record fields after its schema
	// fields; suggested props stay last.
	nounWant := slices.Concat(want[:2], featuresUniversalRecordProps, want[2:])
	if !slices.Equal(noun.Props, nounWant) || len(noun.Concepts) != 1 || noun.Concepts[0].ID != prop.ID {
		t.Fatalf("blueprint detail props %+v, concepts %+v", noun.Props, noun.Concepts)
	}
	page := a.buildFeaturesDetail(snap, "page:"+SpacePanes)
	groupDetail := a.buildFeaturesDetail(snap, "group:"+group.ID)
	for _, detail := range []views.FeaturesDetailSpec{page, groupDetail} {
		if !slices.ContainsFunc(detail.ItemizedNouns, func(n views.FeaturesItemizedNoun) bool {
			return n.Kind == "blueprint" && n.Title == "Blueprints" && slices.Equal(n.Props, want)
		}) {
			t.Fatalf("%q did not itemize the referenced noun: %+v", detail.Key, detail.ItemizedNouns)
		}
	}
	if len(groupDetail.Concepts) != 1 || groupDetail.Concepts[0].ID != linked.ID {
		t.Fatalf("group Concepts = %+v", groupDetail.Concepts)
	}
	if len(page.Concepts) != 1 || page.Concepts[0].ID != pageView.ID ||
		!slices.ContainsFunc(page.Views, func(v views.FeaturesViewRow) bool {
			return v.Name == "Alternate rail" && v.Suggested
		}) {
		t.Fatalf("Panes views %+v, concepts %+v", page.Views, page.Concepts)
	}
	component := a.buildFeaturesDetail(snap, "component:panes.toc")
	if !component.CanSuggestView || len(component.Views) == 0 || component.Views[0].OptOutSetting != SetShowTOC {
		t.Fatalf("page rail component detail = %+v", component)
	}

	d.Keys("6")
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdFeaturesNavSelect, Args: map[string]any{"key": "concept:prop"}, Actor: User}})
	if got := a.featuresCtrl().activeItemKey(); got != "concept:prop" {
		t.Fatalf("selected Features key = %q, want concept:prop", got)
	}
	root := a.View(a.State())
	for _, kind := range []string{"page", "noun", "verb", "view", "feature", "prop", "binding", "setting", "workflow"} {
		if root.Find(views.FeaturesNavPrefix+"concept:"+kind) == nil {
			t.Fatalf("Concepts navigation missing %s", kind)
		}
	}
	outlineModel := a.trees.featuresOutlineState().model
	hasRow := func(id string) bool {
		return slices.ContainsFunc(outlineModel.Flatten(), func(r tree.VisibleRow) bool { return r.ID == id })
	}
	if !hasRow(prop.ID) || hasRow(pageView.ID) {
		t.Fatal("Prop Concepts category did not filter tracked records by kind in unified Features outline")
	}
	d.Keys("space")
	root = a.View(a.State())
	if !a.featuresCtrl().collapsedGroups[featuresConceptsGroup] || root.Find(views.FeaturesNavPrefix+"concept:prop") != nil {
		t.Fatal("Concepts group did not collapse from its focused child")
	}
}

func TestFeaturesSuggestionsUseScopedKeysAndRecordForms(t *testing.T) {
	a, d := newFeaturesTestApp(t, Options{FS: matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")})
	submit := func(title string) *data.Record {
		t.Helper()
		form := a.activeRecordForm()
		if form == nil {
			t.Fatalf("no Concept form: %q, focus %q, mode %q", a.Status(), a.Focus(), a.Mode())
		}
		form.Model().SetValue("title", title)
		form.Model().SetValue("summary", "A tracked suggestion")
		d.Keys("ctrl+s")
		if a.Modal() != "" {
			t.Fatalf("submit left modal open: %s", a.Status())
		}
		rec, err := a.Store.GetByField(data.KindConcept, "title", title)
		if err != nil || rec == nil {
			t.Fatalf("get %s Concept: %v", title, err)
		}
		return rec
	}
	assertRecord := func(rec *data.Record, kind, target, status string) {
		t.Helper()
		if rec.Text(data.FieldConceptKind) != kind || rec.Text(data.FieldConceptTargetKind) != target || rec.Text(data.FieldConceptStatus) != status {
			t.Fatalf("Concept fields = %+v, expected kind %s target %q status %s", rec.Fields, kind, target, status)
		}
	}

	// The chord is global across pages when Features is enabled.
	d.Keys("g", "C")
	form := a.activeRecordForm()
	if form == nil {
		t.Fatalf("g C did not open a form: %s", a.Status())
	}
	form.Model().SetValue(data.FieldConceptKind, data.ConceptKindWorkflow)
	form.Model().SetValue(data.FieldConceptTargetKind, "review")
	assertRecord(submit("Review workflow"), data.ConceptKindWorkflow, "review", data.ConceptStatusIdea)

	d.Keys("6")
	focusNav := func(key string) {
		t.Helper()
		d.Send(core.CommandEvent{Command: core.Command{ID: CmdFeaturesNavSelect, Args: map[string]any{"key": key}, Actor: User}})
		if a.Focus() != views.FeaturesNavPrefix+key {
			t.Fatalf("nav focus = %q, want %q", a.Focus(), views.FeaturesNavPrefix+key)
		}
	}
	focusContent := func(key string) {
		t.Helper()
		focusNav(key)
		d.Keys("enter")
		if a.Focus() != "features.detail.header" {
			t.Fatalf("content focus = %q, want detail header", a.Focus())
		}
	}

	focusNav("noun:" + data.KindTask)
	d.Keys("p")
	if a.Modal() != "" {
		t.Fatal("prop key escaped Features content scope")
	}
	focusContent("noun:" + data.KindTask)
	d.Keys("p")
	assertRecord(submit("Review priority"), data.ConceptKindProp, data.KindTask, data.ConceptStatusProposed)

	focusContent("page:" + SpacePanes)
	d.Keys("v")
	assertRecord(submit("Compact screen"), data.ConceptKindView, SpacePanes, data.ConceptStatusProposed)

	focusContent("component:panes.toc")
	d.Keys("v")
	assertRecord(submit("Page tags layout"), data.ConceptKindView, "panes.toc", data.ConceptStatusProposed)

	a.cfg.userSettings[SetFeaturesItemization] = false
	a.reloadSettings()
	a.applySettings()
	focusNav("page:" + SpacePanes)
	d.Keys("enter", "v")
	if a.Modal() != "" {
		t.Fatalf("view key opened a form after opting out: %q", a.Modal())
	}
	root := a.View(a.State())
	if root.Find("features.section.views") != nil || root.Find("features.section.props."+data.KindPane) != nil ||
		root.Find(views.FeaturesNavPrefix+"concept:prop") == nil {
		t.Fatal("itemization opt-out did not hide prop/view inventory while keeping Concept tracking")
	}
}
