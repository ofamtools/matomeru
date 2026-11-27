package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestFeaturesUnifiedInteractiveOutlineFeaturesConceptsViewsAndRawIdeas(t *testing.T) {
	fs := matomeruCheckoutFS("/home/dev", "/home/dev/proj/matomeru")
	a, d := newFeaturesTestApp(t, Options{FS: fs})

	// Seed a tracked Concept for page:panes so Features, Views, Props, Concepts, and Raw Ideas all populate.
	conceptRec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindConcept,
		Fields: map[string]any{
			"title":                     "Split Pane Presets",
			data.FieldConceptKind:       data.ConceptKindFeature,
			data.FieldConceptTargetKind: SpacePanes,
			data.FieldConceptStatus:     data.ConceptStatusIdea,
			"summary":                   "Saved pane layout presets",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Open Features page (default features.outline=true).
	if !a.featuresOutlineEnabled() {
		t.Fatal("featuresOutlineEnabled() = false by default, want true")
	}
	d.Keys("4")
	frame := d.Frame()
	for _, want := range []string{"Features", "Views", "Props", "Concepts", "Raw Ideas", "Split Pane Presets"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("unified Features outline missing %q:\n%s", want, frame)
		}
	}

	// Verify features.outline.tree is a real KindTree node in the View tree.
	root := a.View(a.State())
	outlineNode := root.Find(featuresOutlineTree)
	if outlineNode == nil || outlineNode.Facts["isTree"] != true || outlineNode.Facts["featuresOutline"] != true {
		t.Fatalf("expected interactive features.outline.tree node, got %+v", outlineNode)
	}

	// 1. Focus outline, fold & unfold the Features section via z c / z o.
	a.Shell.State = withFocus(a.State(), featuresOutlineTree)
	tm := a.trees.featuresOutlineState().model
	selectNode := func(id string) {
		tm.Select(id)
		a.viewMemo.gen++
		_ = d.Frame()
	}
	selectNode(featSecFeatures)
	d.Keys("z", "c")
	if !tm.Folded[featSecFeatures] {
		t.Fatal("z c did not collapse Features section in unified outline")
	}
	d.Keys("z", "o")
	if tm.Folded[featSecFeatures] {
		t.Fatal("z o did not expand Features section in unified outline")
	}

	// 2. Comment (`r`) on a catalog Feature highlight node -> saves note & displays inline under that Feature!
	selectNode("featitem.highlight.0")
	d.Keys("r")
	if a.Modal() != featCommentModalID || a.Mode() != ModeComposer {
		t.Fatalf("r on Feature item did not open comment modal: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Type("Make highlight interactive with comments")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not close comment modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	afterComment := d.Frame()
	if !strings.Contains(afterComment, "💬 1") || !strings.Contains(afterComment, "Make highlight interactive") {
		t.Fatalf("comment did not appear inline under Feature node:\n%s", afterComment)
	}
	// Toggle inline comment thread with `t`.
	d.Keys("t")
	if a.trees.featuresOutlineState().expandedNotes["featitem.highlight.0"] {
		t.Fatal("t did not collapse Feature inline comment thread")
	}
	d.Keys("t")
	if !a.trees.featuresOutlineState().expandedNotes["featitem.highlight.0"] {
		t.Fatal("second t did not re-expand Feature inline comment thread")
	}

	// 3. Interact with a Views node (`r` comment) and add a suggested View inline under Views (`o`).
	selectNode("featitem.view.0")
	d.Keys("r")
	if a.Modal() != featCommentModalID {
		t.Fatalf("r on View node did not open comment modal: modal=%q", a.Modal())
	}
	d.Type("Add compact mode toggle to this view")
	d.Keys("ctrl+s")
	if !strings.Contains(d.Frame(), "Add compact mode toggle") {
		t.Fatalf("comment on View node not shown inline:\n%s", d.Frame())
	}

	selectNode(featSecViews)
	d.Keys("o")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("o on Views section did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type("Kanban Panes View")
	d.Keys("esc")
	viewConcept, err := a.Store.GetByField(data.KindConcept, "title", "Kanban Panes View")
	if err != nil || viewConcept == nil {
		t.Fatalf("inline add under Views did not create view Concept: err=%v", err)
	}
	if viewConcept.Text(data.FieldConceptKind) != data.ConceptKindView {
		t.Fatalf("inline View conceptKind = %q, want %q", viewConcept.Text(data.FieldConceptKind), data.ConceptKindView)
	}

	// 4. Interact with a Concept node: star (`m`), comment (`r`), open (`E`).
	selectNode(conceptRec.ID)
	d.Keys("m")
	if got, _ := a.Store.Get(conceptRec.Ref()); got == nil || !got.IsStarred() {
		t.Fatalf("m did not star Concept node: %+v", got)
	}
	d.Keys("r")
	if a.Modal() != featCommentModalID {
		t.Fatalf("r on Concept node did not open comment modal: modal=%q", a.Modal())
	}
	d.Type("Support per-project presets")
	d.Keys("ctrl+s")
	if rels, err := a.Store.Related(conceptRec.Ref(), data.RelAbout, data.Incoming); err != nil || len(rels) != 1 {
		t.Fatalf("Concept comment not linked via RelAbout: rels=%+v err=%v", rels, err)
	}

	// 5. Inline outline capture under Raw Ideas (`c` focuses Raw Ideas, `o` adds parent + `enter` + `tab` adds nested child).
	d.Keys("c")
	if a.Focus() != featuresOutlineTree || tm.SelectedID != featSecIdeas {
		t.Fatalf("c did not focus Raw Ideas in features.outline.tree: focus=%q sel=%q", a.Focus(), tm.SelectedID)
	}
	d.Keys("o")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("o on Raw Ideas did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type("Unified Idea Parent")
	d.Keys("enter", "tab")
	d.Type("Unified Idea Child")
	d.Keys("esc")

	parentIdea, err := a.Store.GetByField(data.KindTask, "title", "Unified Idea Parent")
	if err != nil || parentIdea == nil {
		t.Fatalf("Unified Idea Parent not saved: err=%v", err)
	}
	childIdea, err := a.Store.GetByField(data.KindTask, "title", "Unified Idea Child")
	if err != nil || childIdea == nil {
		t.Fatalf("Unified Idea Child not saved: err=%v", err)
	}
	if pRef, ok := childIdea.RefField("parent"); !ok || pRef != parentIdea.Ref() {
		t.Fatalf("Unified Idea Child parent = %+v (ok=%v), want %+v", pRef, ok, parentIdea.Ref())
	}

	// 6. Clean slate (`b`) inside Features isolates the unified outline into a blank outline canvas, then `esc` exits cleanly.
	d.Keys("b")
	if a.Mode() != ModeOutlineEdit || !a.isCleanSlateActive(data.KindTask, featuresOutlineTree) {
		t.Fatalf("b did not activate clean slate on features.outline.tree: mode=%q active=%v", a.Mode(), a.isCleanSlateActive(data.KindTask, featuresOutlineTree))
	}
	csBlank := d.Frame()
	if strings.Contains(csBlank, "Unified Idea Parent") {
		t.Fatalf("clean slate on Features outline leaked prior idea:\n%s", csBlank)
	}
	d.Type("Clean Slate Feature Idea")
	d.Keys("esc")
	if !strings.Contains(d.Frame(), "Clean Slate Feature Idea") || strings.Contains(d.Frame(), "Unified Idea Parent") {
		t.Fatalf("clean slate did not isolate newly captured idea:\n%s", d.Frame())
	}
	d.Keys("esc")
	if a.isCleanSlateActive(data.KindTask, featuresOutlineTree) {
		t.Fatal("esc did not exit clean slate on features.outline.tree")
	}
	restored := d.Frame()
	if !strings.Contains(restored, "Clean Slate Feature Idea") || !strings.Contains(restored, "Unified Idea Parent") {
		t.Fatalf("exiting clean slate did not restore full unified Features outline:\n%s", restored)
	}

	// 7. Alternate view cycling (`)` / `(`) works on features.outline.tree.
	d.Keys(")")
	if got := a.outlineViewMode(featuresOutlineTree); got != OutlineViewActive {
		t.Fatalf(") on features.outline.tree got %q, want %q", got, OutlineViewActive)
	}
	d.Keys(")")
	if got := a.outlineViewMode(featuresOutlineTree); got != OutlineViewAgentThreads {
		t.Fatalf("second ) on features.outline.tree got %q, want %q", got, OutlineViewAgentThreads)
	}
	rows := a.trees.featuresOutlineState().model.Flatten()
	if !slices.ContainsFunc(rows, func(r tree.VisibleRow) bool { return r.ID == "featitem.highlight.0" }) {
		t.Fatalf("agent-threads view on Features outline should retain commented Feature node")
	}
	d.Keys(")")
	if got := a.outlineViewMode(featuresOutlineTree); got != OutlineViewAll {
		t.Fatalf("third ) did not wrap back to all: %q", got)
	}

	// 8. Open Concept record in Show (`E`).
	selectNode(conceptRec.ID)
	d.Keys("E")
	if ref, ok := a.Showing(); !ok || ref != conceptRec.Ref() {
		t.Fatalf("E on Concept node in Features outline did not open Show: ok=%v ref=%+v", ok, ref)
	}
}
