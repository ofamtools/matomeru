package views

import (
	"strings"

	"ofam.dev/ochazuke/ui"
)

// AnswerQuestion is the question an answer modal is answering: the words the
// asker wrote and the context they gave under them.
type AnswerQuestion struct {
	Prompt  string
	Details string
}

// Empty reports whether there is nothing to show above the answer fields.
func (q AnswerQuestion) Empty() bool {
	return strings.TrimSpace(q.Prompt) == "" && strings.TrimSpace(q.Details) == ""
}

// AnswerBody is the body of the answer modal: the full question and its
// details on top, then the answer form, so the question being answered is
// always in view whichever page the modal was opened from.
func AnswerBody(id, title string, q AnswerQuestion, form ui.ViewNode) ui.ViewNode {
	body := ui.ViewNode{ID: id + ".body", Kind: ui.KindPanel, Title: title}
	if prompt := strings.TrimSpace(q.Prompt); prompt != "" {
		body.Children = append(body.Children, ui.ViewNode{
			ID: id + ".question", Kind: ui.KindText, Tone: "accent",
			Props: map[string]any{"text": "Question: " + prompt, "wrap": true},
			Facts: map[string]any{"answerQuestion": true},
		})
	}
	if details := strings.TrimSpace(q.Details); details != "" {
		body.Children = append(body.Children, ui.ViewNode{
			ID: id + ".details", Kind: ui.KindMarkdown,
			Props: map[string]any{"markdown": details, "wrap": true},
			Facts: map[string]any{"answerDetails": true},
		})
	}
	body.Children = append(body.Children, form)
	return body
}
