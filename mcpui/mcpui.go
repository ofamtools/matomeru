// Package mcpui is the MCP Open UI server logic: four tools that let an agent
// build an interactive site of ViewNode pages per pane, stored as ui.page
// records in a data.Store.
//
// mcpui imports only the standard library, ofam.dev/matomeru/data and
// ofam.dev/ochazuke/{core,ui}. It never imports Bubble Tea.
package mcpui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// Tool names.
const (
	ToolCreatePage = "ui.create_page"
	ToolUpdatePage = "ui.update_page"
	ToolAsk        = "ui.ask"
	ToolListPages  = "ui.list_pages"
)

// Update modes of ui.update_page.
const (
	ModePrepend = "prepend"
	ModeAppend  = "append"
	ModeReplace = "replace"
	ModePatch   = "patch"
)

// Names the question cards use.
const (
	QuestionsSlug  = "questions"
	ScopeQuestion  = "question"
	AnswerCommand  = "ui.answer"
	badgeOpen      = "open"
	badgeAnswered  = "answered"
	propAnswer     = "answer"
	propAnsweredAt = "answeredAt"
	propAnsweredBy = "answeredBy"
)

var pageKinds = []string{"screen", "questions", "decisions", "plan", "response", "custom"}

var nodeKinds = []string{
	ui.KindVStack, ui.KindHStack, ui.KindSplit, ui.KindGrid, ui.KindTabs, ui.KindPanel, ui.KindScroll, ui.KindOverlay,
	ui.KindCard, ui.KindText, ui.KindMarkdown, ui.KindTable, ui.KindTree, ui.KindInput, ui.KindForm, ui.KindPicker,
	ui.KindTerminal, ui.KindRaw,
}

// ToolSpec describes one tool for an MCP tools/list reply.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Router serves the four Open UI tools over a store.
type Router struct {
	store *data.Store
}

// New returns a Router over store.
func New(store *data.Store) *Router { return &Router{store: store} }

// --- arguments and results ----------------------------------------------------

// CreatePageArgs are the arguments of ui.create_page.
type CreatePageArgs struct {
	Pane     string      `json:"pane,omitempty"`
	Task     string      `json:"task,omitempty"`
	ID       string      `json:"id,omitempty"`
	Title    string      `json:"title"`
	Slug     string      `json:"slug,omitempty"`
	PageKind string      `json:"pageKind,omitempty"`
	Pinned   bool        `json:"pinned,omitempty"`
	Root     ui.ViewNode `json:"root"`
}

// UpdatePageArgs are the arguments of ui.update_page. Page is a page ID, or
// a slug within Pane. The content is given as node, card or root; they are
// the same thing under the name that reads best for the mode.
type UpdatePageArgs struct {
	Pane     string       `json:"pane,omitempty"`
	Page     string       `json:"page"`
	Mode     string       `json:"mode"`
	Node     *ui.ViewNode `json:"node,omitempty"`
	Card     *ui.ViewNode `json:"card,omitempty"`
	Root     *ui.ViewNode `json:"root,omitempty"`
	TargetID string       `json:"targetId,omitempty"`
	Title    string       `json:"title,omitempty"`
}

// Choice is one option of a pick-one question.
type Choice struct {
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// AskForm is the form of a question.
type AskForm struct {
	Fields      []core.ParamSpec `json:"fields"`
	SubmitLabel string           `json:"submitLabel,omitempty"`
}

// AskArgs are the arguments of ui.ask. Give a form, choices, or a whole card.
type AskArgs struct {
	Pane    string       `json:"pane,omitempty"`
	Task    string       `json:"task,omitempty"`
	Page    string       `json:"page,omitempty"` // page ID or slug; default "questions"
	ID      string       `json:"id,omitempty"`   // card ID; generated when empty
	Title   string       `json:"title,omitempty"`
	Details string       `json:"details,omitempty"`
	Form    *AskForm     `json:"form,omitempty"`
	Choices []Choice     `json:"choices,omitempty"`
	Card    *ui.ViewNode `json:"card,omitempty"`
}

// ListPagesArgs are the arguments of ui.list_pages.
type ListPagesArgs struct {
	Pane            string `json:"pane,omitempty"`
	IncludeArchived bool   `json:"includeArchived,omitempty"`
}

// PageResult is what the writing tools return.
type PageResult struct {
	ID        string    `json:"id"`
	Ref       core.Ref  `json:"ref"`
	Slug      string    `json:"slug"`
	UpdatedAt time.Time `json:"updatedAt"`
	CardID    string    `json:"cardId,omitempty"`
}

// QuestionState is the state of one question card on a page.
type QuestionState struct {
	CardID     string         `json:"cardId"`
	Title      string         `json:"title"`
	Answered   bool           `json:"answered"`
	Answer     map[string]any `json:"answer,omitempty"`
	AnsweredAt string         `json:"answeredAt,omitempty"`
}

// PageSummary is one row of ui.list_pages.
type PageSummary struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Slug      string          `json:"slug"`
	PageKind  string          `json:"pageKind"`
	Pinned    bool            `json:"pinned"`
	Unread    bool            `json:"unread"`
	UpdatedAt time.Time       `json:"updatedAt"`
	ViewedAt  *time.Time      `json:"viewedAt"`
	PaneRef   *core.Ref       `json:"paneRef"`
	Questions []QuestionState `json:"questions,omitempty"`
}

// ListPagesResult is what ui.list_pages returns.
type ListPagesResult struct {
	Pages []PageSummary `json:"pages"`
}

// --- dispatch -------------------------------------------------------------------

// decodeArgs reads tool arguments strictly: an unknown key is an error, not
// silently dropped.
func decodeArgs(raw json.RawMessage, into any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// Call runs one tool with JSON arguments.
func (r *Router) Call(ctx context.Context, actor core.Actor, toolName string, rawArgs json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		out any
		err error
	)
	switch toolName {
	case ToolCreatePage:
		var args CreatePageArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = r.CreatePage(actor, args)
		}
	case ToolUpdatePage:
		var args UpdatePageArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = r.UpdatePage(actor, args)
		}
	case ToolAsk:
		var args AskArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = r.Ask(actor, args)
		}
	case ToolListPages:
		var args ListPagesArgs
		if err = decodeArgs(rawArgs, &args); err == nil {
			out, err = r.ListPages(args)
		}
	default:
		return nil, fmt.Errorf("mcpui: unknown tool %q", toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", toolName, err)
	}
	return out, nil
}

// --- ViewNode trees -------------------------------------------------------------

// ValidateNode checks a ViewNode tree: every kind is a supported kind and no
// ID is used twice.
func ValidateNode(root ui.ViewNode) error {
	seen := map[string]bool{}
	var walk func(n *ui.ViewNode, path string) error
	walk = func(n *ui.ViewNode, path string) error {
		if !slices.Contains(nodeKinds, n.Kind) {
			return fmt.Errorf("%s: unknown kind %q (one of %s)", path, n.Kind, strings.Join(nodeKinds, ", "))
		}
		if n.ID != "" {
			if seen[n.ID] {
				return fmt.Errorf("%s: id %q is used twice", path, n.ID)
			}
			seen[n.ID] = true
		}
		for i := range n.Children {
			if err := walk(&n.Children[i], fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&root, "root")
}

// PageRoot decodes the ViewNode tree of a ui.page record.
func PageRoot(page data.Record) (ui.ViewNode, error) {
	var root ui.ViewNode
	v, ok := page.Value("root")
	if !ok || v == nil {
		return ui.ViewNode{Kind: ui.KindVStack}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return root, err
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return root, fmt.Errorf("mcpui: root of %s: %w", data.RefText(page.Ref()), err)
	}
	return root, nil
}

// rootValue turns a tree into the plain value stored in the root field.
func rootValue(root ui.ViewNode) (any, error) {
	b, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var v any
	err = json.Unmarshal(b, &v)
	return v, err
}

func walkNodes(n *ui.ViewNode, visit func(*ui.ViewNode)) {
	visit(n)
	for i := range n.Children {
		walkNodes(&n.Children[i], visit)
	}
}

// replaceNode swaps the node with the given ID for with, anywhere below n.
func replaceNode(n *ui.ViewNode, id string, with ui.ViewNode) bool {
	for i := range n.Children {
		if n.Children[i].ID == id {
			n.Children[i] = with
			return true
		}
		if replaceNode(&n.Children[i], id, with) {
			return true
		}
	}
	return false
}

func isContainer(kind string) bool {
	switch kind {
	case ui.KindVStack, ui.KindHStack, ui.KindGrid, ui.KindScroll:
		return true
	}
	return false
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// --- pages ----------------------------------------------------------------------

func paneRef(id string) core.Ref { return core.Ref{Kind: data.KindPane, ID: id} }
func pageRef(id string) core.Ref { return core.Ref{Kind: data.KindPage, ID: id} }
func taskRef(id string) core.Ref { return core.Ref{Kind: data.KindTask, ID: id} }

func result(page *data.Record) *PageResult {
	return &PageResult{ID: page.ID, Ref: page.Ref(), Slug: page.Text("slug"), UpdatedAt: page.UpdatedAt}
}

// panePages returns a pane's pages in creation order (every page when pane is "").
func (r *Router) panePages(pane string, includeArchived bool) ([]data.Record, error) {
	q := data.Query{Kind: data.KindPage, IncludeArchived: includeArchived}
	if pane != "" {
		ref := paneRef(pane)
		q.Parent, q.ParentField = &ref, "pane"
	}
	res, err := r.store.Query(q)
	return res.Items, err
}

// findPage resolves a page by ID, or by slug within a pane.
func (r *Router) findPage(pane, page string) (*data.Record, error) {
	if page == "" {
		return nil, errors.New("page is required: a page id, or a slug with pane")
	}
	rec, err := r.store.Get(pageRef(page))
	if err == nil {
		return rec, nil
	}
	if !errors.Is(err, data.ErrNotFound) {
		return nil, err
	}
	pages, err := r.panePages(pane, true)
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		if p.Text("slug") == page && (pane != "" || !p.IsArchived()) {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("%w: no page with id or slug %q", data.ErrNotFound, page)
}

// CreatePage is ui.create_page: it validates the tree, stores a ui.page
// record and relates it to its pane and task. A pane that is not a stored
// record is still accepted: the page keeps the reference.
func (r *Router) CreatePage(actor core.Actor, args CreatePageArgs) (*PageResult, error) {
	if strings.TrimSpace(args.Title) == "" {
		return nil, errors.New("title is required")
	}
	if args.Root.Kind == "" {
		return nil, errors.New("root is required: a ViewNode with a kind")
	}
	if err := ValidateNode(args.Root); err != nil {
		return nil, err
	}
	if args.PageKind == "" {
		args.PageKind = "custom"
	}
	if !slices.Contains(pageKinds, args.PageKind) {
		return nil, fmt.Errorf("pageKind %q is not one of %s", args.PageKind, strings.Join(pageKinds, ", "))
	}

	// A page inherits its pane's task, so it shows on that task's Show page.
	if args.Task == "" && args.Pane != "" {
		if pane, err := r.store.Get(paneRef(args.Pane)); err == nil {
			if task, ok := pane.RefField("task"); ok {
				args.Task = task.ID
			}
		}
	}
	siblings, err := r.panePages(args.Pane, true)
	if err != nil {
		return nil, err
	}
	base := args.Slug
	if base == "" {
		base = slugify(args.Title)
	}
	if base == "" {
		base = "page"
	}
	slug := base
	for n := 2; slices.ContainsFunc(siblings, func(p data.Record) bool { return p.Text("slug") == slug }); n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}

	root, err := rootValue(args.Root)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"title": args.Title, "slug": slug, "pageKind": args.PageKind, "pinned": args.Pinned, "root": root}
	if args.Pane != "" {
		fields["pane"] = paneRef(args.Pane)
	}
	if args.Task != "" {
		fields["task"] = taskRef(args.Task)
	}
	page, _, err := r.store.Create(data.CreateInput{Kind: data.KindPage, ID: args.ID, Fields: fields}, actor)
	if err != nil {
		return nil, err
	}
	for _, from := range []struct {
		id  string
		ref core.Ref
	}{{args.Pane, paneRef(args.Pane)}, {args.Task, taskRef(args.Task)}} {
		if from.id == "" {
			continue
		}
		if _, _, err := r.store.Relate(data.RelateInput{From: from.ref, Kind: data.RelPage, To: page.Ref()}, actor); err != nil {
			return nil, err
		}
	}
	return result(page), nil
}

// savePage writes a page's tree (and other fields). The write moves
// UpdatedAt past viewedAt, so the page reads as unread with no extra step.
func (r *Router) savePage(page *data.Record, root ui.ViewNode, extra map[string]any, actor core.Actor) (*data.Record, error) {
	if err := ValidateNode(root); err != nil {
		return nil, err
	}
	value, err := rootValue(root)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"root": value}
	for k, v := range extra {
		fields[k] = v
	}
	rec, _, err := r.store.Patch(data.PatchInput{Ref: page.Ref(), Fields: fields}, actor)
	return rec, err
}

// insert puts node at the top or bottom of a page (or of the node named by
// targetID). A root that cannot hold children is wrapped in a vstack first.
func insert(root *ui.ViewNode, node ui.ViewNode, mode, targetID string) error {
	parent := root
	if targetID != "" {
		if parent = root.Find(targetID); parent == nil {
			return fmt.Errorf("%w: no node with id %q on the page", data.ErrNotFound, targetID)
		}
	} else if !isContainer(root.Kind) {
		*root = ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{*root}}
		parent = root
	}
	if mode == ModePrepend {
		parent.Children = append([]ui.ViewNode{node}, parent.Children...)
	} else {
		parent.Children = append(parent.Children, node)
	}
	return nil
}

// UpdatePage is ui.update_page. prepend and append insert a node into the
// root's children (or into the node named by targetId) and mark a card
// unread; replace swaps the whole root; patch replaces the node whose ID is
// targetId (or the given node's own ID) in place.
func (r *Router) UpdatePage(actor core.Actor, args UpdatePageArgs) (*PageResult, error) {
	page, err := r.findPage(args.Pane, args.Page)
	if err != nil {
		return nil, err
	}
	node := args.Node
	for _, alt := range []*ui.ViewNode{args.Card, args.Root} {
		if node == nil {
			node = alt
		}
	}
	if node == nil || node.Kind == "" {
		return nil, errors.New("node is required: a ViewNode given as node, card or root")
	}
	root, err := PageRoot(*page)
	if err != nil {
		return nil, err
	}
	switch args.Mode {
	case ModePrepend, ModeAppend:
		added := *node
		if added.Kind == ui.KindCard {
			added.Unread = true
		}
		if err := insert(&root, added, args.Mode, args.TargetID); err != nil {
			return nil, err
		}
	case ModeReplace:
		root = *node
	case ModePatch:
		id := args.TargetID
		if id == "" {
			id = node.ID
		}
		if id == "" {
			return nil, errors.New("patch needs targetId, or a node with an id")
		}
		with := *node
		if with.ID == "" {
			with.ID = id
		}
		if root.ID == id {
			root = with
		} else if !replaceNode(&root, id, with) {
			return nil, fmt.Errorf("%w: no node with id %q on the page", data.ErrNotFound, id)
		}
	default:
		return nil, fmt.Errorf("mode %q is not one of prepend, append, replace, patch", args.Mode)
	}
	extra := map[string]any{}
	if args.Title != "" {
		extra["title"] = args.Title
	}
	saved, err := r.savePage(page, root, extra, actor)
	if err != nil {
		return nil, err
	}
	return result(saved), nil
}

// questionCard builds the card ui.ask prepends.
func questionCard(args AskArgs) (ui.ViewNode, error) {
	var card ui.ViewNode
	if args.Card != nil {
		card = *args.Card
		if card.Kind != ui.KindCard {
			return card, fmt.Errorf("card must have kind %q, not %q", ui.KindCard, card.Kind)
		}
	} else {
		if strings.TrimSpace(args.Title) == "" {
			return card, errors.New("title is required: the question")
		}
		var form AskForm
		switch {
		case args.Form != nil && len(args.Form.Fields) > 0:
			form = *args.Form
		case len(args.Choices) > 0:
			field := core.ParamSpec{Name: "choice", Type: core.ParamString, Required: true}
			var help []string
			for _, c := range args.Choices {
				if c.ID == "" {
					return card, errors.New("every choice needs an id")
				}
				field.Enum = append(field.Enum, c.ID)
				if text := strings.TrimSpace(c.Label + " " + c.Description); text != "" {
					help = append(help, c.ID+": "+text)
				}
			}
			field.Description = strings.Join(help, "; ")
			form.Fields = []core.ParamSpec{field, {Name: "notes", Type: core.ParamString, Description: "anything to add"}}
		default:
			return card, errors.New("a question needs a form with fields, choices, or a card")
		}
		if form.SubmitLabel == "" {
			form.SubmitLabel = "Answer"
		}
		card = ui.ViewNode{Kind: ui.KindCard, Title: args.Title, Children: []ui.ViewNode{{
			Kind: ui.KindForm, Scope: "form", Focusable: true,
			Props: map[string]any{"fields": form.Fields, "values": map[string]any{}, "submitLabel": form.SubmitLabel, "onSubmit": AnswerCommand},
		}}}
		if args.Details != "" {
			card.Props = map[string]any{"summary": args.Details}
		}
	}
	if args.ID != "" {
		card.ID = args.ID
	}
	if card.ID == "" {
		card.ID = "q-" + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	card.Scope, card.Focusable, card.Unread, card.Border = ScopeQuestion, true, true, true
	if card.Facts == nil {
		card.Facts = map[string]any{}
	}
	card.Facts["answered"] = false
	if !slices.ContainsFunc(card.Badges, func(b ui.Badge) bool { return b.Text == badgeOpen }) {
		card.Badges = append(card.Badges, ui.Badge{Text: badgeOpen, Tone: "warning"})
	}
	for i := range card.Children {
		if form := &card.Children[i]; form.Kind == ui.KindForm && form.ID == "" {
			form.ID = card.ID + ".form"
			break
		}
	}
	return card, nil
}

// Ask is ui.ask: it finds or creates the pane's persistent questions page
// (slug "questions", pinned) and prepends an unread question card to it.
// The answer is read back with ui.list_pages.
func (r *Router) Ask(actor core.Actor, args AskArgs) (*PageResult, error) {
	card, err := questionCard(args)
	if err != nil {
		return nil, err
	}
	slug := args.Page
	if slug == "" {
		slug = QuestionsSlug
	}
	page, err := r.findPage(args.Pane, slug)
	switch {
	case errors.Is(err, data.ErrNotFound):
		kind, title := "custom", slug
		if slices.Contains(pageKinds, slug) {
			kind, title = slug, strings.ToUpper(slug[:1])+slug[1:]
		}
		created, err := r.CreatePage(actor, CreatePageArgs{Pane: args.Pane, Task: args.Task, Title: title, Slug: slug, PageKind: kind,
			Pinned: true, Root: ui.ViewNode{Kind: ui.KindVStack, Gap: 1}})
		if err != nil {
			return nil, err
		}
		if page, err = r.store.Get(created.Ref); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case page.IsArchived():
		// A new question must be seen: bring its page back.
		if page, _, err = r.store.Restore(page.Ref(), actor); err != nil {
			return nil, err
		}
	}
	root, err := PageRoot(*page)
	if err != nil {
		return nil, err
	}
	if root.Find(card.ID) != nil {
		return nil, fmt.Errorf("the page already has a card with id %q", card.ID)
	}
	if err := insert(&root, card, ModePrepend, ""); err != nil {
		return nil, err
	}
	saved, err := r.savePage(page, root, nil, actor)
	if err != nil {
		return nil, err
	}
	res := result(saved)
	res.CardID = card.ID
	return res, nil
}

// questions lists the question cards of a page, in page order.
func questions(page data.Record) []QuestionState {
	root, err := PageRoot(page)
	if err != nil {
		return nil
	}
	var out []QuestionState
	walkNodes(&root, func(n *ui.ViewNode) {
		if n.Kind != ui.KindCard || n.Scope != ScopeQuestion {
			return
		}
		q := QuestionState{CardID: n.ID, Title: n.Title}
		q.Answered, _ = n.Facts["answered"].(bool)
		q.Answer, _ = n.Props[propAnswer].(map[string]any)
		q.AnsweredAt, _ = n.Props[propAnsweredAt].(string)
		out = append(out, q)
	})
	return out
}

// Summarize builds the list row of a page.
func Summarize(page data.Record) PageSummary {
	sum := PageSummary{
		ID: page.ID, Title: page.Text("title"), Slug: page.Text("slug"), PageKind: page.Text("pageKind"),
		Pinned: page.Bool("pinned"), Unread: page.IsUnread(), UpdatedAt: page.UpdatedAt, Questions: questions(page),
	}
	if viewed := page.Time("viewedAt"); !viewed.IsZero() {
		sum.ViewedAt = &viewed
	}
	if pane, ok := page.RefField("pane"); ok {
		sum.PaneRef = &pane
	}
	return sum
}

// ListPages is ui.list_pages: the pages of one pane in the order they were
// made, or with no pane every page, unread first and then newest first.
func (r *Router) ListPages(args ListPagesArgs) (*ListPagesResult, error) {
	pages, err := r.panePages(args.Pane, args.IncludeArchived)
	if err != nil {
		return nil, err
	}
	out := &ListPagesResult{Pages: make([]PageSummary, len(pages))}
	for i, p := range pages {
		out.Pages[i] = Summarize(p)
	}
	if args.Pane == "" {
		slices.SortStableFunc(out.Pages, func(a, b PageSummary) int {
			if a.Unread != b.Unread {
				if a.Unread {
					return -1
				}
				return 1
			}
			return b.UpdatedAt.Compare(a.UpdatedAt)
		})
	}
	return out, nil
}

// --- what the owner does ----------------------------------------------------------

// MarkViewed records that the owner looked at a page: viewedAt becomes the
// time of this write, so the page stops reading as unread. Cards lose their
// unread mark, except questions that are still waiting for an answer.
func (r *Router) MarkViewed(pageID string, actor core.Actor) (*data.Record, error) {
	page, err := r.store.Get(pageRef(pageID))
	if err != nil {
		return nil, err
	}
	root, err := PageRoot(*page)
	if err != nil {
		return nil, err
	}
	walkNodes(&root, func(n *ui.ViewNode) {
		if answered, _ := n.Facts["answered"].(bool); n.Scope != ScopeQuestion || answered {
			n.Unread = false
		}
	})
	return r.savePage(page, root, map[string]any{"viewedAt": data.WriteTime{}}, actor)
}

// SubmitAnswer stores the owner's structured answer on a question card: the
// card stops being unread, gets an "answered" badge and tone, and keeps the
// values, which ui.list_pages hands back to the agent. Answering again
// replaces the earlier answer.
func (r *Router) SubmitAnswer(pageID, cardID string, values map[string]any, actor core.Actor) (*data.Record, error) {
	page, err := r.store.Get(pageRef(pageID))
	if err != nil {
		return nil, err
	}
	root, err := PageRoot(*page)
	if err != nil {
		return nil, err
	}
	card := root.Find(cardID)
	if card == nil {
		return nil, fmt.Errorf("%w: no card with id %q on page %s", data.ErrNotFound, cardID, pageID)
	}
	if values == nil {
		values = map[string]any{}
	}
	card.Unread, card.Tone = false, "success"
	card.Badges = slices.DeleteFunc(card.Badges, func(b ui.Badge) bool { return b.Text == badgeOpen || b.Text == badgeAnswered })
	card.Badges = append(card.Badges, ui.Badge{Text: badgeAnswered, Tone: "success"})
	if card.Facts == nil {
		card.Facts = map[string]any{}
	}
	card.Facts["answered"] = true
	if card.Props == nil {
		card.Props = map[string]any{}
	}
	card.Props[propAnswer] = values
	card.Props[propAnsweredAt] = time.Now().UTC().Format(data.TimeFormat)
	card.Props[propAnsweredBy] = actor
	walkNodes(card, func(n *ui.ViewNode) {
		if n.Kind == ui.KindForm {
			if n.Props == nil {
				n.Props = map[string]any{}
			}
			n.Props["values"] = values
		}
	})
	// The owner is looking at the page while answering it.
	return r.savePage(page, root, map[string]any{"viewedAt": data.WriteTime{}}, actor)
}
