package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func newPasteTestApp(t *testing.T) (*App, *Driver, *data.Store) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	return a, NewDriver(a, 120, 36), store
}

func TestYankDefaultFormatRefAndTitleAndPickerField(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	enableLegacyPagesForTest(a, SpacePanes) // drives 1 Panes / 2 Work by digit
	t1, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":       "Auth Flow",
		"description": "OAuth2 PKCE login",
		"priority":    "P1",
		"workStatus":  "active",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Billing Sync",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}

	d.Keys("2", "g", "g")
	// 1. Default format is "ref": picker puts "ref" first, y y stores Kind="refs" + canonical ref text.
	d.Keys("y")
	ids := pickerIDs(a, "yank")
	if len(ids) == 0 || ids[0] != "ref" {
		t.Fatalf("expected ref first in yank picker under default format, got %v", ids)
	}
	d.Keys("y")
	reg, ok := a.RegisterValue(core.RegUnnamed)
	if !ok || reg.Kind != "refs" || !reg.HasRefs() || len(reg.Refs) != 1 || reg.Refs[0] != t1.Ref() {
		t.Fatalf("default y y register = %+v (ok=%v), want 1 ref %v", reg, ok, t1.Ref())
	}
	if reg.Text != data.RefText(t1.Ref()) || a.Register(core.RegClipboard) != data.RefText(t1.Ref()) {
		t.Fatalf("default y y text = %q / clip = %q, want %q", reg.Text, a.Register(core.RegClipboard), data.RefText(t1.Ref()))
	}
	if !strings.Contains(a.Status(), "p pastes contextually") {
		t.Fatalf("status = %q, want contextual paste hint", a.Status())
	}

	// 2. Picking an explicit field ("title") stores Kind="field", Field="title", no Refs.
	d.Keys("y", "/")
	d.Type("title")
	d.Keys("enter")
	reg, ok = a.RegisterValue(core.RegUnnamed)
	if !ok || reg.Kind != "field" || reg.Field != "title" || reg.HasRefs() || reg.Text != "Auth Flow" {
		t.Fatalf("picked title register = %+v, want field/title without refs", reg)
	}

	// 3. Visual multi-select y y stores all N refs with Linewise=true and newline-joined canonical refs.
	d.Keys("g", "g", "v", "j", "y", "y")
	reg, ok = a.RegisterValue(core.RegUnnamed)
	wantMulti := data.RefText(t1.Ref()) + "\n" + data.RefText(t2.Ref())
	if !ok || reg.Kind != "refs" || !reg.Linewise || len(reg.Refs) != 2 || reg.Text != wantMulti {
		t.Fatalf("visual multi y y = %+v, want 2 refs + %q", reg, wantMulti)
	}

	// 4. Setting yank.defaultFormat="title": picker puts "title" first, y y copies title text AND preserves Refs!
	a.cfg.userSettings[SetYankDefaultFormat] = YankFormatTitle
	a.reloadSettings()
	d.Keys("g", "g", "y")
	ids = pickerIDs(a, "yank")
	if len(ids) == 0 || ids[0] != "title" {
		t.Fatalf("expected title first when yank.defaultFormat=title, got %v", ids)
	}
	d.Keys("y")
	reg, ok = a.RegisterValue(core.RegUnnamed)
	if !ok || reg.Kind != "refs" || !reg.HasRefs() || reg.Refs[0] != t1.Ref() || reg.Text != "Auth Flow" {
		t.Fatalf("title-format y y = %+v, want title text with preserved Refs", reg)
	}
}

func TestPasteOutlineCloneMoveChildAndText(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	a.cfg.userSettings[SetOutlineStartCollapsed] = false
	a.reloadSettings()

	t1, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Alpha", "description": "Alpha details", "priority": "P1", "workStatus": "active", "order": int64(1),
	}}, User)
	_, _ = store.Tag(t1.Ref(), "backend", User)
	t2, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Beta", "order": int64(2),
	}}, User)
	t3, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Gamma", "order": int64(3),
	}}, User)

	// Open Outline modal (`T`), focus Alpha, yank (`y y`), move to Beta, paste after (`p`).
	d.Keys("T")
	if a.Mode() != ModeOutline {
		t.Fatalf("expected ModeOutline, got %q", a.Mode())
	}
	d.Keys("g", "g", "y", "y", "j", "p")

	nodes, err := a.recordTreeNodes(data.KindTask)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 4 {
		t.Fatalf("after paste after in outline, root count = %d, want 4 (status=%q)", len(nodes), a.Status())
	}
	titles := []string{nodes[0].Record.Text("title"), nodes[1].Record.Text("title"), nodes[2].Record.Text("title"), nodes[3].Record.Text("title")}
	if !slices.Equal(titles, []string{"Alpha", "Beta", "Alpha", "Gamma"}) {
		t.Fatalf("sibling order after p = %v, want [Alpha Beta Alpha Gamma]", titles)
	}
	cloned := nodes[2].Record
	if cloned.ID == t1.ID {
		t.Fatal("paste copy must create a new task ID, not reuse source")
	}
	if cloned.Text("description") != "Alpha details" || cloned.Text("priority") != "P1" || cloned.Text("workStatus") != "active" {
		t.Fatalf("cloned task lost fields: desc=%q prio=%q status=%q", cloned.Text("description"), cloned.Text("priority"), cloned.Text("workStatus"))
	}
	tags, _ := store.Related(cloned.Ref(), data.RelTag, data.Outgoing)
	if len(tags) != 1 || tags[0].To.ID != "backend" {
		t.Fatalf("cloned task tags = %+v, want [backend]", tags)
	}

	// Paste before (`P`) while focused on Alpha (index 0) -> inserts new clone at index 0.
	d.Keys("g", "g", "P")
	nodes, _ = a.recordTreeNodes(data.KindTask)
	if len(nodes) != 5 || nodes[0].Record.ID == t1.ID || nodes[0].Record.Text("title") != "Alpha" || nodes[1].Record.ID != t1.ID {
		t.Fatalf("P did not insert clone before Alpha: ids=%v/%v", nodes[0].Record.ID, nodes[1].Record.ID)
	}

	// Paste into (`g p`) while focused on Gamma -> creates child under Gamma!
	d.Keys("G", "g", "p")
	nodes, _ = a.recordTreeNodes(data.KindTask)
	gammaNode := nodes[len(nodes)-1]
	if gammaNode.Record.ID != t3.ID || len(gammaNode.Children) != 1 || gammaNode.Children[0].Record.Text("title") != "Alpha" {
		t.Fatalf("g p did not create child under Gamma: gamma=%s children=%d", gammaNode.Record.ID, len(gammaNode.Children))
	}

	// Cut-paste (`Cut: true`): moves t2 (Beta) into t3 (Gamma) without duplicating!
	beforeCount := len(nodes)
	a.nav.Registers.SetValue(core.RegUnnamed, core.RegisterValue{
		Kind: "refs",
		Refs: []core.Ref{t2.Ref()},
		Text: data.RefText(t2.Ref()),
		Cut:  true,
	})
	a.trees.outlineModel().Reveal(t3.ID)
	d.Keys("g", "p")
	nodes, _ = a.recordTreeNodes(data.KindTask)
	if len(nodes) != beforeCount-1 {
		t.Fatalf("cut-paste should remove Beta from root: roots=%d, want %d (status=%q)", len(nodes), beforeCount-1, a.Status())
	}
	movedBeta, _ := store.Get(t2.Ref())
	if pRef, ok := movedBeta.RefField("parent"); !ok || pRef != t3.Ref() {
		t.Fatalf("moved Beta parent = %v (%v), want %v", pRef, ok, t3.Ref())
	}

	// Cut-paste cycle guard: moving Gamma into its own child Beta is cleanly refused.
	a.nav.Registers.SetValue(core.RegUnnamed, core.RegisterValue{
		Kind: "refs",
		Refs: []core.Ref{t3.Ref()},
		Text: data.RefText(t3.Ref()),
		Cut:  true,
	})
	a.trees.outlineModel().Reveal(t2.ID)
	d.Keys("g", "p")
	if !strings.Contains(a.Status(), "cannot move") {
		t.Fatalf("cycle move should be refused, got status %q", a.Status())
	}

	// Plain text / field paste into Outline: creates 1 task per non-empty line!
	a.yank("First idea\n\nSecond idea\n", "text")
	d.Keys("g", "g", "p")
	nodes, _ = a.recordTreeNodes(data.KindTask)
	foundFirst, foundSecond := false, false
	for _, n := range nodes {
		if n.Record.Text("title") == "First idea" {
			foundFirst = true
		}
		if n.Record.Text("title") == "Second idea" {
			foundSecond = true
		}
	}
	if !foundFirst || !foundSecond {
		t.Fatalf("multi-line text paste did not create both tasks (first=%v second=%v, status=%q)", foundFirst, foundSecond, a.Status())
	}
}

func TestPasteLanesTreeCopyAndText(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	a.cfg.userSettings[SetOutlineStartCollapsed] = false
	a.reloadSettings()

	proj, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Core"}}, User)
	l1, _, _ := store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{
		"title": "Lane One", "laneStatus": "planned", "task": proj.Ref(), "ownerWords": "Ship typed registers",
	}}, User)
	l2, _, _ := store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{
		"title": "Lane Two", "laneStatus": "running", "task": proj.Ref(),
	}}, User)

	// Browse to Lanes in 2 Work (defaults to tree view).
	d.Keys("2", "g", "n", "/")
	d.Type("lanes")
	d.Keys("enter")
	if a.workKind() != data.KindLane || !a.inWorkTree(a.State()) {
		t.Fatalf("expected Lanes tree in 2 Work, got kind=%q tree=%v", a.workKind(), a.inWorkTree(a.State()))
	}

	// Yank Lane One and paste into Lane Two (`g p`).
	a.trees.workModel(data.KindLane).Reveal(l1.ID)
	d.Keys("y", "y")
	a.trees.workModel(data.KindLane).Reveal(l2.ID)
	d.Keys("g", "p")

	res, _ := store.Query(data.Query{Kind: data.KindLane})
	if len(res.Items) != 3 {
		t.Fatalf("after pasting lane into Lane Two, lane count = %d, want 3 (status=%q)", len(res.Items), a.Status())
	}
	var cloned *data.Record
	for i := range res.Items {
		if res.Items[i].ID != l1.ID && res.Items[i].ID != l2.ID {
			cloned = &res.Items[i]
		}
	}
	if cloned == nil {
		t.Fatal("missing cloned lane")
	}
	if pRef, ok := cloned.RefField("parentLane"); !ok || pRef != l2.Ref() {
		t.Fatalf("cloned lane parentLane = %v (%v), want %v", pRef, ok, l2.Ref())
	}
	if cloned.Text("ownerWords") != "Ship typed registers" {
		t.Fatalf("cloned lane ownerWords = %q", cloned.Text("ownerWords"))
	}

	// Text paste creates a child lane from text.
	a.yank("Sub-lane from text", "text")
	a.trees.workModel(data.KindLane).Reveal(l2.ID)
	d.Keys("g", "p")
	res, _ = store.Query(data.Query{Kind: data.KindLane})
	found := false
	for _, r := range res.Items {
		if r.Text("title") == "Sub-lane from text" {
			if pRef, ok := r.RefField("parentLane"); ok && pRef == l2.Ref() {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("text paste did not create child lane under Lane Two (status=%q)", a.Status())
	}
}

func TestPasteShowRelatesYankedRefs(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	t1, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Target Task"}}, User)
	t2, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Related Task"}}, User)
	doc, _, _ := store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{"title": "Design Spec"}}, User)

	// Store both t2 and doc as yanked refs, open Target Task Show page, press `p`.
	a.yankRefValues([]core.Ref{t2.Ref(), doc.Ref()}, "index")
	a.Shell.State = a.show(a.State(), t1.Ref())
	a.viewMemo.gen++
	d.Keys("p")

	rels, err := store.Related(t1.Ref(), data.RelRelated, data.Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 RelRelated edges on Show paste, got %d (status=%q)", len(rels), a.Status())
	}
	if !strings.Contains(a.Status(), "related 2") {
		t.Fatalf("unexpected Show paste status: %q", a.Status())
	}

	// Non-ref paste on Show reports helpful message and creates no relations.
	a.yank("plain note text", "text")
	d.Keys("p")
	if !strings.Contains(a.Status(), "requires yanked record ref") {
		t.Fatalf("non-ref Show paste status = %q", a.Status())
	}
}

func TestPasteQueueEnqueuesTaskCaptureAndText(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	task, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Queued Task"}}, User)
	capRec, _, _ := store.Create(data.CreateInput{Kind: data.KindCapture, Fields: map[string]any{
		"title": "Capture Idea", "body": "Capture Idea\nDetailed prompt body", "captureStatus": data.CaptureStatusCaptured,
	}}, User)

	d.Keys("g", "q")
	if a.Space() != SpaceQueue {
		t.Fatalf("expected SpaceQueue, got %q", a.Space())
	}

	// 1. Paste Task + Capture refs via `p`.
	a.yankRefValues([]core.Ref{task.Ref(), capRec.Ref()}, "index")
	d.Keys("p")

	gotTask, _ := store.Get(task.Ref())
	if gotTask.Text("agentStatus") != data.AgentStatusQueued {
		t.Fatalf("pasted task agentStatus = %q, want queued (status=%q)", gotTask.Text("agentStatus"), a.Status())
	}
	items, _ := store.QueryNamedQueue(data.DefaultQueueName, "")
	if len(items) < 2 {
		t.Fatalf("expected >=2 items in default queue after pasting task+capture, got %d", len(items))
	}

	// 2. Paste plain text onto Queue -> creates and enqueues a prompt task.
	a.yank("Investigate flaky integration test", "text")
	d.Keys("p")
	items, _ = store.QueryNamedQueue(data.DefaultQueueName, "")
	found := false
	for _, it := range items {
		if it.Task.Text("title") == "Investigate flaky integration test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("text paste onto Queue did not enqueue prompt item (status=%q)", a.Status())
	}
}

func TestPasteTerminalLeaderAndCaptureModalAndOptOut(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	enableLegacyPagesForTest(a, SpacePanes) // drives 1 Panes / 2 Work by digit
	pane, _, _ := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title": "dev-agent", "harness": "claude", "runStatus": "running",
	}}, User)
	task, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Fix bug"}}, User)
	doc, _, _ := store.Create(data.CreateInput{Kind: data.KindDocument, Fields: map[string]any{
		"title": "Architecture", "filePath": "/tmp/arch.md",
	}}, User)

	// 1. Live terminal (ModeTerminal) via leader-mirrored ctrl+a g p:
	//    Record with filePath -> @/tmp/arch.md; record without filePath -> canonical ref!
	d.Keys("1", "enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal, got %q", a.Mode())
	}
	a.yankRefValues([]core.Ref{doc.Ref(), task.Ref()}, "index")
	d.Keys("ctrl+a", "g", "p")
	wantTerm := "@/tmp/arch.md " + data.RefText(task.Ref())
	if got := a.termOf(pane.ID).typed; got != wantTerm {
		t.Fatalf("live terminal paste = %q, want %q (status=%q)", got, wantTerm, a.Status())
	}

	// 2. Capture Modal (ModeComposer) via alt+y:
	//    Ref with filePath pastes @filePath; explicit field pastes literal text.
	d.Keys("ctrl+a", "d")
	d.Keys("g", "i")
	if a.Modal() != captureComposeModalID || a.Mode() != ModeComposer {
		t.Fatalf("expected capture modal in ModeComposer, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	a.yankRefValues([]core.Ref{doc.Ref()}, "index")
	d.Keys("alt+y")
	cm := a.activeRichComposer()
	if cm == nil || cm.in.Value() != "@/tmp/arch.md" {
		t.Fatalf("capture modal ref paste = %q, want @/tmp/arch.md", cm.in.Value())
	}
	a.nav.Registers.SetValue(core.RegUnnamed, core.RegisterValue{Kind: "field", Field: "title", Text: " literal title"})
	d.Keys("alt+y")
	if cm.in.Value() != "@/tmp/arch.md literal title" {
		t.Fatalf("capture modal field paste = %q", cm.in.Value())
	}
	d.Keys("esc", "esc")

	// 3. Opt-out setting paste.contextual=false:
	//    Falls back to plain text only (no @filePath expansion in inputs, no structural paste in Outline).
	a.cfg.userSettings[SetPasteContextual] = false
	a.reloadSettings()

	d.Keys("T", "g", "g")
	before, _ := store.Query(data.Query{Kind: data.KindTask})
	a.yankRefValues([]core.Ref{task.Ref()}, "outline")
	d.Keys("p")
	after, _ := store.Query(data.Query{Kind: data.KindTask})
	if len(after.Items) != len(before.Items) {
		t.Fatalf("paste.contextual=false must not clone tasks in Outline: before=%d after=%d", len(before.Items), len(after.Items))
	}
	if !strings.Contains(a.Status(), "paste.contextual=false") {
		t.Fatalf("expected opt-out status, got %q", a.Status())
	}
	d.Keys("q")

	// Plain-text fallback in Capture Modal pastes literal reg.Text (not @filePath).
	d.Keys("g", "i")
	a.yankRefValues([]core.Ref{doc.Ref()}, "index")
	d.Keys("alt+y")
	cm = a.activeRichComposer()
	if got := cm.in.Value(); got != data.RefText(doc.Ref()) {
		t.Fatalf("paste.contextual=false in capture modal = %q, want literal reg.Text %q", got, data.RefText(doc.Ref()))
	}
}

func TestPasteUndoTakesBackCloneTextAndRelate(t *testing.T) {
	a, d, store := newPasteTestApp(t)
	a.cfg.userSettings[SetOutlineStartCollapsed] = false
	a.reloadSettings()

	proj, _, _ := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Core"}}, User)
	l1, _, _ := store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{"title": "Lane One", "task": proj.Ref()}}, User)
	l2, _, _ := store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{"title": "Lane Two", "task": proj.Ref()}}, User)
	_, _ = store.Tag(l1.Ref(), "backend", User)
	laneCount := func() int {
		res, _ := store.Query(data.Query{Kind: data.KindLane})
		return len(res.Items)
	}

	d.Keys("2", "g", "n", "/")
	d.Type("lanes")
	d.Keys("enter")

	// Clone paste: the tag copy must not take the owner's undo slot.
	a.trees.workModel(data.KindLane).Reveal(l1.ID)
	d.Keys("y", "y")
	a.trees.workModel(data.KindLane).Reveal(l2.ID)
	d.Keys("g", "p")
	if laneCount() != 3 || !strings.Contains(a.Status(), "u undoes") {
		t.Fatalf("clone paste: lanes=%d status=%q", laneCount(), a.Status())
	}
	d.Keys("u")
	if laneCount() != 2 {
		t.Fatalf("u after clone paste left %d lanes, want 2 (status=%q)", laneCount(), a.Status())
	}

	// Text paste.
	a.yank("Lane from text", "text")
	a.trees.workModel(data.KindLane).Reveal(l2.ID)
	d.Keys("g", "p")
	if laneCount() != 3 || !strings.Contains(a.Status(), "u undoes") {
		t.Fatalf("text paste: lanes=%d status=%q", laneCount(), a.Status())
	}
	d.Keys("u")
	if laneCount() != 2 {
		t.Fatalf("u after text paste left %d lanes, want 2 (status=%q)", laneCount(), a.Status())
	}

	// Show relate.
	a.yankRefValues([]core.Ref{l1.Ref()}, "index")
	a.Shell.State = a.show(a.State(), proj.Ref())
	a.viewMemo.gen++
	d.Keys("p")
	if rels, _ := store.Related(proj.Ref(), data.RelRelated, data.Outgoing); len(rels) != 1 || !strings.Contains(a.Status(), "u undoes") {
		t.Fatalf("show paste: rels=%d status=%q", len(rels), a.Status())
	}
	d.Keys("u")
	if rels, _ := store.Related(proj.Ref(), data.RelRelated, data.Outgoing); len(rels) != 0 {
		t.Fatalf("u after show paste left %d relations, want 0 (status=%q)", len(rels), a.Status())
	}
}
