package mcpui

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

var (
	agent = core.Actor{Kind: "agent", ID: "w12", Label: "claude"}
	owner = core.Actor{Kind: "user", ID: "owner"}
)

type fixture struct {
	t     *testing.T
	store *data.Store
	r     *Router
	task  *data.Record
	pane  *data.Record
}

func setup(t *testing.T) *fixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := &fixture{t: t, store: store, r: New(store)}
	f.task, _, err = store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Matomeru v2"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	f.pane, _, err = store.Create(data.CreateInput{Kind: data.KindPane, ID: "w12",
		Fields: map[string]any{"title": "claude", "task": f.task.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// call runs a tool through the JSON entry point and decodes its result into out.
func (f *fixture) call(tool string, args any, out any) {
	f.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		f.t.Fatal(err)
	}
	res, err := f.r.Call(context.Background(), agent, tool, raw)
	if err != nil {
		f.t.Fatalf("%s: %v", tool, err)
	}
	b, _ := json.Marshal(res)
	if err := json.Unmarshal(b, out); err != nil {
		f.t.Fatalf("%s result: %v", tool, err)
	}
}

func (f *fixture) page(id string) (data.Record, ui.ViewNode) {
	f.t.Helper()
	rec, err := f.store.Get(core.Ref{Kind: data.KindPage, ID: id})
	if err != nil {
		f.t.Fatal(err)
	}
	root, err := PageRoot(*rec)
	if err != nil {
		f.t.Fatal(err)
	}
	return *rec, root
}

func childIDs(n ui.ViewNode) []string {
	out := make([]string, len(n.Children))
	for i, c := range n.Children {
		out[i] = c.ID
	}
	return out
}

func TestToolSpecsMatchRealParams(t *testing.T) {
	specs := New(nil).ToolSpecs()
	args := map[string]any{
		ToolCreatePage: CreatePageArgs{}, ToolUpdatePage: UpdatePageArgs{}, ToolAsk: AskArgs{}, ToolListPages: ListPagesArgs{},
	}
	if len(specs) != 4 {
		t.Fatalf("%d tools, want exactly 4", len(specs))
	}
	for _, spec := range specs {
		arg, ok := args[spec.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", spec.Name)
		}
		var fields []string
		rt := reflect.TypeOf(arg)
		for i := range rt.NumField() {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			fields = append(fields, name)
		}
		var declared []string
		for name := range spec.InputSchema["properties"].(map[string]any) {
			declared = append(declared, name)
		}
		slices.Sort(fields)
		slices.Sort(declared)
		if !reflect.DeepEqual(fields, declared) {
			t.Errorf("%s: schema properties %v, real params %v", spec.Name, declared, fields)
		}
		for _, req := range spec.InputSchema["required"].([]string) {
			if !slices.Contains(fields, req) {
				t.Errorf("%s: required %q is not a param", spec.Name, req)
			}
		}
		if spec.InputSchema["additionalProperties"] != false {
			t.Errorf("%s: schema accepts unknown keys", spec.Name)
		}
		if _, err := json.Marshal(spec); err != nil {
			t.Errorf("%s: spec is not JSON: %v", spec.Name, err)
		}
	}
}

func TestCreateAndUpdatePage(t *testing.T) {
	f := setup(t)
	var made PageResult
	f.call(ToolCreatePage, map[string]any{
		"pane": "w12", "title": "Compare v2 architectures", "pageKind": "response",
		"root": map[string]any{"kind": "vstack", "id": "body", "gap": 1, "children": []any{
			map[string]any{"kind": "card", "id": "prompt", "title": "Prompt", "props": map[string]any{"summary": "Propose 4 ways"}},
			map[string]any{"kind": "grid", "id": "options", "minColW": 36, "children": []any{
				map[string]any{"kind": "card", "id": "opt-1", "focusable": true, "scope": "choice", "title": "1. Concentric rings"},
			}},
		}},
	}, &made)
	if made.ID == "" || made.Slug != "compare-v2-architectures" || made.Ref != (core.Ref{Kind: data.KindPage, ID: made.ID}) || made.UpdatedAt.IsZero() {
		t.Fatalf("create result = %+v", made)
	}
	rec, root := f.page(made.ID)
	if rec.Text("pageKind") != "response" || !rec.IsUnread() {
		t.Fatalf("page = %+v", rec.Fields)
	}
	if pane, _ := rec.RefField("pane"); pane != f.pane.Ref() {
		t.Fatalf("pane ref = %v", pane)
	}
	if task, _ := rec.RefField("task"); task != f.task.Ref() {
		t.Fatalf("page did not inherit the pane's task: %v", task)
	}
	if got := root.Find("opt-1"); got == nil || got.Scope != "choice" || !got.Focusable || root.Find("options").MinColW != 36 {
		t.Fatalf("stored tree lost data: %+v", root)
	}
	// The page is related from its pane and from its task.
	for _, from := range []core.Ref{f.pane.Ref(), f.task.Ref()} {
		pages, err := f.store.RelatedRecords(from, data.RelPage, data.Outgoing)
		if err != nil || len(pages) != 1 || pages[0].ID != made.ID {
			t.Fatalf("pages related from %v = %+v, %v", from, pages, err)
		}
	}

	// A second page with the same title gets its own slug.
	var twin PageResult
	f.call(ToolCreatePage, map[string]any{"pane": "w12", "title": "Compare v2 architectures", "root": map[string]any{"kind": "text"}}, &twin)
	if twin.Slug != "compare-v2-architectures-2" {
		t.Fatalf("second slug = %q", twin.Slug)
	}

	var res PageResult
	// append and prepend by slug; an inserted card is marked unread.
	f.call(ToolUpdatePage, map[string]any{"pane": "w12", "page": made.Slug, "mode": "append",
		"node": map[string]any{"kind": "markdown", "id": "notes", "props": map[string]any{"markdown": "## Notes"}}}, &res)
	f.call(ToolUpdatePage, map[string]any{"page": made.ID, "mode": "prepend",
		"card": map[string]any{"kind": "card", "id": "news", "title": "New finding"}}, &res)
	rec, root = f.page(made.ID)
	if got := childIDs(root); !reflect.DeepEqual(got, []string{"news", "prompt", "options", "notes"}) {
		t.Fatalf("children after prepend and append = %v", got)
	}
	if !root.Find("news").Unread || root.Find("notes").Unread {
		t.Fatal("only the inserted card should be unread")
	}
	if !res.UpdatedAt.After(made.UpdatedAt) || !rec.UpdatedAt.Equal(res.UpdatedAt) {
		t.Fatalf("update did not move updatedAt: %v -> %v", made.UpdatedAt, res.UpdatedAt)
	}

	// prepend into a container inside the tree.
	f.call(ToolUpdatePage, map[string]any{"page": made.ID, "mode": "prepend", "targetId": "options",
		"card": map[string]any{"kind": "card", "id": "opt-0", "title": "0. Do nothing"}}, &res)
	// patch one card in place, found anywhere in the tree.
	f.call(ToolUpdatePage, map[string]any{"page": made.ID, "mode": "patch",
		"card": map[string]any{"kind": "card", "id": "opt-1", "title": "1. Rings (picked)", "badges": []any{map[string]any{"text": "picked"}}}}, &res)
	_, root = f.page(made.ID)
	if got := childIDs(*root.Find("options")); !reflect.DeepEqual(got, []string{"opt-0", "opt-1"}) {
		t.Fatalf("grid children = %v", got)
	}
	if got := root.Find("opt-1"); got.Title != "1. Rings (picked)" || len(got.Badges) != 1 {
		t.Fatalf("patched card = %+v", got)
	}
	if got := childIDs(root); len(got) != 4 {
		t.Fatalf("patch changed the page's shape: %v", got)
	}

	// replace swaps the whole tree and can retitle the page.
	f.call(ToolUpdatePage, map[string]any{"page": made.ID, "mode": "replace", "title": "Decision",
		"root": map[string]any{"kind": "text", "id": "done", "props": map[string]any{"text": "Rings."}}}, &res)
	rec, root = f.page(made.ID)
	if root.Kind != ui.KindText || root.ID != "done" || rec.Text("title") != "Decision" {
		t.Fatalf("after replace: %+v, title %q", root, rec.Text("title"))
	}
	// Appending to a root that holds no children wraps it.
	f.call(ToolUpdatePage, map[string]any{"page": made.ID, "mode": "append", "node": map[string]any{"kind": "text", "id": "more"}}, &res)
	if _, root = f.page(made.ID); root.Kind != ui.KindVStack || !reflect.DeepEqual(childIDs(root), []string{"done", "more"}) {
		t.Fatalf("append to a leaf root: %+v", root)
	}

	// Malformed calls are answered with an error that says what is wrong.
	for name, tc := range map[string]struct {
		tool string
		args string
		want string
	}{
		"unknown tool":      {"ui.delete_page", `{}`, "unknown tool"},
		"unknown key":       {ToolCreatePage, `{"title":"x","root":{"kind":"text"},"colour":"red"}`, "colour"},
		"missing root":      {ToolCreatePage, `{"title":"x"}`, "root is required"},
		"unknown node kind": {ToolCreatePage, `{"title":"x","root":{"kind":"vstack","children":[{"kind":"carousel"}]}}`, `unknown kind "carousel"`},
		"duplicate ids":     {ToolCreatePage, `{"title":"x","root":{"kind":"vstack","id":"a","children":[{"kind":"text","id":"a"}]}}`, "used twice"},
		"unknown mode":      {ToolUpdatePage, `{"page":"` + made.ID + `","mode":"merge","node":{"kind":"text"}}`, "mode"},
		"patch target":      {ToolUpdatePage, `{"page":"` + made.ID + `","mode":"patch","node":{"kind":"text","id":"nope"}}`, "nope"},
		"unknown page":      {ToolUpdatePage, `{"page":"nope","mode":"append","node":{"kind":"text"}}`, "nope"},
	} {
		if _, err := f.r.Call(context.Background(), agent, tc.tool, json.RawMessage(tc.args)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to mention %q", name, err, tc.want)
		}
	}
	if _, err := f.r.Call(context.Background(), agent, ToolUpdatePage, json.RawMessage(`{"page":"nope","mode":"append","node":{"kind":"text"}}`)); !errors.Is(err, data.ErrNotFound) {
		t.Errorf("unknown page error = %v, want ErrNotFound", err)
	}
}

func TestAskUnreadLifecycle(t *testing.T) {
	f := setup(t)
	var asked PageResult
	f.call(ToolAsk, map[string]any{"pane": "w12", "title": "Keep or drop Saved Panes?", "details": "It has two users.",
		"choices": []any{
			map[string]any{"id": "keep", "label": "Keep the page"},
			map[string]any{"id": "drop", "label": "Drop the page", "description": "reopen through the pane picker"},
		}}, &asked)
	if asked.Slug != QuestionsSlug || asked.CardID == "" {
		t.Fatalf("ask result = %+v", asked)
	}
	page, root := f.page(asked.ID)
	if page.Text("pageKind") != "questions" || !page.Bool("pinned") || page.Text("title") != "Questions" {
		t.Fatalf("questions page = %v", page.Fields)
	}
	card := root.Find(asked.CardID)
	if card == nil || !card.Unread || card.Scope != ScopeQuestion || !card.Focusable || card.Facts["answered"] != false {
		t.Fatalf("question card = %+v", card)
	}
	form := root.Find(asked.CardID + ".form")
	if form == nil || form.Kind != ui.KindForm || form.Props["onSubmit"] != AnswerCommand {
		t.Fatalf("question form = %+v", form)
	}
	var fields []core.ParamSpec
	b, _ := json.Marshal(form.Props["fields"])
	json.Unmarshal(b, &fields)
	if len(fields) != 2 || !reflect.DeepEqual(fields[0].Enum, []string{"keep", "drop"}) || !fields[0].Required {
		t.Fatalf("form fields = %+v", fields)
	}
	// The card and the page render, and focus on the form sits inside the question.
	if frame, _ := ui.Render(root, ui.DefaultDarkTheme(), form.ID, 80, 20); !strings.Contains(frame.ANSI, "Keep or drop Saved Panes?") {
		t.Fatalf("question does not render:\n%s", frame.ANSI)
	}
	if chain := ui.ContextChain(root, form.ID, nil); len(chain) != 3 || chain[0].Scope != "form" || chain[1].Scope != ScopeQuestion {
		t.Fatalf("context chain = %+v", chain)
	}

	// updatedAt > viewedAt: the page is unread until the owner views it.
	if !page.IsUnread() || !page.Time("viewedAt").IsZero() {
		t.Fatalf("new questions page should be unread: viewedAt %q", page.Text("viewedAt"))
	}
	viewed, err := f.r.MarkViewed(asked.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if viewed.IsUnread() || !viewed.Time("viewedAt").Equal(viewed.UpdatedAt) {
		t.Fatalf("after MarkViewed: viewedAt %v, updatedAt %v", viewed.Time("viewedAt"), viewed.UpdatedAt)
	}
	if _, root = f.page(asked.ID); !root.Find(asked.CardID).Unread {
		t.Fatal("viewing the page must not clear an unanswered question")
	}

	// A second question goes on the same page, on top, and makes it unread again.
	var second PageResult
	f.call(ToolAsk, map[string]any{"pane": "w12", "id": "q-model", "title": "Which model for the lane?",
		"form": map[string]any{"fields": []any{map[string]any{"name": "model", "type": "string", "required": true}}}}, &second)
	page, root = f.page(asked.ID)
	if second.ID != asked.ID || !page.IsUnread() || !reflect.DeepEqual(childIDs(root), []string{"q-model", asked.CardID}) {
		t.Fatalf("second ask: page %s, unread %v, cards %v", second.ID, page.IsUnread(), childIDs(root))
	}
	if _, err := f.r.Call(context.Background(), agent, ToolAsk, json.RawMessage(`{"pane":"w12","id":"q-model","title":"again","choices":[{"id":"a"}]}`)); err == nil {
		t.Fatal("a second card with the same id was accepted")
	}

	// The owner answers: the card is read, badged, and keeps the structured answer.
	answer := map[string]any{"choice": "drop", "notes": "fold into panes"}
	answered, err := f.r.SubmitAnswer(asked.ID, asked.CardID, answer, owner)
	if err != nil {
		t.Fatal(err)
	}
	if answered.IsUnread() {
		t.Fatal("answering should leave the page read")
	}
	_, root = f.page(asked.ID)
	card = root.Find(asked.CardID)
	if card.Unread || card.Tone != "success" || card.Facts["answered"] != true || !reflect.DeepEqual(card.Props[propAnswer], answer) {
		t.Fatalf("answered card = %+v", card)
	}
	if len(card.Badges) != 1 || card.Badges[0].Text != "answered" {
		t.Fatalf("badges = %+v", card.Badges)
	}
	if form = root.Find(asked.CardID + ".form"); !reflect.DeepEqual(form.Props["values"], answer) {
		t.Fatalf("form values = %v", form.Props["values"])
	}
	if open := root.Find("q-model"); !open.Unread || open.Facts["answered"] != false {
		t.Fatalf("the other question changed: %+v", open)
	}

	// The agent reads the answer back with ui.list_pages.
	var list ListPagesResult
	f.call(ToolListPages, map[string]any{"pane": "w12"}, &list)
	if len(list.Pages) != 1 || len(list.Pages[0].Questions) != 2 {
		t.Fatalf("list = %+v", list)
	}
	open, done := list.Pages[0].Questions[0], list.Pages[0].Questions[1]
	if open.CardID != "q-model" || open.Answered || done.CardID != asked.CardID || !done.Answered || !reflect.DeepEqual(done.Answer, answer) || done.AnsweredAt == "" {
		t.Fatalf("question states = %+v / %+v", open, done)
	}
	if _, err := f.r.SubmitAnswer(asked.ID, "nope", answer, owner); !errors.Is(err, data.ErrNotFound) {
		t.Fatalf("answer to an unknown card: %v", err)
	}

	// Every step is in the page's history under the actor who took it.
	ref := core.Ref{Kind: data.KindPage, ID: asked.ID}
	hist, _ := f.store.History(&ref, 1)
	if len(hist) != 1 || hist[0].Actor != owner {
		t.Fatalf("latest change = %+v", hist)
	}
}

func TestListPagesScopedAndGlobal(t *testing.T) {
	f := setup(t)
	text := map[string]any{"kind": "text", "props": map[string]any{"text": "hello"}}
	var screen, plan, other, loose PageResult
	f.call(ToolCreatePage, map[string]any{"pane": "w12", "title": "Screen", "pageKind": "screen", "pinned": true, "root": text}, &screen)
	f.call(ToolCreatePage, map[string]any{"pane": "w12", "title": "Plan", "pageKind": "plan", "root": text}, &plan)
	// A pane that is not a stored record is still accepted.
	f.call(ToolCreatePage, map[string]any{"pane": "w99", "title": "Plan", "root": text}, &other)
	f.call(ToolCreatePage, map[string]any{"title": "No pane", "root": text}, &loose)
	if _, err := f.r.MarkViewed(plan.ID, owner); err != nil {
		t.Fatal(err)
	}

	var scoped ListPagesResult
	f.call(ToolListPages, map[string]any{"pane": "w12"}, &scoped)
	if len(scoped.Pages) != 2 || scoped.Pages[0].ID != screen.ID || scoped.Pages[1].ID != plan.ID {
		t.Fatalf("scoped list = %+v", scoped.Pages)
	}
	first, second := scoped.Pages[0], scoped.Pages[1]
	if first.Title != "Screen" || first.Slug != "screen" || first.PageKind != "screen" || !first.Pinned || !first.Unread ||
		first.ViewedAt != nil || first.PaneRef == nil || *first.PaneRef != f.pane.Ref() || first.UpdatedAt.IsZero() {
		t.Fatalf("screen row = %+v", first)
	}
	if second.Unread || second.Pinned || second.ViewedAt == nil || !second.ViewedAt.Equal(second.UpdatedAt) {
		t.Fatalf("plan row = %+v", second)
	}

	var global ListPagesResult
	f.call(ToolListPages, map[string]any{}, &global)
	if len(global.Pages) != 4 {
		t.Fatalf("global list has %d pages", len(global.Pages))
	}
	// Unread pages first (newest first), then the viewed one.
	var ids []string
	for _, p := range global.Pages {
		ids = append(ids, p.ID)
	}
	if want := []string{loose.ID, other.ID, screen.ID, plan.ID}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("global order = %v, want %v", ids, want)
	}
	if global.Pages[0].PaneRef != nil || *global.Pages[1].PaneRef != (core.Ref{Kind: data.KindPane, ID: "w99"}) {
		t.Fatalf("pane refs = %+v, %+v", global.Pages[0].PaneRef, global.Pages[1].PaneRef)
	}

	// Archived pages are listed only on request.
	if _, _, err := f.store.Archive(core.Ref{Kind: data.KindPage, ID: plan.ID}, owner); err != nil {
		t.Fatal(err)
	}
	f.call(ToolListPages, map[string]any{"pane": "w12"}, &scoped)
	var withArchived ListPagesResult
	f.call(ToolListPages, map[string]any{"pane": "w12", "includeArchived": true}, &withArchived)
	if len(scoped.Pages) != 1 || len(withArchived.Pages) != 2 {
		t.Fatalf("archived filter: %d and %d pages", len(scoped.Pages), len(withArchived.Pages))
	}
}

func TestAskAndQuestionBidirectionalSync(t *testing.T) {
	f := setup(t)

	// 1. Single ui.ask creates a synchronized KindQuestion record inheriting pane's task.
	var single PageResult
	f.call(ToolAsk, map[string]any{
		"pane":    "w12",
		"id":      "q-single",
		"title":   "Use WAL mode?",
		"details": "SQLite journal mode for local DB.",
		"choices": []any{
			map[string]any{"id": "yes", "label": "Yes"},
			map[string]any{"id": "no", "label": "No"},
		},
	}, &single)

	qRes, err := f.store.Query(data.Query{Kind: data.KindQuestion})
	if err != nil || len(qRes.Items) != 1 {
		t.Fatalf("questions after single ui.ask = %+v, %v", qRes, err)
	}
	q1 := qRes.Items[0]
	if q1.Text("prompt") != "Use WAL mode?" || q1.Text("details") != "SQLite journal mode for local DB." ||
		q1.Text("questionKind") != "decision" || q1.Text("status") != "open" || q1.Text("cardId") != "q-single" {
		t.Fatalf("question record fields = %+v", q1.Fields)
	}
	if p, _ := q1.RefField("pane"); p != f.pane.Ref() {
		t.Fatalf("question pane = %v, want %v", p, f.pane.Ref())
	}
	if tr, _ := q1.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("question task = %v, want %v", tr, f.task.Ref())
	}
	if pg, _ := q1.RefField("page"); pg != single.Ref {
		t.Fatalf("question page = %v, want %v", pg, single.Ref)
	}
	if _, hasChoices := q1.Value("choices"); !hasChoices {
		t.Fatalf("question choices missing: %+v", q1.Fields)
	}

	// Answering on ui.page via SubmitAnswer syncs back to KindQuestion.
	if _, err := f.r.SubmitAnswer(single.ID, "q-single", map[string]any{"choice": "yes", "notes": "fast"}, owner); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	q1After, err := f.store.Get(q1.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if q1After.Text("status") != "answered" || q1After.Text("answer") != "yes (fast)" || q1After.Text("answeredAt") == "" {
		t.Fatalf("synced question after SubmitAnswer = %+v", q1After.Fields)
	}

	// 2. Batched ui.ask creates one KindQuestion per card, and AnswerQuestion syncs back to ui.page!
	var batch PageResult
	f.call(ToolAsk, map[string]any{
		"pane": "w12",
		"id":   "b1",
		"questions": []any{
			map[string]any{"id": "q-a", "title": "Pick database", "choices": []any{map[string]any{"id": "sqlite"}, map[string]any{"id": "pg"}}},
			map[string]any{"id": "q-b", "title": "Pick port", "form": map[string]any{"fields": []any{map[string]any{"name": "port", "type": "string", "required": true}}}},
		},
	}, &batch)

	qRes, err = f.store.Query(data.Query{Kind: data.KindQuestion})
	if err != nil || len(qRes.Items) != 3 {
		t.Fatalf("questions after batch ui.ask = %d, %v", len(qRes.Items), err)
	}
	var qa, qb data.Record
	for _, rec := range qRes.Items {
		switch rec.Text("cardId") {
		case "q-a":
			qa = rec
		case "q-b":
			qb = rec
		}
	}
	if qa.ID == "" || qb.ID == "" {
		t.Fatalf("missing batch question records: %+v", qRes.Items)
	}

	// Answer q-a via Router.AnswerQuestion -> must update both KindQuestion and ui.page card!
	answeredQA, err := f.r.AnswerQuestion(qa.ID, "sqlite", owner)
	if err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	if answeredQA.Text("status") != "answered" || answeredQA.Text("answer") != "sqlite" || answeredQA.Text("answeredAt") == "" {
		t.Fatalf("AnswerQuestion record = %+v", answeredQA.Fields)
	}
	_, root := f.page(batch.ID)
	cardA := root.Find("q-a")
	if cardA == nil || cardA.Unread || cardA.Facts["answered"] != true {
		t.Fatalf("ui.page card q-a not synced: %+v", cardA)
	}
	if !reflect.DeepEqual(cardA.Props[propAnswer], map[string]any{"choice": "sqlite"}) {
		t.Fatalf("ui.page card q-a answer = %#v", cardA.Props[propAnswer])
	}
	// q-b remains open until SubmitAnswers answers it.
	if cardB := root.Find("q-b"); cardB == nil || cardB.Facts["answered"] != false {
		t.Fatalf("ui.page card q-b changed unexpectedly: %+v", cardB)
	}
	if _, err := f.r.SubmitAnswers(batch.ID, map[string]map[string]any{"q-b": {"port": "8080"}}, owner); err != nil {
		t.Fatalf("SubmitAnswers: %v", err)
	}
	qbAfter, _ := f.store.Get(qb.Ref())
	if qbAfter.Text("status") != "answered" || !strings.Contains(qbAfter.Text("answer"), "8080") {
		t.Fatalf("qb after SubmitAnswers = %+v", qbAfter.Fields)
	}
}

func TestDailyDataTools(t *testing.T) {
	f := setup(t)
	box := NewToolbox(f.store, f.r)
	ctx := WithPaneID(context.Background(), "w12")

	// 1. data.suggest_command auto-binds pane from context and inherits task from pane.
	rawCmd, err := box.Call(ctx, agent, ToolSuggestCommand, json.RawMessage(`{
		"title": "Run unit tests",
		"command": "go test -race ./...",
		"cwd": "/proj/matomeru",
		"notes": "Verify race detector passes"
	}`))
	if err != nil {
		t.Fatalf("suggest_command: %v", err)
	}
	cmdRes := rawCmd.(*SuggestCommandResult)
	if cmdRes.Command == nil || cmdRes.Command.Kind != data.KindCommand || cmdRes.Command.Text("status") != "pending" ||
		cmdRes.Command.Text("command") != "go test -race ./..." {
		t.Fatalf("suggest_command result = %+v", cmdRes)
	}
	if p, _ := cmdRes.Command.RefField("pane"); p != f.pane.Ref() {
		t.Fatalf("command pane = %v, want %v", p, f.pane.Ref())
	}
	if tr, _ := cmdRes.Command.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("command task = %v, want %v", tr, f.task.Ref())
	}

	// 2. data.create_document auto-binds pane via RelCreated and inherits task.
	rawDoc, err := box.Call(ctx, agent, ToolCreateDocument, json.RawMessage(`{
		"title": "Architecture Plan",
		"filePath": "/proj/matomeru/docs/plan.md",
		"summary": "Daily workflow schema design"
	}`))
	if err != nil {
		t.Fatalf("create_document: %v", err)
	}
	docRes := rawDoc.(*CreateDocumentResult)
	if docRes.Document == nil || docRes.Document.Kind != data.KindDocument || docRes.Document.Text("filePath") != "/proj/matomeru/docs/plan.md" {
		t.Fatalf("create_document result = %+v", docRes)
	}
	if tr, _ := docRes.Document.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("document task = %v, want %v", tr, f.task.Ref())
	}
	if docRes.Relation == nil || docRes.Relation.Kind != data.RelCreated || docRes.Relation.From != f.pane.Ref() || docRes.Relation.To != docRes.Document.Ref() {
		t.Fatalf("document relation = %+v", docRes.Relation)
	}

	// 3. data.add_context creates & dedupes KindContext + links RelContext on pane & task.
	rawCtx1, err := box.Call(ctx, agent, ToolAddContext, json.RawMessage(`{
		"value": "https://github.com/ofam/matomeru/pull/77"
	}`))
	if err != nil {
		t.Fatalf("add_context 1: %v", err)
	}
	ctxRes1 := rawCtx1.(*AddContextResult)
	if ctxRes1.Deduplicated || ctxRes1.Context.Kind != data.KindContext || ctxRes1.Context.Text("contextKind") != "pr" ||
		ctxRes1.Context.Text("title") != "ofam/matomeru#77" || ctxRes1.Context.Text("source") != "tool" {
		t.Fatalf("add_context 1 = %+v (fields=%+v)", ctxRes1, ctxRes1.Context.Fields)
	}
	if p, _ := ctxRes1.Context.RefField("pane"); p != f.pane.Ref() {
		t.Fatalf("context pane = %v", p)
	}
	if tr, _ := ctxRes1.Context.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("context task = %v", tr)
	}
	for _, from := range []core.Ref{f.pane.Ref(), f.task.Ref()} {
		rels, err := f.store.Related(from, data.RelContext, data.Outgoing)
		if err != nil || len(rels) != 1 || rels[0].To != ctxRes1.Context.Ref() {
			t.Fatalf("RelContext from %v = %+v, %v", from, rels, err)
		}
	}

	// Deduplicating the same PR by shorthand "ofam/matomeru#77" reuses the existing record.
	rawCtx2, err := box.Call(ctx, agent, ToolAddContext, json.RawMessage(`{
		"value": "ofam/matomeru#77"
	}`))
	if err != nil {
		t.Fatalf("add_context 2: %v", err)
	}
	ctxRes2 := rawCtx2.(*AddContextResult)
	if !ctxRes2.Deduplicated || ctxRes2.Context.ID != ctxRes1.Context.ID {
		t.Fatalf("add_context 2 did not dedupe: %+v vs %+v", ctxRes2, ctxRes1)
	}

	// 4. data.log_qa logs answered Q&A without unread nagging.
	rawQA, err := box.Call(ctx, agent, ToolLogQA, json.RawMessage(`{
		"prompt": "Why does Matomeru use WAL mode?",
		"answer": "WAL allows concurrent readers alongside a single writer.",
		"page": "qa"
	}`))
	if err != nil {
		t.Fatalf("log_qa: %v", err)
	}
	qaRes := rawQA.(*LogQAResult)
	if qaRes.Question == nil || qaRes.Question.Text("questionKind") != "qa" || qaRes.Question.Text("status") != "answered" {
		t.Fatalf("log_qa question = %+v", qaRes.Question)
	}
	if tr, _ := qaRes.Question.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("log_qa task = %v, want %v", tr, f.task.Ref())
	}
	if qaRes.Page == nil || qaRes.Page.IsUnread() || qaRes.CardID == "" {
		t.Fatalf("log_qa page should be read (not unread): %+v cardId=%q", qaRes.Page, qaRes.CardID)
	}

	// 5. Generic schema-driven data.create and data.update auto-bind pane & inherit task.
	rawCreated, err := box.Call(ctx, agent, ToolCreate, json.RawMessage(`{
		"kind": "command",
		"fields": {
			"title": "Build binary",
			"command": "go build ./cmd/mtm"
		}
	}`))
	if err != nil {
		t.Fatalf("data.create: %v", err)
	}
	created := rawCreated.(*data.Record)
	if created.Kind != data.KindCommand || created.Text("status") != "pending" {
		t.Fatalf("data.create record = %+v", created)
	}
	if p, _ := created.RefField("pane"); p != f.pane.Ref() {
		t.Fatalf("data.create pane = %v, want %v", p, f.pane.Ref())
	}
	if tr, _ := created.RefField("task"); tr != f.task.Ref() {
		t.Fatalf("data.create task = %v, want %v", tr, f.task.Ref())
	}

	rawUpdated, err := box.Call(ctx, agent, ToolUpdate, json.RawMessage(`{
		"kind": "command",
		"id": "`+created.ID+`",
		"fields": {"status": "ran", "notes": "succeeded"}
	}`))
	if err != nil {
		t.Fatalf("data.update: %v", err)
	}
	updated := rawUpdated.(*data.Record)
	if updated.Text("status") != "ran" || updated.Text("notes") != "succeeded" {
		t.Fatalf("data.update record = %+v", updated.Fields)
	}
}
