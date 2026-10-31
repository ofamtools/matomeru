package main

import (
	"errors"

	"ofam.dev/ochazuke"
)

// The prompt: one single-line input of the field noun (insert pinned, like
// the framework's prompt noun, so the editor's Readline rows edit it) in one
// modal, reused by every verb that needs a line of text. enter commits; esc
// closes and keeps what was typed, committing it when it changed (esc never
// discards, UX review 2 F4). The shape is examples/csv/prompt.go: the label rides in the
// body's payload, the commit is application state, and the *Input keeps its
// undo tree across uses although its instance closes with the modal.

const promptFrame = "mtm-prompt"
const fieldID = "field"

// question is the open prompt's commit.
type question struct {
	commit  func(text string) error
	initial string // the text the prompt opened with
}

// registerPrompt creates the field and the verbs that close it.
func (a *App) registerPrompt() {
	a.r.Define("field").Pin("insert").
		Verb("commit", a.commitPrompt, "Close the prompt and run the question with the field's text").
		Verb("leave", a.leavePrompt, "Close the prompt, keeping what was typed: a changed text is committed").
		Mode("insert").
		Bind("enter", ochazuke.Do("field.commit")).
		Bind("esc", ochazuke.Do("field.leave"))
	a.field = ochazuke.NewInput(fieldID, "", false)
	a.m.ModalViews[promptFrame] = ochazuke.ModalView{Title: "Prompt", Render: func(_ *ochazuke.Runtime, body *ochazuke.Instance, w, _ int) ochazuke.Block {
		field := ochazuke.InputBlock(a.field, max(1, w), 1)
		return field.Offset(0, 2).WithText(body.Payload.Prompt + "\n\n" + field.Text)
	}}
}

func (a *App) commitPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	q := a.ask
	if q == nil || ctx.Runtime.Focus != fieldID {
		return ochazuke.Failed(errors.New("no prompt is open"))
	}
	text := string(a.field.Text)
	a.ask = nil
	if err := ctx.Runtime.Pop(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := q.commit(text); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(text)
}

func (a *App) leavePrompt(ctx ochazuke.Ctx) ochazuke.Result {
	if a.ask == nil {
		return ochazuke.Failed(errors.New("no prompt is open"))
	}
	if string(a.field.Text) != a.ask.initial {
		return a.commitPrompt(ctx)
	}
	a.ask = nil
	if err := ctx.Runtime.Pop(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// prompt opens the modal with a title, a label line and initial text;
// commit runs with the text when enter is pressed.
func (a *App) prompt(title, label, text string, commit func(string) error) error {
	if a.ask != nil {
		return errors.New("a prompt is already open")
	}
	if err := a.field.Replace(0, len(a.field.Text), text); err != nil {
		return err
	}
	a.field.Cursor = len(a.field.Text)
	view := a.m.ModalViews[promptFrame]
	view.Title = title
	a.m.ModalViews[promptFrame] = view
	a.ask = &question{commit: commit, initial: text}
	if _, err := a.r.OpenModal(promptFrame, ochazuke.Payload{Prompt: label}, ochazuke.WithInput(a.field, ochazuke.Kind("field"))); err != nil {
		a.ask = nil
		return err
	}
	return nil
}
