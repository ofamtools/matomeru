package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
)

const questionBatchSubmitPrefix = "question.batch.submit."

type batchQuestionInfo struct {
	pageID     string
	batchID    string
	batchTitle string
	cards      []ui.ViewNode
}

func cardBatchID(n *ui.ViewNode) string {
	if n == nil {
		return ""
	}
	if s, ok := n.Props["batchId"].(string); ok && s != "" {
		return s
	}
	if s, ok := n.Facts["batchId"].(string); ok && s != "" {
		return s
	}
	return ""
}

func cardBatchIndex(n *ui.ViewNode) int {
	if n == nil {
		return 0
	}
	for _, m := range []map[string]any{n.Props, n.Facts} {
		switch v := m["batchIndex"].(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return 0
}

func cardBatchTitle(n *ui.ViewNode) string {
	if n == nil || n.Props == nil {
		return ""
	}
	if s, ok := n.Props["batchTitle"].(string); ok {
		return s
	}
	return ""
}

func cardIsAnswered(n *ui.ViewNode) bool {
	if n == nil || n.Facts == nil {
		return false
	}
	b, _ := n.Facts["answered"].(bool)
	return b
}

func (a *App) stagedFor(pageID, cardID string) (map[string]any, bool) {
	if a.panes.stagedAnswers == nil || a.panes.stagedAnswers[pageID] == nil {
		return nil, false
	}
	v, ok := a.panes.stagedAnswers[pageID][cardID]
	return v, ok
}

func (a *App) setStagedAnswer(pageID, cardID string, values map[string]any) {
	if a.panes.stagedAnswers == nil {
		a.panes.stagedAnswers = map[string]map[string]map[string]any{}
	}
	if a.panes.stagedAnswers[pageID] == nil {
		a.panes.stagedAnswers[pageID] = map[string]map[string]any{}
	}
	a.panes.stagedAnswers[pageID][cardID] = values
}

func (a *App) clearStagedAnswers(pageID string, cardIDs []string) {
	if a.panes.stagedAnswers == nil || a.panes.stagedAnswers[pageID] == nil {
		return
	}
	for _, id := range cardIDs {
		delete(a.panes.stagedAnswers[pageID], id)
	}
	if len(a.panes.stagedAnswers[pageID]) == 0 {
		delete(a.panes.stagedAnswers, pageID)
	}
}

func formatStagedSummary(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		if v := strings.TrimSpace(data.Text(values[k])); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, ", ")
}

// findQuestionBatches scans a ViewNode subtree for batched ui.ask cards
// (groups of >= 2 ScopeQuestion cards sharing the same batchId).
func findQuestionBatches(root *ui.ViewNode, pageID string) map[string]*batchQuestionInfo {
	if root == nil {
		return nil
	}
	groups := map[string]*batchQuestionInfo{}
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if n.Kind == ui.KindCard && n.Scope == mcpui.ScopeQuestion && n.Facts["batchSubmit"] != true && !strings.HasPrefix(n.ID, questionBatchSubmitPrefix) {
			if bid := cardBatchID(n); bid != "" {
				b := groups[bid]
				if b == nil {
					b = &batchQuestionInfo{pageID: pageID, batchID: bid, batchTitle: cardBatchTitle(n)}
					groups[bid] = b
				}
				if b.batchTitle == "" {
					b.batchTitle = cardBatchTitle(n)
				}
				b.cards = append(b.cards, *n)
			}
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(root)
	for bid, b := range groups {
		if len(b.cards) < 2 {
			delete(groups, bid)
			continue
		}
		slices.SortStableFunc(b.cards, func(x, y ui.ViewNode) int {
			return cardBatchIndex(&x) - cardBatchIndex(&y)
		})
	}
	return groups
}

// enhanceQuestionsSite updates a pane's Questions page view tree when it
// contains one or more batched ui.ask sets: adds a Batch TOC / progress strip,
// numbered/staged badges on each question card, and a focusable Submit All card.
func (a *App) enhanceQuestionsSite(site *ui.ViewNode, pane data.Record, pages []data.Record) {
	if site == nil {
		return
	}
	content := site.Find("site.content")
	if content == nil || content.Ref == nil || content.Ref.Kind != data.KindPage {
		return
	}
	pageID := content.Ref.ID
	batches := findQuestionBatches(content, pageID)
	if len(batches) == 0 {
		return
	}

	// Preserve deterministic batch order as cards appear in content.
	var batchOrder []string
	seenBatch := map[string]bool{}
	var walkOrder func(n *ui.ViewNode)
	walkOrder = func(n *ui.ViewNode) {
		if n.Kind == ui.KindCard && n.Scope == mcpui.ScopeQuestion {
			if bid := cardBatchID(n); bid != "" && batches[bid] != nil && !seenBatch[bid] {
				seenBatch[bid] = true
				batchOrder = append(batchOrder, bid)
			}
		}
		for i := range n.Children {
			walkOrder(&n.Children[i])
		}
	}
	walkOrder(content)

	// Decorate every batched question card with index, staged preview, and badges.
	var decorate func(n *ui.ViewNode)
	decorate = func(n *ui.ViewNode) {
		if n.Kind == ui.KindCard && n.Scope == mcpui.ScopeQuestion {
			if bid := cardBatchID(n); bid != "" && batches[bid] != nil {
				b := batches[bid]
				pos := slices.IndexFunc(b.cards, func(c ui.ViewNode) bool { return c.ID == n.ID })
				if pos >= 0 {
					idxBadge := fmt.Sprintf("%d/%d", pos+1, len(b.cards))
					if !slices.ContainsFunc(n.Badges, func(bg ui.Badge) bool { return bg.Text == idxBadge }) {
						n.Badges = append([]ui.Badge{{Text: idxBadge, Tone: "info"}}, n.Badges...)
					}
				}
				if staged, ok := a.stagedFor(pageID, n.ID); ok && !cardIsAnswered(n) {
					n.Badges = append(n.Badges, ui.Badge{Text: "staged", Tone: "accent"})
					if summary := formatStagedSummary(staged); summary != "" {
						if n.Props == nil {
							n.Props = map[string]any{}
						}
						prev, _ := n.Props["summary"].(string)
						if prev != "" {
							n.Props["summary"] = prev + " · Staged: " + summary
						} else {
							n.Props["summary"] = "Staged: " + summary
						}
					}
				}
			}
		}
		for i := range n.Children {
			decorate(&n.Children[i])
		}
	}
	decorate(content)

	// Build a batch TOC header and Submit All card for each batch.
	var headerNodes []ui.ViewNode
	var submitNodes []ui.ViewNode
	for _, bid := range batchOrder {
		b := batches[bid]
		answeredCount, stagedCount := 0, 0
		var tocItems []string
		for i, c := range b.cards {
			sym := "○"
			if cardIsAnswered(&c) {
				sym = "✓"
				answeredCount++
			} else if _, ok := a.stagedFor(pageID, c.ID); ok {
				sym = "●"
				stagedCount++
			}
			tocItems = append(tocItems, fmt.Sprintf("[%d] %s %s", i+1, sym, clip(c.Title, 24)))
		}
		title := b.batchTitle
		if title == "" {
			title = "Question Batch"
		}
		header := fmt.Sprintf(" %s (%d/%d answered, %d staged) · %s · n/p or j/k navigate · a answer · ctrl+s Submit All",
			title, answeredCount, len(b.cards), stagedCount, strings.Join(tocItems, "  "))
		headerNodes = append(headerNodes, line("question.batch.toc."+bid, header, "accent"))

		if answeredCount < len(b.cards) {
			ready := answeredCount + stagedCount
			submitCard := ui.ViewNode{
				ID:        questionBatchSubmitPrefix + bid,
				Kind:      ui.KindCard,
				Scope:     mcpui.ScopeQuestion,
				Focusable: true,
				Border:    true,
				Tone:      "accent",
				Title:     fmt.Sprintf("Submit All (%d/%d ready)", ready, len(b.cards)),
				Box:       layout.Constraints{FixedH: 3},
				Badges:    []ui.Badge{{Text: fmt.Sprintf("%d staged", stagedCount), Tone: "accent"}},
				Props:     map[string]any{"summary": "Enter or ctrl+s submits all staged answers for this batch"},
				Facts: map[string]any{
					"batchSubmit": true,
					"batchId":     bid,
					"pageId":      pageID,
				},
			}
			submitNodes = append(submitNodes, submitCard)
		}
	}

	scrollBody := ui.ViewNode{
		ID:        views.PageScrollID,
		Kind:      ui.KindScroll,
		Scope:     data.KindPage,
		Focusable: true,
		Box:       layout.Constraints{Flex: 1},
		Props:     map[string]any{"offset": a.pageScrollOffset(pageID)},
		Children:  content.Children,
	}
	if len(content.Children) == 1 && content.Children[0].Kind == ui.KindScroll {
		scrollBody = content.Children[0]
		scrollBody.Box.Flex = 1
	}
	layoutKids := make([]ui.ViewNode, 0, len(headerNodes)+1+len(submitNodes))
	layoutKids = append(layoutKids, headerNodes...)
	layoutKids = append(layoutKids, scrollBody)
	layoutKids = append(layoutKids, submitNodes...)
	content.Children = []ui.ViewNode{{
		Kind:     ui.KindVStack,
		Children: layoutKids,
	}}

	// Update unread / open count badge on the content panel cleanly.
	_ = pane
	_ = pages
}

// questionStep navigates between question cards on the Questions page (n/p),
// skipping inner form children so each press advances one full question card.
func (a *App) questionStep(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		root := a.View(s)
		content := root.Find("site.content")
		if content == nil {
			return core.Next(s)
		}
		var qNodes []string
		var walk func(n *ui.ViewNode)
		walk = func(n *ui.ViewNode) {
			if n.Kind == ui.KindCard && n.Scope == mcpui.ScopeQuestion && n.Focusable && n.ID != "" {
				qNodes = append(qNodes, n.ID)
				return // do not descend into inner form focusables
			}
			for i := range n.Children {
				walk(&n.Children[i])
			}
		}
		walk(content)
		if len(qNodes) == 0 {
			return core.Next(s)
		}
		cur := slices.Index(qNodes, s.ActiveFrame().FocusID)
		target := qNodes[0]
		if cur >= 0 {
			nextIdx := min(max(cur+delta, 0), len(qNodes)-1)
			target = qNodes[nextIdx]
		}
		if !a.scrollPageReveal(s, a.activeSitePageIDFor(s), target) {
			return core.Next(s)
		}
		return core.Next(withFocus(s, target))
	}
}

func formSpecsAndValuesFromCard(card *ui.ViewNode) ([]core.ParamSpec, map[string]any) {
	var specs []core.ParamSpec
	values := map[string]any{}
	if card != nil {
		for i := range card.Children {
			if fn := &card.Children[i]; fn.Kind == ui.KindForm {
				if b, err := json.Marshal(fn.Props["fields"]); err == nil {
					_ = json.Unmarshal(b, &specs)
				}
				if b, err := json.Marshal(fn.Props["values"]); err == nil {
					_ = json.Unmarshal(b, &values)
				}
				break
			}
		}
	}
	if len(specs) == 0 {
		specs = []core.ParamSpec{{Name: "answer", Type: core.ParamString, Required: true}}
	}
	return specs, values
}

// cardAnswerQuestion reads the question a ui.page card asks: its title and
// the summary under it (ui.ask's details).
func cardAnswerQuestion(card *ui.ViewNode) views.AnswerQuestion {
	if card == nil {
		return views.AnswerQuestion{}
	}
	q := views.AnswerQuestion{Prompt: card.Title}
	if s, ok := card.Props["summary"].(string); ok {
		q.Details = s
	}
	return q
}

func (a *App) newAnswerFormModal(fm *form.Model, am *answerFormModal) *answerFormModal {
	fm.Wrap = a.wrapInputs()
	a.attachFormDrafts(fm, am.draftKey)
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:          "answer",
		Scope:       "answer",
		DraftBadges: true,
		RenderBody:  func(m *form.Model, focusID string) ui.ViewNode { return a.answerBody(am, m, focusID) },
		Badges: func(*form.Model) []ui.Badge {
			if am.batch != nil && len(am.batch.cards) > 1 {
				return []ui.Badge{{
					Text: fmt.Sprintf("Q %d/%d · ctrl+n/p", am.batchPos+1, len(am.batch.cards)),
					Tone: "accent",
				}}
			}
			return nil
		},
		Facts:          func(*form.Model) map[string]any { return a.answerFormFacts(am) },
		OnPickPath:     a.formPickPath(func() *modal.FormModal { return mod }),
		OnPickRef:      a.formPickRef(func() *modal.FormModal { return mod }),
		OnDraftDiscard: a.formDraftDiscard(am.draftKey),
		OnCommand: func(m *form.Model, cmd core.Command) (modal.Result, bool) {
			if res, ok := a.answerQuestionScroll(am, m, cmd); ok {
				return res, true
			}
			return a.answerBatchStep(am, m, cmd)
		},
		OnSubmit: func(values map[string]any) modal.Result {
			if a.submitAnswerModal(am, values) {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			return modal.Result{Handled: true}
		},
		OnClose: a.formCancelDraftClose(func() *form.Model { return mod.Model() }, am.draftKey),
	})
	am.FormModal = mod
	return am
}

func (a *App) newAnswerCardModal(pageID string, card *ui.ViewNode, batch *batchQuestionInfo, batchPos int) (*answerFormModal, string) {
	specs, values := formSpecsAndValuesFromCard(card)
	if staged, ok := a.stagedFor(pageID, card.ID); ok {
		for k, v := range staged {
			values[k] = v
		}
	}
	title := firstLine(card.Title) // the full prompt is in the body; the border holds one line
	submitLabel := "Send answer"
	if batch != nil && len(batch.cards) > 1 {
		title = fmt.Sprintf("[%d/%d] %s", batchPos+1, len(batch.cards), firstLine(card.Title))
		remainingOther := 0
		for _, c := range batch.cards {
			if c.ID == card.ID || cardIsAnswered(&c) {
				continue
			}
			if _, staged := a.stagedFor(pageID, c.ID); !staged {
				remainingOther++
			}
		}
		if remainingOther == 0 {
			submitLabel = "Submit All"
		} else {
			submitLabel = "Stage & Next"
		}
	}
	fm := form.New(title, specs, values)
	fm.SubmitRow = true
	fm.SubmitLabel = submitLabel
	am := a.newAnswerFormModal(fm, &answerFormModal{
		pageID:   pageID,
		cardID:   card.ID,
		batch:    batch,
		batchPos: batchPos,
		draftKey: answerDraftKey(pageID, card.ID),
		question: cardAnswerQuestion(card),
	})
	status := "space and h/l choose · i types · ctrl+s sends"
	if batch != nil && len(batch.cards) > 1 {
		status = fmt.Sprintf("question %d/%d · space/h/l choose · i types · ctrl+n/p next/prev · ctrl+s %s",
			batchPos+1, len(batch.cards), strings.ToLower(submitLabel))
	}
	return am, status
}

func (a *App) openAnswerFormForCard(s core.State, pageID string, card *ui.ViewNode, batch *batchQuestionInfo, batchPos int) core.State {
	am, status := a.newAnswerCardModal(pageID, card, batch, batchPos)
	a.say("%s", status)
	return a.pushModal(s, am)
}

func hasNonEmptyAnswer(values map[string]any) bool {
	for _, v := range values {
		if strings.TrimSpace(data.Text(v)) != "" {
			return true
		}
	}
	return false
}

// submitAnswerModal handles form submission inside modalAnswer for global
// KindQuestion records (synced via Router.AnswerQuestion), single page
// questions (direct SubmitAnswer), and batched ui.ask questions (staging +
// atomic SubmitAnswers when the batch is complete). It reports whether the
// modal should close.
func (a *App) submitAnswerModal(am *answerFormModal, values map[string]any) bool {
	if am == nil || am.Model() == nil {
		return false
	}
	fm := am.Model()
	if am.questionID != "" {
		answerText := formatQuestionAnswerValues(values)
		if _, err := a.Router.AnswerQuestion(am.questionID, answerText, User); err != nil {
			a.fail("answer", err)
			return false
		}
		a.clearSubmittedDraft(fm, am.draftKey)
		a.say("answered %q · synced to pane page · u undoes", fm.Title)
		return true
	}
	if am.batch == nil || len(am.batch.cards) < 2 {
		_, err := a.Router.SubmitAnswer(am.pageID, am.cardID, values, User)
		if err != nil {
			a.fail("answer", err)
			return false
		}
		a.clearSubmittedDraft(fm, am.draftKey)
		a.say("answered %q · u undoes", fm.Title)
		return true
	}

	// Batched question: stage this card's answer.
	a.setStagedAnswer(am.pageID, am.cardID, values)
	a.clearSubmittedDraft(fm, am.draftKey)

	// Check if every unanswered card in the batch now has a staged answer.
	batchAnswers := map[string]map[string]any{}
	var cardIDs []string
	nextUnansweredID := ""
	for _, c := range am.batch.cards {
		cardIDs = append(cardIDs, c.ID)
		if staged, ok := a.stagedFor(am.pageID, c.ID); ok {
			batchAnswers[c.ID] = staged
		} else if !cardIsAnswered(&c) && nextUnansweredID == "" {
			nextUnansweredID = c.ID
		}
	}
	if nextUnansweredID == "" && len(batchAnswers) > 0 {
		if _, err := a.Router.SubmitAnswers(am.pageID, batchAnswers, User); err != nil {
			a.fail("submit answers", err)
			return false
		}
		a.clearStagedAnswers(am.pageID, cardIDs)
		a.say("submitted all %d answers · u undoes", len(batchAnswers))
		return true
	}

	// Otherwise close modal and advance focus on the Questions page to the next unanswered question.
	if nextUnansweredID != "" {
		targetFocus := nextUnansweredID
		a.afterModal(func(st core.State) core.State {
			return withFocus(st, targetFocus)
		})
	}
	a.say("staged %d/%d · answer next or ctrl+s to Submit All", len(batchAnswers), len(am.batch.cards))
	return true
}

// questionSubmitAll submits all staged answers for the active question batch on
// the Questions page via mcpui.Router.SubmitAnswers.
func (a *App) questionSubmitAll(s core.State, _ core.Command) core.StepResult {
	// If invoked while inside the answer modal, stage the current form first.
	if am := a.activeAnswerForm(); a.Modal() == "answer" && am != nil && am.batch != nil {
		fm := am.Model()
		if fm.Editing {
			_, _ = fm.HandleCommand(core.Command{ID: form.CmdStop})
		}
		if vals := fm.TypedValues(); hasNonEmptyAnswer(vals) {
			a.setStagedAnswer(am.pageID, am.cardID, vals)
		}
		s = a.popModal(s, modal.CloseSubmit)
	}

	root := a.View(s)
	content := root.Find("site.content")
	if content == nil || content.Ref == nil || content.Ref.Kind != data.KindPage {
		a.say("no questions page is open")
		return core.Next(s)
	}
	pane, hasPane := a.activePane(s)
	pageID := content.Ref.ID
	batches := findQuestionBatches(content, pageID)
	if len(batches) == 0 {
		a.say("no question batch on this page")
		return core.Next(s)
	}

	// Determine target batch from focus, or fall back to the first batch with staged answers.
	focus := s.ActiveFrame().FocusID
	var targetBatch *batchQuestionInfo
	if bid, ok := strings.CutPrefix(focus, questionBatchSubmitPrefix); ok {
		targetBatch = batches[bid]
	}
	if targetBatch == nil {
		for _, b := range batches {
			for _, c := range b.cards {
				if c.ID == focus {
					targetBatch = b
					break
				}
			}
		}
	}
	if targetBatch == nil {
		for _, b := range batches {
			targetBatch = b
			break
		}
	}

	batchAnswers := map[string]map[string]any{}
	var cardIDs []string
	for _, c := range targetBatch.cards {
		cardIDs = append(cardIDs, c.ID)
		if staged, ok := a.stagedFor(pageID, c.ID); ok {
			batchAnswers[c.ID] = staged
		}
	}
	if len(batchAnswers) == 0 {
		a.say("no staged answers yet · press a on a question first")
		return core.Next(s)
	}
	if _, err := a.Router.SubmitAnswers(pageID, batchAnswers, User); err != nil {
		a.fail("submit all answers", err)
		return core.Next(s)
	}
	a.clearStagedAnswers(pageID, cardIDs)
	targetLabel := content.Title
	if hasPane && pane != nil {
		targetLabel = views.Title(a.schema(data.KindPane), *pane)
	}
	a.say("submitted %d answers for %s · u undoes", len(batchAnswers), targetLabel)
	return core.Next(s)
}

// --- Global KindQuestion answering synced to pane ui.page ------------------------

func parseQuestionChoices(rec data.Record) []string {
	raw, has := rec.Value("choices")
	if !has || raw == nil {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		for _, elem := range v {
			switch item := elem.(type) {
			case string:
				if s := strings.TrimSpace(item); s != "" {
					out = append(out, s)
				}
			case map[string]any:
				if id := strings.TrimSpace(data.Text(item["id"])); id != "" {
					out = append(out, id)
				} else if lbl := strings.TrimSpace(data.Text(item["label"])); lbl != "" {
					out = append(out, lbl)
				}
			default:
				if s := strings.TrimSpace(data.Text(elem)); s != "" {
					out = append(out, s)
				}
			}
		}
	case string:
		var parsed []any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			recCopy := rec
			recCopy.Fields = map[string]any{"choices": parsed}
			return parseQuestionChoices(recCopy)
		}
	}
	return out
}

func (a *App) openAnswerFormForQuestionRecord(s core.State, rec data.Record) core.State {
	choices := parseQuestionChoices(rec)
	var specs []core.ParamSpec
	values := map[string]any{}
	existing := strings.TrimSpace(rec.Text("answer"))
	if len(choices) > 0 {
		specs = []core.ParamSpec{
			{Name: "choice", Type: core.ParamString, Required: true, Enum: choices, Default: choices[0], Description: "select an answer"},
			{Name: "notes", Type: core.ParamString, Description: "optional notes"},
		}
		if existing != "" && slices.Contains(choices, existing) {
			values["choice"] = existing
		} else {
			values["choice"] = choices[0]
		}
	} else {
		specs = []core.ParamSpec{
			{Name: "answer", Type: core.ParamString, Required: true, Description: "your answer"},
		}
		if existing != "" {
			values["answer"] = existing
		}
	}
	promptTitle := firstLine(rec.Text("prompt"))
	if promptTitle == "" {
		promptTitle = rec.ID
	}
	fm := form.New(promptTitle, specs, values)
	fm.SubmitRow = true
	fm.SubmitLabel = "Send answer"
	am := a.newAnswerFormModal(fm, &answerFormModal{
		ref:        rec.Ref(),
		questionID: rec.ID,
		draftKey:   "answer:question:" + rec.ID,
		question:   views.AnswerQuestion{Prompt: rec.Text("prompt"), Details: rec.Text("details"), Choices: questionChoiceHelp(rec)},
	})
	a.say("space/h/l choose · i types · ctrl+s sends and syncs to pane")
	return a.pushModal(s, am)
}

func formatQuestionAnswerValues(values map[string]any) string {
	if choice := strings.TrimSpace(data.Text(values["choice"])); choice != "" {
		if notes := strings.TrimSpace(data.Text(values["notes"])); notes != "" {
			return choice + " (" + notes + ")"
		}
		return choice
	}
	if ans := strings.TrimSpace(data.Text(values["answer"])); ans != "" {
		return ans
	}
	b, err := json.Marshal(values)
	if err != nil {
		return data.Text(values)
	}
	return string(b)
}

// answerQuestion opens the form of the focused question card (or submits a
// batch when focused on the Submit All card).
func (a *App) answerQuestion(s core.State, c core.Command) core.StepResult {
	root := a.View(s)
	focus := s.ActiveFrame().FocusID
	if strings.HasPrefix(focus, questionBatchSubmitPrefix) {
		return a.questionSubmitAll(s, c)
	}
	var card *ui.ViewNode
	var walk func(n *ui.ViewNode, within *ui.ViewNode) bool
	walk = func(n *ui.ViewNode, within *ui.ViewNode) bool {
		if n.Scope == mcpui.ScopeQuestion && n.Kind == ui.KindCard {
			within = n
		}
		if n.ID == focus && focus != "" {
			card = within
			return true
		}
		for i := range n.Children {
			if walk(&n.Children[i], within) {
				return true
			}
		}
		return false
	}
	walk(&root, nil)
	if card == nil && a.Engine.Mode(s) == ModePage && focus == views.PageScrollID {
		if content := root.Find("site.content"); content != nil {
			var firstCard *ui.ViewNode
			var findQ func(n *ui.ViewNode)
			findQ = func(n *ui.ViewNode) {
				if n.Scope == mcpui.ScopeQuestion && n.Kind == ui.KindCard && !strings.HasPrefix(n.ID, questionBatchSubmitPrefix) {
					if firstCard == nil {
						firstCard = n
					}
					if card == nil && !cardIsAnswered(n) {
						card = n
					}
					return
				}
				for i := range n.Children {
					findQ(&n.Children[i])
				}
			}
			findQ(content)
			if card == nil {
				card = firstCard
			}
		}
	}
	pageID := ""
	if content := root.Find("site.content"); content != nil && content.Ref != nil && content.Ref.Kind == data.KindPage {
		pageID = content.Ref.ID
	}
	pane, hasPane := a.activePane(s)
	if pageID == "" && hasPane && pane != nil {
		pageID = a.panes.activePage[pane.ID]
	}
	if card == nil || pageID == "" {
		// Global KindQuestion record (e.g. in 2 Work -> Questions or Show page).
		if ref, hasRef := a.subject(s, c); hasRef && ref.Kind == data.KindQuestion {
			rec, err := a.Store.Get(ref)
			if err != nil {
				a.fail("answer", err)
				return core.Next(s)
			}
			// If pressing enter on an already-answered question in the index, open its Show page;
			// pressing `a` always opens the answer form.
			if rec.Text("status") != "open" && strings.HasSuffix(c.Source, ":enter") {
				if cur, showing := a.Showing(); !showing || cur != ref {
					return core.Next(a.show(s, ref))
				}
			}
			return core.Next(a.openAnswerFormForQuestionRecord(s, *rec))
		}
		a.say("no question here to answer")
		return core.Next(s)
	}
	if card.Facts != nil && card.Facts["batchSubmit"] == true {
		return a.questionSubmitAll(s, c)
	}
	var batch *batchQuestionInfo
	batchPos := 0
	if bid := cardBatchID(card); bid != "" {
		if content := root.Find("site.content"); content != nil {
			if batches := findQuestionBatches(content, pageID); batches[bid] != nil {
				batch = batches[bid]
				if idx := slices.IndexFunc(batch.cards, func(cn ui.ViewNode) bool { return cn.ID == card.ID }); idx >= 0 {
					batchPos = idx
				}
			}
		}
	}
	return core.Next(a.openAnswerFormForCard(s, pageID, card, batch, batchPos))
}
