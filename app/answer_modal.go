package app

import (
	"encoding/json"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
)

// answerBody draws the answer modal: the question in its scroll viewport
// above the form, or the bare form when the question is hidden.
func (a *App) answerBody(am *answerFormModal, m *form.Model, focusID string) ui.ViewNode {
	if !a.answerShowsQuestion(am) {
		return m.View(focusID)
	}
	off := -1
	if am.questionOff != nil {
		off = *am.questionOff
	}
	return views.AnswerBody("answer", m.Title, am.question, off, m.View(focusID))
}

// answerFormFacts are the answer modal's facts for binding guards.
func (a *App) answerFormFacts(am *answerFormModal) map[string]any {
	facts := map[string]any{}
	if am.batch != nil && len(am.batch.cards) > 1 {
		facts["isBatch"] = true
		facts["batchPos"] = am.batchPos
		facts["batchTotal"] = len(am.batch.cards)
	}
	if a.answerShowsQuestion(am) {
		facts["answerHasQuestion"] = true
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

// answerBatchStep stages the current answer and opens the next or previous
// question of a batch (ctrl+n/ctrl+p).
func (a *App) answerBatchStep(am *answerFormModal, m *form.Model, cmd core.Command) (modal.Result, bool) {
	var delta int
	switch cmd.ID {
	case CmdAnswerBatchNext:
		delta = 1
	case CmdAnswerBatchPrev:
		delta = -1
	default:
		return modal.Result{}, false
	}
	if am.batch == nil || len(am.batch.cards) < 2 {
		return modal.Result{Handled: true}, true
	}
	if m.Editing {
		_, _ = m.HandleCommand(core.Command{ID: form.CmdStop})
	}
	vals := m.TypedValues()
	if len(vals) > 0 && hasNonEmptyAnswer(vals) {
		a.setStagedAnswer(am.pageID, am.cardID, vals)
	}
	nextPos := (am.batchPos + delta + len(am.batch.cards)) % len(am.batch.cards)
	nextCard := am.batch.cards[nextPos]
	nextMod, status := a.newAnswerCardModal(am.pageID, &nextCard, am.batch, nextPos)
	return modal.Result{
		Handled:     true,
		Close:       true,
		CloseReason: modal.CloseSubmit,
		Push:        nextMod,
		Status:      status,
		SetStatus:   true,
	}, true
}

// answerShowsQuestion reports whether the answer modal draws the question
// above its fields.
func (a *App) answerShowsQuestion(am *answerFormModal) bool {
	return !am.question.Empty() && a.cfg.Settings.Bool(SetAnswerShowQuestion, true)
}

// answerQuestionScroll moves the answer modal's question viewport for the
// scroll motions (ctrl+e/ctrl+y, ctrl+d/ctrl+u) while the fields are not
// being typed into, so a question taller than the modal reads in full.
func (a *App) answerQuestionScroll(am *answerFormModal, m *form.Model, cmd core.Command) (modal.Result, bool) {
	switch cmd.ID {
	case "motion.scroll.down", "motion.scroll.up", "motion.half.down", "motion.half.up":
	default:
		return modal.Result{}, false
	}
	if m.Editing || !a.answerShowsQuestion(am) {
		return modal.Result{}, false
	}
	info, hasInfo := a.State().Layout.Scrolls[views.AnswerScrollID("answer")]
	cur := info.Offset
	if am.questionOff != nil {
		cur = *am.questionOff
	}
	next, _, _ := traits.ScrollStep(scrollStateFromInfo(cur, info, hasInfo), a.scrollConfig(), cmd)
	if am.questionOff == nil || *am.questionOff != next.OffsetY {
		off := next.OffsetY
		am.questionOff = &off
		a.viewMemo.gen++
	}
	return modal.Result{Handled: true}, true
}

// questionChoiceHelp reads the labels and descriptions of a question
// record's choices, for the answer modal's header.
func questionChoiceHelp(rec data.Record) []views.AnswerChoice {
	raw, _ := rec.Value("choices")
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var items []map[string]any
	if json.Unmarshal(b, &items) != nil {
		return nil
	}
	out := make([]views.AnswerChoice, 0, len(items))
	for _, it := range items {
		out = append(out, views.AnswerChoice{ID: data.Text(it["id"]), Label: data.Text(it["label"]), Description: data.Text(it["description"])})
	}
	return out
}
