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
	if a.stagedAnswers == nil || a.stagedAnswers[pageID] == nil {
		return nil, false
	}
	v, ok := a.stagedAnswers[pageID][cardID]
	return v, ok
}

func (a *App) setStagedAnswer(pageID, cardID string, values map[string]any) {
	if a.stagedAnswers == nil {
		a.stagedAnswers = map[string]map[string]map[string]any{}
	}
	if a.stagedAnswers[pageID] == nil {
		a.stagedAnswers[pageID] = map[string]map[string]any{}
	}
	a.stagedAnswers[pageID][cardID] = values
}

func (a *App) clearStagedAnswers(pageID string, cardIDs []string) {
	if a.stagedAnswers == nil || a.stagedAnswers[pageID] == nil {
		return
	}
	for _, id := range cardIDs {
		delete(a.stagedAnswers[pageID], id)
	}
	if len(a.stagedAnswers[pageID]) == 0 {
		delete(a.stagedAnswers, pageID)
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
		ID:       "question.batch.scroll",
		Kind:     ui.KindScroll,
		Box:      layout.Constraints{Flex: 1},
		Children: content.Children,
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
		if cur < 0 {
			return core.Next(withFocus(s, qNodes[0]))
		}
		nextIdx := min(max(cur+delta, 0), len(qNodes)-1)
		return core.Next(withFocus(s, qNodes[nextIdx]))
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

func (a *App) openAnswerFormForCard(s core.State, pageID string, card *ui.ViewNode, batch *batchQuestionInfo, batchPos int) core.State {
	specs, values := formSpecsAndValuesFromCard(card)
	if staged, ok := a.stagedFor(pageID, card.ID); ok {
		for k, v := range staged {
			values[k] = v
		}
	}
	title := card.Title
	submitLabel := "Send answer"
	if batch != nil && len(batch.cards) > 1 {
		title = fmt.Sprintf("[%d/%d] %s", batchPos+1, len(batch.cards), card.Title)
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
	fs := &formState{
		fm:       fm,
		pageID:   pageID,
		cardID:   card.ID,
		batch:    batch,
		batchPos: batchPos,
	}
	a.attachFormDrafts(fs, answerDraftKey(fs.pageID, fs.cardID))
	a.answer = fs
	if batch != nil && len(batch.cards) > 1 {
		a.say("question %d/%d · space/h/l choose · i types · ctrl+n/p next/prev · ctrl+s %s",
			batchPos+1, len(batch.cards), strings.ToLower(submitLabel))
	} else {
		a.say("space and h/l choose · i types · ctrl+s sends")
	}
	return openModal(s, modalAnswer, ModeForm, modalAnswer+".form")
}

// answerBatchStep steps between questions inside the batch answer modal (ctrl+n / ctrl+p).
func (a *App) answerBatchStep(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		fs := a.answer
		if topModal(s) != modalAnswer || fs == nil || fs.batch == nil || len(fs.batch.cards) < 2 {
			return core.Next(s)
		}
		if fs.fm.Editing {
			_, _ = fs.fm.HandleCommand(core.Command{ID: form.CmdStop})
		}
		vals := fs.fm.TypedValues()
		if len(vals) > 0 && hasNonEmptyAnswer(vals) {
			a.setStagedAnswer(fs.pageID, fs.cardID, vals)
		}
		nextPos := (fs.batchPos + delta + len(fs.batch.cards)) % len(fs.batch.cards)
		nextCard := fs.batch.cards[nextPos]
		return core.Next(a.openAnswerFormForCard(s, fs.pageID, &nextCard, fs.batch, nextPos))
	}
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
// atomic SubmitAnswers when the batch is complete).
func (a *App) submitAnswerModal(s core.State, fs *formState, values map[string]any) core.State {
	if fs.questionID != "" {
		answerText := formatQuestionAnswerValues(values)
		if _, err := a.Router.AnswerQuestion(fs.questionID, answerText, User); err != nil {
			a.fail("answer", err)
			return s
		}
		a.clearSubmittedDraft(fs)
		a.answer = nil
		a.say("answered %q · synced to pane page · u undoes", fs.fm.Title)
		return closeModal(s, modalAnswer)
	}
	if fs.batch == nil || len(fs.batch.cards) < 2 {
		_, err := a.Router.SubmitAnswer(fs.pageID, fs.cardID, values, User)
		if err != nil {
			a.fail("answer", err)
			return s
		}
		a.clearSubmittedDraft(fs)
		a.answer = nil
		a.say("answered %q · u undoes", fs.fm.Title)
		return closeModal(s, modalAnswer)
	}

	// Batched question: stage this card's answer.
	a.setStagedAnswer(fs.pageID, fs.cardID, values)
	a.clearSubmittedDraft(fs)

	// Check if every unanswered card in the batch now has a staged answer.
	batchAnswers := map[string]map[string]any{}
	var cardIDs []string
	nextUnansweredID := ""
	for _, c := range fs.batch.cards {
		cardIDs = append(cardIDs, c.ID)
		if staged, ok := a.stagedFor(fs.pageID, c.ID); ok {
			batchAnswers[c.ID] = staged
		} else if !cardIsAnswered(&c) && nextUnansweredID == "" {
			nextUnansweredID = c.ID
		}
	}
	if nextUnansweredID == "" && len(batchAnswers) > 0 {
		if _, err := a.Router.SubmitAnswers(fs.pageID, batchAnswers, User); err != nil {
			a.fail("submit answers", err)
			return s
		}
		a.clearStagedAnswers(fs.pageID, cardIDs)
		a.answer = nil
		a.say("submitted all %d answers · u undoes", len(batchAnswers))
		return closeModal(s, modalAnswer)
	}

	// Otherwise close modal and advance focus on the Questions page to the next unanswered question.
	a.answer = nil
	s = closeModal(s, modalAnswer)
	if nextUnansweredID != "" {
		s = withFocus(s, nextUnansweredID)
	}
	a.say("staged %d/%d · answer next or ctrl+s to Submit All", len(batchAnswers), len(fs.batch.cards))
	return s
}

// questionSubmitAll submits all staged answers for the active question batch on
// the Questions page via mcpui.Router.SubmitAnswers.
func (a *App) questionSubmitAll(s core.State, _ core.Command) core.StepResult {
	// If invoked while inside the answer modal, stage the current form first.
	if topModal(s) == modalAnswer && a.answer != nil && a.answer.batch != nil {
		fs := a.answer
		if fs.fm.Editing {
			_, _ = fs.fm.HandleCommand(core.Command{ID: form.CmdStop})
		}
		if vals := fs.fm.TypedValues(); hasNonEmptyAnswer(vals) {
			a.setStagedAnswer(fs.pageID, fs.cardID, vals)
		}
		s = closeModal(s, modalAnswer)
		a.answer = nil
	}

	pane, ok := a.SelectedPane()
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	root := a.View(s)
	content := root.Find("site.content")
	if content == nil || content.Ref == nil || content.Ref.Kind != data.KindPage {
		a.say("no questions page is open")
		return core.Next(s)
	}
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
	a.say("submitted %d answers for %s · u undoes", len(batchAnswers), views.Title(a.schema(data.KindPane), *pane))
	return core.Next(s)
}
